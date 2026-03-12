package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/anthropics/agent/api"
)

func TestSessionDataRoundTrip(t *testing.T) {
	sd := SessionData{
		ID:             "abc12345",
		Name:           "Test Session",
		NameSet:        true,
		TabOrder:       2,
		PermissionMode: "Confirm",
		AllowedTools:   []string{"bash"},
		TotalCost:      0.42,
		ContextUsed:    5000,
		CreatedAt:      time.Now().Truncate(time.Second),
		UpdatedAt:      time.Now().Truncate(time.Second),
		Messages: []api.Message{
			api.UserMessage("hello"),
			{Role: api.RoleAssistant, Content: []api.ContentBlock{api.TextBlock("Hi there!")}},
		},
		Lines: []line{
			{},
			{Type: linePrompt, Data: "hello"},
			{Type: lineText, Data: "Hi there!"},
			{Type: lineTool, Data: "read\x00/tmp/file.go"},
			{Type: lineConfirmAllow},
			{Type: lineCompaction},
			{Type: lineError, Data: "something went wrong"},
			{},
		},
	}

	b, err := json.Marshal(sd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got SessionData
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ID != sd.ID {
		t.Errorf("ID = %q, want %q", got.ID, sd.ID)
	}
	if got.Name != sd.Name {
		t.Errorf("Name = %q, want %q", got.Name, sd.Name)
	}
	if got.NameSet != sd.NameSet {
		t.Errorf("NameSet = %v, want %v", got.NameSet, sd.NameSet)
	}
	if got.TabOrder != sd.TabOrder {
		t.Errorf("TabOrder = %d, want %d", got.TabOrder, sd.TabOrder)
	}
	if got.PermissionMode != sd.PermissionMode {
		t.Errorf("PermissionMode = %q, want %q", got.PermissionMode, sd.PermissionMode)
	}
	if len(got.AllowedTools) != 1 || got.AllowedTools[0] != "bash" {
		t.Errorf("AllowedTools = %v, want [bash]", got.AllowedTools)
	}
	if got.TotalCost != sd.TotalCost {
		t.Errorf("TotalCost = %f, want %f", got.TotalCost, sd.TotalCost)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("Messages len = %d, want 2", len(got.Messages))
	}
	if got.Messages[0].Role != api.RoleUser {
		t.Errorf("Messages[0].Role = %q, want %q", got.Messages[0].Role, api.RoleUser)
	}
	if got.Messages[1].Role != api.RoleAssistant {
		t.Errorf("Messages[1].Role = %q, want %q", got.Messages[1].Role, api.RoleAssistant)
	}
	if len(got.Lines) != len(sd.Lines) {
		t.Fatalf("Lines len = %d, want %d", len(got.Lines), len(sd.Lines))
	}
	for i, l := range got.Lines {
		if l.Type != sd.Lines[i].Type || l.Data != sd.Lines[i].Data {
			t.Errorf("Lines[%d] = {%q, %q}, want {%q, %q}", i, l.Type, l.Data, sd.Lines[i].Type, sd.Lines[i].Data)
		}
	}
}

func TestSessionDataRuntimeIDRoundTrip(t *testing.T) {
	sd := SessionData{
		ID:        "test123",
		Name:      "Remote Session",
		RuntimeID: "sandbox-abc123",
		CreatedAt: time.Now().Truncate(time.Second),
		UpdatedAt: time.Now().Truncate(time.Second),
	}

	b, err := json.Marshal(sd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got SessionData
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.RuntimeID != sd.RuntimeID {
		t.Errorf("RuntimeID = %q, want %q", got.RuntimeID, sd.RuntimeID)
	}
}

func TestRenderLineRoundTrip(t *testing.T) {
	// Verify that all line types render without panicking.
	lines := []line{
		{},
		{Type: lineText, Data: "hello world"},
		{Type: lineTool, Data: "bash\x00ls -la"},
		{Type: lineTool, Data: "read\x00/tmp/file.go"},
		{Type: linePrompt, Data: "fix the bug"},
		{Type: lineShellPrompt, Data: "ls -la"},
		{Type: lineShellOutput, Data: "file1.go"},
		{Type: lineShellError, Data: "(exit code 1)"},
		{Type: lineInfo, Data: "  ⏎ interrupted"},
		{Type: lineModeChange, Data: "Plan"},
		{Type: lineModeChange, Data: "Confirm"},
		{Type: lineModeChange, Data: "YOLO"},
		{Type: lineModeChange, Data: "Terminal"},
		{Type: lineModelChange, Data: "openai/gpt-4o"},
		{Type: lineConfirmPrompt, Data: "\x00bash\x00ls -la"},
		{Type: lineConfirmPrompt, Data: "(sub-agent) \x00edit\x00file.go"},
		{Type: lineConfirmAllow},
		{Type: lineConfirmDeny},
		{Type: lineConfirmDenyInt},
		{Type: lineConfirmAlways, Data: "bash"},
		{Type: lineCompaction},
		{Type: lineError, Data: "api call failed"},
		{Type: lineDiff, Data: "bash\x00" + `{"command":"ls"}`},
		{Type: lineDiff, Data: "edit\x00" + `{"file_path":"f.go","old_string":"a","new_string":"b"}`},
	}

	for i, l := range lines {
		result := renderLine(l)
		if result == "" && l.Type != lineEmpty {
			// Some types like lineCompaction always produce output
			if l.Type == lineCompaction || l.Type == lineConfirmAllow || l.Type == lineConfirmDeny || l.Type == lineConfirmDenyInt {
				t.Errorf("renderLine(%d: %q) returned empty string", i, l.Type)
			}
		}
		// Just verify no panics — rendering is visual
	}
}

func TestLocalSessionStore(t *testing.T) {
	dir := t.TempDir()
	store := NewLocalSessionStore(dir)

	// No sessions yet
	got, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(got))
	}

	// Save two sessions
	sd1 := SessionData{
		ID:        "aaa",
		Name:      "First",
		UpdatedAt: time.Now().Add(-time.Hour),
	}
	sd2 := SessionData{
		ID:        "bbb",
		Name:      "Second",
		UpdatedAt: time.Now(),
	}
	for _, sd := range []SessionData{sd1, sd2} {
		if err := store.Save(sd); err != nil {
			t.Fatalf("Save(%s): %v", sd.ID, err)
		}
	}

	got, err = store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(got))
	}
	// Should be sorted by UpdatedAt desc — "bbb" first
	if got[0].ID != "bbb" {
		t.Errorf("expected first session ID=bbb, got %q", got[0].ID)
	}
	if got[1].ID != "aaa" {
		t.Errorf("expected second session ID=aaa, got %q", got[1].ID)
	}

	// Load single session
	loaded, err := store.Load("aaa")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Name != "First" {
		t.Errorf("Load name = %q, want First", loaded.Name)
	}

	// Delete
	if err := store.Delete("aaa"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, err = store.List()
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 session after delete, got %d", len(got))
	}
}

