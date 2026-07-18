package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// 复现「输入区多行时, 鼠标滚轮应上下移动输入框光标, 而非滚动历史区」。
// 直接用 MouseWheelMsg 且 Y 落在输入区(vpH 以下)驱动:
//   - 滚轮下(MouseWheelDown) → 光标下移(下移到末行后由 textarea 钳住)
//   - 滚轮上(MouseWheelUp)   → 光标上移
// 同时历史区视口不应被滚动(YOffset 不变)。
func TestInputWheelScrollsCursor(t *testing.T) {
	m := initModel()
	m.input.SetWidth(40)
	m.input.SetHeight(inputTextRows)
	multiline := "line1\nline2\nline3\nline4" // 4 逻辑行, 超过 inputTextRows(3) → 可滚动
	m.input.SetValue(multiline)
	m.input.MoveToBegin() // 光标在顶端(line=0)

	_, vpH := m.layout()
	t.Logf("[init] vpH=%d line=%d", vpH, m.input.Line())

	// 在输入区发滚轮下: 光标应下移
	down := tea.MouseWheelMsg{Y: vpH + 2, Button: tea.MouseWheelDown, X: 5}
	mm, _ := m.Update(down)
	m = mm.(model)
	t.Logf("[wheel down] line=%d", m.input.Line())
	if m.input.Line() <= 0 {
		t.Errorf("❌ 输入区滚轮下应下移光标, 实际 line=%d", m.input.Line())
	}

	// 在输入区发滚轮上: 光标应上移回
	up := tea.MouseWheelMsg{Y: vpH + 2, Button: tea.MouseWheelUp, X: 5}
	mm, _ = m.Update(up)
	m = mm.(model)
	t.Logf("[wheel up] line=%d", m.input.Line())
	if m.input.Line() != 0 {
		t.Errorf("❌ 输入区滚轮上应上移回顶端, 实际 line=%d", m.input.Line())
	}
}

// 对照: 滚轮在历史区(Y <= vpH)时仍滚动 chat 视口, 不动输入光标。
func TestWheelInHistoryAreaScrollsChat(t *testing.T) {
	m := initModel()
	m.input.SetWidth(40)
	m.input.SetHeight(inputTextRows)
	m.input.SetValue("line1\nline2\nline3\nline4")
	m.input.MoveToBegin()

	_, vpH := m.layout()
	chatBefore := m.chatViewport.YOffset()

	// Y 落在历史区(<= vpH), 滚轮下 → 应滚动 chat(或至少不移动输入光标)
	down := tea.MouseWheelMsg{Y: vpH - 1, Button: tea.MouseWheelDown, X: 5}
	mm, _ := m.Update(down)
	m = mm.(model)
	t.Logf("[history wheel down] inputLine=%d chatYOffset=%d (before=%d)",
		m.input.Line(), m.chatViewport.YOffset(), chatBefore)

	if m.input.Line() != 0 {
		t.Errorf("❌ 历史区滚轮不应移动输入光标, 实际 line=%d", m.input.Line())
	}
}
