package serve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

type ConfigLayer string

const (
	ConfigLayerGlobal   ConfigLayer = "global"
	ConfigLayerProject  ConfigLayer = "project"
	ConfigLayerExplicit ConfigLayer = "explicit"
)

// ServeConfigState separates the merged runtime configuration from the file
// layer edited by the WebUI. Runtime CLI overrides are never written back.
type ServeConfigState struct {
	mu            sync.RWMutex
	Effective     *Config
	WritablePath  string
	WritableLayer ConfigLayer
	overrides     RunOptions
	explicitPath  string
	// writeAtomic is injectable for transaction tests. Production instances
	// leave it nil and use atomicWritePrivateFile.
	writeAtomic func(string, []byte) error
}

func loadServeConfigState(opts RunOptions) (*ServeConfigState, error) {
	state := &ServeConfigState{overrides: opts, explicitPath: opts.ConfigPath}
	if opts.ConfigPath != "" {
		state.WritablePath = opts.ConfigPath
		state.WritableLayer = ConfigLayerExplicit
	} else if _, err := os.Stat(ProjectConfigPath()); err == nil {
		state.WritablePath = ProjectConfigPath()
		state.WritableLayer = ConfigLayerProject
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect project serve config: %w", err)
	} else {
		state.WritablePath = ConfigPath()
		state.WritableLayer = ConfigLayerGlobal
	}
	if err := state.Reload(); err != nil {
		return nil, err
	}
	return state, nil
}

func (s *ServeConfigState) Reload() error {
	if s == nil {
		return fmt.Errorf("serve config state is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reloadLocked()
}

func (s *ServeConfigState) reloadLocked() error {
	var (
		cfg *Config
		err error
	)
	if s.explicitPath != "" {
		cfg, err = LoadConfigFrom(s.explicitPath)
	} else {
		cfg, err = LoadConfig()
	}
	if err != nil {
		return err
	}
	applyOverrides(cfg, s.overrides)
	applyRuntimeFeatures(cfg)
	s.Effective = cfg
	return nil
}

// Snapshot returns an isolated runtime configuration snapshot. Callers may
// safely pass it to code that normalizes or annotates the configuration.
func (s *ServeConfigState) Snapshot() *Config {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneServeConfig(s.Effective)
}

func cloneServeConfig(cfg *Config) *Config {
	if cfg == nil {
		return nil
	}
	// Config.MarshalJSON normalizes its receiver in place. Marshal an alias so
	// taking a snapshot cannot change feature flags or other runtime fields.
	type configSnapshotAlias Config
	data, err := json.Marshal((*configSnapshotAlias)(cfg))
	if err != nil {
		return nil
	}
	var copy Config
	if err := json.Unmarshal(data, &copy); err != nil {
		return nil
	}
	return &copy
}

type channelConfigPatchResponse struct {
	Layer      ConfigLayer `json:"layer"`
	Path       string      `json:"path"`
	Platform   string      `json:"platform"`
	Configured any         `json:"configured"`
	Effective  any         `json:"effective"`
	Restart    any         `json:"restart"`
}

func parseChannelConfigPatch(platform string, body []byte) (map[string]any, error) {
	var patch map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&patch); err != nil {
		return nil, fmt.Errorf("decode %s channel config: %w", platform, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode %s channel config: multiple JSON values", platform)
		}
		return nil, fmt.Errorf("decode %s channel config: %w", platform, err)
	}
	if patch == nil {
		return nil, fmt.Errorf("channel config must be an object")
	}
	allowed := map[string]map[string]bool{
		"wechat": {"enabled": true, "credPath": true, "workDir": true, "autoTyping": true},
		"feishu": {"enabled": true, "appId": true, "appSecret": true, "workDir": true},
	}
	fields, ok := allowed[platform]
	if !ok {
		return nil, fmt.Errorf("unsupported channel %q", platform)
	}
	for key := range patch {
		if !fields[key] {
			return nil, fmt.Errorf("unsupported %s channel field %q", platform, key)
		}
	}
	if value, exists := patch["enabled"]; exists {
		if _, ok := value.(bool); !ok {
			return nil, fmt.Errorf("enabled must be a boolean")
		}
	}
	for _, key := range []string{"credPath", "workDir", "appId", "appSecret"} {
		if value, exists := patch[key]; exists {
			if _, ok := value.(string); !ok {
				return nil, fmt.Errorf("%s must be a string", key)
			}
		}
	}
	if value, exists := patch["autoTyping"]; exists {
		if _, ok := value.(bool); !ok {
			return nil, fmt.Errorf("autoTyping must be a boolean")
		}
	}
	return patch, nil
}

// UpdateChannel applies a channel merge-patch to the selected writable layer,
// applies the resulting effective runtime config, and restores the old file if
// runtime application fails. The callback must not perform network I/O.
func (s *ServeConfigState) UpdateChannel(platform string, body []byte, apply func(*Config) error) (channelConfigPatchResponse, error) {
	if s == nil {
		return channelConfigPatchResponse{}, fmt.Errorf("serve config state is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	patch, err := parseChannelConfigPatch(platform, body)
	if err != nil {
		return channelConfigPatchResponse{}, err
	}

	root := map[string]any{}
	var oldData []byte
	oldExists := false
	var oldInfo os.FileInfo
	if data, readErr := os.ReadFile(s.WritablePath); readErr == nil {
		oldExists = true
		oldData = append([]byte(nil), data...)
		oldInfo, _ = os.Stat(s.WritablePath)
		if len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, &root); err != nil {
				return channelConfigPatchResponse{}, fmt.Errorf("parse writable serve config %s: %w", s.WritablePath, err)
			}
		}
	} else if !os.IsNotExist(readErr) {
		return channelConfigPatchResponse{}, fmt.Errorf("read writable serve config %s: %w", s.WritablePath, readErr)
	}
	channelsObject := objectField(root, "channels")
	platformObject := objectField(channelsObject, platform)
	for key, value := range patch {
		platformObject[key] = value
	}
	featuresObject := objectField(root, "features")
	if enabled, ok := platformObject["enabled"].(bool); ok {
		featuresObject[platform] = enabled
	}
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return channelConfigPatchResponse{}, fmt.Errorf("marshal serve config patch: %w", err)
	}

	candidate := cloneServeConfig(s.Effective)
	if candidate == nil {
		candidate = DefaultConfig()
	}
	if err := applyEffectiveChannelPatch(candidate, platform, patch); err != nil {
		return channelConfigPatchResponse{}, err
	}
	applyOverrides(candidate, s.overrides)
	applyRuntimeFeatures(candidate)
	if err := s.writeConfigFile(append(data, '\n')); err != nil {
		return channelConfigPatchResponse{}, err
	}
	if apply != nil {
		if err := apply(candidate); err != nil {
			if restoreErr := s.restoreConfigFile(oldData, oldExists, oldInfo); restoreErr != nil {
				return channelConfigPatchResponse{}, fmt.Errorf("apply channel config: %w; restore config: %v", err, restoreErr)
			}
			return channelConfigPatchResponse{}, fmt.Errorf("apply channel config: %w", err)
		}
	}
	s.Effective = candidate
	return channelConfigPatchResponse{
		Layer: s.WritableLayer, Path: s.WritablePath, Platform: platform,
		Configured: platformObject, Effective: effectiveChannelConfig(candidate, platform),
		Restart: map[string]any{"platform": platform},
	}, nil
}

