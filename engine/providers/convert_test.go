package providers

import (
	"testing"

	"github.com/discorgento/ouija/engine"
)

func sampleMsgs() []engine.Message {
	return []engine.Message{
		{Role: engine.RoleUser, Content: "hi"},
		{Role: engine.RoleAssistant, ToolCalls: []engine.ToolCall{{ID: "1", Name: "read_file", Input: map[string]any{"path": "x"}}}},
		{Role: engine.RoleTool, ToolCallID: "1", Content: "content"},
	}
}

func TestAntMessagesGroupsToolResults(t *testing.T) {
	out := antMessages(sampleMsgs())
	if len(out) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(out))
	}
	if out[2]["role"] != "user" {
		t.Fatalf("tool_result must go in a user message, got %v", out[2]["role"])
	}
	content, ok := out[2]["content"].([]map[string]any)
	if !ok || len(content) != 1 || content[0]["type"] != "tool_result" {
		t.Fatalf("malformed tool_result block: %#v", out[2]["content"])
	}
}

func TestOAIMessagesRolesAndSystem(t *testing.T) {
	out := oaiMessages("rules", sampleMsgs())
	if out[0]["role"] != "system" {
		t.Fatalf("first message should be system, got %v", out[0]["role"])
	}
	last := out[len(out)-1]
	if last["role"] != "tool" || last["tool_call_id"] != "1" {
		t.Fatalf("tool result mapped incorrectly: %#v", last)
	}
}
