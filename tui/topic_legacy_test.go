package tui

import "testing"

// 老会话历史里的 <topic> 标签重放时要剥掉,且不能吞掉正文 / 留下空行。
func TestStripLegacyTopicTags(t *testing.T) {
	cases := []struct{ in, want string }{
		{"改完了。\n\n<topic shift=\"no\">修 bug</topic>", "改完了。"},
		{"<topic shift=\"yes\">换话题</topic>", ""},
		{"没有标签的回复", "没有标签的回复"},
		{"a\n<topic>x</topic>\nb\n<topic>y</topic>", "a\nb"},
		{"被截断了 <topic shift=\"no\">修", "被截断了 <topic shift=\"no\">修"}, // 没闭合:原样保留
	}
	for _, c := range cases {
		if got := stripLegacyTopicTags(c.in); got != c.want {
			t.Errorf("stripLegacyTopicTags(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
