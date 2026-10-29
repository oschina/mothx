package serve

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/oschina/mothx/internal/terminal"
	"golang.org/x/net/websocket"
)

const (
	terminalFrameInput  = 0x01
	terminalFrameResize = 0x02
	terminalFramePing   = 0x03

	terminalFrameOutput  = 0x01
	terminalFrameExit    = 0x02
	terminalFrameControl = 0x03
)

type terminalCreateRequest struct {
	SessionID string `json:"sessionId"`
	Cols      uint16 `json:"cols"`
	Rows      uint16 `json:"rows"`
}

type terminalInfo struct {
	TerminalID    string         `json:"terminalId"`
	SessionID     string         `json:"sessionId"`
	Cwd           string         `json:"cwd"`
	Shell         string         `json:"shell"`
	Cols          uint16         `json:"cols"`
	Rows          uint16         `json:"rows"`
	State         terminal.State `json:"state"`
	TerminalToken string         `json:"terminalToken,omitempty"`
}

func (rt *channelRuntime) handleTerminals(sessions activeSessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rt == nil || rt.terminals == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "terminal backend unavailable", "code": "terminal_unsupported"})
			return
		}
		switch r.Method {
		case http.MethodGet:
			sessionID := strings.TrimSpace(r.URL.Query().Get("sessionId"))
			list := rt.terminals.List(sessionID)
			out := make([]terminalInfo, 0, len(list))
			for _, t := range list {
				cols, rows := t.Size()
				out = append(out, terminalInfo{
					TerminalID: t.ID, SessionID: t.SessionID, Cwd: t.Cwd, Shell: t.Shell,
					Cols: cols, Rows: rows, State: t.State(),
				})
			}
			writeJSON(w, http.StatusOK, map[string]any{"terminals": out})
		case http.MethodPost:
			enabled, reason := EffectiveTerminalEnabled(rt.configSnapshot())
			if !enabled {
				code := "terminal_disabled"
				if reason == "unsafe" {
					code = "terminal_unsafe"
				}
				writeJSON(w, http.StatusForbidden, map[string]string{"error": code, "code": code})
				return
			}
			if sessions == nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "API server not ready"})
				return
			}
			var body terminalCreateRequest
			if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON", "code": "invalid_request"})
				return
			}
			if body.Cols == 0 {
				body.Cols = 80
			}
			if body.Rows == 0 {
				body.Rows = 24
			}
			if body.Cols < 20 || body.Cols > 500 || body.Rows < 5 || body.Rows > 300 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid terminal size", "code": "invalid_size"})
				return
			}
			cwd, err := sessions.GetSessionWorkDir(body.SessionID)
			if err != nil || cwd == "" {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found", "code": "session_not_found"})
				return
			}
			term, err := rt.terminals.Create(body.SessionID, cwd, body.Cols, body.Rows)
			if err != nil {
				switch {
				case errors.Is(err, terminal.ErrLimit):
					writeJSON(w, http.StatusConflict, map[string]string{"error": "terminal limit reached", "code": "terminal_limit"})
				case errors.Is(err, terminal.ErrUnsupported):
					writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "terminal unsupported", "code": "terminal_unsupported"})
				default:
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error(), "code": "terminal_error"})
				}
				return
			}
			// Config can change while the PTY is starting. Re-check after creation
			// so a disable/unsafe transition cannot leave an orphaned shell behind.
			if enabled, reason := EffectiveTerminalEnabled(rt.configSnapshot()); !enabled {
				_ = rt.terminals.CloseByID(term.ID)
				code := "terminal_disabled"
				if reason == "unsafe" {
					code = "terminal_unsafe"
				}
				writeJSON(w, http.StatusForbidden, map[string]string{"error": code, "code": code})
				return
			}
			cols, rows := term.Size()
			info := terminalInfo{
				TerminalID: term.ID, SessionID: term.SessionID, Cwd: term.Cwd, Shell: term.Shell,
				Cols: cols, Rows: rows, State: term.State(),
			}
			// A browser already has an HttpOnly cookie session, so it does not
			// need a long-lived bearer token in a JSON body. Only non-browser
			// callers (auth disabled, or Bearer-authenticated CLI clients) need
			// the token to attach a WebSocket without a browser Origin.
			if terminalTokenExposed(r, rt.configSnapshot()) {
				info.TerminalToken = term.Token
			}
			writeJSON(w, http.StatusOK, info)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func terminalTokenExposed(r *http.Request, cfg *Config) bool {
	if r == nil {
		return false
	}
	if strings.TrimSpace(r.Header.Get("Authorization")) != "" {
		return true
	}
	// With auth disabled every caller is already authorized, and a non-browser
	// client still needs a token because it cannot present a same-origin
	// WebSocket Origin.
	return cfg == nil || !cfg.API.Auth.Enabled
}

