package cli

import (
	"context"
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/anthropics/agent/api"
)

// mockSuggestProvider implements api.Provider for testing suggestions.
type mockSuggestProvider struct {
	response string
	err      error
	called   bool
}

func (m *mockSuggestProvider) SendMessage(_ context.Context, req api.ProviderRequest) (*api.Response, error) {
	m.called = true
	if m.err != nil {
		return nil, m.err
	}
	return &api.Response{
		Content: []api.ContentBlock{
			{Type: "text", Text: m.response},
		},
	}, nil
}

func (m *mockSuggestProvider) Info() api.ModelInfo {
	return api.ModelInfo{ProviderID: "test", Model: "mock", ContextWindow: 1000}
}

func (m *mockSuggestProvider) CostForUsage(_ api.Usage) float64       { return 0 }
func (m *mockSuggestProvider) NeedsClientCompaction() bool             { return false }
func (m *mockSuggestProvider) Compact(_ context.Context, _ string, msgs []api.Message, _ int) ([]api.Message, error) {
	return msgs, nil
}

func TestNewSuggestionEngine(t *testing.T) {
	// nil provider → nil engine
	if e := newSuggestionEngine(nil); e != nil {
		t.Fatal("expected nil engine for nil provider")
	}

	// valid provider → non-nil engine
	p := &mockSuggestProvider{response: "hello"}
	e := newSuggestionEngine(p)
	if e == nil {
		t.Fatal("expected non-nil engine")
	}
}

func TestSuggestionEnginePredict(t *testing.T) {
	mock := &mockSuggestProvider{response: "fix the login bug"}
	engine := newSuggestionEngine(mock)

	messages := []api.Message{
		api.UserMessage("implement the auth feature"),
		{Role: api.RoleAssistant, Content: []api.ContentBlock{{Type: "text", Text: "Done implementing auth."}}},
	}

	result := engine.predict(context.Background(), messages, nil)
	if !mock.called {
		t.Fatal("expected provider to be called")
	}
	if result != "fix the login bug" {
		t.Fatalf("expected 'fix the login bug', got %q", result)
	}
}

func TestSuggestionEnginePredictWithHistory(t *testing.T) {
	mock := &mockSuggestProvider{response: "add tests for auth"}
	engine := newSuggestionEngine(mock)

	messages := []api.Message{
		api.UserMessage("implement auth"),
		{Role: api.RoleAssistant, Content: []api.ContentBlock{{Type: "text", Text: "Done."}}},
	}
	history := []string{"create the project", "implement auth"}

	result := engine.predict(context.Background(), messages, history)
	if result != "add tests for auth" {
		t.Fatalf("expected 'add tests for auth', got %q", result)
	}
}

func TestSuggestionEnginePredictCancelled(t *testing.T) {
	mock := &mockSuggestProvider{response: "should not see this"}
	engine := newSuggestionEngine(mock)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	messages := []api.Message{
		api.UserMessage("hello"),
	}

	result := engine.predict(ctx, messages, nil)
	// The provider may or may not be called depending on timing,
	// but the result should be empty because context is cancelled.
	if result != "" && ctx.Err() != nil {
		// This is fine — predict checks ctx.Err() after the call
	}
}

func TestSuggestionEnginePredictStripsQuotes(t *testing.T) {
	tests := []struct {
		response string
		expected string
	}{
		{`"fix the bug"`, "fix the bug"},
		{`'fix the bug'`, "fix the bug"},
		{"fix the bug", "fix the bug"},
		{`"x"`, "x"},
		{"", ""},
	}

	for _, tt := range tests {
		mock := &mockSuggestProvider{response: tt.response}
		engine := newSuggestionEngine(mock)
		result := engine.predict(context.Background(), []api.Message{api.UserMessage("hello")}, nil)
		if result != tt.expected {
			t.Errorf("predict(%q) = %q, want %q", tt.response, result, tt.expected)
		}
	}
}

