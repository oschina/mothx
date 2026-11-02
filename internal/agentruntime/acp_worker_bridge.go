package agentruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	acpclient "github.com/oschina/mothx/internal/acp/client"
	"github.com/oschina/mothx/internal/config"
)

// ACPWorkerInvocation is the already-authorized Runtime input to one external
// ACP worker turn. It deliberately contains no adapter state or provider
// content; Router task construction owns that conversion before this boundary.
type ACPWorkerInvocation struct {
	Profile        config.ACPAgentProfile
	WorkDir        string
	Task           string
	ClientInfo     acpclient.Implementation
	HandleRequest  acpclient.RequestHandler
	OnNotification func(acpclient.Message)
}

// ACPWorkerResult is the protocol result of one remote worker turn. The
// future child adapter converts it into canonical Agent events and tool output.
type ACPWorkerResult struct {
	AgentInfo  acpclient.Implementation
	SessionID  string
	StopReason string
}

// ACPWorkerBridge owns one ACP v1 worker connection. It is intentionally not
// an Agent, session store, or queue; Runtime supplies policy and consumes the
// typed protocol result.
type ACPWorkerBridge struct{}

// Run starts one configured stdio ACP Agent, performs the MVP handshake and
// one text prompt turn, then closes the remote session when supported.
func (ACPWorkerBridge) Run(ctx context.Context, invocation ACPWorkerInvocation) (ACPWorkerResult, error) {
	if err := invocation.Profile.Validate(); err != nil {
		return ACPWorkerResult{}, err
	}
	if !invocation.Profile.Enabled {
		return ACPWorkerResult{}, fmt.Errorf("ACP worker profile %q is disabled", invocation.Profile.ID)
	}
	if strings.TrimSpace(invocation.WorkDir) == "" || !filepath.IsAbs(invocation.WorkDir) {
		return ACPWorkerResult{}, fmt.Errorf("ACP worker requires an absolute work directory")
	}
	if strings.TrimSpace(invocation.Task) == "" {
		return ACPWorkerResult{}, fmt.Errorf("ACP worker task is required")
	}

	env, err := acpWorkerEnvironment(invocation.Profile)
	if err != nil {
		return ACPWorkerResult{}, err
	}
	client, err := acpclient.Start(ctx, acpclient.Config{
		Command:       invocation.Profile.Command,
		Dir:           invocation.WorkDir,
		Env:           env,
		HandleRequest: invocation.HandleRequest,
	})
	if err != nil {
		return ACPWorkerResult{}, err
	}
	defer client.Close()

	// Drain updates independently while session/prompt waits for its terminal
	// response; the bounded client notification buffer must never be allowed to
	// back up a long-running worker.
	notificationsDone := make(chan struct{})
	go func() {
		defer close(notificationsDone)
		for notification := range client.Notifications() {
			if invocation.OnNotification != nil {
				invocation.OnNotification(notification)
			}
		}
	}()

	clientInfo := invocation.ClientInfo
	if clientInfo.Name == "" {
		clientInfo = acpclient.Implementation{Name: "mothx", Title: "MothX Router", Version: "dev"}
	}
	initialized, err := client.Initialize(ctx, acpclient.InitializeParams{
		ClientInfo: clientInfo,
		ClientCapabilities: map[string]any{
			"fs":       map[string]bool{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
			"auth":     map[string]bool{"terminal": false},
		},
	})
	if err != nil {
		return ACPWorkerResult{}, fmt.Errorf("initialize ACP worker: %w", err)
	}
	session, err := client.NewSession(ctx, acpclient.NewSessionParams{Cwd: invocation.WorkDir})
	if err != nil {
		return ACPWorkerResult{}, fmt.Errorf("create ACP worker session: %w", err)
	}

	// ACP session/cancel is the semantic cancellation signal for an active
	// remote turn. The client context cancellation alone must not be mistaken
	// for a successful remote completion.
	cancelDone := make(chan struct{})
	defer close(cancelDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = client.Cancel(session.SessionID)
		case <-cancelDone:
		}
	}()
	prompt, promptErr := client.Prompt(ctx, acpclient.PromptParams{
		SessionID: session.SessionID,
		Prompt:    []acpclient.ContentBlock{{Type: "text", Text: invocation.Task}},
	})
	if promptErr != nil {
		return ACPWorkerResult{}, fmt.Errorf("run ACP worker prompt: %w", promptErr)
	}
	if supportsSessionClose(initialized.AgentCapabilities) {
		if err := client.CloseSession(ctx, session.SessionID); err != nil {
			return ACPWorkerResult{}, fmt.Errorf("close ACP worker session: %w", err)
		}
	}
	// Client.Close closes Notifications. Wait before returning so callers never
	// receive an event after the terminal bridge result.
	_ = client.Close()
	<-notificationsDone
	return ACPWorkerResult{AgentInfo: initialized.AgentInfo, SessionID: session.SessionID, StopReason: prompt.StopReason}, nil
}

func supportsSessionClose(capabilities map[string]any) bool {
	sessionCaps, ok := capabilities["sessionCapabilities"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = sessionCaps["close"]
	return ok
}

func acpWorkerEnvironment(profile config.ACPAgentProfile) ([]string, error) {
	configured := config.LoadEnv().List()
	pairs := make([]string, 0, len(profile.Environment))
	for _, name := range profile.Environment {
		value, ok := configured[name]
		if !ok {
			value, ok = os.LookupEnv(name)
		}
		if !ok {
			return nil, fmt.Errorf("ACP worker profile %q requires environment variable %q", profile.ID, name)
		}
		pairs = append(pairs, name+"="+value)
	}
	sort.Strings(pairs)
	return pairs, nil
}
