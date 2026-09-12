package serve

import (
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"time"

	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/dao"
	"github.com/startvibecoding/mothx/internal/messaging"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/session"
)

const durableDeliveryRecoveryInterval = 5 * time.Second

// runDeliveryRecovery owns the serve process' background outbox worker. It
// intentionally polls through the PlatformSupervisor: a disabled or
// disconnected platform is left pending and is picked up when that platform is
// reconnected, while every actual provider call still goes through the shared
// claim/fence/retry coordinator.
func (rt *channelRuntime) runDeliveryRecovery(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	if rt.deliveryDone != nil {
		defer close(rt.deliveryDone)
	}

	// Run once at startup so an already-connected transport can pick up rows
	// left by the previous process without waiting for the first tick.
	rt.reconcileDurableDeliveries(ctx)
	ticker := time.NewTicker(durableDeliveryRecoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rt.reconcileDurableDeliveries(ctx)
		}
	}
}

func (rt *channelRuntime) reconcileDurableDeliveries(ctx context.Context) {
	if rt == nil || rt.platforms == nil || strings.TrimSpace(rt.sessionDir) == "" {
		return
	}
	for _, name := range []string{"wechat", "feishu"} {
		platform := rt.platforms.Get(name)
		if platform == nil || !platform.IsConnected() {
			continue
		}
		executor, ok := platform.(messaging.DurableDeliveryExecutor)
		if !ok {
			continue
		}
		rt.reopenFailedDeliveries(ctx, name)
		coordinator := agentruntime.NewDeliveryCoordinator(rt.sessionDir, "serve-delivery-recovery-"+name)
		processed, err := coordinator.ReconcileDue(ctx, time.Now().UTC(), func(execCtx context.Context, operation session.DeliveryOperation) (agentruntime.DeliveryResult, error) {
			request, projectionErr := rt.deliveryRecoveryRequest(execCtx, operation)
			if projectionErr != nil {
				// A missing plan/attachment cannot become valid by retrying the
				// provider. Keep the failure durable and visible to operators.
				if errors.Is(projectionErr, session.ErrDeliveryOperationAbsent) || errors.Is(projectionErr, dao.ErrNoRows) {
					return agentruntime.DeliveryResult{Status: "failed", FailureCode: "delivery_projection_missing"}, nil
				}
				return agentruntime.DeliveryResult{}, projectionErr
			}
			result, executeErr := executor.ExecuteDurableDelivery(execCtx, request)
			return agentruntime.DeliveryResult{
				Status:            result.Status,
				ProviderAssetID:   result.ProviderAssetID,
				ProviderMessageID: result.ProviderMessageID,
				ProviderState:     result.ProviderState,
				FailureCode:       result.FailureCode,
				NextAttemptAt:     result.NextAttemptAt,
			}, executeErr
		})
		if err != nil {
			log.Printf("[serve] durable %s delivery recovery failed after %d operation(s): %v", name, processed, err)
		} else if processed > 0 {
			log.Printf("[serve] durable %s delivery recovery processed %d operation(s)", name, processed)
		}
	}
}

// reopenFailedDeliveries gives operations that exhausted a retry window another
// chance while the platform is connected. A disconnected or rate-limited
// platform can outlast one retry window, and without this the reply would be
// permanently lost even after the transport recovered. Each operation is
// reopened at most once per process, so a genuinely undeliverable target cannot
// loop forever across reconnects.
func (rt *channelRuntime) reopenFailedDeliveries(ctx context.Context, platform string) {
	if rt == nil || strings.TrimSpace(rt.sessionDir) == "" {
		return
	}
	ids, err := session.ListFailedTransientDeliveryOperations(ctx, rt.sessionDir, platform)
	if err != nil {
		log.Printf("[serve] list failed %s deliveries: %v", platform, err)
		return
	}
	for _, id := range ids {
		if !rt.markDeliveryReopened(platform, id) {
			continue
		}
		reopened, err := session.ReopenFailedDeliveryOperation(ctx, rt.sessionDir, id, time.Now().UTC())
		if err != nil {
			log.Printf("[serve] reopen %s delivery %s: %v", platform, id, err)
			continue
		}
		if reopened {
			log.Printf("[serve] reopened %s delivery %s for another retry window", platform, id)
		}
	}
}

