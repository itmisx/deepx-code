package agent

import (
	"strings"
	"testing"
)

// 问题 1:执行记录插入破坏 assistant/tool 配对 —— 摘除逻辑只摘成功折叠项,配对保持完整。
func TestStripElidedToolCalls_PairIntegrity(t *testing.T) {
	// 混合批次:大 Write(成功折叠)+ Read(正常保留)。
	big := `{"path":"big.go","content":` + jsonStr(strings.Repeat("a", 4000)) + `}`
	tcs := []ToolCall{
		mkTCID("call_write", "Write", big),
		mkTCID("call_read", "Read", `{"path":"r.go"}`),
	}
	// 执行后:大 Write 成功 → 从 assistant tool_calls 摘除;Read 保留。
	// 历史里 assistant 剩 [Read],其 tool 消息正常配对 → 不悬挂、不 400。
	kept := stripElidedToolCalls(tcs, []string{"call_write"})
	if len(kept) != 1 {
		t.Fatalf("应只剩 Read, got %d 条", len(kept))
	}
	if kept[0].Function.Name != "Read" || kept[0].ID != "call_read" {
		t.Fatalf("应保留 Read(call_read), got %s(%s)", kept[0].Function.Name, kept[0].ID)
	}
	// 配对完整性:assistant 剩余 tool_call 的每个 ID,都必须有对应的 tool 消息(模拟结果)。
	results := map[string]bool{"call_read": true} // Read 的工具结果
	for _, tc := range kept {
		if !results[tc.ID] {
			t.Fatalf("剩余 tool_call %s 无对应 tool 结果 → 悬挂", tc.ID)
		}
	}
}

// 问题 2:失败的大 Write 不被吞 —— 失败项不在 successIDs,保留 tool_call 供错误配对。
func TestStripElidedToolCalls_FailedWritePreserved(t *testing.T) {
	big := `{"path":"fail.go","content":` + jsonStr(strings.Repeat("b", 4000)) + `}`
	tcs := []ToolCall{
		mkTCID("call_fail", "Write", big),
		mkTCID("call_read", "Read", `{"path":"r.go"}`),
	}
	// 大 Write 失败 → 不进 successIDs;只摘除成功项(无)。
	kept := stripElidedToolCalls(tcs, nil)
	if len(kept) != 2 {
		t.Fatalf("失败 Write 应保留, got %d 条", len(kept))
	}
	// 失败 Write 的 tool 错误消息能与保留的 tool_call 配对(assistant 仍有该调用)。
	if kept[0].Function.Name != "Write" || kept[0].ID != "call_fail" || kept[1].Function.Name != "Read" {
		t.Fatalf("应保留 [失败Write, Read], got %s(%s),%s(%s)", kept[0].Function.Name, kept[0].ID, kept[1].Function.Name, kept[1].ID)
	}
	// 失败 Write 有对应 tool 错误消息 → 配对、错误透传。
	results := map[string]bool{"call_fail": true, "call_read": true}
	for _, tc := range kept {
		if !results[tc.ID] {
			t.Fatalf("剩余 tool_call %s 无对应 tool 结果 → 悬挂", tc.ID)
		}
	}
}

// mkTCID 构造指定 ID 的工具调用(测试需要区分多个 tool_call,mkTC 固定 id1 不可用)。
func mkTCID(id, name, argsJSON string) ToolCall {
	return ToolCall{ID: id, Type: "function", Function: ToolCallFunc{Name: name, Arguments: argsJSON}}
}

// 问题 3:elided 判定缓存一致 —— 截断 args 经修复后判定 ok,且记录内容非空;
// 对比原始截断 args 直接判定 !ok,证明"两处各算一次"会产生空路径/0 字节记录。
func TestCollectElided_Consistent(t *testing.T) {
	// 模拟模型吐出的截断 arguments:content 被截成半截(issue #201 典型场景)。
	truncated := `{"path":"t.go","content":"` + strings.Repeat("x", 2000) + `"`
	// 原始截断 args:JSON 不完整 → elidedWriteInfo 判定失败(ok=false)。
	if _, _, _, ok := elidedWriteInfo(truncated); ok {
		t.Fatalf("原始截断 args 应判定 !ok")
	}
	// 修复后 args(repairArgsJSON 补全)→ 判定 ok,且缓存记录非空。
	repaired := repairArgsJSON(truncated)
	elided := collectElided([]ToolCall{mkTC("Write", repaired)})
	if len(elided) != 1 {
		t.Fatalf("修复后 args 应判定为 elide, got %d 条", len(elided))
	}
	for id, ew := range elided {
		if ew.path == "" || ew.size == 0 || ew.lines == 0 {
			t.Fatalf("缓存记录不应为空路径/0 字节: id=%s path=%q size=%d lines=%d", id, ew.path, ew.size, ew.lines)
		}
	}
	// 两处共用同一份缓存 → 执行循环不再用原始 args 二次判定(避免空记录)。
	_ = elided
}

// 问题 4:执行记录(role=user)不计对话轮边界 —— isTurnBoundary 过滤 IsExecRecord。
func TestIsTurnBoundary_ExecRecord(t *testing.T) {
	if isTurnBoundary(ChatMessage{Role: "user", IsExecRecord: true}) {
		t.Fatalf("执行记录不应算作轮边界")
	}
	if !isTurnBoundary(ChatMessage{Role: "user"}) {
		t.Fatalf("普通 user 消息应算轮边界")
	}
	if !isTurnBoundary(ChatMessage{Role: "assistant"}) {
		t.Fatalf("assistant 消息应算轮边界")
	}
	if isTurnBoundary(ChatMessage{Role: "tool"}) {
		t.Fatalf("tool 消息不应算轮边界")
	}
}
