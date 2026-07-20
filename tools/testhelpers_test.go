package tools

import (
	"strings"
	"testing"
)

// extractID 从 RunCommand/startBackground 的输出里解析出后台句柄 id(形如 "id: bash_123)")。
// 纯字符串解析、无平台依赖,放在不带 //go:build 约束的文件里 —— 这样 bg_test.go(!windows)
// 和 command_test.go(跨平台、运行时按 GOOS 跳过)都能用,且不会在 Windows 上因它未定义而
// 编译失败(issue #195 交叉编译时暴露)。
func extractID(t *testing.T, output string) string {
	t.Helper()
	_, rest, ok := strings.Cut(output, "id: ")
	if !ok {
		t.Fatalf("输出里找不到 id: %q", output)
	}
	end := strings.IndexAny(rest, ")\n ")
	if end < 0 {
		t.Fatalf("解析 id 失败: %q", output)
	}
	return rest[:end]
}
