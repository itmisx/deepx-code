package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// 复现「多行输入时方向键↑失效」:光标不在首行时,按↑应把光标上移一行,
// 而非误触发历史翻阅(inputHistoryIndex 变 >=0)。
func TestUpArrowMovesCursorOnMultiline(t *testing.T) {
	m := initModel()
	m.input.SetWidth(60)
	m.input.SetHeight(inputTextRows) // 真实 3 行框
	// 5 行内容,超过 3 行框 → 可滚动。
	m.input.SetValue("l1\nl2\nl3\nl4\nl5")

	// 把光标放到最后一行(行号 4,0-indexed)。
	m.input.CursorEnd()

	before := m.input.Line()
	if before != 4 {
		t.Fatalf("前置失败:预期光标在第 4 行,实际 %d", before)
	}

	// 模拟键盘 ↑。
	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	after := mm.(model)

	t.Logf("[up] before line=%d after line=%d historyIdx=%d input=%q",
		before, after.input.Line(), after.inputHistoryIndex, after.input.Value())

	// 光标应上移一行(值不变,且不进入历史翻阅态:空历史下 navigateHistoryUp 会提前返回,
	// 故不能用 historyIdx>=0 判定 —— int 零值就是 0;这里用「值未被替换」佐证未翻阅)。
	if after.input.Value() != "l1\nl2\nl3\nl4\nl5" {
		t.Errorf("❌ ↑ 不应改变输入值,实际 %q", after.input.Value())
	}
	if after.input.Line() != before-1 {
		t.Errorf("❌ ↑ 未把光标上移:期望 %d 行,实际 %d 行", before-1, after.input.Line())
	}
}

// 对照:光标确在首行(0)、内容非空时,↑ 才应翻阅历史。
func TestUpArrowOnFirstLineNavigatesHistory(t *testing.T) {
	m := initModel()
	m.input.SetWidth(60)
	m.input.SetHeight(inputTextRows)
	m.input.SetValue("l1\nl2\nl3\nl4\nl5")
	// 光标移到首行首列。
	m.input.MoveToBegin()

	before := m.input.Line()
	if before != 0 {
		t.Fatalf("前置失败:预期光标在第 0 行,实际 %d", before)
	}
	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	after := mm.(model)
	t.Logf("[up@0] historyIdx=%d input=%q", after.inputHistoryIndex, after.input.Value())
	if after.inputHistoryIndex < 0 {
		t.Errorf("❌ 首行↑应触发历史翻阅,实际未触发(idx=%d)", after.inputHistoryIndex)
	}
}
