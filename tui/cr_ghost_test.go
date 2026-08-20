package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	"github.com/charmbracelet/x/ansi"
)

// 裸 \r 进 viewport 会吞行:viewport 按行切分时把 "A\rB" 当成一个逻辑行,
// TotalLineCount 偏小 → YOffset / 取行全部错位 → 历史"吃掉上一条对话",
// 整帧行数对不上时聊天内容叠进输入区(issue #232)。
//
// 这里先证明毛病真实存在,再证明 normalizeNewlines 修得掉 —— 否则光看修复代码
// 无法判断它是不是在解决一个想象出来的问题。
func TestBareCR_SwallowsViewportLines(t *testing.T) {
	const w, h = 24, 8
	dirty := "第一条消息\r第二条消息\n第三条消息"

	count := func(content string) (int, []string) {
		vp := viewport.New()
		vp.SetWidth(w)
		vp.SetHeight(h)
		vp.SetContent(content)
		var out []string
		for _, ln := range strings.Split(ansi.Strip(vp.View()), "\n") {
			if ln = strings.TrimRight(ln, " "); ln != "" {
				out = append(out, ln)
			}
		}
		return vp.TotalLineCount(), out
	}

	if n, lines := count(dirty); n != 2 || len(lines) != 2 {
		t.Fatalf("前提变了:裸 \\r 本应把 3 行吞成 2 行,got TotalLineCount=%d lines=%v\n"+
			"若上游 viewport 已自行处理 \\r,本修复可以撤掉", n, lines)
	}

	n, lines := count(normalizeNewlines(dirty))
	if n != 3 {
		t.Errorf("归一化后 TotalLineCount 应为 3,got %d", n)
	}
	if len(lines) != 3 {
		t.Fatalf("归一化后应有 3 行,got %v", lines)
	}
	for i, want := range []string{"第一条消息", "第二条消息", "第三条消息"} {
		if lines[i] != want {
			t.Errorf("行%d want %q got %q", i, want, lines[i])
		}
	}
}

// 守住调用点:refreshViewport 必须在 SetContent **之前**归一化。
//
// 上一条测的是 normalizeNewlines 这个函数本身 —— 把 refreshViewport 里的调用删掉,
// 它照样绿(实测如此)。真正要守的是"清洗发生在进 viewport 之前"这件事:
// 早先 padLinesToWidth 里那道归一化就是因为作用在 viewport 的**输出**上而没能修好
// (见 dashboard.go 与 issue #232)。
func TestRefreshViewport_NormalizesBeforeSetContent(t *testing.T) {
	const w, h = 24, 8
	m := model{chatContent: newChatLog(1 << 20)}
	m.chatViewport = viewport.New()
	m.chatViewport.SetWidth(w)
	m.chatViewport.SetHeight(h)

	// 一条消息内部残留裸 \r —— 典型来源:LLM 输出 / 工具结果 / 从旧会话载入的历史。
	m.chatContent.Open(kindSystem, "第一条消息\r第二条消息")
	m.chatContent.Open(kindSystem, "第三条消息")

	m.refreshViewport()

	out := ansi.Strip(m.chatViewport.View())
	if strings.ContainsRune(out, '\r') {
		t.Error("裸 \\r 进了 viewport —— 会吞行并让滚动偏移错位")
	}
	// 两条消息不能被 \r 粘成一行
	if strings.Contains(out, "第一条消息第二条消息") {
		t.Errorf("两条消息被 \\r 粘成一行 —— 归一化没有发生在 SetContent 之前:\n%s", out)
	}
	if !strings.Contains(out, "第二条消息") || !strings.Contains(out, "第三条消息") {
		t.Errorf("有内容被吞掉:\n%s", out)
	}
}

// normalizeFrame 收到裸 \r 时**删除**,不能换成换行。
// 此刻入参是已经排好版的整帧:多出一行就会被随后的 lines[:height] 从底部截掉,
// 整个版式往下错位 —— 比原来的毛病更重。
func TestNormalizeFrame_DropsCRWithoutAddingLines(t *testing.T) {
	const w, h = 10, 4
	clean := normalizeFrame("aaa\nbbb\nccc\nddd", w, h)
	dirty := normalizeFrame("aaa\nb\rbb\nccc\nddd", w, h)

	if got := len(strings.Split(dirty, "\n")); got != h {
		t.Errorf("整帧行数应恒为 %d,got %d —— \\r 被换成了换行会顶掉底部内容", h, got)
	}
	if strings.ContainsRune(dirty, '\r') {
		t.Error("整帧里不该残留 \\r")
	}
	// 末行必须还在(被截掉的话说明多出了行)
	if !strings.Contains(dirty, "ddd") {
		t.Errorf("末行被挤掉了:\n%q", dirty)
	}
	if len(strings.Split(clean, "\n")) != h {
		t.Errorf("干净输入的行数也应为 %d", h)
	}
}
