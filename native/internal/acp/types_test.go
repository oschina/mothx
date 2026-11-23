package acp

import (
	"encoding/json"
	"strings"
	"testing"
)

// These keys are part of the ACP v1 and additive mothx.dev wire contract.
// Type renames inside the private package must never leak into JSON tags.
func TestWireKeyCompatibility(t *testing.T) {
	update, err := json.Marshal(SessionUpdate{SessionUpdate: "agent_message_chunk", MessageID: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(update), `"sessionUpdate":`) {
		t.Fatalf("session update wire key changed: %s", update)
	}
	if strings.Contains(string(update), `"SessionUpdate":`) {
		t.Fatalf("session update leaked Go field casing: %s", update)
	}

	statusLine, err := json.Marshal(ApplicationStatusLineView{Command: "echo hi", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(statusLine), `"command":`) {
		t.Fatalf("status line command wire key changed: %s", statusLine)
	}
}
