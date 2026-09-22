package openaiapi

// WebUI ESM control uses the same /esm slash command as the TUI instead of
// dedicated graphical controls. The command runs through the shared Server
// ESM operations (objective API), so chat input and API stay on one path.

import (
	"context"
	"errors"
	"strings"

	"github.com/oschina/mothx/internal/esm"
)

// cmdESM implements the /esm slash command with TUI parity. input is the full
// trimmed command line; objective and guidance text keep their raw spacing.
func (s *Server) cmdESM(sess *APISession, input string) *CommandResult {
	if sess == nil || sess.ID == "" {
		return &CommandResult{Message: "No active session for ESM.", Error: true}
	}
	sessionID := sess.ID
	store := s.esmStore()
	if store == nil {
		return &CommandResult{Message: "ESM storage is unavailable.", Error: true}
	}
	raw := strings.TrimSpace(strings.TrimPrefix(input, "/esm"))
	if raw == "" || raw == "status" {
		obj, err := store.Get(context.Background(), sessionID)
		if errors.Is(err, esm.ErrNotFound) {
			return &CommandResult{Message: "Enable Supervisor Mode\nStatus: none\n\nCreate one with /esm <objective>."}
		}
		if err != nil {
			return &CommandResult{Message: esmCommandErrorText(err), Error: true}
		}
		return &CommandResult{Message: formatESMCommandStatus(obj)}
	}

	sub, rest := splitESMCommand(raw)
	var (
		err  error
		note string
	)
	switch sub {
	case "edit":
		if rest == "" {
			return &CommandResult{Message: "Usage: /esm edit <objective>", Error: true}
		}
		_, err = s.EditESM(sessionID, rest)
	case "pause":
		if rest != "" {
			return &CommandResult{Message: "Usage: /esm pause", Error: true}
		}
		_, err = s.PauseESM(sessionID)
	case "resume":
		if rest != "" {
			return &CommandResult{Message: "Usage: /esm resume", Error: true}
		}
		_, err = s.ResumeESM(sessionID)
	case "clear":
		if rest != "" {
			return &CommandResult{Message: "Usage: /esm clear", Error: true}
		}
		if err = s.ClearESM(sessionID); err == nil {
			return &CommandResult{Message: "Enable Supervisor Mode cleared."}
		}
	case "guide":
		if rest == "" {
			return &CommandResult{Message: "Usage: /esm guide <text>", Error: true}
		}
		_, err = s.AddESMGuidance(sessionID, "", rest)
		note = "Guidance queued for the next ESM role run."
	default:
		_, err = s.CreateESM(sessionID, raw)
	}
	if err != nil {
		return &CommandResult{Message: esmCommandErrorText(err), Error: true}
	}
	obj, getErr := store.Get(context.Background(), sessionID)
	if getErr != nil {
		return &CommandResult{Message: esmCommandErrorText(getErr), Error: true}
	}
	message := formatESMCommandStatus(obj)
	if note != "" {
		message = note + "\n" + message
	}
	return &CommandResult{Message: message}
}

func splitESMCommand(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	idx := strings.IndexAny(raw, " \t")
	if idx < 0 {
		return raw, ""
	}
	return raw[:idx], strings.TrimSpace(raw[idx+1:])
}

func formatESMCommandStatus(obj *esm.Objective) string {
	return esm.FormatObjective(obj) + "\n\nCommands: /esm edit <objective>, /esm pause, /esm resume, /esm clear, /esm guide <text>"
}

func esmCommandErrorText(err error) string {
	switch {
	case errors.Is(err, esm.ErrNotFound):
		return "No ESM objective. Create one with /esm <objective>."
	case errors.Is(err, esm.ErrObjectiveExists):
		return "An unfinished ESM objective already exists. Use /esm edit <objective> or /esm clear."
	case errors.Is(err, esm.ErrInvalidObjective):
		return "ESM objective cannot be empty."
	case errors.Is(err, esm.ErrInvalidTransition):
		return "ESM status cannot be changed that way."
	default:
		return err.Error()
	}
}
