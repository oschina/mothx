package session

import (
	"context"
	"fmt"
	"github.com/startvibecoding/mothx/internal/dao"
	"path/filepath"
	"strings"
	"time"
)

// Binding describes a current external channel binding.
type Binding struct {
	SessionID   string `json:"sessionId"`
	ChannelType string `json:"channelType"`
	ChannelID   string `json:"channelId"`
}

// ChannelToolConfig describes one persisted tool selection for a channel session.
type ChannelToolConfig struct {
	ToolName string `json:"toolName"`
	Enabled  bool   `json:"enabled"`
}

func ListChannelTools(sessionDir, sessionID string) ([]ChannelToolConfig, error) {
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	records, err := dao.NewBindingDAO(db.Bun()).ListChannelTools(context.Background(), sessionID)
	if err != nil {
		return nil, err
	}
	result := make([]ChannelToolConfig, 0, len(records))
	for _, record := range records {
		result = append(result, ChannelToolConfig{ToolName: record.ToolName, Enabled: record.Enabled})
	}
	return result, nil
}

func SetChannelTools(sessionDir, sessionID string, tools []ChannelToolConfig) error {
	if sessionID == "" {
		return fmt.Errorf("session ID is required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	records := make([]dao.ChannelToolRecord, 0, len(tools))
	for _, item := range tools {
		name := strings.TrimSpace(item.ToolName)
		if name == "" {
			continue
		}
		records = append(records, dao.ChannelToolRecord{ToolName: name, Enabled: item.Enabled})
	}
	return dao.NewBindingDAO(db.Bun()).SetChannelTools(context.Background(), sessionID, records)
}

func GetChannelToolGeneration(sessionDir, sessionID string) (int64, error) {
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return 0, err
	}
	return dao.NewBindingDAO(db.Bun()).ChannelToolGeneration(context.Background(), sessionID)
}

func ListBindings(sessionDir string) ([]Binding, error) {
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	records, err := dao.NewBindingDAO(db.Bun()).List(context.Background())
	if err != nil {
		return nil, fmt.Errorf("list session bindings: %w", err)
	}
	result := make([]Binding, 0, len(records))
	for _, record := range records {
		result = append(result, Binding{SessionID: record.SessionID, ChannelType: record.ChannelType, ChannelID: record.ChannelID})
	}
	return result, nil
}

func FindBinding(sessionDir, channelType, channelID string) (*Binding, error) {
	if err := validateBinding(channelType, channelID); err != nil {
		return nil, err
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	record, err := dao.NewBindingDAO(db.Bun()).Find(context.Background(), channelType, channelID)
	if err == dao.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find session binding: %w", err)
	}
	return &Binding{SessionID: record.SessionID, ChannelType: record.ChannelType, ChannelID: record.ChannelID}, nil
}

// FindBindingBySessionID returns the current external binding for a session.
func FindBindingBySessionID(sessionDir, sessionID string) (*Binding, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session ID is required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	record, err := dao.NewBindingDAO(db.Bun()).FindBySession(context.Background(), sessionID)
	if err == dao.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find session binding: %w", err)
	}
	return &Binding{SessionID: record.SessionID, ChannelType: record.ChannelType, ChannelID: record.ChannelID}, nil
}

func BindSession(sessionDir, sessionID, channelType, channelID string) error {
	if sessionID == "" {
		return fmt.Errorf("session ID is required")
	}
	if err := validateBinding(channelType, channelID); err != nil {
		return err
	}
	if channelType == "local" {
		return fmt.Errorf("use UnbindSession to make a session local")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	return dao.NewBindingDAO(db.Bun()).Bind(context.Background(), sessionID, channelType, channelID)
}

// UnbindSession makes a channel-bound session local while retaining its history.
func UnbindSession(sessionDir, sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("session ID is required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	return dao.NewBindingDAO(db.Bun()).Unbind(context.Background(), sessionID)
}

// TransferBinding atomically moves a channel identity from one session to another.
func TransferBinding(sessionDir, channelType, channelID, fromSessionID, toSessionID string) error {
	if channelType != "wechat" && channelType != "feishu" {
		return fmt.Errorf("unsupported channel type %q", channelType)
	}
	if channelID == "" || fromSessionID == "" || toSessionID == "" {
		return fmt.Errorf("channel ID and session IDs are required")
	}
	if fromSessionID == toSessionID {
		return fmt.Errorf("source and target sessions must differ")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	return dao.NewBindingDAO(db.Bun()).Transfer(context.Background(), channelType, channelID, fromSessionID, toSessionID)
}

// RotateBoundSession atomically creates a new bound session and clears the old one.
func RotateBoundSession(workDir, sessionDir, channelType, channelID, oldSessionID string) (*Manager, error) {
	if err := validateBinding(channelType, channelID); err != nil {
		return nil, err
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	id := GenerateID()
	if err := dao.NewBindingDAO(db.Bun()).Rotate(context.Background(), workDir, channelType, channelID, oldSessionID, CurrentVersion, id, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return nil, err
	}
	return OpenByIDExact(sessionDir, id)
}

func CreateBound(workDir, sessionDir, channelType, channelID string) (*Manager, error) {
	if err := validateBinding(channelType, channelID); err != nil {
		return nil, err
	}
	m := New(workDir, sessionDir)
	if err := m.InitWithBinding(channelType, channelID); err != nil {
		return nil, err
	}
	return m, nil
}

// SetSessionBinding updates a Manager's in-memory header after a binding change.
func (m *Manager) SetSessionBinding(channelType, channelID string) error {
	if err := validateBinding(channelType, channelID); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.header == nil {
		return fmt.Errorf("session is not initialized")
	}
	m.header.ChannelType, m.header.ChannelID = channelType, channelID
	return nil
}

// SetExpertBinding updates the session's expert binding (empty string
// unbinds). It patches the in-memory header and persists the sessions row so
// reloads restore the identity; callers rebuild the Agent afterwards (same
// lifecycle as capability toggles such as /delegate).
func (m *Manager) SetExpertBinding(expertID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.header == nil {
		return fmt.Errorf("session is not initialized")
	}
	sessionID := m.header.ID
	table := m.sessionTable()
	if err := m.withDB(func(db *dao.Database) error {
		return dao.NewSessionDAO(db.Bun()).UpdateSessionExpertID(context.Background(), db.Bun(), table, sessionID, expertID)
	}); err != nil {
		return err
	}
	m.header.ExpertID = expertID
	return nil
}

// SetWorkDir changes the persisted working directory of an idle session. The
// caller is responsible for ensuring that the new directory is authorized and
// for rebuilding any Runtime resources that were assembled for the old one.
func (m *Manager) SetWorkDir(cwd string) error {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" || !filepath.IsAbs(cwd) {
		return fmt.Errorf("session work directory must be an absolute path")
	}
	cwd = filepath.Clean(cwd)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.header == nil {
		return fmt.Errorf("session is not initialized")
	}
	if m.header.Cwd == cwd {
		m.cwd = cwd
		return nil
	}
	sessionID := m.header.ID
	if err := m.withDB(func(db *dao.Database) error {
		return dao.NewSessionDAO(db.Bun()).UpdateSessionCWD(context.Background(), db.Bun(), m.sessionTable(), sessionID, cwd)
	}); err != nil {
		return err
	}
	m.header.Cwd = cwd
	m.cwd = cwd
	return nil
}

// GetExpertID returns the bound expert bundle name ("" when unbound).
func (m *Manager) GetExpertID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.header == nil {
		return ""
	}
	return m.header.ExpertID
}