// UpdateFull persists a complete serve configuration through the same
// serialize/apply/rollback boundary as channel updates. The body is decoded
// into a normalized writable-layer representation; CLI runtime overrides stay
// effective in memory but are stripped from the persisted copy.
func (s *ServeConfigState) UpdateFull(body []byte, apply func(*Config) error) (*Config, error) {
	if s == nil {
		return nil, fmt.Errorf("serve config state is nil")
	}
	candidate, err := DecodeConfigBytes(body)
	if err != nil {
		return nil, fmt.Errorf("decode serve config: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var oldData []byte
	oldExists := false
	var oldInfo os.FileInfo
	if data, readErr := os.ReadFile(s.WritablePath); readErr == nil {
		oldExists = true
		oldData = append([]byte(nil), data...)
		oldInfo, _ = os.Stat(s.WritablePath)
	} else if !os.IsNotExist(readErr) {
		return nil, fmt.Errorf("read writable serve config %s: %w", s.WritablePath, readErr)
	}
	applyOverrides(candidate, s.overrides)
	applyRuntimeFeatures(candidate)
	persisted := cloneServeConfig(candidate)
	// Full PUT commonly sends the effective document. Restore fields supplied
	// only by CLI flags before writing so runtime overrides remain ephemeral.
	if base, loadErr := LoadConfigFrom(s.WritablePath); loadErr == nil {
		stripRunOverrides(persisted, base, s.overrides)
	}
	persistedData, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal serve config: %w", err)
	}
	if err := s.writeConfigFile(append(persistedData, '\n')); err != nil {
		return nil, err
	}
	if apply != nil {
		if err := apply(candidate); err != nil {
			if restoreErr := s.restoreConfigFile(oldData, oldExists, oldInfo); restoreErr != nil {
				return nil, fmt.Errorf("apply serve config: %w; restore config: %v", err, restoreErr)
			}
			return nil, fmt.Errorf("apply serve config: %w", err)
		}
	}
	s.Effective = candidate
	return cloneServeConfig(candidate), nil
}

func stripRunOverrides(cfg, base *Config, opts RunOptions) {
	if cfg == nil || base == nil {
		return
	}
	if opts.Port != "" || opts.Unsafe {
		cfg.API.Listen = base.API.Listen
	}
	if opts.Unsafe {
		cfg.API.Auth = base.API.Auth
	}
	if opts.WebUIDir != "" {
		cfg.WebUI.Dir = base.WebUI.Dir
		cfg.WebUI.Enabled = base.WebUI.Enabled
		cfg.Features.WebUI = base.Features.WebUI
	}
	if opts.WorkDir != "" {
		cfg.API.DefaultWorkDir = base.API.DefaultWorkDir
		cfg.API.WorkingDir = base.API.WorkingDir
	}
	if opts.Provider != "" {
		cfg.API.Provider = base.API.Provider
	}
	if opts.Model != "" {
		cfg.API.Model = base.API.Model
	}
	if opts.Sandbox {
		cfg.API.Sandbox = base.API.Sandbox
	}
	if opts.MultiAgent {
		cfg.API.EnableSubAgents = base.API.EnableSubAgents
		cfg.Features.MultiAgent = base.Features.MultiAgent
	}
	if opts.Delegate {
		cfg.API.EnableDelegate = base.API.EnableDelegate
	}
	if opts.Workflows {
		cfg.API.EnableWorkflows = base.API.EnableWorkflows
	}
	if opts.WebSearch {
		cfg.API.EnableWebSearch = base.API.EnableWebSearch
	}
	if opts.Browser {
		cfg.API.EnableBrowser = base.API.EnableBrowser
	}
	if opts.Artifact {
		cfg.API.EnableArtifact = base.API.EnableArtifact
	}
	if opts.A2AMaster {
		cfg.API.EnableA2AMaster = base.API.EnableA2AMaster
	}
	if opts.Lobster {
		cfg.LobsterMode = base.LobsterMode
		cfg.API.DefaultMode = base.API.DefaultMode
		cfg.API.Sandbox = base.API.Sandbox
		cfg.API.EnableSubAgents = base.API.EnableSubAgents
		cfg.Features.MultiAgent = base.Features.MultiAgent
	}
}

func applyEffectiveChannelPatch(cfg *Config, platform string, patch map[string]any) error {
	stringValue := func(key string) (string, bool) {
		value, ok := patch[key].(string)
		return value, ok
	}
	switch platform {
	case "wechat":
		if value, ok := patch["enabled"].(bool); ok {
			cfg.Channels.Wechat.Enabled = value
			cfg.Features.Wechat = value
		}
		if value, ok := stringValue("credPath"); ok {
			cfg.Channels.Wechat.CredPath = value
		}
		if value, ok := stringValue("workDir"); ok {
			cfg.Channels.Wechat.WorkDir = value
		}
		if value, ok := patch["autoTyping"].(bool); ok {
			cfg.Channels.Wechat.AutoTyping = value
		}
	case "feishu":
		if value, ok := patch["enabled"].(bool); ok {
			cfg.Channels.Feishu.Enabled = value
			cfg.Features.Feishu = value
		}
		if value, ok := stringValue("appId"); ok {
			cfg.Channels.Feishu.AppID = value
		}
		if value, ok := stringValue("appSecret"); ok {
			cfg.Channels.Feishu.AppSecret = value
		}
		if value, ok := stringValue("workDir"); ok {
			cfg.Channels.Feishu.WorkDir = value
		}
	default:
		return fmt.Errorf("unsupported channel %q", platform)
	}
	return nil
}

func (s *ServeConfigState) writeConfigFile(data []byte) error {
	if s != nil && s.writeAtomic != nil {
		return s.writeAtomic(s.WritablePath, data)
	}
	return atomicWritePrivateFile(s.WritablePath, data)
}

func (s *ServeConfigState) restoreConfigFile(oldData []byte, existed bool, oldInfo os.FileInfo) error {
	if s == nil {
		return fmt.Errorf("serve config state is nil")
	}
	if existed {
		if err := s.writeConfigFile(oldData); err != nil {
			return err
		}
		if oldInfo != nil {
			if err := os.Chmod(s.WritablePath, oldInfo.Mode().Perm()); err != nil {
				return err
			}
			if err := os.Chtimes(s.WritablePath, oldInfo.ModTime(), oldInfo.ModTime()); err != nil {
				return err
			}
		}
		return nil
	}
	if err := os.Remove(s.WritablePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func objectField(parent map[string]any, key string) map[string]any {
	if current, ok := parent[key].(map[string]any); ok {
		return current
	}
	next := map[string]any{}
	parent[key] = next
	return next
}

func atomicWritePrivateFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".serve-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary serve config: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary serve config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temporary serve config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary serve config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace serve config: %w", err)
	}
	return syncConfigDirForOS(dir, runtime.GOOS == "windows")
}

// syncConfigDirForOS flushes the parent directory entry after an atomic rename
// so the new name survives a crash. Directory fsync is a POSIX durability idiom
// with no Windows equivalent: Go opens directories with read-only access while
// FlushFileBuffers requires FILE_WRITE_DATA, so the call always fails with
// ERROR_ACCESS_DENIED ("Access is denied") on every Windows filesystem,
// including exFAT volumes. Windows must therefore skip the directory flush
// (matching etcd/bolt practice) instead of failing an otherwise successful
// config write; the file data itself is already synced before the rename.
func syncConfigDirForOS(dir string, windows bool) error {
	if windows {
		return nil
	}
	dirHandle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open config directory for sync: %w", err)
	}
	syncErr := dirHandle.Sync()
	closeErr := dirHandle.Close()
	if syncErr != nil {
		return fmt.Errorf("sync config directory: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close config directory: %w", closeErr)
	}
	return nil
}
