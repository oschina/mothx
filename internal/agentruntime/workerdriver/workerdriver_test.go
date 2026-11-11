package workerdriver

import (
	"strings"
	"testing"
)

func advertisedSession(names ...string) map[string]any {
	session := map[string]any{}
	for _, name := range names {
		session[name] = map[string]any{}
	}
	return map[string]any{"sessionCapabilities": session}
}

func TestGenericResolveUsesAdvertisedAsUpperBound(t *testing.T) {
	adapter := Lookup(WorkerDriverGenericACP)
	caps := adapter.Resolve(ResolveInput{
		Profile:    Profile{ID: "worker", Driver: WorkerDriverGenericACP, TerminalAccess: true},
		Advertised: advertisedSession("resume", "close", "additionalDirectories"),
		Policy:     Policy{BridgeAvailable: true, FSReadAuthorized: true, FSWriteAuthorized: true, TerminalAuthorized: true},
	})
	if !caps.SessionResume || !caps.SessionClose || !caps.AdditionalDirectories {
		t.Fatalf("advertised session capabilities were not resolved: %#v", caps)
	}
	if caps.SessionLoad || caps.SessionFork {
		t.Fatalf("unadvertised session capabilities were granted: %#v", caps)
	}
	if caps.PromptImage || caps.PromptAudio || caps.PromptEmbeddedContext {
		t.Fatalf("unadvertised prompt capabilities were granted: %#v", caps)
	}
	if caps.Source != "negotiated" {
		t.Fatalf("source = %q, want negotiated", caps.Source)
	}
}

func TestGenericResolveLoadLegacySpelling(t *testing.T) {
	caps := Lookup(WorkerDriverGenericACP).Resolve(ResolveInput{
		Profile:    Profile{ID: "worker", TerminalAccess: true},
		Advertised: map[string]any{"loadSession": true},
		Policy:     Policy{FSReadAuthorized: true, FSWriteAuthorized: true, TerminalAuthorized: true},
	})
	if !caps.SessionLoad {
		t.Fatalf("legacy loadSession advertisement was ignored: %#v", caps)
	}
}

func TestGenericResolvePromptCapabilities(t *testing.T) {
	caps := Lookup(WorkerDriverGenericACP).Resolve(ResolveInput{
		Profile: Profile{ID: "worker", TerminalAccess: true},
		Advertised: map[string]any{
			"promptCapabilities": map[string]any{"image": true, "audio": true, "embeddedContext": true},
		},
		Policy: Policy{FSReadAuthorized: true, FSWriteAuthorized: true, TerminalAuthorized: true},
	})
	if !caps.PromptImage || !caps.PromptAudio || !caps.PromptEmbeddedContext {
		t.Fatalf("advertised prompt capabilities missing: %#v", caps)
	}
}

func TestRemoteTransportDegradesMCPBridge(t *testing.T) {
	caps := Lookup(WorkerDriverGenericACP).Resolve(ResolveInput{
		Profile:    Profile{ID: "remote", Remote: true},
		Advertised: advertisedSession("resume"),
	})
	if caps.MCPHTTP || caps.MCPSSE || caps.MCPStdio {
		t.Fatalf("remote transport kept an MCP transport: %#v", caps)
	}
	if !containsDegradation(caps.Degradations, "remote transport cannot reach host loopback") {
		t.Fatalf("remote degradation missing: %v", caps.Degradations)
	}
	if caps.Source != "degraded" {
		t.Fatalf("source = %q, want degraded", caps.Source)
	}
}

func TestBridgePolicyAndProfileDisabledDegrade(t *testing.T) {
	unavailable := Lookup(WorkerDriverGenericACP).Resolve(ResolveInput{
		Profile: Profile{ID: "worker"},
		Policy:  Policy{BridgeAvailable: false},
	})
	if unavailable.MCPHTTP {
		t.Fatal("bridge was resolved available without a listener")
	}
	disabled := Lookup(WorkerDriverGenericACP).Resolve(ResolveInput{
		Profile: Profile{ID: "worker", MCPBridge: "off"},
		Policy:  Policy{BridgeAvailable: true},
	})
	if disabled.MCPHTTP {
		t.Fatal("profile mcpBridge=off did not disable the bridge")
	}
	enabled := Lookup(WorkerDriverGenericACP).Resolve(ResolveInput{
		Profile: Profile{ID: "worker"},
		Policy:  Policy{BridgeAvailable: true},
	})
	if !enabled.MCPHTTP {
		t.Fatalf("local worker with a live bridge did not resolve MCPHTTP: %#v", enabled)
	}
}

func TestFilesystemAndTerminalNeedPolicy(t *testing.T) {
	caps := Lookup(WorkerDriverGenericACP).Resolve(ResolveInput{
		Profile: Profile{ID: "worker", TerminalAccess: true},
		Policy:  Policy{FSReadAuthorized: true, FSWriteAuthorized: false, TerminalAuthorized: false},
	})
	if !caps.FSReadText {
		t.Fatal("authorized read bridge was not resolved")
	}
	if caps.FSWriteText {
		t.Fatal("unauthorized write bridge was granted")
	}
	if caps.Terminal {
		t.Fatal("terminal was granted without Runtime authorization")
	}

	noOptIn := Lookup(WorkerDriverGenericACP).Resolve(ResolveInput{
		Profile: Profile{ID: "worker", TerminalAccess: false},
		Policy:  Policy{TerminalAuthorized: true},
	})
	if noOptIn.Terminal {
		t.Fatal("terminal was granted without profile opt-in")
	}
}

func TestOverridesOnlyTighten(t *testing.T) {
	caps := Lookup(WorkerDriverGenericACP).Resolve(ResolveInput{
		Profile: Profile{
			ID: "worker",
			CapabilityOverrides: map[string]bool{
				"promptImage": false,
				// A true override cannot widen because configuration validation
				// rejects it; the resolver still must not enable anything.
				"promptAudio": true,
			},
		},
		Advertised: map[string]any{
			"promptCapabilities": map[string]any{"image": true},
		},
	})
	if caps.PromptImage {
		t.Fatal("override did not tighten promptImage")
	}
	if caps.PromptAudio {
		t.Fatal("a true override widened an unadvertised capability")
	}
	if !containsDegradation(caps.Degradations, "promptImage: disabled by capabilityOverrides") {
		t.Fatalf("override degradation missing: %v", caps.Degradations)
	}
}

func TestLookupFallsBackToGeneric(t *testing.T) {
	for _, driver := range []WorkerDriver{"", "unknown-acp", WorkerDriverCodexACP} {
		if got := Lookup(driver).Driver(); got != WorkerDriverGenericACP {
			t.Fatalf("Lookup(%q) driver = %q, want generic fallback", driver, got)
		}
	}
	if got := Lookup(WorkerDriverGenericACP).Driver(); got != WorkerDriverGenericACP {
		t.Fatalf("generic lookup = %q", got)
	}
}

func TestMCPCapabilitiesAdvertisementNarrows(t *testing.T) {
	caps := Lookup(WorkerDriverGenericACP).Resolve(ResolveInput{
		Profile: Profile{ID: "worker", TerminalAccess: true},
		Advertised: map[string]any{
			"mcpCapabilities": map[string]any{"http": false, "stdio": true},
		},
		Policy: Policy{BridgeAvailable: true, FSReadAuthorized: true, FSWriteAuthorized: true, TerminalAuthorized: true},
	})
	if caps.MCPHTTP {
		t.Fatal("advertised mcpCapabilities.http=false was ignored")
	}
	if !caps.MCPStdio {
		t.Fatal("advertised stdio capability was lost")
	}
}

func containsDegradation(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}
