package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// SessionStore abstracts persistence of session data.
// Implementations can store sessions on the local filesystem, in a database, etc.
type SessionStore interface {
	Save(data SessionData) error
	Load(id string) (*SessionData, error)
	List() ([]SessionData, error)
	Delete(id string) error
}

// LocalSessionStore persists sessions as JSON files under .cogent/sessions/ in
// the project directory. It wraps the original free functions from persist.go.
type LocalSessionStore struct {
	cwd string
}

// NewLocalSessionStore creates a store rooted at cwd/.cogent/sessions/.
func NewLocalSessionStore(cwd string) *LocalSessionStore {
	return &LocalSessionStore{cwd: cwd}
}

func (s *LocalSessionStore) dir() string {
	return filepath.Join(s.cwd, ".cogent", "sessions")
}

func (s *LocalSessionStore) filePath(id string) string {
	return filepath.Join(s.dir(), id+".json")
}

func (s *LocalSessionStore) Save(data SessionData) error {
	dir := s.dir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath(data.ID), b, 0644)
}

func (s *LocalSessionStore) Load(id string) (*SessionData, error) {
	b, err := os.ReadFile(s.filePath(id))
	if err != nil {
		return nil, err
	}
	var sd SessionData
	if err := json.Unmarshal(b, &sd); err != nil {
		return nil, err
	}
	return &sd, nil
}

func (s *LocalSessionStore) List() ([]SessionData, error) {
	dir := s.dir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var sessions []SessionData
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var sd SessionData
		if err := json.Unmarshal(b, &sd); err != nil {
			continue
		}
		sessions = append(sessions, sd)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	return sessions, nil
}

func (s *LocalSessionStore) Delete(id string) error {
	return os.Remove(s.filePath(id))
}
