package session

import (
	"context"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/provider"
)

func TestExpertBindingPersistsAcrossReopen(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("expert-src"); err != nil {
		t.Fatal(err)
	}
	if got := mgr.GetExpertID(); got != "" {
		t.Fatalf("fresh session expert = %q, want empty", got)
	}
	if err := mgr.SetExpertBinding("software-company"); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if got := mgr.GetExpertID(); got != "software-company" {
		t.Fatalf("in-memory expert = %q", got)
	}

	reopened, err := OpenByIDExact(sessionDir, "expert-src")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := reopened.GetExpertID(); got != "software-company" {
		t.Fatalf("reopened expert = %q, want persisted binding restored", got)
	}

	if err := reopened.SetExpertBinding(""); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	again, err := OpenByIDExact(sessionDir, "expert-src")
	if err != nil {
		t.Fatalf("reopen after unbind: %v", err)
	}
	if got := again.GetExpertID(); got != "" {
		t.Fatalf("expert after unbind = %q, want empty", got)
	}
}

func TestForkExpertInheritanceAndOverride(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("fork-src"); err != nil {
		t.Fatal(err)
	}
	turnID := "expert-turn"
	if err := StartConversationTurn(sessionDir, ConversationTurn{ID: turnID, SessionID: "fork-src", IntentID: "i-1", RunID: "r-1"}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.AppendMessage(provider.NewUserMessage("hello")); err != nil {
		t.Fatal(err)
	}
	if err := EndConversationTurn(sessionDir, "fork-src", turnID, "completed", "stop", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetExpertBinding("studio"); err != nil {
		t.Fatalf("bind studio: %v", err)
	}

	// Default fork preserves the source expert binding.
	inherited, err := ForkSession(context.Background(), sessionDir, ForkOptions{SourceSessionID: "fork-src", RequestID: "f-inherit"})
	if err != nil {
		t.Fatalf("inherit fork: %v", err)
	}
	child, err := OpenByIDExact(sessionDir, inherited.SessionID)
	if err != nil {
		t.Fatalf("open inherited child: %v", err)
	}
	if got := child.GetExpertID(); got != "studio" {
		t.Fatalf("inherited expert = %q, want studio", got)
	}

	// Switching experts forks with an explicit override.
	other := "software-company"
	swapped, err := ForkSession(context.Background(), sessionDir, ForkOptions{SourceSessionID: "fork-src", RequestID: "f-swap", ExpertID: &other})
	if err != nil {
		t.Fatalf("swap fork: %v", err)
	}
	child2, err := OpenByIDExact(sessionDir, swapped.SessionID)
	if err != nil {
		t.Fatalf("open swapped child: %v", err)
	}
	if got := child2.GetExpertID(); got != "software-company" {
		t.Fatalf("swapped expert = %q, want software-company", got)
	}

	// An empty-string override unbinds, while the source keeps its binding.
	none := ""
	unbound, err := ForkSession(context.Background(), sessionDir, ForkOptions{SourceSessionID: "fork-src", RequestID: "f-unbind", ExpertID: &none})
	if err != nil {
		t.Fatalf("unbind fork: %v", err)
	}
	child3, err := OpenByIDExact(sessionDir, unbound.SessionID)
	if err != nil {
		t.Fatalf("open unbound child: %v", err)
	}
	if got := child3.GetExpertID(); got != "" {
		t.Fatalf("unbound child expert = %q, want empty", got)
	}
	if got := mgr.GetExpertID(); got != "studio" {
		t.Fatalf("source expert = %q after fork variants, want studio (original branch untouched)", got)
	}
}