func TestDeleteSessionFile(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, ".cogent", "sessions")
	os.MkdirAll(sessDir, 0755)

	store := NewLocalSessionStore(dir)
	store.Save(SessionData{ID: "test123", Name: "Test"})

	path := filepath.Join(sessDir, "test123.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatal("file should exist before delete")
	}

	store.Delete("test123")

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file should not exist after delete")
	}
}

func TestGeneratePersistID(t *testing.T) {
	id1 := generatePersistID()
	id2 := generatePersistID()

	if len(id1) != 8 {
		t.Errorf("expected 8-char ID, got %q (len %d)", id1, len(id1))
	}
	if id1 == id2 {
		t.Error("two generated IDs should not be equal")
	}
}

func TestParseModeString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Plan", "Plan"},
		{"Confirm", "Confirm"},
		{"YOLO", "YOLO"},
		{"Terminal", "Terminal"},
		{"unknown", "Confirm"},
	}
	for _, tt := range tests {
		got := parseModeString(tt.input)
		if got.String() != tt.want {
			t.Errorf("parseModeString(%q) = %q, want %q", tt.input, got.String(), tt.want)
		}
	}
}

func TestFormatAge(t *testing.T) {
	now := time.Now()
	tests := []struct {
		t    time.Time
		want string
	}{
		{now.Add(-30 * time.Second), "just now"},
		{now.Add(-5 * time.Minute), "5 mins ago"},
		{now.Add(-1 * time.Minute), "1 min ago"},
		{now.Add(-3 * time.Hour), "3 hours ago"},
		{now.Add(-1 * time.Hour), "1 hour ago"},
		{now.Add(-48 * time.Hour), "2 days ago"},
		{now.Add(-24 * time.Hour), "1 day ago"},
	}
	for _, tt := range tests {
		got := formatAge(tt.t)
		if got != tt.want {
			t.Errorf("formatAge(%v) = %q, want %q", now.Sub(tt.t), got, tt.want)
		}
	}
}

