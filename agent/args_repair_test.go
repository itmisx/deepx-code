package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRepairArgsJSON_Table 覆盖修复器的三类输入:合法(原样)、可补全截断(确定性补全)、
// 垃圾(_raw 兜底)。每个用例额外断言:输出必为合法 JSON(vLLM json.loads 硬要求)。
func TestRepairArgsJSON_Table(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		// --- 合法输入:逐字节原样返回(前缀缓存要求零变化)---
		{"合法对象", `{"path":"a.go"}`, `{"path":"a.go"}`},
		{"合法带空白", ` {"a":1} `, ` {"a":1} `},
		{"合法嵌套", `{"a":{"b":[1,2]}}`, `{"a":{"b":[1,2]}}`},
		// --- 空参数:归一为 {}(无参工具调用,vLLM json.loads("") 会炸)---
		{"空串", ``, `{}`},
		{"纯空白", `  `, `{}`},
		{"null", `null`, `{}`},
		// --- 截断:确定性补全 ---
		{"断在字符串值中", `{"path":"a/b`, `{"path":"a/b"}`},
		{"断在键中", `{"pa`, `{"pa":null}`},
		{"键完整缺冒号", `{"path"`, `{"path":null}`},
		{"冒号后缺值", `{"path":`, `{"path":null}`},
		{"尾逗号对象", `{"a":1,`, `{"a":1}`},
		{"尾逗号数组", `[1,`, `[1]`},
		{"断在字面量", `{"a":tru`, `{"a":true}`},
		{"断在null", `{"a":n`, `{"a":null}`},
		{"断在数字尾", `{"a":12.`, `{"a":12.0}`},
		{"断在指数尾", `{"a":1e`, `{"a":1e0}`},
		{"多层嵌套截断", `{"a":{"b":[1,2`, `{"a":{"b":[1,2]}}`},
		{"断在转义符", `{"a":"x\`, `{"a":"x\\"}`},
		{"只有左括号", `{`, `{}`},
		{"空值字符串截断", `{"a":"`, `{"a":""}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := repairArgsJSON(c.in)
			if got != c.want {
				t.Fatalf("repairArgsJSON(%q) = %q, want %q", c.in, got, c.want)
			}
			if !json.Valid([]byte(got)) {
				t.Fatalf("修复结果不是合法 JSON: %q", got)
			}
		})
	}
}

// TestRepairArgsJSON_RawFallback 修不出来的垃圾必须走 _raw 包裹:结构合法、原文不丢。
func TestRepairArgsJSON_RawFallback(t *testing.T) {
	for _, in := range []string{
		`{'a':1}`,          // 单引号(邋遢 JSON,超出最小修复器范围)
		`{a:1}`,            // 键无引号 → 补全后仍非法
		`{}{}`,             // 拼接对象
		`{"a":1}}`,         // 多余闭合
		`总之失败了`,            // 纯文本
		`{"a":"x","a":1e5x`, // 数字里混字母
	} {
		got := repairArgsJSON(in)
		if !json.Valid([]byte(got)) {
			t.Fatalf("兜底结果不是合法 JSON: %q -> %q", in, got)
		}
		var m map[string]string
		if err := json.Unmarshal([]byte(got), &m); err != nil || m["_raw"] != in {
			t.Fatalf("兜底应为 {\"_raw\": 原文}: %q -> %q", in, got)
		}
	}
}

// TestRepairArgsJSON_Idempotent 幂等:修复结果再修一遍必须原样返回,
// 否则同一会话反复发送时 arguments 字节不稳定,前缀缓存被击穿。
func TestRepairArgsJSON_Idempotent(t *testing.T) {
	for _, in := range []string{
		``, `null`, `{"path":"a/b`, `{"pa`, `{"a":1,`, `{'a':1}`, `垃圾`, `{"a":tru`,
	} {
		once := repairArgsJSON(in)
		twice := repairArgsJSON(once)
		if once != twice {
			t.Fatalf("不幂等: %q -> %q -> %q", in, once, twice)
		}
	}
}

