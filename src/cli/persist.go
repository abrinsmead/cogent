package cli

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
	"time"

	"github.com/anthropics/agent/api"
)

// SessionData is the JSON-serializable representation of a session.
type SessionData struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	NameSet        bool          `json:"name_set,omitempty"`
	Model          string        `json:"model,omitempty"` // "provider/model" e.g. "anthropic/claude-opus-4-6"
	RuntimeID      string        `json:"runtime_id,omitempty"` // identifies which runtime the session was last running on (empty = local)
	TabOrder       int           `json:"tab_order"`            // 0 = no tab (closed), 1+ = tab position
	Messages       []api.Message `json:"messages,omitempty"`
	PermissionMode string        `json:"permission_mode"`
	AllowedTools   []string      `json:"allowed_tools,omitempty"`
	Lines          []line        `json:"lines,omitempty"`
	TotalCost      float64       `json:"total_cost,omitempty"`
	ContextUsed    int           `json:"context_used,omitempty"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

// toSessionData builds a SessionData from the session's current state.
func (s *session) toSessionData(tabOrder int) SessionData {
	info := s.provider.Info()
	runtimeID := ""
	if s.runtime != nil {
		runtimeID = s.runtime.ID()
	}
	return SessionData{
		ID:             s.persistID,
		Name:           s.name,
		NameSet:        s.nameSet,
		Model:          info.ProviderID + "/" + info.Model,
		RuntimeID:      runtimeID,
		TabOrder:       tabOrder,
		Messages:       s.agentSession.Messages(),
		PermissionMode: s.agentSession.GetPermissionMode().String(),
		AllowedTools:   mapKeys(s.agentSession.AllowedTools()),
		Lines:          s.slines,
		TotalCost:      s.totalCost,
		ContextUsed:    s.contextUsed,
		CreatedAt:      s.createdAt,
		UpdatedAt:      time.Now(),
	}
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
