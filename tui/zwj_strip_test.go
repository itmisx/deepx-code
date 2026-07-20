package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestStripEmojiZWJ 验证只剥「两侧都是 emoji」的 ZWJ,普通文本里的 ZWJ 必须保留
// (天城文/阿拉伯文靠它控制连字,删了会破字形)。
func TestStripEmojiZWJ(t *testing.T) {
	const zwj = "‍"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"家庭组合拆开", "👨" + zwj + "👩" + zwj + "👧", "👨👩👧"},
		{"技术员拆开", "👨" + zwj + "💻", "👨💻"},
		{"彩虹旗(跨 VS16)拆开", "🏳️" + zwj + "🌈", "🏳️🌈"},
		{"无 ZWJ 原样", "你好 👋 世界", "你好 👋 世界"},
		{"非 emoji 的 ZWJ 保留", "क" + zwj + "ष", "क" + zwj + "ष"},
		{"ASCII 间的 ZWJ 保留", "a" + zwj + "b", "a" + zwj + "b"},
		{"emoji 与非 emoji 之间保留", "👋" + zwj + "a", "👋" + zwj + "a"},
		{"空串", "", ""},
	}
	for _, c := range cases {
		if got := stripEmojiZWJ(c.in); got != c.want {
			t.Errorf("%s: stripEmojiZWJ(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestStripEmojiZWJAlignsWidth 钉死本修复的目的:剥离后 deepx 算出的宽度 = N 个独立
// emoji 各 2 列之和,与各终端实测一致(VSCode/Terminal.app 对独立 emoji 都渲 2)。
// 剥离前 deepx 按字素簇只算 2,与终端实渲(VSCode 6 / Terminal.app 8)对不上 → 分割线偏。
func TestStripEmojiZWJAlignsWidth(t *testing.T) {
	const zwj = "‍"
	family := "👨" + zwj + "👩" + zwj + "👧"

	before := ansi.StringWidth(family)
	if before != 2 {
		t.Logf("注意:剥离前宽度为 %d(预期 2,按字素簇算一次)", before)
	}
	after := ansi.StringWidth(stripEmojiZWJ(family))
	if after != 6 {
		t.Fatalf("剥离后应为 3 个独立 emoji = 6 列,实得 %d", after)
	}
	if strings.ContainsRune(stripEmojiZWJ(family), 0x200D) {
		t.Fatal("剥离后不应残留 ZWJ")
	}
}
