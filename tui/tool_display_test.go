package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUpdatePreviewLineNumbers 字符串模式 + 文件可读:locateLineInFile 反推出行号,
// old/new 块都带行号。
func TestUpdatePreviewLineNumbers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte("line1\nline2\nline3\nline4\n"), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	// 用 json.Marshal 构造,正确转义 Windows 反斜杠路径(否则 C:\...\sample.txt
	// 里的反斜杠会让 Unmarshal 失败、函数退化成 "Update"——这是 Windows 下
	// 该测试失败的真因(t.TempDir() 在非 Unix 返回反斜杠路径)。
	argsBytes, err := json.Marshal(map[string]string{
		"path":       path,
		"old_string": "line2\nline3",
		"new_string": "newA\nnewB",
	})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	args := string(argsBytes)
	out := formatUpdatePreview(args)
	if !strings.Contains(out, "2 - line2") {
		t.Errorf("expected '2 - line2', got:\n%s", out)
	}
	if !strings.Contains(out, "3 - line3") {
		t.Errorf("expected '3 - line3', got:\n%s", out)
	}
	if !strings.Contains(out, "2 + newA") {
		t.Errorf("expected '2 + newA', got:\n%s", out)
	}
	if !strings.Contains(out, "3 + newB") {
		t.Errorf("expected '3 + newB', got:\n%s", out)
	}
}

// TestUpdatePreviewNoLineInfo 文件读不到 / 没匹配:退化成无行号渲染。
func TestUpdatePreviewNoLineInfo(t *testing.T) {
	args := `{"path":"/nonexistent/file.txt","old_string":"foo","new_string":"bar"}`
	out := formatUpdatePreview(args)
	if !strings.Contains(out, "- foo") {
		t.Errorf("expected '- foo' fallback, got:\n%s", out)
	}
	if !strings.Contains(out, "+ bar") {
		t.Errorf("expected '+ bar' fallback, got:\n%s", out)
	}
}

// TestColorizeDiffBlock fence 行不显示,+/- 行染色,sign 检测能跳过 leading 数字行号。
func TestColorizeDiffBlock(t *testing.T) {
	in := "📝 Update (x.go)\n\n~~~diff\n  42 - old\n  42 + new\n~~~"
	out := colorizeDiffBlock(in)
	if strings.Contains(out, "~~~") {
		t.Errorf("fence should be hidden, got:\n%s", out)
	}
	if !strings.Contains(out, "42 - old") {
		t.Errorf("content preserved, got:\n%s", out)
	}
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("expected ANSI styling, got:\n%s", out)
	}
}
