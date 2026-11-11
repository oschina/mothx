package workerdriver

// resolveGeneric is the shared three-way intersection used by the built-in
// adapters. Driver-specific adapters may adjust a quirk, but they must not turn
// a capability on that this function resolved to false.
//
// Discipline:
//   - Advertised is an upper bound for capabilities the Agent declares
//     (session resume/load/close/fork, prompt media, MCP transports). Absence
//     means "not supported", except for the ACP-mandated mcpServers parameter
//     of session/new, which every Agent accepts unless it explicitly narrows it.
//   - The adapter baseline describes what the shared ACP client itself can do.
//     It never grants an advertised-only capability.
//   - Runtime policy is the final arbiter: remote transports cannot reach host
//     loopback, and the filesystem/terminal bridges need explicit authorization.
func resolveGeneric(baseline WorkerCapabilities, in ResolveInput) WorkerCapabilities {
	caps := baseline
	var degradations []string

	sessionCaps := mapValue(in.Advertised, "sessionCapabilities")
	caps.SessionResume = baseline.SessionResume && sessionCapPresent(sessionCaps, in.Advertised, "resume")
	caps.SessionLoad = baseline.SessionLoad &&
		(sessionCapPresent(sessionCaps, in.Advertised, "load") || boolValue(in.Advertised, "loadSession"))
	caps.SessionClose = baseline.SessionClose && sessionCapPresent(sessionCaps, in.Advertised, "close")
	caps.SessionFork = baseline.SessionFork && sessionCapPresent(sessionCaps, in.Advertised, "fork")
	caps.AdditionalDirectories = baseline.AdditionalDirectories &&
		sessionCapPresent(sessionCaps, in.Advertised, "additionalDirectories")

	promptCaps := mapValue(in.Advertised, "promptCapabilities")
	caps.PromptImage = baseline.PromptImage && boolValue(promptCaps, "image")
	caps.PromptAudio = baseline.PromptAudio && boolValue(promptCaps, "audio")
	caps.PromptEmbeddedContext = baseline.PromptEmbeddedContext && boolValue(promptCaps, "embeddedContext")

	// MCP transports: ACP session/new always carries mcpServers, so the client
	// baseline stays usable when the Agent advertises no mcpCapabilities map. An
	// explicit advertisement narrows it.
	if mcpCaps, ok := mapValueOK(in.Advertised, "mcpCapabilities"); ok {
		caps.MCPHTTP = baseline.MCPHTTP && boolValue(mcpCaps, "http")
		caps.MCPSSE = baseline.MCPSSE && boolValue(mcpCaps, "sse")
		caps.MCPStdio = baseline.MCPStdio && boolValue(mcpCaps, "stdio")
	}

	caps.ModelConfigOption = baseline.ModelConfigOption && advertisedConfigOptions(in.Advertised)
	caps.ThoughtStream = baseline.ThoughtStream && boolValue(in.Advertised, "thoughtStream")
	caps.NativeSubagents = baseline.NativeSubagents && boolValue(in.Advertised, "nativeSubagents")

	// --- Policy: final arbiter -------------------------------------------------
	if in.Profile.Remote {
		if caps.MCPHTTP {
			degradations = append(degradations, "mcpHttp: remote transport cannot reach host loopback")
		}
		caps.MCPHTTP, caps.MCPSSE, caps.MCPStdio = false, false, false
	}
	if in.Profile.MCPBridge == "off" {
		if caps.MCPHTTP {
			degradations = append(degradations, "mcpHttp: profile disabled the bridge")
		}
		caps.MCPHTTP = false
	}
	if !in.Policy.BridgeAvailable {
		if caps.MCPHTTP {
			degradations = append(degradations, "mcpHttp: no live bridge listener")
		}
		caps.MCPHTTP = false
	}
	// Filesystem/terminal policy denials are expected scoping, not degradations:
	// a read-only task legitimately has no write bridge, and a profile that did
	// not opt into terminal is not missing anything. Only an explicit opt-in that
	// Runtime then refuses is reported.
	if !in.Policy.FSReadAuthorized {
		caps.FSReadText = false
	}
	if !in.Policy.FSWriteAuthorized {
		caps.FSWriteText = false
	}
	if !in.Profile.TerminalAccess {
		caps.Terminal = false
	} else if !in.Policy.TerminalAuthorized {
		if caps.Terminal {
			degradations = append(degradations, "terminal: profile opted in but Runtime refused")
		}
		caps.Terminal = false
	}

	degradations = append(degradations, applyOverrides(&caps, in.Profile.CapabilityOverrides)...)

	if len(degradations) > 0 {
		caps.Source = "degraded"
	} else if len(in.Advertised) > 0 {
		caps.Source = "negotiated"
	} else {
		caps.Source = "profile"
	}
	caps.Degradations = degradations
	return caps.NormalizeBounds()
}

func sessionCapPresent(sessionCaps, advertised map[string]any, name string) bool {
	if sessionCaps != nil {
		if _, ok := sessionCaps[name]; ok {
			return true
		}
	}
	// Older agents expose load as a top-level loadSession bool; resume/close/fork
	// have no legacy spelling.
	if name == "load" {
		return boolValue(advertised, "loadSession")
	}
	return false
}

func advertisedConfigOptions(advertised map[string]any) bool {
	if advertised == nil {
		return false
	}
	switch value := advertised["configOptions"].(type) {
	case []any:
		return len(value) > 0
	case map[string]any:
		return len(value) > 0
	case bool:
		return value
	}
	return false
}

// applyOverrides tightens every explicitly disabled capability and reports each
// one. Configuration validation rejects a true override, so an override can
// never widen the resolved result.
func applyOverrides(caps *WorkerCapabilities, overrides map[string]bool) []string {
	if len(overrides) == 0 {
		return nil
	}
	var degradations []string
	set := func(name string, enabled *bool) {
		if enabled == nil {
			return
		}
		if value, ok := overrides[name]; ok && !value && *enabled {
			*enabled = false
			degradations = append(degradations, name+": disabled by capabilityOverrides")
		}
	}
	set("sessionResume", &caps.SessionResume)
	set("sessionLoad", &caps.SessionLoad)
	set("sessionClose", &caps.SessionClose)
	set("sessionFork", &caps.SessionFork)
	set("additionalDirectories", &caps.AdditionalDirectories)
	set("promptImage", &caps.PromptImage)
	set("promptAudio", &caps.PromptAudio)
	set("promptEmbeddedContext", &caps.PromptEmbeddedContext)
	set("mcpHttp", &caps.MCPHTTP)
	set("mcpSse", &caps.MCPSSE)
	set("mcpStdio", &caps.MCPStdio)
	set("fsReadText", &caps.FSReadText)
	set("fsWriteText", &caps.FSWriteText)
	set("terminal", &caps.Terminal)
	set("modelConfigOption", &caps.ModelConfigOption)
	set("thoughtStream", &caps.ThoughtStream)
	set("nativeSubagents", &caps.NativeSubagents)
	return degradations
}

func mapValue(values map[string]any, key string) map[string]any {
	value, _ := mapValueOK(values, key)
	return value
}

func mapValueOK(values map[string]any, key string) (map[string]any, bool) {
	if values == nil {
		return nil, false
	}
	raw, ok := values[key]
	if !ok {
		return nil, false
	}
	typed, ok := raw.(map[string]any)
	return typed, ok
}

func boolValue(values map[string]any, key string) bool {
	if values == nil {
		return false
	}
	value, ok := values[key].(bool)
	return ok && value
}
