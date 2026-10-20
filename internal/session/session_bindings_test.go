package session

import (
	"context"
	"testing"

	"github.com/oschina/mothx/internal/provider"
)

func TestLatestSessionBindingsReadsNewestMetadataWithoutReplay(t *testing.T) {
	sessionDir := t.TempDir()

	legacy := New("/tmp/legacy", sessionDir)
	if err := legacy.InitWithID("legacy-bindings"); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.AppendModelChange("provider-old", "model-old"); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.AppendModeChange("agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.AppendThinkingLevelChange("low"); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.AppendAdditionalDirectories([]string{"/tmp/old"}); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.AppendModelChange("provider-new", "model-new"); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.AppendModeChange("yolo"); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.AppendThinkingLevelChange("high"); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.AppendAdditionalDirectories([]string{"/tmp/new-a", "/tmp/new-b"}); err != nil {
		t.Fatal(err)
	}

	eventStore := New("/tmp/event", sessionDir)
	if err := eventStore.InitWithID("event-bindings"); err != nil {
		t.Fatal(err)
	}
	if err := eventStore.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireMutation(sessionDir, "event-bindings")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eventStore.AppendModelChange("event-provider", "event-model"); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	if _, err := eventStore.AppendModeChange("plan"); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	if _, err := eventStore.AppendThinkingLevelChange("medium"); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	if _, err := eventStore.AppendAdditionalDirectories([]string{"/tmp/event"}); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	guard.Release()

	bindings, err := LatestSessionBindings(sessionDir, []string{"legacy-bindings", "event-bindings", "missing"})
	if err != nil {
		t.Fatal(err)
	}

	legacyState, ok := bindings["legacy-bindings"]
	if !ok {
		t.Fatal("legacy session missing from batch")
	}
	if !legacyState.HasModelChange || legacyState.ModelChange.Provider != "provider-new" || legacyState.ModelChange.ModelID != "model-new" {
		t.Fatalf("legacy model binding = %#v", legacyState)
	}
	if !legacyState.HasModeChange || legacyState.ModeChange.Mode != "yolo" {
		t.Fatalf("legacy mode binding = %#v", legacyState)
	}
	if !legacyState.HasThinkingLevelChange || legacyState.ThinkingLevelChange.ThinkingLevel != "high" {
		t.Fatalf("legacy thinking binding = %#v", legacyState)
	}
	if !legacyState.HasAdditionalDirectories {
		t.Fatalf("legacy directory binding missing: %#v", legacyState)
	}
	if got := legacyState.AdditionalDirectories.Directories; len(got) != 2 || got[0] != "/tmp/new-a" || got[1] != "/tmp/new-b" {
		t.Fatalf("legacy directories = %#v", got)
	}

	eventState, ok := bindings["event-bindings"]
	if !ok {
		t.Fatal("event store session missing from batch")
	}
	if !eventState.HasModelChange || eventState.ModelChange.Provider != "event-provider" || eventState.ModelChange.ModelID != "event-model" {
		t.Fatalf("event store model binding = %#v", eventState)
	}
	if !eventState.HasModeChange || eventState.ModeChange.Mode != "plan" {
		t.Fatalf("event store mode binding = %#v", eventState)
	}
	if !eventState.HasThinkingLevelChange || eventState.ThinkingLevelChange.ThinkingLevel != "medium" {
		t.Fatalf("event store thinking binding = %#v", eventState)
	}
	if !eventState.HasAdditionalDirectories || len(eventState.AdditionalDirectories.Directories) != 1 || eventState.AdditionalDirectories.Directories[0] != "/tmp/event" {
		t.Fatalf("event store directory binding = %#v", eventState)
	}

	if _, ok := bindings["missing"]; ok {
		t.Fatal("missing session should not carry a binding entry")
	}
}

func TestDeferredEventStorePreviewFillsCurrentPage(t *testing.T) {
	sessionDir := t.TempDir()
	manager := New("/tmp/deferred-preview", sessionDir)
	if err := manager.InitWithID("deferred-preview"); err != nil {
		t.Fatal(err)
	}
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireMutation(sessionDir, "deferred-preview")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("hello from the event store")); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	if _, err := manager.AppendSessionTitle("Deferred title", "manual"); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	guard.Release()

	details, err := ListAllDetailed(sessionDir, WithDeferredEventStorePreview())
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 1 {
		t.Fatalf("details = %#v", details)
	}
	if details[0].Preview != "" || details[0].Name != "" {
		t.Fatalf("deferred projection unexpectedly loaded EventStore content: %#v", details[0])
	}

	FillSessionDetailPreviews(sessionDir, details)
	if details[0].Preview != "hello from the event store" {
		t.Fatalf("preview = %q", details[0].Preview)
	}
	if details[0].Name != "Deferred title" {
		t.Fatalf("name = %q", details[0].Name)
	}
}
