package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/oschina/mothx/internal/config"
)

// AgentEntry describes a remote A2A agent in a2a-list.json.
type AgentEntry struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	AuthToken string `json:"auth_token,omitempty"`
}

// AgentListConfig is the top-level structure of a2a-list.json.
type AgentListConfig struct {
	Agents []AgentEntry `json:"agents"`
}

// AgentListConfigPath returns the path to the global a2a-list.json.
func AgentListConfigPath() string {
	return filepath.Join(config.ConfigDir(), "a2a-list.json")
}

// ProjectAgentListConfigPath returns the path to the project-level .mothx/a2a-list.json.
func ProjectAgentListConfigPath() string {
	return config.ProjectPath("a2a-list.json")
}

// LoadAgentList loads a2a-list.json from the given path.
func LoadAgentList(path string) (*AgentListConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read a2a-list.json: %w", err)
	}
	var cfg AgentListConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse a2a-list.json: %w", err)
	}
	return &cfg, nil
}

// SaveAgentList writes the agent list config to a JSON file.
func SaveAgentList(path string, cfg *AgentListConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal a2a-list config: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

// InitA2AMasterConfig creates a sample a2a-list.json at the default location.
// Returns the file path. If force is false and the file already exists, returns an error.
func InitA2AMasterConfig(force bool) (string, error) {
	path := AgentListConfigPath()
	if !force {
		if _, err := os.Stat(path); err == nil {
			return path, fmt.Errorf("a2a-list.json already exists: %s", path)
		}
	}
	cfg := &AgentListConfig{
		Agents: []AgentEntry{
			{
				Name:      "code-reviewer",
				URL:       "http://localhost:8093",
				AuthToken: "",
			},
			{
				Name:      "ci-agent",
				URL:       "http://ci-server:8093",
				AuthToken: "change-me-to-a-random-secret",
			},
		},
	}
	if err := SaveAgentList(path, cfg); err != nil {
		return "", err
	}
	return path, nil
}

// A2AManager manages a list of remote A2A agents and provides dispatch methods.
type A2AManager struct {
	mu      sync.RWMutex
	entries map[string]*AgentEntry
	order   []string
}

// NewA2AManager creates a new A2A manager from a config.
func NewA2AManager(cfg *AgentListConfig) *A2AManager {
	m := &A2AManager{
		entries: make(map[string]*AgentEntry),
	}
	if cfg != nil {
		for i := range cfg.Agents {
			e := &cfg.Agents[i]
			m.entries[e.Name] = e
			m.order = append(m.order, e.Name)
		}
	}
	return m
}

// List returns all registered agent entries in order.
func (m *A2AManager) List() []*AgentEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*AgentEntry
	for _, name := range m.order {
		if e, ok := m.entries[name]; ok {
			result = append(result, e)
		}
	}
	return result
}

// Get returns an agent entry by name.
func (m *A2AManager) Get(name string) (*AgentEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.entries[name]
	return e, ok
}

// Dispatch sends a message to the named remote A2A agent and returns the response text.
func (m *A2AManager) Dispatch(ctx context.Context, name, message string) (string, error) {
	m.mu.RLock()
	entry, ok := m.entries[name]
	m.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("agent '%s' not found in a2a-list", name)
	}

	client := NewClient(entry.URL, entry.AuthToken)
	task, err := client.SendMessage(ctx, "", &Message{
		Role:  "user",
		Parts: []MessagePart{{Type: "text", Text: message}},
	})
	if err != nil {
		return "", fmt.Errorf("dispatch to '%s': %w", name, err)
	}

	// Extract response text
	if len(task.Artifacts) > 0 {
		var texts []string
		for _, a := range task.Artifacts {
			for _, p := range a.Parts {
				if p.Type == "text" && p.Text != "" {
					texts = append(texts, p.Text)
				}
			}
		}
		if len(texts) > 0 {
			return joinTexts(texts), nil
		}
	}
	if task.Message != nil {
		var texts []string
		for _, p := range task.Message.Parts {
			if p.Type == "text" && p.Text != "" {
				texts = append(texts, p.Text)
			}
		}
		if len(texts) > 0 {
			return joinTexts(texts), nil
		}
	}

	return "(no text response from agent)", nil
}

func joinTexts(texts []string) string {
	result := ""
	for i, t := range texts {
		if i > 0 {
			result += "\n"
		}
		result += t
	}
	return result
}
