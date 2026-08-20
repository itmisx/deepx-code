package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestMarshalAssistantEmptyContentEmitsEmptyString 防止回归:
// 模型只输出 reasoning_content 时,assistant 消息序列化必须含 content 字段(哪怕空字符串),
// 否则 DeepSeek API 会 400 "Invalid assistant message: content or tool_calls must be set"。
func TestMarshalAssistantEmptyContentEmitsEmptyString(t *testing.T) {
	m := ChatMessage{
		Role:             "assistant",
		Content:          "",
		ReasoningContent: "internal thoughts...",
		ToolCalls:        nil,
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal err: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"content":""`) {
		t.Errorf("expected content field present (empty string), got: %s", s)
	}
}

// TestMarshalAssistantWithToolCallsOmitsContentOK:
// 有 tool_calls 时不需要 content,空 content 仍可省略(omitempty 生效)。
func TestMarshalAssistantWithToolCallsOmitsContentOK(t *testing.T) {
	m := ChatMessage{
		Role:    "assistant",
		Content: "",
		ToolCalls: []ToolCall{
			{ID: "call_1", Type: "function", Function: ToolCallFunc{Name: "Read"}},
		},
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal err: %v", err)
	}
	s := string(b)
	if strings.Contains(s, `"content"`) {
		t.Errorf("assistant with tool_calls and empty content shouldn't emit content, got: %s", s)
	}
	if !strings.Contains(s, `"tool_calls"`) {
		t.Errorf("expected tool_calls present, got: %s", s)
	}
}

// TestMarshalToolMessageStillOmits:
// tool 角色 + 空 content 仍按 omitempty 省略 —— 空的工具结果是正常的,
// 这条形状线上验证过,不动。
//
// 这个测试原先断言的是 **system** 也省略("非 assistant 角色一律 omitempty")。
// 那只是当初加 assistant 兜底时写的"别误伤其它角色"守卫,并非某个服务端的要求;
// 而它恰恰锁死了一个真 bug:空的 user / system 消息发出去没有 content 字段,
// 服务端直接 `Param Incorrect: "content" is not set`,整轮对话失败。
// 现在 user / system 一律带 content(见 ChatMessage.MarshalJSON 与
// TestMarshal_UserSystemAlwaysHaveContent),这里改测 tool。
func TestMarshalToolMessageStillOmits(t *testing.T) {
	m := ChatMessage{Role: "tool", Content: "", ToolCallID: "call_1"}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal err: %v", err)
	}
	if strings.Contains(string(b), `"content"`) {
		t.Errorf("tool with empty content should still be omitted, got: %s", string(b))
	}
}
