package acp

import (
	"context"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/esm"
)

func TestACPSteeringMessagesInjectsChangedESMObjective(t *testing.T) {
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	mgr, err := agentruntime.CreateSession(agentruntime.CreateSessionOptions{WorkDir: t.TempDir(), SessionDir: settings.SessionDir})
	if err != nil {
		t.Fatal(err)
	}
	sessionID := mgr.GetHeader().ID
	callback := esmSteeringMessages(settings, sessionID)
	if callback == nil {
		t.Fatal("ACP ESM steering callback is nil")
	}
	store := esm.NewStore(settings.SessionDir)
	if _, err := store.Create(context.Background(), sessionID, "finish the ACP objective"); err != nil {
		t.Fatal(err)
	}
	messages := callback()
	if len(messages) != 1 || !messages[0].SystemInjected || !strings.Contains(messages[0].Content, "finish the ACP objective") {
		t.Fatalf("ACP steering = %#v", messages)
	}
	if messages := callback(); len(messages) != 0 {
		t.Fatalf("duplicate ACP steering = %#v, want none", messages)
	}
}
