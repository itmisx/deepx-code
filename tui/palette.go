package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// === Slash 命令注册表 ===
//
// 顺序 = palette 默认展示顺序,跟 /help 列表保持一致(plan/auto 先,help 最后)。
// 新增命令时记得三处同步:本表、handleSlashCommand 的 switch、/help 的提示文本。
//
// 用函数动态构建而非 package var,desc 走 T() 翻译,运行时切语言能反映到 palette。
func slashCommands() []struct{ name, desc string } {
	return []struct{ name, desc string }{
		{"/plan", T("cmd.plan.desc")},
		{"/auto", T("cmd.auto.desc")},
		{"/review", T("cmd.review.desc")},
		{"/model", T("cmd.model.desc")},
		{"/reasoning", T("cmd.reasoning.desc")},
		{"/mode", T("cmd.mode.desc")},
		{"/config", T("cmd.config.desc")},
		{"/provider", T("cmd.provider.desc")},
		{"/skills", T("cmd.skills.desc")},
		{"/skill-add", T("cmd.skill-add.desc")},
		{"/skill-delete", T("cmd.skill-delete.desc")},
		{"/ultracode", T("cmd.ultracode.desc")},
		{"/workflows", T("cmd.workflows.desc")},
		{"/workflow", T("cmd.workflow.desc")},
		{"/mcp-list", T("cmd.mcp-list.desc")},
		{"/mcp-add", T("cmd.mcp-add.desc")},
		{"/mcp-delete", T("cmd.mcp-delete.desc")},
		{"/lang", T("cmd.lang.desc")},
		{"/compact", T("cmd.compact.desc")},
		{"/new", T("cmd.new.desc")},
		{"/sessions", T("cmd.sessions.desc")},
		{"/session-rename", T("cmd.sessionrename.desc")},
		{"/session-delete", T("cmd.sessiondelete.desc")},
		{"/status", T("cmd.status.desc")},
		{"/thinking", T("cmd.thinking.desc")},
		{"/web-config", T("cmd.web-config.desc")},
		{"/sandbox", T("cmd.sandbox.desc")},
		{"/working-mode", T("cmd.workingmode.desc")},
		{"/undo", T("cmd.undo.desc")},
		{"/router-list-pro", T("cmd.routerlistpro.desc")},
		{"/router-list-flash", T("cmd.routerlistflash.desc")},
		{"/router-add-pro", T("cmd.routeraddpro.desc")},
		{"/router-add-flash", T("cmd.routeraddflash.desc")},
		{"/router-delete-pro", T("cmd.routerdeletepro.desc")},
		{"/router-delete-flash", T("cmd.routerdeleteflash.desc")},
		{"/help", T("cmd.help.desc")},
		{"/exit", T("cmd.exit.desc")},
	}
}

// slashCommandNeedsArg 标记「需要内联参数、且无弹窗兜底」的命令。
// 这类命令无参回车时不该直接执行(只会得到一句"用法"提示),而应把命令补全进输入框、
// 末尾留空格,等用户手动补参数。注:有弹窗收集输入的命令(/model /config /skill-add /reasoning
// /mcp-add /lang /sessions 等)不在此列——它们无参即弹窗,本就是"弹出"。
func slashCommandNeedsArg(name string) bool {
	switch name {
	case "/workflow", "/ultracode", "/session-rename",
		"/router-add-pro", "/router-add-flash", "/router-delete-pro", "/router-delete-flash":
		return true
	}
	return false
}

// bareSlashNeedsArg:input 恰好是一个「需要参数」的命令且没带任何参数时,返回该命令名 + true。
func bareSlashNeedsArg(input string) (string, bool) {
	fields := strings.Fields(strings.TrimSpace(input))
	if len(fields) != 1 {
		return "", false
	}
	name := strings.ToLower(fields[0])
	if slashCommandNeedsArg(name) {
		return name, true
	}
	return "", false
}