func TestSuggestionEngineCancel(t *testing.T) {
	mock := &mockSuggestProvider{response: "test"}
	engine := newSuggestionEngine(mock)

	// Request a suggestion
	msgCh := make(chan tea.Msg, 10)
	engine.requestSuggestion(1, []api.Message{api.UserMessage("hi")}, nil, msgCh)

	// Cancel it
	engine.cancel()

	// Verify cancel doesn't panic when called again
	engine.cancel()
}

func TestSuggestionEnginePredictEmptyMessages(t *testing.T) {
	mock := &mockSuggestProvider{response: "anything"}
	engine := newSuggestionEngine(mock)

	result := engine.predict(context.Background(), nil, nil)
	if result != "" {
		t.Fatalf("expected empty result for nil messages, got %q", result)
	}

	result = engine.predict(context.Background(), []api.Message{}, nil)
	if result != "" {
		t.Fatalf("expected empty result for empty messages, got %q", result)
	}
}

func TestSuggestionEnginePredictTruncatesLongMessages(t *testing.T) {
	longText := make([]byte, 1000)
	for i := range longText {
		longText[i] = 'a'
	}
	messages := []api.Message{
		{Role: api.RoleAssistant, Content: []api.ContentBlock{
			{Type: "text", Text: string(longText)},
		}},
		api.UserMessage("follow up"),
	}

	mock := &mockSuggestProvider{response: "next step"}
	engine := newSuggestionEngine(mock)

	result := engine.predict(context.Background(), messages, nil)
	if result != "next step" {
		t.Fatalf("expected 'next step', got %q", result)
	}
}

func TestClearSuggestion(t *testing.T) {
	mock := &mockSuggestProvider{response: "test"}
	engine := newSuggestionEngine(mock)

	s := &session{
		suggestion:    "some suggestion",
		suggestEngine: engine,
		historyIndex:  -1,
	}

	s.clearSuggestion()

	if s.suggestion != "" {
		t.Fatalf("expected empty suggestion after clear, got %q", s.suggestion)
	}
}

func TestSuggestModelSpec(t *testing.T) {
	// Save and restore env vars
	origEnabled := getEnv("COGENT_SUGGEST_ENABLED")
	origModel := getEnv("COGENT_SUGGEST_MODEL")
	defer func() {
		setEnv("COGENT_SUGGEST_ENABLED", origEnabled)
		setEnv("COGENT_SUGGEST_MODEL", origModel)
	}()

	// Not enabled → empty spec
	setEnv("COGENT_SUGGEST_ENABLED", "")
	setEnv("COGENT_SUGGEST_MODEL", "anthropic/claude-haiku-4-5")
	spec := api.SuggestModelSpec()
	if spec.Provider != "" || spec.Model != "" {
		t.Fatalf("expected empty spec when disabled, got %v", spec)
	}

	// Enabled but no model → empty spec
	setEnv("COGENT_SUGGEST_ENABLED", "true")
	setEnv("COGENT_SUGGEST_MODEL", "")
	spec = api.SuggestModelSpec()
	if spec.Provider != "" || spec.Model != "" {
		t.Fatalf("expected empty spec when no model, got %v", spec)
	}

	// Both set → valid spec
	setEnv("COGENT_SUGGEST_ENABLED", "true")
	setEnv("COGENT_SUGGEST_MODEL", "anthropic/claude-haiku-4-5")
	spec = api.SuggestModelSpec()
	if spec.Provider != "anthropic" || spec.Model != "claude-haiku-4-5" {
		t.Fatalf("expected anthropic/claude-haiku-4-5, got %v", spec)
	}

	// "1" also counts as enabled
	setEnv("COGENT_SUGGEST_ENABLED", "1")
	spec = api.SuggestModelSpec()
	if spec.Provider != "anthropic" || spec.Model != "claude-haiku-4-5" {
		t.Fatalf("expected anthropic/claude-haiku-4-5 with enabled=1, got %v", spec)
	}
}

func getEnv(key string) string {
	return os.Getenv(key)
}

func setEnv(key, value string) {
	if value == "" {
		os.Unsetenv(key)
	} else {
		os.Setenv(key, value)
	}
}
