package channels

import (
	"context"
	"fmt"
	"log"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/messaging"
	"github.com/oschina/mothx/internal/serve/webhook"
)

// WebhookHandler implements webhook.Handler by spawning agent tasks.
type WebhookHandler struct {
	dispatcher *Dispatcher
	platforms  map[string]messaging.Platform // platform name → Platform for delivery
}

// NewWebhookHandler creates a webhook handler that spawns agent tasks.
func NewWebhookHandler(dispatcher *Dispatcher, platforms map[string]messaging.Platform) *WebhookHandler {
	return &WebhookHandler{
		dispatcher: dispatcher,
		platforms:  platforms,
	}
}

// SetPlatforms replaces the platform map. Used to wire platforms after construction.
func (h *WebhookHandler) SetPlatforms(platforms map[string]messaging.Platform) {
	h.platforms = platforms
}

// HandleWebhookEvent processes an incoming webhook event by spawning an agent task.
func (h *WebhookHandler) HandleWebhookEvent(ctx context.Context, route webhook.RouteConfig, payload []byte) error {
	if h.dispatcher.agentMgr == nil {
		return fmt.Errorf("webhook requires --multi-agent mode")
	}

	// Build prompt from webhook event
	prompt := fmt.Sprintf("Process this webhook event (route: %s, skill: %s):\n\n%s",
		route.Path, route.Skill, string(payload))

	// Create a sub-agent to handle the task
	a, err := h.dispatcher.agentMgr.Create(agent.AgentOptions{
		IsSubAgent: true,
		Mode:       "yolo",
		WorkDir:    h.dispatcher.cfg.GetWorkDir(),
	})
	if err != nil {
		return fmt.Errorf("create webhook agent: %w", err)
	}

	// Run agent and collect result
	ch := a.Run(ctx, prompt)
	var result string
	var lastErr error
	for ev := range ch {
		if ev.Error != nil {
			lastErr = ev.Error
		}
		// Collect text deltas from the underlying agent loop events
		if ev.TextDelta != "" {
			result += ev.TextDelta
		}
	}

	// Clean up
	h.dispatcher.agentMgr.Destroy(a.ID())

	if lastErr != nil {
		return fmt.Errorf("webhook agent error: %w", lastErr)
	}

	// Deliver result if configured
	if route.Delivery != "" && result != "" {
		h.deliverResult(route.Delivery, route.DeliveryTarget, result)
	}

	log.Printf("[webhook] Task completed for route %s (result len=%d)", route.Path, len(result))
	return nil
}

// deliverResult sends the result to the configured messaging platform.
func (h *WebhookHandler) deliverResult(platform, target, result string) {
	p, ok := h.platforms[platform]
	if !ok {
		log.Printf("[webhook] Delivery platform %q not found", platform)
		return
	}
	if target == "" {
		log.Printf("[webhook] Delivery target missing for %s", platform)
		return
	}
	if err := p.SendMessage(context.Background(), target, result); err != nil {
		log.Printf("[webhook] Delivery error to %s: %v", platform, err)
	}
}
