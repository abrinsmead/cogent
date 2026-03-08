package cli

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/anthropics/agent/api"
)

// sessionData is the JSON-serializable representation of a session.
type sessionData struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	NameSet        bool          `json:"name_set,omitempty"`
	Model          string        `json:"model,omitempty"` // "provider/model" e.g. "anthropic/claude-opus-4-6"
	TabOrder       int           `json:"tab_order"`       // 0 = no tab (closed), 1+ = tab position
	Messages       []api.Message `json:"messages,omitempty"`
	PermissionMode string        `json:"permission_mode"`
	AllowedTools   []string      `json:"allowed_tools,omitempty"`
	Lines          []line        `json:"lines,omitempty"`
	TotalCost      float64       `json:"total_cost,omitempty"`
	ContextUsed    int           `json:"context_used,omitempty"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

// sessionsDir returns the path to .cogent/sessions/ in the project directory.
func sessionsDir(cwd string) string {
	return filepath.Join(cwd, ".cogent", "sessions")
}

// sessionFilePath returns the path for a specific session file.
func sessionFilePath(cwd, persistID string) string {
	return filepath.Join(sessionsDir(cwd), persistID+".json")
}

// saveSession persists the session to .cogent/sessions/<persistID>.json.
// tabOrder is the 1-based tab position (0 = not in a tab).
// Sessions with no conversation history are not saved.
func saveSession(cwd string, s *session, tabOrder int) error {
	if len(s.agent.Messages()) == 0 {
		return nil
	}

	dir := sessionsDir(cwd)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	now := time.Now()
	info := s.provider.Info()
	data := sessionData{
		ID:             s.persistID,
		Name:           s.name,
		NameSet:        s.nameSet,
		Model:          info.ProviderID + "/" + info.Model,
		TabOrder:       tabOrder,
		Messages:       s.agent.Messages(),
		PermissionMode: s.agent.GetPermissionMode().String(),
		AllowedTools:   mapKeys(s.agent.AllowedTools()),
		Lines:          s.slines,
		TotalCost:      s.totalCost,
		ContextUsed:    s.contextUsed,
		CreatedAt:      s.createdAt,
		UpdatedAt:      now,
	}

	b, err := json.Marshal(data)
	if err != nil {
		return err
	}

	return os.WriteFile(sessionFilePath(cwd, s.persistID), b, 0644)
}

// deleteSessionFile removes a session file from disk.
func deleteSessionFile(cwd, persistID string) {
	os.Remove(sessionFilePath(cwd, persistID))
}

// listSavedSessions reads all session files from .cogent/sessions/ and returns
// them sorted by UpdatedAt descending (most recent first).
func listSavedSessions(cwd string) []sessionData {
	dir := sessionsDir(cwd)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var sessions []sessionData
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var sd sessionData
		if err := json.Unmarshal(b, &sd); err != nil {
			continue
		}
		sessions = append(sessions, sd)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	return sessions
}

// loadSessionData loads a single session file by its persistent ID.
func loadSessionData(cwd, persistID string) (*sessionData, error) {
	b, err := os.ReadFile(sessionFilePath(cwd, persistID))
	if err != nil {
		return nil, err
	}
	var sd sessionData
	if err := json.Unmarshal(b, &sd); err != nil {
		return nil, err
	}
	return &sd, nil
}

// generatePersistID creates a random 8-character hex string.
func generatePersistID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// mapKeys returns the keys of a map as a sorted slice.
func mapKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		if m[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}
