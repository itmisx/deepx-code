package tui

import (
	"deepx/agent"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// mouseWheelDelta 每个滚轮事件滚动的行数(viewport.MouseWheelDelta)。VSCode 内置终端每个
// 物理刻度发出的滚轮事件比系统终端少,默认 3 行会显得滚动偏慢,调大补偿。
func mouseWheelDelta() int {
	if os.Getenv("TERM_PROGRAM") == "vscode" {
		return 6
	}
	return 3
}

// padLinesToWidth 把每行强制到精确 w 列宽:短行末尾补空格,长行用 ansi.Cut 切到 w。
func padLinesToWidth(content string, w int) string {
	if w <= 0 {
		return content
	}
	// 历史区(agent 输出、工具结果、从会话文件载入的旧消息)里残留的裸 \r 会一路
	// 透传到 uv 渲染层:uv 把裸 \r 当作"光标回到行首"(见 ultraviolet styled.go
	// 的 printString:遇 \r 即 x = bounds.Min.X),后续字符覆盖本行前半段,
	// 分隔线 ┃ 被顶到列 0 或错位,于是滚到那一条旧消息(固定滚动位置)时
	// 就整行错乱。这个 \r 隐患之前只在输入框/粘贴入口兜过,历史展示路径漏了。
	// 这里在"按 \n 切行 + 补/截列宽"之前统一归一化(\r\n 与单独的 \r 都转成 \n),
	// 覆盖 chat / 输入框 / 排队区全部展示内容,从源头消除。
	if strings.ContainsRune(content, '\r') {
		content = strings.ReplaceAll(content, "\r\n", "\n")
		content = strings.ReplaceAll(content, "\r", "\n")
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		cur := lineDisplayWidth(line)
		switch {
		case cur < w:
			lines[i] = line + strings.Repeat(" ", w-cur)
		case cur > w:
			lines[i] = ansi.Cut(line, 0, w)
		}
	}
	return strings.Join(lines, "\n")
}

var graphemeWidthMode = detectGraphemeMode()

var widthFunc = func() func(string) int {
	if graphemeWidthMode {
		return ansi.StringWidth
	}
	return ansi.StringWidthWc
}()

func detectGraphemeMode() bool {
	switch os.Getenv("TERM_PROGRAM") {
	case "vscode", "Apple_Terminal", "iTerm.app", "WezTerm", "ghostty":
		return true
	}
	if os.Getenv("KITTY_WINDOW_ID") != "" {
		return true
	}
	if os.Getenv("ALACRITTY_LOG") != "" || os.Getenv("ALACRITTY_WINDOW_ID") != "" {
		return true
	}
	if os.Getenv("VTE_VERSION") != "" || os.Getenv("KONSOLE_VERSION") != "" {
		return true
	}
	if runtime.GOOS == "windows" || os.Getenv("WT_SESSION") != "" {
		return true
	}
	return false
}

func lineDisplayWidth(s string) int {
	return widthFunc(s)
}

// isWhitespaceLike 判断 rune 是否是已经能起字符边界作用的空白。
func isWhitespaceLike(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', 0x00A0, 0x3000:
		return true
	}
	return false
}

// sumHistoryChars 把整段对话历史的 Content 字符数加起来,用作"已用上下文"的近似值。
// 不调 tokenizer 是为了零依赖 + 跨模型通用;按 ~3 chars/token 估算足够给用户一个量级感知。
func sumHistoryChars(h []agent.ChatMessage) int {
	total := 0
	for _, m := range h {
		total += len([]rune(m.Content))
		total += len([]rune(m.ReasoningContent))
		for _, p := range m.ContentParts {
			total += len([]rune(p.Text))
		}
	}
	return total
}

// formatTokenCount 把 token 计数格式化成紧凑字符串: 12 / 1.2K / 12.4K。
func formatTokenCount(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fK", float64(n)/1024.0)
}

// formatElapsed 把 duration 格式化成右栏能塞下的紧凑字符串。
// <60s: "4.2s"; 60-3600s: "2m13s"; ≥1h: "1h05m"。
func formatElapsed(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	if d < time.Hour {
		m := int(d / time.Minute)
		s := int(d/time.Second) % 60
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	h := int(d / time.Hour)
	m := int(d/time.Minute) % 60
	return fmt.Sprintf("%dh%02dm", h, m)
}

// abbreviatePath 把绝对路径压缩成 ~/... 形式以适配右栏窄宽。
// 超过 maxWidth 时从中间截断,保留头几段和最后一段。
func abbreviatePath(path string, maxWidth int) string {
	home := homeDir()
	if home != "" && strings.HasPrefix(path, home) {
		path = "~" + path[len(home):]
	}
	if maxWidth <= 0 || len(path) <= maxWidth {
		return path
	}
	// 从中间截断: 保留头部 + … + 尾部
	parts := strings.Split(path, "/")
	if len(parts) <= 2 {
		// 没法分段,从中间硬截
		half := (maxWidth - 1) / 2
		return path[:half] + "…" + path[len(path)-half:]
	}
	// 留最后一个目录名 + 尽量多的前段
	tail := "/" + parts[len(parts)-1]
	if len(tail) >= maxWidth-2 {
		return "…" + tail[len(tail)-(maxWidth-1):]
	}
	head := strings.Join(parts[:len(parts)-1], "/")
	budget := maxWidth - len(tail) - 1 // -1 给 "…"
	if budget < 1 {
		budget = 1
	}
	if len(head) > budget {
		head = head[:budget]
	}
	return head + "…" + tail
}

// homeDir 一次性查 $HOME,失败返回空串(走原路径)。
func homeDir() string {
	return os.Getenv("HOME")
}

func isEmojiLike(r rune) bool {
	switch {
	case r == 0x2713 || r == 0x2715 || r == 0x2717 || r == 0x2718:
		return false
	case r >= 0x2768 && r <= 0x2775:
		return false
	}
	switch {
	case r >= 0x1F000 && r <= 0x1FFFF:
		return true
	case r >= 0x2600 && r <= 0x27BF:
		return true
	case r >= 0x2300 && r <= 0x23FF:
		return true
	case r >= 0x2B00 && r <= 0x2BFF:
		return true
	}
	return false
}

func ensureEmojiSpacing(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	var sb strings.Builder
	sb.Grow(len(s) + 32)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		sb.WriteRune(r)
		if !isEmojiLike(r) {
			continue
		}
		if i+1 >= len(runes) {
			sb.WriteRune(0xFE0F)
			continue
		}
		next := runes[i+1]
		if next == 0x200D {
			continue
		}
		if next == 0xFE0F || next == 0xFE0E {
			sb.WriteRune(next)
			i++
			if i+1 >= len(runes) {
				continue
			}
			after := runes[i+1]
			if after != 0x200D && !isWhitespaceLike(after) {
				sb.WriteRune(' ')
			}
			continue
		}
		sb.WriteRune(0xFE0F)
		if !isWhitespaceLike(next) {
			sb.WriteRune(' ')
		}
	}
	return sb.String()
}