func (rt *channelRuntime) handleTerminalByID(w http.ResponseWriter, r *http.Request) {
	if rt == nil || rt.terminals == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "terminal backend unavailable", "code": "terminal_unsupported"})
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/terminals/"), "/")
	if id == "" || strings.Contains(id, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "terminal id required", "code": "invalid_request"})
		return
	}
	switch r.Method {
	case http.MethodDelete:
		if err := rt.terminals.CloseByID(id); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "terminal not found", "code": "terminal_not_found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"terminalId": id, "closed": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (rt *channelRuntime) terminalHandshake(r *http.Request) error {
	if rt == nil || rt.terminals == nil {
		return terminal.ErrUnsupported
	}
	if enabled, reason := EffectiveTerminalEnabled(rt.configSnapshot()); !enabled {
		return fmt.Errorf("terminal disabled: %s", reason)
	}
	id := terminalIDFromPath(r.URL.Path)
	if id == "" || strings.Contains(id, "/") {
		return terminal.ErrNotFound
	}
	token := terminalTokenFromRequest(r)
	if token != "" {
		if _, err := rt.terminals.Authenticate(id, token); err != nil {
			return err
		}
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin != "" {
		if !rt.terminalOriginAllowed(r, origin) {
			return errors.New("terminal origin not allowed")
		}
		return nil
	}
	if token == "" {
		return errors.New("terminal origin required")
	}
	return nil
}

func (rt *channelRuntime) terminalOriginAllowed(r *http.Request, origin string) bool {
	cfg := rt.configSnapshot()
	if cfg == nil {
		return false
	}
	if len(cfg.Terminal.AllowedOrigins) > 0 {
		for _, allowed := range cfg.Terminal.AllowedOrigins {
			if origin == allowed {
				return true
			}
		}
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); forwarded != "" {
		scheme = forwarded
	}
	return strings.EqualFold(u.Scheme, scheme) && strings.EqualFold(u.Host, r.Host)
}

func terminalIDFromPath(path string) string {
	const prefix = "/ws/terminals/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	return strings.Trim(strings.TrimPrefix(path, prefix), "/")
}

func terminalTokenFromRequest(r *http.Request) string {
	if token := strings.TrimSpace(r.URL.Query().Get("token")); token != "" {
		return token
	}
	for _, protocol := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
		protocol = strings.TrimSpace(protocol)
		if strings.HasPrefix(protocol, "terminal-token.") {
			return strings.TrimPrefix(protocol, "terminal-token.")
		}
	}
	return ""
}

func (rt *channelRuntime) handleTerminalWebSocket(w http.ResponseWriter, r *http.Request) {
	if rt == nil || rt.terminals == nil {
		http.Error(w, "terminal backend unavailable", http.StatusServiceUnavailable)
		return
	}
	websocket.Server{
		Handler:   rt.serveTerminalWebSocket,
		Handshake: func(_ *websocket.Config, req *http.Request) error { return rt.terminalHandshake(req) },
	}.ServeHTTP(w, r)
}

