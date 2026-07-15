package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// 复现并修复:"粘贴长文本→发送→打断→上翻(占位符正确)→下翻(不应展开截断全文)"。
// 根因:pendingUserText 存的是「展开后的全文」,打断回填输入框时把 190 行全文塞回
// 4000 上限的输入框被截断;随后上翻记下这个全文作 draft,下翻恢复 draft → 全文显示在输入区。
// 修复:回填用占位符形态 pendingInputOriginal,jsonl 仍落全文(pendingUserText)。
func TestPasteInterruptRefillKeepsPlaceholder(t *testing.T) {
	m := initModel()
	m.streamCh = make(chan tea.Msg) // 打断路径需要非空 streamCh

	// 1) 粘贴 190 行长文本 → 占位符
	raw := crlfText(190)
	mm, _ := m.Update(tea.PasteMsg{Content: raw})
	m = mm.(model)
	ph := m.input.Value() // "[Pasted text #0 +189 lines]"
	if !strings.Contains(ph, "[Pasted text #0") {
		t.Fatalf("前置失败:长粘贴未生成占位符: %q", ph)
	}
	t.Logf("[paste] input=%q", ph)

	// 2) 回车发送(此时 streaming=true,pendingUserText=全文,pendingInputOriginal=占位符)
	em, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = em.(model)
	t.Logf("[enter] streaming=%v pendingInputOriginal=%q pendingUserText.len=%d",
		m.streaming, m.pendingInputOriginal, len(m.pendingUserText))

	// 3) 连续两次 Esc(窗口内)→ 打断,回填输入框
	mm2, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mm2.(model)
	mm3, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mm3.(model)
	t.Logf("[interrupt] input=%q streaming=%v", m.input.Value(), m.streaming)

	got := m.input.Value()
	if strings.Contains(got, "\n") && len(got) > 200 {
		t.Errorf("❌ 打断回填把展开的全文塞回输入框(被截断/撑爆):\nlen=%d head=%q",
			len(got), firstN(got, 60))
	}
	if got != ph {
		t.Errorf("❌ 打断回填应为占位符 %q,实际 %q", ph, got)
	}

	// 4) 上翻:召回历史(占位符,正确)
	um, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = um.(model)
	t.Logf("[up] input=%q idx=%d draft=%q", m.input.Value(), m.inputHistoryIndex, m.inputDraft)
	if !strings.Contains(m.input.Value(), "[Pasted text #0") {
		t.Errorf("❌ 上翻应显示占位符,实际 %q", m.input.Value())
	}

	// 5) 下翻:退出翻阅,应恢复占位符(draft),绝不可是展开的全文。
	dm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = dm.(model)
	t.Logf("[down] input=%q idx=%d", m.input.Value(), m.inputHistoryIndex)
	if strings.Contains(m.input.Value(), "\n") && len(m.input.Value()) > 200 {
		t.Errorf("❌ 下翻把全文展开截断进输入框: len=%d", len(m.input.Value()))
	}
	if m.input.Value() != ph {
		t.Errorf("❌ 下翻应恢复占位符 %q,实际 %q", ph, m.input.Value())
	}
}

// 确认 jsonl 落盘路径仍拿「展开后的全文」(pendingUserText),不被占位符污染。
func TestPastePendingUserTextIsFull(t *testing.T) {
	m := initModel()
	raw := crlfText(190)
	mm, _ := m.Update(tea.PasteMsg{Content: raw})
	m = mm.(model)
	em, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = em.(model)
	if !strings.Contains(m.pendingUserText, "行内容0") || strings.Contains(m.pendingUserText, "[Pasted text #") {
		t.Errorf("❌ pendingUserText 应已展开为全文,实际 head=%q", firstN(m.pendingUserText, 40))
	}
	if m.pendingInputOriginal == "" || strings.Contains(m.pendingInputOriginal, "行内容0") {
		t.Errorf("❌ pendingInputOriginal 应保持占位符形态,实际 %q", m.pendingInputOriginal)
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
