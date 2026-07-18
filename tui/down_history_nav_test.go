package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// 复现「翻阅态下,光标在多行历史项的中间行,按 ↓ 应下移光标,而非翻到下一条历史」。
// 当前 down 键只要 inputHistoryIndex>=0 就直接 navigateHistoryDown(),
// 不判断光标是否已在历史项底部 —— 与 up 键(只在顶端才翻)不对称,
// 导致翻阅一条多行历史时,在中间行按 ↓ 会跳过剩余内容直接翻下一条。
func TestDownArrowInsideMultilineHistoryMovesCursor(t *testing.T) {
	m := initModel()
	m.input.SetWidth(40)
	m.input.SetHeight(inputTextRows)
	// 一条多行历史项, 让它在输入框里占 3 逻辑行
	m.inputHistory = []string{"h1\nh2\nh3"}
	m.inputHistoryIndex = -1

	// 按 ↑ 进入翻阅态, 显示 "h1\nh2\nh3", 光标默认在末尾(第2行)
	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = mm.(model)
	if m.inputHistoryIndex < 0 {
		t.Fatalf("前置失败: ↑ 未进入翻阅态")
	}
	// 把光标从末尾上移到中间行(第1行): 按两次 ↑(在翻阅态内应只移光标, 因为不在顶端)
	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = mm.(model)
	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = mm.(model)
	t.Logf("[before ↓] line=%d RowOffset=%d Height=%d historyIdx=%d val=%q",
		m.input.Line(), m.input.LineInfo().RowOffset, m.input.LineInfo().Height, m.inputHistoryIndex, m.input.Value())

	// 此刻光标在第1行(共3行, 0/1/2), 按 ↓ 应下移光标到末行, 不该翻历史
	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	after := mm.(model)
	t.Logf("[after ↓] line=%d RowOffset=%d historyIdx=%d val=%q",
		after.input.Line(), after.input.LineInfo().RowOffset, after.inputHistoryIndex, after.input.Value())

	if after.inputHistoryIndex < 0 {
		t.Errorf("❌ 翻阅态中间行按↓误恢复草稿/退出翻阅(historyIdx=%d)", after.inputHistoryIndex)
	}
	if after.input.Value() != "h1\nh2\nh3" {
		t.Errorf("❌ 翻阅态中间行按↓不应替换历史项, 实际 val=%q", after.input.Value())
	}
	if after.input.Line() <= m.input.Line() {
		t.Errorf("❌ 翻阅态中间行按↓应下移光标, 实际 line 未增大 (before=%d after=%d)",
			m.input.Line(), after.input.Line())
	}
}

// 对照: 光标已在多行历史项底部时, 按 ↓ 仍应翻下一条历史(标准 chat 行为)。
func TestDownArrowAtHistoryBottomNavigates(t *testing.T) {
	m := initModel()
	m.input.SetWidth(40)
	m.input.SetHeight(inputTextRows)
	m.inputHistory = []string{"h1\nh2\nh3", "__NEXT__"}
	// 直接置为翻阅态、停在第 0 条(多行)底部, 后面还有第 1 条可翻
	m.inputHistoryIndex = 0
	m.input.SetValue("h1\nh2\nh3")
	m.input.CursorEnd() // 光标在底部

	li := m.input.LineInfo()
	lastLine := strings.Count(m.input.Value(), "\n")
	if m.input.Line() != lastLine || li.RowOffset != li.Height-1 {
		t.Fatalf("前置失败: 光标应在历史项底部, 实际 line=%d lastLine=%d RowOffset=%d Height=%d",
			m.input.Line(), lastLine, li.RowOffset, li.Height)
	}

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	after := mm.(model)
	t.Logf("[down@bottom] historyIdx=%d val=%q lenHistory=%d", after.inputHistoryIndex, after.input.Value(), len(after.inputHistory))
	if after.input.Value() != "__NEXT__" {
		t.Errorf("❌ 历史项底部按↓应翻下一条, 实际 val=%q (historyIdx=%d)",
			after.input.Value(), after.inputHistoryIndex)
	}
}
