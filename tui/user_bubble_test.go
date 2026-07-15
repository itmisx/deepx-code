package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestRenderUserBubbleNoOverflow 验证用户消息里的超长无空格单行(日志/JSON/minified
// 代码)会被硬折行到 boxW 以内,不会横向溢出 chat 区。这是粘贴长文本的回归点。
func TestRenderUserBubbleNoOverflow(t *testing.T) {
	const viewportW = 40
	longNoSpace := strings.Repeat("x", 200) // 200 字符无空格,远超 boxW
	out := renderUserBubble(longNoSpace, viewportW)

	// 去掉 ANSI 后逐行检查显示宽度 <= boxW(boxW = viewportW-1)
	boxW := viewportW - 1
	maxW := 0
	for _, line := range strings.Split(out, "\n") {
		plain := ansi.Strip(line)
		w := ansi.StringWidth(plain)
		if w > maxW {
			maxW = w
		}
	}
	if maxW > boxW {
		t.Fatalf("line display width %d exceeds boxW %d (overflow)", maxW, boxW)
	}
	if maxW == 0 {
		t.Fatal("empty output")
	}
}

// TestRenderUserBubbleNormalText 验证普通带空格文本仍正常按行渲染。
func TestRenderUserBubbleNormalText(t *testing.T) {
	out := renderUserBubble("hello world\nsecond line", 40)
	if !strings.Contains(out, "hello world") || !strings.Contains(out, "second line") {
		t.Fatalf("normal text lost: %q", out)
	}
}
