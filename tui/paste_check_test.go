package tui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

func initModel() model {
	m := model{}
	m.input = textarea.New()
	m.input.Focus()
	m.chatContent = newChatLog(1 << 20)
	m.chatViewport = viewport.New()
	m.currentReply = &strings.Builder{}
	return m
}

// 造一段带 Windows 换行 \r\n 的粘贴文本。
func crlfText(lines int) string {
	raw := ""
	for i := 0; i < lines; i++ {
		raw += "行内容" + fmt.Sprint(i) + "\r\n"
	}
	return strings.TrimSuffix(raw, "\r\n")
}

// TestPasteSendShort:真正的"短粘贴"(行数<=2 且 <800 字节)走 textarea 正常路径,
// 验证 \r\n 不再被 Sanitizer 翻成 \n\n(这是"历史/输入框错乱"的根因)。
func TestPasteSendShort(t *testing.T) {
	m := initModel()
	// 3 行、2 个 \n、约 20 字节:行数(2)不超 pasteLineThreshold(2),
	// 字节数远低于 pasteTextThreshold(800)→走短粘贴路径(不生成占位符)。
	raw := "行A\r\n行B\r\n行C"
	if len(raw) >= pasteTextThreshold {
		t.Fatalf("测试构造错误:短粘贴样本 %d 字节 >= 阈值 %d", len(raw), pasteTextThreshold)
	}
	if strings.Count(raw, "\n") > pasteLineThreshold {
		t.Fatalf("测试构造错误:短粘贴样本行数 %d 超阈值 %d", strings.Count(raw, "\n"), pasteLineThreshold)
	}

	mm, _ := m.Update(tea.PasteMsg{Content: raw})
	m = mm.(model)
	t.Logf("输入框=%q  pastedTexts=%d", m.input.Value(), len(m.pastedTexts))
	if len(m.pastedTexts) != 0 {
		t.Errorf("❌ 短粘贴不应生成占位符: %q", m.input.Value())
	}

	em, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = em.(model)

	chat := m.chatContent.String()
	t.Logf("chatContent=%q", chat)
	if strings.Contains(chat, "[Pasted text #") {
		t.Errorf("❌ 聊天历史区仍是占位符: %q", chat)
	}
	if !strings.Contains(chat, "行A") || !strings.Contains(chat, "行C") {
		t.Errorf("❌ 聊天历史区未包含正文: %q", chat)
	}
	if strings.Contains(chat, "\n\n") {
		t.Errorf("❌ 仍存在翻倍换行 \\n\\n(历史/输入框会错乱): %q", chat)
	}
	if strings.Contains(chat, "\r") {
		t.Errorf("❌ 仍残留 \\r: %q", chat)
	}
}

// TestPasteSendLong:长粘贴(>800 字符)走占位符,验证发送时展开为全文且 \r\n 已归一。
func TestPasteSendLong(t *testing.T) {
	m := initModel()
	raw := crlfText(200) // 远超阈值
	if len(raw) <= pasteTextThreshold {
		t.Fatalf("测试构造错误:长粘贴样本 %d 字节 <= 阈值 %d", len(raw), pasteTextThreshold)
	}

	mm, _ := m.Update(tea.PasteMsg{Content: raw})
	m = mm.(model)
	t.Logf("输入框=%q  pastedTexts=%d", m.input.Value(), len(m.pastedTexts))
	if !strings.Contains(m.input.Value(), "[Pasted text #") {
		t.Errorf("❌ 长粘贴未生成占位符: %q", m.input.Value())
	}

	em, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = em.(model)

	chat := m.chatContent.String()
	t.Logf("chatContent=%q", chat)
	if strings.Contains(chat, "[Pasted text #") {
		t.Errorf("❌ 聊天历史区仍是占位符,发送时展开未生效: %q", chat)
	}
	if !strings.Contains(chat, "行内容199") {
		t.Errorf("❌ 聊天历史区未包含展开后的全文: %q", chat)
	}
	if strings.Contains(chat, "\n\n") {
		t.Errorf("❌ 仍存在翻倍换行 \\n\\n: %q", chat)
	}
	if strings.Contains(chat, "\r") {
		t.Errorf("❌ 仍残留 \\r: %q", chat)
	}
}