// markDeliveryReopened reports whether this process has not yet reopened the
// operation.
func (rt *channelRuntime) markDeliveryReopened(platform, operationID string) bool {
	rt.deliveryReopenedMu.Lock()
	defer rt.deliveryReopenedMu.Unlock()
	if rt.deliveryReopened == nil {
		rt.deliveryReopened = make(map[string]struct{})
	}
	key := platform + "\x00" + operationID
	if _, seen := rt.deliveryReopened[key]; seen {
		return false
	}
	rt.deliveryReopened[key] = struct{}{}
	return true
}

func (rt *channelRuntime) deliveryRecoveryRequest(ctx context.Context, operation session.DeliveryOperation) (messaging.DurableDeliveryRequest, error) {
	plan, err := session.GetDeliveryPlan(ctx, rt.sessionDir, operation.IntentID)
	if err != nil {
		return messaging.DurableDeliveryRequest{}, err
	}
	if plan == nil {
		return messaging.DurableDeliveryRequest{}, session.ErrDeliveryOperationAbsent
	}
	request := messaging.DurableDeliveryRequest{Intent: plan.Intent, Operation: operation}
	for index := range plan.Operations {
		candidate := plan.Operations[index]
		if candidate.ID == operation.DependsOn {
			request.Dependency = &candidate
			break
		}
	}
	request.Caption = agentruntime.DeliveryOperationText(plan.Intent.TransportContext, operation.OperationKind)
	if request.Caption == "" && (operation.OperationKind == "send_text" || operation.OperationKind == "send_fallback_text") {
		request.Caption, err = loadAssistantDeliveryCaption(rt.sessionDir, plan.Intent.SessionID, plan.Intent.RunID)
		if err != nil {
			return messaging.DurableDeliveryRequest{}, err
		}
	}
	if operation.ArtifactID == "" {
		return request, nil
	}
	attachments, err := agentruntime.NewAttachmentService(rt.sessionDir, agentruntime.DefaultAttachmentPolicy())
	if err != nil {
		return messaging.DurableDeliveryRequest{}, err
	}
	artifact, err := attachments.Get(ctx, plan.Intent.SessionID, operation.ArtifactID)
	if err != nil {
		return messaging.DurableDeliveryRequest{}, err
	}
	request.ArtifactKind = messaging.AttachmentKind(artifact.Kind)
	request.ArtifactFilename = artifact.Filename
	request.ArtifactMediaType = artifact.MediaType
	request.OpenArtifact = func(openCtx context.Context) (io.ReadCloser, error) {
		_, reader, openErr := attachments.Open(openCtx, artifact.SessionID, artifact.ID)
		return reader, openErr
	}
	return request, nil
}

func loadAssistantDeliveryCaption(sessionDir, sessionID, runID string) (string, error) {
	messages, err := session.ListSessionMessagesWithSeq(sessionDir, sessionID)
	if err != nil {
		return "", err
	}
	targetID := session.RunAssistantEntryID(runID)
	for _, message := range messages {
		if message.EntryID == targetID && message.Message.Role == "assistant" {
			return deliveryMessageText(message.Message.Content, message.Message.Contents), nil
		}
	}
	// Never fall back to the latest assistant entry: a missing deterministic
	// entry must surface as a projection error rather than replaying another
	// Run's response into this delivery target.
	return "", nil
}

func deliveryMessageText(content string, blocks []provider.ContentBlock) string {
	if strings.TrimSpace(content) != "" {
		return strings.TrimSpace(content)
	}
	var parts []string
	for _, block := range blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, strings.TrimSpace(block.Text))
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}