func ensureEmojiSpacingANSI(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	var sb strings.Builder
	sb.Grow(len(s) + 32)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		sb.WriteRune(r)

		// 遇 ESC:透传整段 ANSI 序列。覆盖最常见的 CSI(ESC [ ... final_byte)和
		// OSC(ESC ] ... BEL or ST)。final_byte 范围 0x40-0x7E 对 CSI。
		if r == 0x1B && i+1 < len(runes) {
			i++
			sb.WriteRune(runes[i])
			switch runes[i] {
			case '[':
				for i+1 < len(runes) {
					i++
					sb.WriteRune(runes[i])
					if runes[i] >= 0x40 && runes[i] <= 0x7E {
						break
					}
				}
			case ']':
				for i+1 < len(runes) {
					i++
					sb.WriteRune(runes[i])
					if runes[i] == 0x07 { // BEL
						break
					}
					if runes[i] == 0x1B && i+1 < len(runes) && runes[i+1] == '\\' {
						i++
						sb.WriteRune('\\')
						break
					}
				}
			}
			continue
		}

		if !isEmojiLike(r) {
			continue
		}
		if i+1 >= len(runes) {
			sb.WriteRune(0xFE0F)
			continue
		}
		next := runes[i+1]
		if next == 0x200D {
			continue
		}
		if next == 0xFE0F || next == 0xFE0E {
			sb.WriteRune(next)
			i++
			if i+1 >= len(runes) {
				continue
			}
			after := runes[i+1]
			if after != 0x200D && !isWhitespaceLike(after) {
				sb.WriteRune(' ')
			}
			continue
		}
		sb.WriteRune(0xFE0F)
		if !isWhitespaceLike(next) {
			sb.WriteRune(' ')
		}
	}
	return sb.String()
}

// stripVS16 去掉文本里所有 VS16(U+FE0F)。
func stripVS16(s string) string {
	if !strings.ContainsRune(s, '️') {
		return s
	}
	return strings.ReplaceAll(s, "️", "")
}

// stripEmojiZWJ 剥掉「两侧都是 emoji」的 ZWJ(U+200D),把 👨‍👩‍👧 这类组合拆成各自
// 独立的 emoji(👨👩👧)。
//
// 为什么这么做:ZWJ 组合的显示宽度**各终端口径互不相同** —— deepx 按字素簇算 2,
// DSR 探针实测 VSCode 渲 6、Terminal.app 渲 8、支持字素合并的终端渲 2。三方都不一样,
// 不存在能同时满足的宽度模型(照哪家改都会弄坏另外两家)。剥掉 ZWJ 后它变成 N 个独立
// emoji,deepx 与所有终端都算 N×2 → **终端无关地对齐**。
//
// 这与 stripVS16 是同一条原则:不去猜/复刻各终端怎么渲,而是**不发出「各家宽度有分歧」
// 的字符**。VS16 那条已被验证有效(❤️ 剥成 ❤ 后两终端都是 1,不再偏)。
//
// 只在**前后都是 emoji** 时剥:ZWJ 在天城文 / 阿拉伯文等脚本里用于控制连字,
// 普通文本里的 ZWJ 必须原样保留,否则会破坏这些语言的字形。
func stripEmojiZWJ(s string) string {
	if !strings.ContainsRune(s, 0x200D) {
		return s
	}
	runes := []rune(s)
	out := make([]rune, 0, len(runes))
	for i, r := range runes {
		if r == 0x200D &&
			emojiBeforeIdx(runes, i) &&
			i+1 < len(runes) && isEmojiLike(runes[i+1]) {
			continue // 两侧都是 emoji → 剥掉这个 ZWJ
		}
		out = append(out, r)
	}
	return string(out)
}

// emojiBeforeIdx 从 i-1 往回找第一个「非修饰符」rune,判断它是不是 emoji。
// 跳过变体选择符(FE0E/FE0F,如 🏳️‍🌈 里的)与肤色修饰符(1F3FB-1F3FF,如 👍🏽 里的),
// 这些不是 emoji 本体但会夹在 emoji 与 ZWJ 之间。
func emojiBeforeIdx(runes []rune, i int) bool {
	for j := i - 1; j >= 0; j-- {
		r := runes[j]
		if (r >= 0xFE0E && r <= 0xFE0F) || (r >= 0x1F3FB && r <= 0x1F3FF) {
			continue // 修饰符,继续往前找本体
		}
		return isEmojiLike(r)
	}
	return false
}
