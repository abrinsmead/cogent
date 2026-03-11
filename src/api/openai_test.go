package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTranslateMessages_EmptyToolResult(t *testing.T) {
	p := &OpenAIProvider{model: "gpt-4o"}

	messages := []Message{
		{Role: RoleUser, Content: []ContentBlock{TextBlock("hello")}},
		{Role: RoleAssistant, Content: []ContentBlock{
			{Type: "tool_use", ID: "call_1", Name: "bash", Input: map[string]any{"command": "true"}},
		}},
		{Role: RoleUser, Content: []ContentBlock{
			ToolResultBlock("call_1", "", false),
		}},
	}

	items := p.translateMessages(messages)

	// Find the function_call_output item
	var found bool
	for _, item := range items {
		if item.Type == "function_call_output" {
			found = true
			data, err := json.Marshal(item)
			if err != nil {
				t.Fatalf("marshal error: %v", err)
			}
			jsonStr := string(data)
			if !strings.Contains(jsonStr, `"output":""`) {
				t.Errorf("expected JSON to contain \"output\":\"\", got: %s", jsonStr)
			}
		}
	}
	if !found {
		t.Fatal("no function_call_output item found in result")
	}
}

func TestTranslateMessages_NormalToolResult(t *testing.T) {
	p := &OpenAIProvider{model: "gpt-4o"}

	messages := []Message{
		{Role: RoleUser, Content: []ContentBlock{TextBlock("hello")}},
		{Role: RoleAssistant, Content: []ContentBlock{
			{Type: "tool_use", ID: "call_1", Name: "bash", Input: map[string]any{"command": "touch foo"}},
		}},
		{Role: RoleUser, Content: []ContentBlock{
			ToolResultBlock("call_1", "file created", false),
		}},
	}

	items := p.translateMessages(messages)

	var found bool
	for _, item := range items {
		if item.Type == "function_call_output" {
			found = true
			data, err := json.Marshal(item)
			if err != nil {
				t.Fatalf("marshal error: %v", err)
			}
			jsonStr := string(data)
			if !strings.Contains(jsonStr, `"output":"file created"`) {
				t.Errorf("expected JSON to contain \"output\":\"file created\", got: %s", jsonStr)
			}
		}
	}
	if !found {
		t.Fatal("no function_call_output item found in result")
	}
}

func TestTranslateMessages_ErrorToolResult(t *testing.T) {
	p := &OpenAIProvider{model: "gpt-4o"}

	messages := []Message{
		{Role: RoleUser, Content: []ContentBlock{TextBlock("run it")}},
		{Role: RoleAssistant, Content: []ContentBlock{
			{Type: "tool_use", ID: "call_1", Name: "bash", Input: map[string]any{"command": "badcmd"}},
		}},
		{Role: RoleUser, Content: []ContentBlock{
			ToolResultBlock("call_1", "command not found", true),
		}},
	}

	items := p.translateMessages(messages)

	var found bool
	for _, item := range items {
		if item.Type == "function_call_output" {
			found = true
			data, err := json.Marshal(item)
			if err != nil {
				t.Fatalf("marshal error: %v", err)
			}
			jsonStr := string(data)
			if !strings.Contains(jsonStr, `"output":"Error: command not found"`) {
				t.Errorf("expected output to contain \"Error: command not found\", got: %s", jsonStr)
			}
		}
	}
	if !found {
		t.Fatal("no function_call_output item found in result")
	}
}

func TestTranslateMessages_Ordering(t *testing.T) {
	p := &OpenAIProvider{model: "gpt-4o"}

	messages := []Message{
		{Role: RoleAssistant, Content: []ContentBlock{
			TextBlock("Let me check"),
			{Type: "tool_use", ID: "call_1", Name: "bash", Input: map[string]any{"command": "ls"}},
		}},
	}

	items := p.translateMessages(messages)

	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	// The assistant text message should appear before the function_call
	if items[0].Role != "assistant" {
		t.Errorf("expected first item to be assistant message, got role=%q type=%q", items[0].Role, items[0].Type)
	}
	if items[1].Type != "function_call" {
		t.Errorf("expected second item to be function_call, got type=%q", items[1].Type)
	}
}

func TestTranslateMessages_FunctionCallNoOutput(t *testing.T) {
	p := &OpenAIProvider{model: "gpt-4o"}

	messages := []Message{
		{Role: RoleAssistant, Content: []ContentBlock{
			{Type: "tool_use", ID: "call_1", Name: "bash", Input: map[string]any{"command": "ls"}},
		}},
	}

	items := p.translateMessages(messages)

	var found bool
	for _, item := range items {
		if item.Type == "function_call" {
			found = true
			data, err := json.Marshal(item)
			if err != nil {
				t.Fatalf("marshal error: %v", err)
			}
			jsonStr := string(data)
			if strings.Contains(jsonStr, `"output"`) {
				t.Errorf("function_call item should not have output field, got: %s", jsonStr)
			}
		}
	}
	if !found {
		t.Fatal("no function_call item found in result")
	}
}
