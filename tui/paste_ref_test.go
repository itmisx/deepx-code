package tui

import (
	"strings"
	"testing"
)

func TestFormatPastedTextRef(t *testing.T) {
	if got := formatPastedTextRef(1, 0); got != "[Pasted text #1]" {
		t.Fatalf("numLines=0: got %q", got)
	}
	if got := formatPastedTextRef(3, 12); got != "[Pasted text #3 +12 lines]" {
		t.Fatalf("numLines>0: got %q", got)
	}
}

func TestExpandPastedTextRefs(t *testing.T) {
	store := map[int]string{
		1: "line1\nline2\nline3",
		2: "alpha\nbeta\n",
	}
	// 单占位符
	if got := expandPastedTextRefs("[Pasted text #1 +2 lines]", store); got != "line1\nline2\nline3" {
		t.Fatalf("single: got %q", got)
	}
	// 多占位符(混合文本),验证顺序与偏移
	in := "head [Pasted text #1 +2 lines] mid [Pasted text #2 +1 lines] tail"
	want := "head line1\nline2\nline3 mid alpha\nbeta\n tail"
	if got := expandPastedTextRefs(in, store); got != want {
		t.Fatalf("multi: got %q want %q", got, want)
	}
	// 无对应存储 → 保留占位符原样
	if got := expandPastedTextRefs("[Pasted text #9 +5 lines]", store); got != "[Pasted text #9 +5 lines]" {
		t.Fatalf("missing: got %q", got)
	}
}

// TestPasteThresholdLogic 验证触发占位符的判定与 free-code 一致:>800 字符或 >2 行。
func TestPasteThresholdLogic(t *testing.T) {
	check := func(text string) bool {
		return len(text) > pasteTextThreshold || strings.Count(text, "\n") > pasteLineThreshold
	}
	// 短单行 → 不触发
	if check("hello world") {
		t.Fatal("short single line should NOT trigger")
	}
	// 恰好 800 字符、1 行 → 不触发(严格大于)
	if check(strings.Repeat("a", 800)) {
		t.Fatal("exactly 800 chars, 1 line should NOT trigger")
	}
	// 801 字符 → 触发
	if !check(strings.Repeat("a", 801)) {
		t.Fatal("801 chars should trigger")
	}
	// 4 行(3 个换行) → 触发(free-code: numLines>2 才占位)
	if !check("a\nb\nc\nd") {
		t.Fatal("4 lines (3 newlines) should trigger")
	}
	// 3 行(2 个换行) → 不触发
	if check("a\nb\nc") {
		t.Fatal("3 lines (2 newlines) should NOT trigger")
	}
	// 2 行(1 个换行) → 不触发
	if check("a\nb") {
		t.Fatal("2 lines should NOT trigger")
	}
}
