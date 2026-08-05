package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// mkTC 构造一个工具调用。
func mkTC(name, argsJSON string) ToolCall {
	return ToolCall{ID: "id1", Type: "function", Function: ToolCallFunc{Name: name, Arguments: argsJSON}}
}

// argsMap 把 arguments JSON 解回 map,断言仍是合法 JSON。
func argsMap(t *testing.T, argsJSON string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		t.Fatalf("结果不是合法 JSON: %v\n%s", err, argsJSON)
	}
	return m
}

// bigWriteArgs 构造一个 content 远超 512 字节的 Write 调用。
func bigWriteArgs(path string) string {
	return `{"path":"` + path + `","content":` + jsonStr(strings.Repeat("这是一段中文内容。", 200)) + `}`
}

// 核心:大 content 的 Write 不再折叠参数,而是整体判定为"需外置" ——
// 由调用方从 assistant tool_calls 移除并渲染成独立执行记录。elidedWriteInfo 是判定依据。
func TestElidedWriteInfo_LargeContent(t *testing.T) {
	in := mkTC("Write", bigWriteArgs("a/b/中文.go"))
	path, size, lines, ok := elidedWriteInfo(in.Function.Arguments)
	if !ok {
		t.Fatalf("大 content 应判定为需外置")
	}
	if path != "a/b/中文.go" {
		t.Fatalf("path 解析错误, got=%q", path)
	}
	if size <= 512 || lines < 1 {
		t.Fatalf("size/lines 应反映实际内容, size=%d lines=%d", size, lines)
	}
}

func TestElidedWriteInfo_NotElided(t *testing.T) {
	cases := []string{
		`{"path":"x","content":"小内容"}`,
		"{broken" + strings.Repeat("x", 600),
		`{"path":"x.go","command":"` + strings.Repeat("a", 600) + `"}`,
	}
	for _, c := range cases {
		if _, _, _, ok := elidedWriteInfo(c); ok {
			t.Fatalf("不应判定为需外置: %q", c)
		}
	}
}

// 执行记录:固定模板,只含确定性元信息(路径/大小/行数),不含 content 预览
// (预览会成为新的模仿源)。模型读到的是"结果记录",语义上不会与 Write 调用范式混淆。
func TestExecRecordMessage_FixedTemplate(t *testing.T) {
	msg := execRecordMessage("config.yaml", 1247, 42)
	if msg.Role != "user" {
		t.Fatalf("执行记录应为 user 消息(系统注入), got=%q", msg.Role)
	}
	for _, want := range []string{"Write 执行记录", "工具: Write", "config.yaml", "1247", "42", "状态: 成功"} {
		if !strings.Contains(msg.Content, want) {
			t.Fatalf("执行记录应含 %q, got=%q", want, msg.Content)
		}
	}
	if strings.Contains(msg.Content, "content") || strings.Contains(msg.Content, "body") {
		t.Fatalf("执行记录不应含内容预览, got=%q", msg.Content)
	}
}

// rewriteToolCallArgsForHistory 现在只修 JSON,不再折叠任何参数 ——
// 大 Write 的 content 原样保留(由调用方决定是否整体移除并渲染执行记录)。
func TestRewrite_KeepsLargeWriteContent(t *testing.T) {
	raw := bigWriteArgs("big.go")
	in := mkTC("Write", raw)
	out := rewriteToolCallArgsForHistory([]ToolCall{in})
	if out[0].Function.Arguments != raw {
		t.Fatalf("不折叠参数:大 Write content 应原样保留\n want=%s\n got =%s", raw, out[0].Function.Arguments)
	}
}

func TestRewrite_RepairsBadJSON(t *testing.T) {
	// 坏 arguments 仍应被修复为合法 JSON(issue #201 防严格后端 400)。
	in := mkTC("Bash", `{"command":`)
	out := rewriteToolCallArgsForHistory([]ToolCall{in})
	argsMap(t, out[0].Function.Arguments) // 合法 JSON 断言
}

func TestRewrite_DoesNotMutateOriginal(t *testing.T) {
	content := strings.Repeat("y", 600)
	raw := `{"path":"z.go","content":` + jsonStr(content) + `}`
	orig := []ToolCall{mkTC("Write", raw)}
	_ = rewriteToolCallArgsForHistory(orig)
	if orig[0].Function.Arguments != raw {
		t.Fatalf("原始 toolCalls 不应被改动(执行用的是它)")
	}
}

func TestRewrite_MixedBatch(t *testing.T) {
	bigWrite := `{"path":"big.go","content":` + jsonStr(strings.Repeat("a", 600)) + `}`
	tcs := []ToolCall{
		mkTC("Write", bigWrite),
		mkTC("Update", `{"path":"u.go","old_string":"a","new_string":"b"}`),
		mkTC("Read", `{"path":"r.go"}`),
	}
	out := rewriteToolCallArgsForHistory(tcs)
	for i := range out {
		if out[i].Function.Arguments != tcs[i].Function.Arguments {
			t.Fatalf("不折叠任何参数(仅修 JSON), 第 %d 个被改动:\n want=%s\n got =%s", i, tcs[i].Function.Arguments, out[i].Function.Arguments)
		}
	}
}

// 多轮连续 Write 大文件:每轮的 Write 都应被 elidedWriteInfo 识别为"需外置",
// 调用方据此把它从 assistant tool_calls 移除 → 历史里不存在任何
// "缺 content / 带折叠标记"的伪 Write,模型学到的 Write 范式始终完整。
func TestMultiTurn_AllLargeWritesElided(t *testing.T) {
	for i := 0; i < 5; i++ {
		in := mkTC("Write", `{"path":"f`+string(rune('a'+i))+`.txt","content":`+jsonStr(strings.Repeat("内容", 300))+`}`)
		if _, _, _, ok := elidedWriteInfo(in.Function.Arguments); !ok {
			t.Fatalf("第 %d 轮大 Write 应判定为需外置", i+1)
		}
	}
}

// jsonStr 把字符串编成合法 JSON 字面量(带引号、转义),供拼接测试用例。
func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
