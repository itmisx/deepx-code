package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// 复现「多行(实为被 wrap 的长行)输入时方向键↑失效」:
// 窄宽下一个超长单行被折成多虚拟行,光标停在(内容行0,底部虚拟行)。
// 此时 m.input.Line() 恒为 0,旧条件 Line()==0 误判为「已在首行」→ 触发历史翻阅,
// 光标无法在折行内上移;而鼠标滚轮直接 m.input.Update(KeyUp) 绕过该分支故正常。
// 修复后:仅在光标真处于「最顶端(内容行0 且 RowOffset==0)」时才翻阅历史。
func TestUpArrowOnWrappedLineBug(t *testing.T) {
	m := initModel()
	m.input.SetWidth(10) // 窄宽 → 长行大量 wrap
	m.input.SetHeight(inputTextRows)

	long := "一二三四五六七八九十甲乙丙丁戊己庚辛壬癸子丑寅卯辰巳午未申酉戌亥"
	m.input.SetValue(long) // 单行,无换行
	m.input.CursorEnd()    // 光标在(行0, 底虚拟行)

	// 注入一条历史,使「误触历史翻阅」可观测(值会被替换成历史项)。
	m.inputHistory = []string{"__HISTORY_OLD__"}
	m.inputHistoryIndex = -1

	before := m.input.Line()
	li := m.input.LineInfo()
	t.Logf("[before] line=%d RowOffset=%d Height=%d value=%q", before, li.RowOffset, li.Height, m.input.Value())

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	after := mm.(model)
	t.Logf("[up] value=%q historyIdx=%d", after.input.Value(), after.inputHistoryIndex)

	if after.input.Value() == "__HISTORY_OLD__" {
		t.Errorf("❌ ↑ 在 wrap 行内误触发历史翻阅(光标无法上移): value 被替换为历史项")
	}
	if after.input.Value() != long {
		t.Errorf("❌ 输入值不应被改,实际 %q", after.input.Value())
	}
}

// 对照:光标真在内容顶端(行0 且 RowOffset==0)时,↑ 仍应翻阅历史(标准 chat 行为)。
func TestUpArrowAtTrueTopNavigatesHistory(t *testing.T) {
	m := initModel()
	m.input.SetWidth(40) // 宽,单行不 wrap
	m.input.SetHeight(inputTextRows)
	m.input.SetValue("短行") // 单行,无换行,光标在行首即顶端
	m.input.MoveToBegin()

	li := m.input.LineInfo()
	if m.input.Line() != 0 || li.RowOffset != 0 {
		t.Fatalf("前置失败:预期光标在真顶端(line=0,RowOffset=0),实际 line=%d RowOffset=%d",
			m.input.Line(), li.RowOffset)
	}

	m.inputHistory = []string{"__HISTORY_OLD__"}
	m.inputHistoryIndex = -1

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	after := mm.(model)
	t.Logf("[up@top] value=%q historyIdx=%d", after.input.Value(), after.inputHistoryIndex)
	if after.input.Value() != "__HISTORY_OLD__" {
		t.Errorf("❌ 真顶端↑应翻阅历史,实际 value=%q", after.input.Value())
	}
}
