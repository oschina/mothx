package workerdriver

// genericACP is the only required adapter. It reads exactly what the Agent
// advertised and applies the shared client baseline; it never guesses a
// product-specific behavior.
type genericACP struct{}

func (genericACP) Driver() WorkerDriver { return WorkerDriverGenericACP }

// Baseline describes the shared ACP client's own abilities. An advertised
// capability is still required for anything the Agent must implement; the
// baseline only says this client can use it when offered.
func (genericACP) Baseline() WorkerCapabilities {
	return WorkerCapabilities{
		// Session capabilities are Agent features: the client supports all of
		// them, the Agent advertises which it implements.
		SessionResume:         true,
		SessionLoad:           true,
		SessionClose:          true,
		SessionFork:           true,
		AdditionalDirectories: true,
		// Prompt content is encoded by the client; the Agent advertises which
		// content blocks it accepts.
		PromptImage:           true,
		PromptAudio:           true,
		PromptEmbeddedContext: true,
		// ACP session/new always carries mcpServers, so HTTP/stdio are usable
		// unless the Agent explicitly narrows mcpCapabilities. Policy decides
		// whether injection actually happens.
		MCPHTTP:  true,
		MCPStdio: true,
		// The client owns these bridges; Runtime policy grants or withholds them.
		FSReadText:  true,
		FSWriteText: true,
		Terminal:    true,
		// Thought chunks have no advertisement in ACP v1; accepted as an
		// optional diagnostic only when the Agent says so.
		ThoughtStream:         true,
		TerminalStatusQuality: "none",
		Source:                "profile",
	}
}

func (g genericACP) Resolve(in ResolveInput) WorkerCapabilities {
	return resolveGeneric(g.Baseline(), in)
}
