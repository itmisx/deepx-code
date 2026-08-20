package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// 修复后的历史,序列化出去每条都必须带 content —— 这是修复存在的唯一理由。
func TestRepairHistory_NoMessageLosesContentField(t *testing.T) {
	broken := []ChatMessage{
		{Role: "system"},                // 空 system
		{Role: "user"},                  // 空 user
		{Role: "user", Content: "正常一句"}, // 正常
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "a"}}}, // 标准形状
		{Role: "tool", ToolCallID: "a"},                       // 空工具结果
		{Role: "assistant", Content: "答复"},
	}
	fixed, rep := RepairHistory(broken)
	if !rep.Any() {
		t.Fatal("这份历史明明有坏消息,却报告无需修复")
	}

	for i, m := range fixed {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		// assistant + tool_calls 省略 content 是 OpenAI 标准形状,豁免。
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			continue
		}
		if !strings.Contains(string(b), `"content"`) {
			t.Errorf("#%d(role=%s)仍然没有 content 字段 → 会被服务端 400:%s", i, m.Role, b)
		}
	}
}

// tool 消息绝不能被丢弃:丢了就成孤儿 tool_call,sanitizeToolPairs 会连带把
// assistant 的 tool_calls 剥掉,一路塌下去。
func TestRepairHistory_KeepsToolPairing(t *testing.T) {
	in := []ChatMessage{
		{Role: "user", Content: "干活"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "a", Type: "function"}}},
		{Role: "tool", ToolCallID: "a"}, // 空输出
	}
	fixed, _ := RepairHistory(in)

	out := sanitizeToolPairs(fixed)
	var hasCall, hasResult bool
	for _, m := range out {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			hasCall = true
		}
		if m.Role == "tool" && m.ToolCallID == "a" {
			hasResult = true
		}
	}
	if !hasCall || !hasResult {
		t.Errorf("空工具结果被修复后应保住配对,got tool_call=%v tool_result=%v", hasCall, hasResult)
	}
}

// 空 user 用占位补齐而**不是丢弃**:丢掉可能让历史以 assistant 开头,那是另一种 400。
func TestRepairHistory_EmptyUserIsFilledNotDropped(t *testing.T) {
	in := []ChatMessage{
		{Role: "user"},
		{Role: "assistant", Content: "回答"},
	}
	fixed, rep := RepairHistory(in)
	if len(fixed) != 2 || fixed[0].Role != "user" {
		t.Fatalf("空 user 不该被丢弃(会让历史以 assistant 开头),got %+v", fixed)
	}
	if strings.TrimSpace(fixed[0].Content) == "" {
		t.Error("空 user 应被填上占位内容")
	}
	if rep.Dropped != 0 {
		t.Errorf("不该有丢弃,got %d", rep.Dropped)
	}
}

// 贴图不打字的消息原样保留 —— 内容由图片渲染层补,这里插占位反而会污染。
func TestRepairHistory_KeepsImageOnlyMessage(t *testing.T) {
	in := []ChatMessage{{Role: "user", ImagePaths: []string{"/a.png"}}}
	fixed, rep := RepairHistory(in)
	if rep.Any() {
		t.Errorf("带图的空消息不该被改动,got %+v", rep)
	}
	if fixed[0].Content != "" {
		t.Errorf("不该插占位,got %q", fixed[0].Content)
	}
}

// 干净历史不该被碰:返回原切片、零改动。避免每次开会话都白拷一遍。
func TestRepairHistory_CleanHistoryUntouched(t *testing.T) {
	in := []ChatMessage{
		{Role: "user", Content: "问"},
		{Role: "assistant", Content: "答"},
	}
	fixed, rep := RepairHistory(in)
	if rep.Any() {
		t.Errorf("干净历史被误判为需要修复:%+v", rep)
	}
	if &fixed[0] != &in[0] {
		t.Error("无需修复时应直接返回原切片,不做拷贝")
	}
}
