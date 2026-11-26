package desktop

import "strings"

// databaseIntegrityPayload is the additive mothx/database/integrity
// notification: the outcome of a session database integrity check that
// finished after the Runtime had already started. A large database is checked
// behind the handshake, so its verdict arrives beside the connection instead of
// through it or through a startup error. It is a read-only projection: the
// Runtime owns the verdict and has already done what it can, which for a stale
// index means rebuilding it before the notification is sent.
type databaseIntegrityPayload struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Detail string `json:"detail"`
	Report string `json:"report,omitempty"`
	At     string `json:"at,omitempty"`
}

// The kinds a databaseIntegrityPayload can carry. Anything else is treated as
// a check that could not finish, which is the safe reading of an unknown
// outcome.
const (
	databaseIntegrityRepaired   = "index_repaired"
	databaseIntegrityUnrepaired = "index_unrepaired"
	databaseIntegrityCorrupt    = "corrupt"
)

// applyDatabaseIntegrity records every finding in the diagnostic log and puts
// the ones that need the user into the transcript. Before the check moved
// behind the handshake, a damaged database kept the Runtime from starting and
// the shell showed that as a connection failure; a Runtime that now starts
// must not look healthy only because it started. A rebuilt index changed no
// row and stays in the diagnostic log.
func (a *nativeApp) applyDatabaseIntegrity(payload databaseIntegrityPayload) {
	kind := strings.TrimSpace(payload.Kind)
	detail := strings.TrimSpace(payload.Detail)
	addDiagnostic(databaseIntegrityDiagnosticLevel(kind), "session database integrity check "+kind+": "+detail)
	switch kind {
	case databaseIntegrityRepaired:
		return
	case databaseIntegrityCorrupt:
		a.appendMessage(&chatMessage{
			ID:   a.nextLocalMessageID("system"),
			Kind: messageSystem,
			Text: T("会话数据库完整性检查失败：") + detail + "\n" + T("请停止 MothX，备份数据库并恢复一份已知正常的副本；不要删除它。"),
			Done: true,
		})
	case databaseIntegrityUnrepaired:
		a.appendMessage(&chatMessage{
			ID:   a.nextLocalMessageID("system"),
			Kind: messageSystem,
			Text: T("会话数据库有一个失效索引未能重建：") + detail + "\n" + T("经该索引的查询可能缺失结果；重启 MothX 会再次尝试重建。"),
			Done: true,
		})
	default:
		a.appendMessage(&chatMessage{
			ID:   a.nextLocalMessageID("system"),
			Kind: messageSystem,
			Text: T("会话数据库完整性检查未能完成：") + detail + "\n" + T("请运行 mothx doctor 检查数据库。"),
			Done: true,
		})
	}
}

func databaseIntegrityDiagnosticLevel(kind string) string {
	if kind == databaseIntegrityRepaired {
		return "info"
	}
	return "error"
}