func TestParseResumeNumber(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"1", 1},
		{"10", 10},
		{"0", 0},
		{"abc", 0},
		{"1a", 0},
		{"", 0},
	}
	for _, tt := range tests {
		got := parseResumeNumber(tt.input)
		if got != tt.want {
			t.Errorf("parseResumeNumber(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestSaveAllSessionsCreatesFiles(t *testing.T) {
	dir := t.TempDir()
	store := NewLocalSessionStore(dir)

	// Save two sessions
	now := time.Now()
	for _, sd := range []SessionData{
		{
			ID:        "sess_aaa",
			Name:      "First",
			Messages:  []api.Message{api.UserMessage("hello")},
			Lines:     []line{{Type: linePrompt, Data: "hello"}},
			CreatedAt: now.Add(-time.Hour),
			UpdatedAt: now.Add(-time.Hour),
		},
		{
			ID:        "sess_bbb",
			Name:      "Second",
			Messages:  []api.Message{api.UserMessage("world")},
			Lines:     []line{{Type: linePrompt, Data: "world"}},
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		store.Save(sd)
	}

	// Both files should exist
	sessDir := filepath.Join(dir, ".cogent", "sessions")
	for _, id := range []string{"sess_aaa", "sess_bbb"} {
		path := filepath.Join(sessDir, id+".json")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected session file %s to exist", path)
		}
	}

	// Verify we can list them back, sorted newest first
	saved, _ := store.List()
	if len(saved) != 2 {
		t.Fatalf("expected 2 saved sessions, got %d", len(saved))
	}
	if saved[0].ID != "sess_bbb" {
		t.Errorf("expected newest session first, got %q", saved[0].ID)
	}
}

func TestTabOrderFiltering(t *testing.T) {
	dir := t.TempDir()
	store := NewLocalSessionStore(dir)

	now := time.Now()
	sessions := []SessionData{
		{ID: "tab1", Name: "Tab One", TabOrder: 2, UpdatedAt: now.Add(-time.Minute)},
		{ID: "tab2", Name: "Tab Two", TabOrder: 1, UpdatedAt: now},
		{ID: "closed1", Name: "Closed", TabOrder: 0, UpdatedAt: now.Add(-time.Hour)},
	}
	for _, sd := range sessions {
		store.Save(sd)
	}

	saved, _ := store.List()
	if len(saved) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(saved))
	}

	// Filter to tab sessions and sort by TabOrder
	var tabSessions []SessionData
	var closedSessions []SessionData
	for _, sd := range saved {
		if sd.TabOrder > 0 {
			tabSessions = append(tabSessions, sd)
		} else {
			closedSessions = append(closedSessions, sd)
		}
	}

	if len(tabSessions) != 2 {
		t.Fatalf("expected 2 tab sessions, got %d", len(tabSessions))
	}
	if len(closedSessions) != 1 {
		t.Fatalf("expected 1 closed session, got %d", len(closedSessions))
	}
	if closedSessions[0].ID != "closed1" {
		t.Errorf("expected closed session ID=closed1, got %q", closedSessions[0].ID)
	}

	// Sort tab sessions by TabOrder
	sort.Slice(tabSessions, func(i, j int) bool {
		return tabSessions[i].TabOrder < tabSessions[j].TabOrder
	})
	if tabSessions[0].ID != "tab2" {
		t.Errorf("expected first tab ID=tab2 (order 1), got %q (order %d)", tabSessions[0].ID, tabSessions[0].TabOrder)
	}
	if tabSessions[1].ID != "tab1" {
		t.Errorf("expected second tab ID=tab1 (order 2), got %q (order %d)", tabSessions[1].ID, tabSessions[1].TabOrder)
	}
}

func TestInProcessRuntime(t *testing.T) {
	rt := &InProcessRuntime{}
	if rt.Kind() != RuntimeLocal {
		t.Errorf("Kind() = %v, want RuntimeLocal", rt.Kind())
	}
	if rt.ID() != "" {
		t.Errorf("ID() = %q, want empty", rt.ID())
	}
	if rt.Status() != StatusReady {
		t.Errorf("Status() = %v, want StatusReady", rt.Status())
	}
}