// TestRepairToolCallArgs_CopyOnWrite 修复必须 copy-on-write:
// 出站副本被修好,而与存储 history 共享底层数组的原切片一个字节都不能动。
func TestRepairToolCallArgs_CopyOnWrite(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "任务"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "c1", Type: "function", Function: ToolCallFunc{Name: "Bash", Arguments: `{"command":"ls`}},
			{ID: "c2", Type: "function", Function: ToolCallFunc{Name: "Read", Arguments: `{"path":"a.go"}`}},
		}},
		{Role: "tool", ToolCallID: "c1", Content: "参数解析失败"},
	}
	out := repairToolCallArgs(msgs)

	if got := out[1].ToolCalls[0].Function.Arguments; got != `{"command":"ls"}` {
		t.Fatalf("出站副本应被修复, got %q", got)
	}
	if got := out[1].ToolCalls[1].Function.Arguments; got != `{"path":"a.go"}` {
		t.Fatalf("合法 arguments 不应变化, got %q", got)
	}
	if got := msgs[1].ToolCalls[0].Function.Arguments; got != `{"command":"ls` {
		t.Fatalf("原切片被就地修改(会污染存储的 history): %q", got)
	}
}

// TestRepairToolCallArgs_NoopSameSlice 无需修复时必须原样返回原切片(零分配、零字节变化)。
func TestRepairToolCallArgs_NoopSameSlice(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "任务"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "c1", Type: "function", Function: ToolCallFunc{Name: "Read", Arguments: `{"path":"a.go"}`}},
		}},
	}
	out := repairToolCallArgs(msgs)
	if &out[0] != &msgs[0] {
		t.Fatal("no-op 时应返回原切片,不应复制")
	}
}

// TestRepairThenSanitize_Compose 发送前两道变换组合:repair 修 arguments,sanitize 修配对,
// 互不干扰 —— 这是 streamAttempt / CallWithTools 实际的发送形态。
func TestRepairThenSanitize_Compose(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "任务"},
		// 坏 arguments 但配对完整:repair 修,sanitize 保留
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "c1", Type: "function", Function: ToolCallFunc{Name: "Bash", Arguments: ``}},
		}},
		{Role: "tool", ToolCallID: "c1", Content: "ok"},
		// 孤儿 tool:sanitize 剔除
		{Role: "tool", ToolCallID: "ghost", Content: "孤儿"},
	}
	out := sanitizeToolPairs(repairToolCallArgs(msgs))
	if len(out) != 3 {
		t.Fatalf("孤儿 tool 应被剔除, got %d 条", len(out))
	}
	if got := out[1].ToolCalls[0].Function.Arguments; got != `{}` {
		t.Fatalf("空 arguments 应修为 {}, got %q", got)
	}
}

// TestRewriteToolCallArgsForHistory_Repairs 入历史路径:空/坏 arguments 被修复;
// 当前实现不再折叠 Write 参数 —— 大 content 原样保留(由调用方按 elidedWriteInfo
// 整体移除并渲染执行记录)。
func TestRewriteToolCallArgsForHistory_Repairs(t *testing.T) {
	big := strings.Repeat("x", maxInlineWriteContentBytes+1)
	bigArgs, _ := json.Marshal(map[string]string{"path": "a.txt", "content": big})
	in := []ToolCall{
		{ID: "c1", Type: "function", Function: ToolCallFunc{Name: "Bash", Arguments: ``}},
		{ID: "c2", Type: "function", Function: ToolCallFunc{Name: "Read", Arguments: `{"path":"a.go`}},
		{ID: "c3", Type: "function", Function: ToolCallFunc{Name: "Write", Arguments: string(bigArgs)}},
	}
	out := rewriteToolCallArgsForHistory(in)

	if got := out[0].Function.Arguments; got != `{}` {
		t.Fatalf("空 arguments 应修为 {}, got %q", got)
	}
	if got := out[1].Function.Arguments; got != `{"path":"a.go"}` {
		t.Fatalf("截断 arguments 应被补全, got %q", got)
	}
	if !json.Valid([]byte(out[2].Function.Arguments)) || !strings.Contains(out[2].Function.Arguments, big) {
		t.Fatalf("Write 大 content 应原样保留(仅修 JSON): %q", out[2].Function.Arguments)
	}
	// 执行用的原始 toolCalls 不受影响
	if in[0].Function.Arguments != `` || in[1].Function.Arguments != `{"path":"a.go` {
		t.Fatal("原始 toolCalls 不应被修改")
	}
}
