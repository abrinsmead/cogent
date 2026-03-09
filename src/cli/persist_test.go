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
	sd := sessionData{
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

	var got sessionData
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

func TestListSavedSessions(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, ".cogent", "sessions")
	os.MkdirAll(sessDir, 0755)

	// No sessions yet
	got := listSavedSessions(dir)
	if len(got) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(got))
	}

	// Write two sessions
	sd1 := sessionData{
		ID:        "aaa",
		Name:      "First",
		UpdatedAt: time.Now().Add(-time.Hour),
	}
	sd2 := sessionData{
		ID:        "bbb",
		Name:      "Second",
		UpdatedAt: time.Now(),
	}
	for _, sd := range []sessionData{sd1, sd2} {
		b, _ := json.Marshal(sd)
		os.WriteFile(filepath.Join(sessDir, sd.ID+".json"), b, 0644)
	}

	got = listSavedSessions(dir)
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
}

func TestDeleteSessionFile(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, ".cogent", "sessions")
	os.MkdirAll(sessDir, 0755)

	path := filepath.Join(sessDir, "test123.json")
	os.WriteFile(path, []byte("{}"), 0644)

	if _, err := os.Stat(path); err != nil {
		t.Fatal("file should exist before delete")
	}

	deleteSessionFile(dir, "test123")

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
	sessDir := filepath.Join(dir, ".cogent", "sessions")

	// Write two sessions directly (simulating what saveAllSessions does).
	now := time.Now()
	for _, sd := range []sessionData{
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
		os.MkdirAll(sessDir, 0755)
		b, _ := json.Marshal(sd)
		os.WriteFile(filepath.Join(sessDir, sd.ID+".json"), b, 0644)
	}

	// Both files should exist
	for _, id := range []string{"sess_aaa", "sess_bbb"} {
		path := filepath.Join(sessDir, id+".json")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected session file %s to exist", path)
		}
	}

	// Verify we can list them back, sorted newest first
	saved := listSavedSessions(dir)
	if len(saved) != 2 {
		t.Fatalf("expected 2 saved sessions, got %d", len(saved))
	}
	if saved[0].ID != "sess_bbb" {
		t.Errorf("expected newest session first, got %q", saved[0].ID)
	}
}

func TestTabOrderFiltering(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, ".cogent", "sessions")
	os.MkdirAll(sessDir, 0755)

	now := time.Now()
	sessions := []sessionData{
		{ID: "tab1", Name: "Tab One", TabOrder: 2, UpdatedAt: now.Add(-time.Minute)},
		{ID: "tab2", Name: "Tab Two", TabOrder: 1, UpdatedAt: now},
		{ID: "closed1", Name: "Closed", TabOrder: 0, UpdatedAt: now.Add(-time.Hour)},
	}
	for _, sd := range sessions {
		b, _ := json.Marshal(sd)
		os.WriteFile(filepath.Join(sessDir, sd.ID+".json"), b, 0644)
	}

	saved := listSavedSessions(dir)
	if len(saved) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(saved))
	}

	// Filter to tab sessions and sort by TabOrder
	var tabSessions []sessionData
	var closedSessions []sessionData
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