func (rt *channelRuntime) serveTerminalWebSocket(ws *websocket.Conn) {
	defer ws.Close()
	id := terminalIDFromPath(ws.Request().URL.Path)
	term, err := rt.terminals.GetByID(id)
	if err != nil {
		_ = sendTerminalControl(ws, map[string]any{"type": "error", "message": "terminal not found"})
		return
	}
	sub, err := term.Attach()
	if err != nil {
		_ = sendTerminalControl(ws, map[string]any{"type": "error", "message": err.Error()})
		return
	}
	defer term.Detach(sub)
	cols, rows := term.Size()
	if err := sendTerminalControl(ws, map[string]any{
		"type": "attached", "terminalId": term.ID, "cols": cols, "rows": rows,
		"state": term.State(), "replayBytes": len(sub.Replay()), "throttled": sub.Throttled(),
	}); err != nil {
		return
	}
	if replay := sub.Replay(); len(replay) > 0 {
		if err := sendTerminalOutput(ws, replay); err != nil {
			return
		}
	}
	if err := sendTerminalControl(ws, map[string]any{"type": "replay-done"}); err != nil {
		return
	}

	readDone := make(chan struct{})
	controlCh := make(chan any, 8)
	go func() {
		defer close(readDone)
		for {
			var msg []byte
			if err := websocket.Message.Receive(ws, &msg); err != nil {
				return
			}
			if len(msg) == 0 {
				continue
			}
			// Text JSON is accepted for compatibility/debugging clients. The
			// browser path uses the compact binary frames below.
			if msg[0] == '{' {
				var ctl struct {
					Type string `json:"type"`
					Cols uint16 `json:"cols"`
					Rows uint16 `json:"rows"`
					Data string `json:"data"`
				}
				if json.Unmarshal(msg, &ctl) == nil {
					switch ctl.Type {
					case "input":
						if ctl.Data != "" {
							if err := term.Write([]byte(ctl.Data)); err != nil {
								return
							}
						}
					case "resize":
						if ctl.Cols > 0 && ctl.Rows > 0 {
							_ = term.Resize(ctl.Cols, ctl.Rows)
						}
					case "ping":
						select {
						case controlCh <- map[string]any{"type": "pong"}:
						default:
						}
					}
					continue
				}
			}
			switch msg[0] {
			case terminalFrameInput:
				if len(msg) > 1 {
					if err := term.Write(msg[1:]); err != nil {
						return
					}
				}
			case terminalFrameResize:
				if len(msg) >= 5 {
					cols := binary.BigEndian.Uint16(msg[1:3])
					rows := binary.BigEndian.Uint16(msg[3:5])
					_ = term.Resize(cols, rows)
				}
			case terminalFramePing:
				select {
				case controlCh <- map[string]any{"type": "pong"}:
				default:
				}
			}
		}
	}()

	for {
		select {
		case data, ok := <-sub.Output():
			if !ok {
				return
			}
			if err := sendTerminalOutput(ws, data); err != nil {
				return
			}
		case <-sub.Stop():
			switch sub.Reason() {
			case "exited":
				_ = sendTerminalExit(ws, term.ExitCode())
			case "takeover":
				_ = sendTerminalControl(ws, map[string]any{"type": "terminal_takeover"})
			default:
				_ = sendTerminalControl(ws, map[string]any{"type": "closed", "reason": sub.Reason()})
			}
			return
		case control := <-sub.Control():
			if err := sendTerminalControl(ws, map[string]any{"type": control.Type, "active": control.Active}); err != nil {
				return
			}
		case control := <-controlCh:
			if err := sendTerminalControl(ws, control); err != nil {
				return
			}
		case <-readDone:
			return
		}
	}
}

func sendTerminalOutput(ws *websocket.Conn, data []byte) error {
	frame := make([]byte, 1+len(data))
	frame[0] = terminalFrameOutput
	copy(frame[1:], data)
	return websocket.Message.Send(ws, frame)
}

func sendTerminalExit(ws *websocket.Conn, code int) error {
	if code < 0 {
		code = 0
	}
	frame := make([]byte, 5)
	frame[0] = terminalFrameExit
	binary.BigEndian.PutUint32(frame[1:], uint32(code))
	return websocket.Message.Send(ws, frame)
}

func sendTerminalControl(ws *websocket.Conn, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	frame := append([]byte{terminalFrameControl}, payload...)
	return websocket.Message.Send(ws, frame)
}
