package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// makeBigArgs 返回字节长度精确为 n 的合法 JSON 字符串(模拟被内联的大文件参数)。
func makeBigArgs(n int) string {
	// 用 {"x":"<pad>"} 形态,先算出固定开销,再补 pad 到精确 n 字节。
	const prefix = `{"x":"`
	const suffix = `"}`
	pad := strings.Repeat("a", n-len(prefix)-len(suffix))
	s := prefix + pad + suffix
	if len(s) != n {
		panic("makeBigArgs 长度计算错误")
	}
	return s
}

func TestTruncateNoOpWhenSmall(t *testing.T) {
	// 所有参数都远小于阈值 → 不应截断,原 convo 不被改动,返回 truncated=false。
	orig := []ChatMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "c1", Function: ToolCallFunc{Name: "Read", Arguments: `{"path":"a.txt"}`}},
		}},
		{Role: "tool", ToolCallID: "c1", Content: "ok"},
	}
	snapshot := append([]ChatMessage(nil), orig...)

	out, truncated := truncateToolArgs(orig)
	if truncated {
		t.Fatal("小参数不应触发截断")
	}
	if len(out) != len(orig) {
		t.Fatalf("消息条数应一致,got %d", len(out))
	}
	if out[1].ToolCalls[0].Function.Arguments != `{"path":"a.txt"}` {
		t.Fatalf("小参数应原样保留,got %q", out[1].ToolCalls[0].Function.Arguments)
	}
	// 原始 convo 必须未被改动(走副本)。
	if orig[1].ToolCalls[0].Function.Arguments != `{"path":"a.txt"}` {
		t.Fatal("原始 convo 被意外改动")
	}
	if len(snapshot) != len(orig) {
		t.Fatal("原始 convo 被意外改动(长度)")
	}
}

func TestTruncateLargeArg(t *testing.T) {
	// 单个超大参数 → 替换为占位 JSON,original_bytes 正确,原始 convo 不动。
	big := makeBigArgs(maxToolArgBytes + 100)
	orig := []ChatMessage{
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "c1", Function: ToolCallFunc{Name: "Write", Arguments: big}},
		}},
	}
	out, truncated := truncateToolArgs(orig)
	if !truncated {
		t.Fatal("超大参数应触发截断")
	}
	got := out[0].ToolCalls[0].Function.Arguments
	if got == big {
		t.Fatal("超大参数未被替换")
	}
	// 占位必须是合法 JSON,且 original_bytes 等于原字节数。
	var ph map[string]any
	if err := json.Unmarshal([]byte(got), &ph); err != nil {
		t.Fatalf("占位不是合法 JSON: %v (%q)", err, got)
	}
	if ph["_deepx_truncated"] != true {
		t.Fatalf("占位缺少 _deepx_truncated 标记: %v", ph)
	}
	if int(ph["original_bytes"].(float64)) != len(big) {
		t.Fatalf("original_bytes 应为 %d, got %v", len(big), ph["original_bytes"])
	}
	// 原始 convo 不动。
	if orig[0].ToolCalls[0].Function.Arguments != big {
		t.Fatal("原始 convo 被意外改动")
	}
}

func TestTruncateBoundary(t *testing.T) {
	// 边界:恰好等于阈值不截断;阈值+1 截断。这是最容易出 off-by-one 的地方。
	atThreshold := makeBigArgs(maxToolArgBytes)
	overThreshold := makeBigArgs(maxToolArgBytes + 1)

	in := []ChatMessage{
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "a", Function: ToolCallFunc{Name: "Write", Arguments: atThreshold}},
			{ID: "b", Function: ToolCallFunc{Name: "Write", Arguments: overThreshold}},
		}},
	}
	out, truncated := truncateToolArgs(in)
	if !truncated {
		t.Fatal("超过阈值的参数应触发截断")
	}
	if out[0].ToolCalls[0].Function.Arguments != atThreshold {
		t.Fatalf("恰好等于阈值不应被截断,got %q", out[0].ToolCalls[0].Function.Arguments)
	}
	if out[0].ToolCalls[1].Function.Arguments == overThreshold {
		t.Fatal("超过阈值应被截断")
	}
}