// isExactSlashCommand 判断 input 的首 token 是否精确等于某个已知命令名
// (用于支持带参数的命令,如 "/model flash" —— 首 token "/model" 精确命中即按命令处理)。
func isExactSlashCommand(input string) bool {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(input)))
	if len(fields) == 0 {
		return false
	}
	for _, c := range slashCommands() {
		if c.name == fields[0] {
			return true
		}
	}
	return false
}

// filterSlashCommands 按当前 input value 前缀过滤候选。
// 规则:
//   - value 不以 "/" 开头 → nil (palette 不显示)
//   - value 含空格 → nil (用户已经在打参数 / 完整命令,palette 退场)
//   - 否则 prefix 匹配所有 slashCommands.name
//
// 返回 nil 时调用方应判定 palette 关闭。
func filterSlashCommands(input string) []struct{ name, desc string } {
	if !strings.HasPrefix(input, "/") {
		return nil
	}
	if strings.ContainsAny(input, " \t") {
		return nil
	}
	cmds := slashCommands()
	out := make([]struct{ name, desc string }, 0, len(cmds))
	for _, c := range cmds {
		if strings.HasPrefix(c.name, input) {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// === Palette 渲染 ===
//
// 不带 border,直接列若干行候选。选中行用 reverse 视觉标识,其余暗一点的描述色。
// 渲染结果是 N 行 × maxW 列的 string,左对齐,准备 splice 到主 UI 输入框上方。
var (
	paletteSelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("238")).Bold(true)
	paletteNameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Bold(true)
	paletteDescStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// renderCommandPalette 把 matches 渲染成多行字符串,每行宽度精确 width 列(用空格右 pad)。
// selIdx 是 matches 内的选中索引(-1 表示无选中,所有项常态色)。
func renderCommandPalette(matches []struct{ name, desc string }, selIdx, width int) string {
	if len(matches) == 0 || width <= 0 {
		return ""
	}
	// 命令名最长几列,用来对齐 desc
	nameW := 0
	for _, m := range matches {
		if w := ansi.StringWidth(m.name); w > nameW {
			nameW = w
		}
	}

	var sb strings.Builder
	for i, m := range matches {
		marker := "  "
		if i == selIdx {
			marker = "▸ "
		}
		// 命令名补齐到 nameW,desc 后留空 pad 到精确 width 列
		namePadded := m.name + strings.Repeat(" ", nameW-ansi.StringWidth(m.name))
		raw := marker + namePadded + "  " + m.desc
		// pad / truncate 到 width
		if cur := ansi.StringWidth(raw); cur < width {
			raw += strings.Repeat(" ", width-cur)
		} else if cur > width {
			raw = ansi.Cut(raw, 0, width)
		}

		var styled string
		if i == selIdx {
			styled = paletteSelStyle.Render(raw)
		} else {
			// 分段染色:marker 灰,name 白粗,desc 暗灰。重建可读 ansi 行。
			nameSeg := paletteNameStyle.Render(namePadded)
			descSeg := paletteDescStyle.Render(m.desc)
			markSeg := paletteDescStyle.Render(marker)
			styled = markSeg + nameSeg + "  " + descSeg
			// pad 到精确 width(整行结尾)
			if cur := ansi.StringWidth(styled); cur < width {
				styled += strings.Repeat(" ", width-cur)
			} else if cur > width {
				styled = ansi.Cut(styled, 0, width)
			}
		}
		sb.WriteString(styled)
		if i < len(matches)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// overlayAt 把 fg 叠到 bg 上的 (startX, startY) 位置(左上角对齐)。
// 类似 overlayCentered 但不居中。startY 越界时 fg 行被丢。
func overlayAt(bg, fg string, startX, startY int) string {
	fgLines := strings.Split(strings.TrimRight(fg, "\n"), "\n")
	fgW := 0
	for _, ln := range fgLines {
		if w := ansi.StringWidth(ln); w > fgW {
			fgW = w
		}
	}
	bgLines := strings.Split(bg, "\n")
	for i, fgLine := range fgLines {
		y := startY + i
		if y < 0 || y >= len(bgLines) {
			continue
		}
		bgLines[y] = spliceLineCells(bgLines[y], fgLine, startX, fgW)
	}
	return strings.Join(bgLines, "\n")
}
