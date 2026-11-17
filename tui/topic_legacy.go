package tui

import "strings"

// 会话主题跟踪(模型每轮尾部自报 <topic shift="yes|no">…</topic>)已移除:右栏那一行信息量
// 不足以抵掉它的代价 —— 每轮多一条格式硬性要求、流式增量要跨 chunk 剥标签。
//
// 这里只留一个「读历史时的向后兼容」:旧会话的 history.gob / jsonl 里存的是原文,还带着那些
// 标签。重放对话区时一次性剥掉,否则老会话一打开就能看见裸标签。只处理完整文本、不处理流式,
// 所以不需要原来那套状态机。
func stripLegacyTopicTags(s string) string {
	for {
		i := strings.Index(s, "<topic")
		if i < 0 {
			break
		}
		j := strings.Index(s[i:], "</topic>")
		if j < 0 {
			break // 标签没闭合(被截断的老回复):原样保留,不吞正文
		}
		// 标签独占一行,连它前面的空白一起丢,免得回复末尾留空行。
		head := strings.TrimRight(s[:i], " \t\r\n")
		s = head + s[i+j+len("</topic>"):]
	}
	return strings.TrimRight(s, " \t\r\n")
}