func TestTruncateMixedSizesSameMessage(t *testing.T) {
	// 同一条 assistant 消息里多个 tool_calls,只有大的被截,小的保留。
	small := `{"path":"a.txt"}`
	big := makeBigArgs(maxToolArgBytes + 50)
	in := []ChatMessage{
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "s", Function: ToolCallFunc{Name: "Read", Arguments: small}},
			{ID: "b", Function: ToolCallFunc{Name: "Write", Arguments: big}},
		}},
	}
	out, truncated := truncateToolArgs(in)
	if !truncated {
		t.Fatal("应触发截断")
	}
	if out[0].ToolCalls[0].Function.Arguments != small {
		t.Fatalf("小参数应保留,got %q", out[0].ToolCalls[0].Function.Arguments)
	}
	if out[0].ToolCalls[1].Function.Arguments == big {
		t.Fatal("大参数应被截断")
	}
	// ID 必须保留,否则后续 sanitize 配对会失效。
	if out[0].ToolCalls[1].ID != "b" {
		t.Fatalf("截断后 tool_call ID 丢失: %q", out[0].ToolCalls[1].ID)
	}
}

func TestTruncateOnlyTargetsOversizedMessages(t *testing.T) {
	// 多条消息,只有含超大参数的那条被改;其余原样。
	big := makeBigArgs(maxToolArgBytes + 10)
	in := []ChatMessage{
		{Role: "user", Content: "read a.html"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "c1", Function: ToolCallFunc{Name: "Read", Arguments: `{"path":"a.html"}`}},
		}},
		{Role: "tool", ToolCallID: "c1", Content: "file content"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "c2", Function: ToolCallFunc{Name: "Write", Arguments: big}},
		}},
	}
	out, truncated := truncateToolArgs(in)
	if !truncated {
		t.Fatal("应触发截断")
	}
	if len(out) != 4 {
		t.Fatalf("消息条数应不变,got %d", len(out))
	}
	if out[1].ToolCalls[0].Function.Arguments != `{"path":"a.html"}` {
		t.Fatal("无超大参数的消息不应被改")
	}
	if out[3].ToolCalls[0].Function.Arguments == big {
		t.Fatal("含超大参数的消息应被截")
	}
}

func TestTruncateSurvivesSanitize(t *testing.T) {
	// 关键集成点:截断后仍要能通过 sanitizeToolPairs 配对(截断只改 Arguments,不动 ID)。
	// 否则 streamAttempt 里 truncate→sanitize 的顺序会让被截的 tool_call 因失配被剥掉。
	big := makeBigArgs(maxToolArgBytes + 200)
	in := []ChatMessage{
		{Role: "user", Content: "copy a.html to b.html"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "c1", Function: ToolCallFunc{Name: "Write", Arguments: big}},
		}},
		{Role: "tool", ToolCallID: "c1", Content: "written"},
	}
	truncated, _ := truncateToolArgs(in)
	sanitized := sanitizeToolPairs(truncated)

	// 截断后的 assistant 仍应保留其 tool_call(ID 配对成功),tool 响应也保留。
	if len(sanitized) != 3 {
		t.Fatalf("截断+消毒后配对应完整,got %d 条: %v", len(sanitized), roles(sanitized))
	}
	if len(sanitized[1].ToolCalls) != 1 || sanitized[1].ToolCalls[0].ID != "c1" {
		t.Fatalf("截断后 tool_call 应在 sanitize 后保留,got %+v", sanitized[1])
	}
	if sanitized[2].Role != "tool" || sanitized[2].ToolCallID != "c1" {
		t.Fatalf("tool 响应应保留,got %+v", sanitized[2])
	}
	// 占位 JSON 必须能随整条消息正常序列化(避免 400)。
	if _, err := json.Marshal(sanitized); err != nil {
		t.Fatalf("截断后消息无法序列化: %v", err)
	}
}
