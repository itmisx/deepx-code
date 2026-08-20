package tui

import (
	"image/color"
	"strconv"
	"strings"
	"time"

	"deepx/tools"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// wrapView 把字符串 mainUI 包成 v2 tea.View,并设置 alt-screen / mouse mode 等终端能力。
// v2 的 View() 不再返回 string,而是带元数据的结构体;终端选项也从 NewProgram 的
// option 迁到 View 字段(声明式)。
//
// MouseMode = CellMotion:启用 DEC 1002 + SGR 1006,鼠标事件(wheel / click / drag)
// 全部送进程序。代价是 native 终端拖拽选择被接管 — 用户要在 chat 区拖拽选择得用
// Option+拖拽(macOS Terminal.app / iTerm2 / Kitty 都支持 Option/Alt 旁路鼠标追踪)。
// 收益是触控板/滚轮直接滚 chat(MouseWheelMsg → chatViewport.Update),
// 程序内拖拽选区会高亮并在松开时写入系统剪贴板(model.go 的 selection 流程)。
func (m model) wrapView(content string) tea.View {
	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	// 鼠标穿透模式:关掉鼠标捕获,让终端原生处理右键粘贴与文字选择。
	// 用户可按 F2 在 TUI 内切换,无需退出 deepx。
	if m.mousePassthrough || m.showSetup || m.showMcpAdd || m.showSkillAdd || m.showWebConfig {
		v.MouseMode = tea.MouseModeNone
	}
	// 换行键统一为 ctrl+j(LF,终端原生、不依赖 Kitty 协议、三平台一致,见 issue #124),
	// 不再绑定 Ctrl+Enter / Shift+Enter,故无需开 Kitty "alternate keys" 上报。
	return v
}

// inputTopPad / inputBotPad 是输入区 textarea 上下的留白行数。顶部留 2 行把输入框跟上方
// chat 拉开,底部不留白。改这俩值光标 / palette 起点会跟着 inputTopPad 自动对齐。
const inputTopPad = 2
const inputBotPad = 0

// inputTextRows 是输入框 textarea 的固定显示行数。内容超过时不长高,
// 靠 ↑/↓ 移动光标带动 textarea 内部滚动(见 model.go 按键处理)。
const inputTextRows = 3

// inputAreaHeight 是底部输入框区域占用的固定行数:textarea inputTextRows 行 + 上下留白。
const inputAreaHeight = inputTextRows + inputTopPad + inputBotPad

// inputGutterWidth 是输入区左侧固定 gutter 列宽:首行画 "❱ ",其余行 "  "。
// textarea 实际宽度 = m.width - inputGutterWidth。
const inputGutterWidth = 2

// inputPromptStyle 是 gutter 里 "❱ " 的样式(亮青加粗,同 banner 品牌主色)。
var inputPromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Bold(true)

var (
	scrollbarDividerStyle = lipgloss.NewStyle().Foreground(bannerDecoColor)       // 轨道:暗色粗 ┃
	scrollbarThumbStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("255")) // 滑块:亮白粗 ┃
)

// scrollbarWidth 是右侧分隔线/滚动条区占的列数:1 列。
const scrollbarWidth = 1

// scrollbarDividers 生成 body 每行右侧的分隔线/滚动条字形:轨道行用暗色 `┃`,滑块行用亮白 `┃`
// (同一字符、同列、同宽、上下连续,只靠加亮区分;与左侧引用条 ┃ 一致)。
func (m model) scrollbarDividers(height int) []string {
	track := scrollbarDividerStyle.Render("┃")
	thumbStr := scrollbarThumbStyle.Render("┃")
	out := make([]string, height)
	for i := range out {
		out[i] = track
	}
	if height <= 0 {
		return out
	}
	total := m.chatViewport.TotalLineCount()
	visible := m.chatViewport.Height()
	if total <= visible || visible <= 0 {
		return out
	}
	thumb := height * visible / total
	if thumb < 1 {
		thumb = 1
	}
	if thumb > height {
		thumb = height
	}
	maxOff := total - visible
	yoff := m.chatViewport.YOffset()
	if yoff < 0 {
		yoff = 0
	}
	if yoff > maxOff {
		yoff = maxOff
	}
	pos := 0
	if maxOff > 0 {
		pos = yoff * (height - thumb) / maxOff
	}
	if pos > height-thumb {
		pos = height - thumb
	}
	for i := pos; i < pos+thumb && i < height; i++ {
		out[i] = thumbStr
	}
	return out
}

// scrollChatToTrackRow 把滚动条轨道上的第 row 行(光标 Y - chatTop)映射成 chat 的滚动偏移并应用。
// trackH 是轨道高度(= 可视行数)。滑块中心贴着光标;越界自动钳到 [0, maxOff]。内容不溢出则什么都不做。
func (m *model) scrollChatToTrackRow(row, trackH int) {
	total := m.chatViewport.TotalLineCount()
	visible := m.chatViewport.Height()
	if total <= visible || visible <= 0 || trackH <= 0 {
		return
	}
	thumb := trackH * visible / total
	if thumb < 1 {
		thumb = 1
	}
	if thumb > trackH {
		thumb = trackH
	}
	maxOff := total - visible
	target := maxOff
	if denom := trackH - thumb; denom > 0 {
		target = (row - thumb/2) * maxOff / denom // 滑块中心对齐光标
	}
	if target < 0 {
		target = 0
	}
	if target > maxOff {
		target = maxOff
	}
	m.chatViewport.SetYOffset(target)
}

// layout 计算 chat viewport 的宽度与高度。
// 总体结构:
//
//	body(vpH) + input(inputAreaHeight) = m.height
//	body 内部:chat(leftW) + divider(1) + rightPanel(rightW),banner 摆右栏顶部。
//	vpH = m.height - inputAreaHeight
//	leftW = m.width - rightPanelWidth - 1
func (m model) layout() (leftW, vpH int) {
	rightW := 0 // 隐藏状态栏 → rightW=0 → chat 整宽(仅留最右列给滚动条)
	if !m.hideStatusPanel {
		rightW = rightPanelWidth
		if rightW > m.width/2 {
			rightW = m.width / 2
		}
	}
	leftW = m.width - rightW - scrollbarWidth
	if leftW < 1 {
		leftW = 1
	}
	vpH = m.height - inputAreaHeight
	if vpH < 1 {
		vpH = 1
	}
	return
}

func (m model) View() tea.View {
	if m.width < 30 || m.height < 7 {
		return m.wrapView(T("misc.terminal_too_small"))
	}

	rightW := 0 // 隐藏状态栏时 rightW=0 → chat 铺满整宽(仅留最右一列给滚动条/分隔线)
	if !m.hideStatusPanel {
		rightW = rightPanelWidth
		if rightW > m.width/2 {
			rightW = m.width / 2
		}
	}
	leftW := m.width - rightW - scrollbarWidth // 右侧留 scrollbarWidth 列给分隔线/滚动条
	if leftW < 1 {
		leftW = 1
	}
	// 排队区(流式中暂存的待发送消息)挂在输入框上方,占 queuedH 行,从 body 高度里扣。
	// 别让它把对话挤没:至少给 chat 留 1 行。它在左列,按 leftW 折行。
	queuedLines := m.queuedDisplayLines(leftW)
	if maxQ := m.height - inputAreaHeight - 1; len(queuedLines) > maxQ {
		if maxQ < 0 {
			maxQ = 0
		}
		queuedLines = queuedLines[:maxQ]
	}
	queuedH := len(queuedLines)

	bodyH := m.height - inputAreaHeight - queuedH
	if bodyH < 1 {
		bodyH = 1
	}

	// chat 区:pad/截到精确 leftW × bodyH。
	chatPadded := padLinesToWidth(m.chatViewport.View(), leftW)
	chatLines := strings.Split(chatPadded, "\n")
	for len(chatLines) < bodyH {
		chatLines = append(chatLines, strings.Repeat(" ", leftW))
	}
	if len(chatLines) > bodyH {
		// 仅当排队区压缩了 body 时才会溢出(viewport 自身高度=无排队时的 bodyH)。
		// 留"尾部"而非头部:流式时 viewport 贴底,最新输出在末尾,要保证它不被排队区盖掉。
		chatLines = chatLines[len(chatLines)-bodyH:]
	}

	// === 布局:左列(对话 + 输入)│ 分隔线 │ 右列(状态栏,全高)===
	// 状态栏独占右半区、从顶到底;分隔线一条 ┃ 贯穿全高,把左半区(对话+输入)与状态栏隔开。
	// 输入区因此收进左列(宽 leftW,见 resize / toggleStatusPanel 的 SetWidth)。
	// 分隔线始终贯穿全高;状态栏隐藏(rightW==0)时右列为空,线仍在最右列一直到底。

	// 输入列内容:gutter + textarea 逐行拼接,首行 "❱ ";上接活动状态行/留白,中间夹排队区。
	taView := m.input.View()
	if m.inputSelecting {
		// 鼠标拖拽选中的片段:复用 chat 的流式选区高亮(反色),坐标 = inputSelAnchor/End,
		// 宽度 = textarea 显示宽(leftW - gutter)。它内部会 strip 选中段的 ANSI 再套反色,
		// 规避 textarea 输出里的内部 reset 取消高亮的问题。
		taView = applySelectionHighlight(taView, m.inputSelAnchor, m.inputSelEnd, leftW-inputGutterWidth)
	}
	taLines := strings.Split(taView, "\n")
	inputRows := make([]string, len(taLines))
	for i, tl := range taLines {
		gutter := strings.Repeat(" ", inputGutterWidth)
		if i == 0 {
			gutter = inputPromptStyle.Render("❱ ")
		}
		inputRows[i] = gutter + tl
	}
	inputLines := make([]string, 0, queuedH+len(inputRows)+inputTopPad+inputBotPad)
	for i := 0; i < inputTopPad; i++ {
		// 顶部留白第一行挂活动状态行(运行中 spinner+耗时 / 空闲"就绪"),其余空行。
		// inputTopPad 不变 → 光标 Y(bodyH+queuedH+inputTopPad)也不变。
		if i == 0 && inputTopPad > 0 {
			inputLines = append(inputLines, m.statusFooterLine(leftW))
			continue
		}
		inputLines = append(inputLines, "")
	}
	inputLines = append(inputLines, queuedLines...) // 排队区紧贴输入框上方
	inputLines = append(inputLines, inputRows...)
	for i := 0; i < inputBotPad; i++ {
		inputLines = append(inputLines, "")
	}

	// 左列 = 对话(bodyH 行)+ 输入列,逐行锁到精确 leftW(短补空格/长截断),
	// 保证分隔线在每行都落在同一列、不会参差。
	leftLines := make([]string, 0, len(chatLines)+len(inputLines))
	leftLines = append(leftLines, chatLines...)
	leftLines = append(leftLines, inputLines...)
	leftCol := strings.Split(padLinesToWidth(strings.Join(leftLines, "\n"), leftW), "\n")

	// 右列 = 状态栏,全高 rightW;隐藏时空。
	panelShown := !m.hideStatusPanel && rightW > 0
	rightCol := make([]string, m.height)
	if panelShown {
		right := lipgloss.NewStyle().
			Width(rightW).
			Height(m.height).
			Padding(0, 1).
			Render(m.rightPanelView())
		rightCol = strings.Split(right, "\n")
		for len(rightCol) < m.height {
			rightCol = append(rightCol, strings.Repeat(" ", rightW))
		}
		if len(rightCol) > m.height {
			rightCol = rightCol[:m.height]
		}
	}

	// 分隔线始终贯穿全高:对话区那 bodyH 行是滚动条(可拖滑块,亮白滑块+暗轨道),
	// 其余行(输入区)是纯暗色 ┃ —— 状态栏显隐都一样,线一直到底。
	divs := m.scrollbarDividers(bodyH)
	track := scrollbarDividerStyle.Render("┃")
	rows := make([]string, m.height)
	for i := 0; i < m.height; i++ {
		l := strings.Repeat(" ", leftW)
		if i < len(leftCol) {
			l = leftCol[i]
		}
		d := track
		if i < bodyH && i < len(divs) {
			d = divs[i]
		}
		r := ""
		if i < len(rightCol) {
			r = rightCol[i]
		}
		rows[i] = l + d + r
	}
	mainUI := strings.Join(rows, "\n")

	// 复制成功提示:在鼠标松开的位置叠一个绿色"✓ 已复制"小标(copyHintClearMsg 到点清空)。
	if m.copyHint != "" {
		hint := lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("10")).Bold(true).
			Render(" " + m.copyHint + " ")
		hw := ansi.StringWidth(hint)
		x := m.copyHintX + 1 // 紧贴松开点右边一点,不盖住光标
		if x+hw > m.width {
			x = m.width - hw // 贴右边界,别出屏
		}
		if x < 0 {
			x = 0
		}
		y := m.copyHintY
		if y < 0 {
			y = 0
		}
		if y >= m.height {
			y = m.height - 1
		}
		mainUI = overlayAt(mainUI, hint, x, y)
	}

	// 命令 palette:input value 以 "/" 起手时叠在输入框上方。
	if matches := filterSlashCommands(m.input.Value()); len(matches) > 0 && !m.showSetup {
		idx := m.commandPaletteIdx
		if idx >= len(matches) {
			idx = len(matches) - 1
		}
		if idx < 0 {
			idx = 0
		}
		palette := renderCommandPalette(matches, idx, leftW)
		// 输入框首行 Y = body(bodyH) + 排队区行数 + 顶部留白行数
		inputY := bodyH + queuedH + inputTopPad
		startY := inputY - len(matches)
		if startY < 0 {
			startY = 0
		}
		mainUI = overlayAt(mainUI, palette, 0, startY)
	} else if _, _, query, active := fileMentionContext(m.input.Value(), m.input.Line(), m.input.Column()); active && len(m.fileMentionCache) > 0 && !m.showSetup {
		// @ 文件提及选择器:叠在输入框上方(与 / palette 同位,二者互斥)。
		if matches := filterWorkspaceFiles(query, m.fileMentionCache, fileMentionMaxRows); len(matches) > 0 {
			idx := m.fileMentionIdx
			if idx >= len(matches) {
				idx = len(matches) - 1
			}
			if idx < 0 {
				idx = 0
			}
			palette := renderFileMentionPalette(matches, idx, leftW)
			inputY := bodyH + queuedH + inputTopPad
			startY := inputY - len(matches)
			if startY < 0 {
				startY = 0
			}
			mainUI = overlayAt(mainUI, palette, 0, startY)
		}
	}

	// modal 覆盖在主 UI 上居中显示。
	if m.showSetup {
		mainUI = overlayCentered(mainUI, m.setupModalBlock(), m.width, m.height)
	}
	if m.reviewPending {
		mainUI = overlayCentered(mainUI, m.reviewBlock(), m.width, m.height)
	}
	// AskUser 选择题不再用居中浮层:卡片已内联进对话流末尾(见 renderChatBaseContent),
	// 随会话一起滚动,用户可 PgUp/滚轮回看历史,作答后折叠成档案留痕(issue #134)。
	if m.showLangModal {
		mainUI = overlayCentered(mainUI, m.langModalBlock(), m.width, m.height)
	}
	if m.showWorkingModeModal {
		mainUI = overlayCentered(mainUI, m.workingModeModalBlock(), m.width, m.height)
	}
	if m.showModelModal {
		mainUI = overlayCentered(mainUI, m.modelModalBlock(), m.width, m.height)
	}
	if m.showSandboxModal {
		mainUI = overlayCentered(mainUI, m.sandboxModalBlock(), m.width, m.height)
	}
	if m.showProviderModal {
		mainUI = overlayCentered(mainUI, m.providerModalBlock(), m.width, m.height)
	}
	if m.showReasoningModal {
		mainUI = overlayCentered(mainUI, m.reasoningModalBlock(), m.width, m.height)
	}
	if m.showMcpAdd {
		mainUI = overlayCentered(mainUI, m.mcpAddModalBlock(), m.width, m.height)
	}
	if m.showWebConfig {
		mainUI = overlayCentered(mainUI, m.webConfigModalBlock(), m.width, m.height)
	}
	if m.showMcpDelete {
		mainUI = overlayCentered(mainUI, m.mcpDeleteModalBlock(), m.width, m.height)
	}
	if m.showSkillAdd {
		mainUI = overlayCentered(mainUI, m.skillAddModalBlock(), m.width, m.height)
	}
	if m.showSkillDelete {
		mainUI = overlayCentered(mainUI, m.skillDeleteModalBlock(), m.width, m.height)
	}
	if m.showSessionList {
		mainUI = overlayCentered(mainUI, m.sessionListModalBlock(), m.width, m.height)
	}
	v := m.wrapView(normalizeFrame(mainUI, m.width, m.height))
	// 真实终端光标定位到 input 内的 cursor 位置。
	// textarea.Cursor() 给的 X/Y 是相对 textarea 自身的局部坐标;input 起始 Y =
	// body 占的行数 + 顶部留白行数;X 要加上左侧 gutter 宽(textarea 现在从 gutter 右边起)。
	// modal 打开时不显示真实光标 —— 避免光标卡在 modal 背后。
	// cursorBlinkOff 只在 appSideCursorBlink 的终端(VS Code)会翻转:亮时塞 Cursor、灭时不塞。
	// 其余终端恒 false —— 光标常驻,由终端按 DECSCUSR 的 blink 位自己闪。
	// 别为了闪烁再去切 Cursor 的有无:那会让 bubbletea 每拍重发 DECSCUSR,在 CSI 解析不健全的
	// 终端上把序列末尾的 q 打印到屏幕上(issue #167,详见 model.go 的 appSideCursorBlink)。
	if !m.showSetup && !m.showLangModal && !m.showWorkingModeModal && !m.showSandboxModal && !m.showProviderModal && !m.showMcpAdd && !m.showWebConfig && !m.showMcpDelete && !m.showSkillAdd && !m.showSkillDelete && !m.showSessionList && !m.reviewPending && !m.askPending && !m.cursorBlinkOff {
		if c := m.input.Cursor(); c != nil {
			c.Position.X += inputGutterWidth
			c.Position.Y += bodyH + queuedH + inputTopPad
			v.Cursor = c
		}
	}
	return v
}

// normalizeFrame 把整帧锁到精确 width × height:行数不足补空行/过多截尾,
// 每行宽度不足补空格/过宽用 ansi.Cut 切到精确 width。
func normalizeFrame(s string, width, height int) string {
	// 最后一道:右栏状态区、banner 等也可能携带来自 LLM / 工具输出的裸 \r
	// (主题串、模型名等)。聊天区在 refreshViewport 已经清过,这里兜住其余来源。
	//
	// **删除而不是换成 \n**:此刻 s 是已经排好版的整帧,把 \r 变成换行会凭空多出行,
	// 紧接着的 lines[:height] 就把底部截掉,整个版式往下错 —— 那比原来的毛病更重。
	if strings.ContainsRune(s, '\r') {
		s = strings.ReplaceAll(s, "\r\n", "\n")
		s = strings.ReplaceAll(s, "\r", "")
	}
	lines := strings.Split(s, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i, ln := range lines {
		w := lineDisplayWidth(ln)
		switch {
		case w < width:
			lines[i] = ln + strings.Repeat(" ", width-w)
		case w > width:
			lines[i] = ansi.Cut(ln, 0, width)
		}
	}
	return strings.Join(lines, "\n")
}

func statusColor(s string) color.Color {
	switch s {
	case "idle":
		return lipgloss.Color("10")
	case "thinking":
		return lipgloss.Color("11")
	case "streaming":
		return lipgloss.Color("14")
	case "tool":
		return highlightColor
	case "error":
		return lipgloss.Color("9")
	}
	return lipgloss.Color("7")
}

// codegraphColor 给代码图谱状态上色:加载=高亮、就绪=绿、更新/降级=黄、未构建/已禁用=暗。
func codegraphColor(s string) color.Color {
	switch s {
	case "loading":
		return highlightColor
	case "ready":
		return lipgloss.Color("10")
	case "stale", "degraded":
		return lipgloss.Color("11")
	}
	return subtleColor
}

// queuedMaxRows 排队区最多显示几条原文,超出折叠成 "… +N"。
const queuedMaxRows = 5

// queuedDisplayLines 把排队消息(流式中按 Enter 暂存、本轮结束自动发)渲染成输入框上方的
// "待发送"区——仿 Claude Code:让用户直接看到排队的原文,而不是一个计数。
// 每条折成一行(换行压成空格)按宽度截断;超过 queuedMaxRows 条时末行折叠成 "… +N"。
// 返回的行已含左侧 gutter 缩进,直接拼进 inputBlock 顶部即可。
func (m model) queuedDisplayLines(width int) []string {
	if len(m.queuedInput) == 0 {
		return nil
	}
	dim := lipgloss.NewStyle().Foreground(subtleColor).Render
	gutter := strings.Repeat(" ", inputGutterWidth)
	avail := width - inputGutterWidth - 2 // 2 = "↳ " 标记宽
	if avail < 8 {
		avail = 8
	}
	shown := m.queuedInput
	overflow := 0
	if len(shown) > queuedMaxRows {
		overflow = len(shown) - (queuedMaxRows - 1)
		shown = shown[:queuedMaxRows-1]
	}
	lines := make([]string, 0, len(shown)+1)
	for _, q := range shown {
		oneLine := strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(q)
		oneLine = ansi.Truncate(oneLine, avail, "…")
		lines = append(lines, gutter+dim("↳ "+oneLine))
	}
	if overflow > 0 {
		lines = append(lines, gutter+dim("… +"+strconv.Itoa(overflow)))
	}
	return lines
}

// statusFooterLine 渲染输入框正上方那一行活动状态——把"在跑还是结束了"放到用户注意力
// 所在处(主列底部),而不是只藏在右侧状态栏。
//   - 运行中:spinner(thinking/tool 时转)/状态图标 + 状态词 + 实时耗时 + 当前工具("Esc 中断"不在这儿,输入框 placeholder 已有)。
//   - 空闲:一个暗色 "● 就绪",和运行态形成明显对比。
func (m model) statusFooterLine(_ int) string {
	dim := lipgloss.NewStyle().Foreground(subtleColor).Render
	// 压缩期间(手动 /compact、轮末自动、以及 agent 在一轮进行中压):转 spinner +「压缩中…」;
	// 输入不丢,排队待压缩后自动发。轮内压缩最长会卡 2 分钟且不吐 token,没有这行用户只当卡死了。
	if m.compactingFG || m.compactingInTurn {
		head := m.spinner.View()
		word := lipgloss.NewStyle().Foreground(statusColor("thinking")).Bold(true).Render("压缩中…")
		hint := " · 消息将在压缩后自动发出"
		if len(m.queuedInput) > 0 {
			hint = " · 已排队 " + strconv.Itoa(len(m.queuedInput)) + " 条,压缩后自动发出"
		}
		return head + " " + word + dim(hint)
	}
	if !m.streaming {
		// 上一轮出错:红色 ✗ 出错(chat 里已有具体错误信息)。
		if m.status == "error" {
			return lipgloss.NewStyle().Foreground(statusColor("error")).Render("✗ " + T("footer.error"))
		}
		// 跑过至少一轮:绿色 ✓ 完成 + 用时 + 工具数(取代之前打进 chat 的 done 行)。
		if m.turnElapsed > 0 {
			done := lipgloss.NewStyle().Foreground(statusColor("idle")).Render("✓ " + T("done.done"))
			s := done + dim(" · "+formatElapsed(m.turnElapsed))
			if m.turnToolCalls > 0 {
				s += dim(" · " + strconv.Itoa(m.turnToolCalls) + " " + T("done.tools"))
			}
			if m.mousePassthrough {
				s += dim(" · " + T("mouse.passthrough.badge"))
			}
			return s
		}
		// 还没跑过任何一轮:活动行留空(见上),但穿透开着时要给个可见提示 ——
		// 这时 deepx 的选区/滚轮都不响应,不说明用户会以为界面坏了。
		if m.mousePassthrough {
			return dim(T("mouse.passthrough.badge"))
		}
		return ""
	}
	head := statusIcon(m.status)
	if m.thinking {
		head = m.spinner.View() // thinking / 工具执行期间动起来
	}
	statusWord := lipgloss.NewStyle().Foreground(statusColor(m.status)).Bold(true).Render(T("footer." + m.status))
	left := head + " " + statusWord + dim(" · "+formatElapsed(time.Since(m.turnStartedAt)))
	if m.activeTool != "" {
		left += dim(" · " + m.activeTool)
	}
	// API 退避重试中:状态行实时显示「重试 N/10」,醒目色(对标 Claude Code 的 attempt 计数)。
	if m.retryNotice != "" {
		left += dim(" · ") + lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true).Render("⟳ "+m.retryNotice)
	}
	if m.mousePassthrough {
		left += dim(" · " + T("mouse.passthrough.badge"))
	}
	// 不再右贴 "Esc 中断" —— 输入框 placeholder(misc.input_placeholder)已含,避免重复。
	return left
}

func statusIcon(s string) string {
	switch s {
	case "idle":
		return "●"
	case "thinking":
		return "◌"
	case "streaming":
		return "▶"
	case "tool":
		return "⚙"
	case "error":
		return "✗"
	}
	return "·"
}

// reviewBlock 渲染审核确认弹窗。
func (m model) reviewBlock() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11")).Render(T("review.title"))
	desc := lipgloss.NewStyle().Foreground(softFgColor).Render(
		T("review.desc_prefix") + m.reviewToolName + " " + truncateReviewArgs(m.reviewToolArgs, 40))

	yesStyle := lipgloss.NewStyle().Foreground(softFgColor)
	noStyle := lipgloss.NewStyle().Foreground(softFgColor)
	if m.reviewYesNo {
		yesStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10")).Background(lipgloss.Color("236"))
	} else {
		noStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9")).Background(lipgloss.Color("236"))
	}
	content := lipgloss.JoinVertical(lipgloss.Left,
		title,
		"",
		desc,
		"",
		yesStyle.Render(T("review.yes")),
		noStyle.Render(T("review.no")),
		"",
		lipgloss.NewStyle().Foreground(subtleColor).Render(T("review.footer")),
	)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("11")).
		Padding(1, 2).
		Width(45).
		Render(content)
}

// versionLine 渲染右栏 banner 下方的版本行。
// 默认显示 `v<current>`(暗色),如果检测到 GitHub 有更高版本则改成
// `v<current> ↑ <latest>` 并把 ↑ 跟新版本染高亮色 — 视觉上是温和提示而非弹窗。
// width 用来居中:右栏内宽 = rightPanelWidth-2,banner 居中,这里也居中对齐。
func (m model) versionLine(width int) string {
	cur := m.version
	if cur == "" {
		cur = "dev"
	}
	dim := lipgloss.NewStyle().Foreground(subtleColor).Render
	hi := lipgloss.NewStyle().Foreground(highlightColor).Bold(true).Render

	left := dim("v" + cur)
	if m.upgradeAvailable && m.latestVersion != "" {
		left = left + " " + hi("↑ "+m.latestVersion)
	}
	// 居中 pad,跟 banner 同一对齐
	cellWidth := ansi.StringWidth(ansi.Strip(left))
	if cellWidth >= width {
		return left
	}
	pad := (width - cellWidth) / 2
	return strings.Repeat(" ", pad) + left
}

// langModalBlock 渲染 /lang 语言选择弹窗。两项:中文 / 英文,langModalIdx 是当前光标。
func (m model) langModalBlock() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(highlightColor).Render(T("lang.title"))

	options := []string{T("lang.option.zh"), T("lang.option.en")}
	rows := make([]string, 0, len(options))
	for i, opt := range options {
		marker := "  "
		style := lipgloss.NewStyle().Foreground(softFgColor)
		if i == m.langModalIdx {
			marker = "▸ "
			style = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10")).Background(lipgloss.Color("236"))
		}
		rows = append(rows, style.Render(marker+opt))
	}

	footer := lipgloss.NewStyle().Foreground(subtleColor).Render(T("lang.footer"))
	parts := []string{title, ""}
	parts = append(parts, rows...)
	parts = append(parts, "", footer)
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlightColor).
		Padding(1, 2).
		Width(42).
		Render(content)
}

// providerModalBlock 渲染 /provider 选择弹窗,逐行列出 provider.yaml 里已存的供应商名,
// providerModalIdx 是当前光标。
func (m model) providerModalBlock() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(highlightColor).Render(T("provider.title"))

	rows := make([]string, 0, len(m.providerNames))
	for i, name := range m.providerNames {
		marker := "  "
		style := lipgloss.NewStyle().Foreground(softFgColor)
		if i == m.providerModalIdx {
			marker = "▸ "
			style = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10")).Background(lipgloss.Color("236"))
		}
		rows = append(rows, style.Render(marker+name))
	}

	footer := lipgloss.NewStyle().Foreground(subtleColor).Render(T("lang.footer"))
	parts := []string{title, ""}
	parts = append(parts, rows...)
	parts = append(parts, "", footer)
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlightColor).
		Padding(1, 2).
		Width(42).
		Render(content)
}

// workingModeModalBlock 渲染 /working-mode 选择弹窗。三项 karpathy/openspec/superpowers,
// workingModeModalIdx 是当前光标。
func (m model) workingModeModalBlock() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(highlightColor).Render(T("workingmode.title"))

	options := []string{
		T("workingmode.opt.karpathy"),
		T("workingmode.opt.openspec"),
		T("workingmode.opt.superpowers"),
	}
	rows := make([]string, 0, len(options))
	for i, opt := range options {
		marker := "  "
		style := lipgloss.NewStyle().Foreground(softFgColor)
		if i == m.workingModeModalIdx {
			marker = "▸ "
			style = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10")).Background(lipgloss.Color("236"))
		}
		rows = append(rows, style.Render(marker+opt))
	}

	footer := lipgloss.NewStyle().Foreground(subtleColor).Render(T("lang.footer"))
	parts := []string{title, ""}
	parts = append(parts, rows...)
	parts = append(parts, "", footer)
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlightColor).
		Padding(1, 2).
		Width(42).
		Render(content)
}

// sandboxModalBlock 渲染 /sandbox 选择弹窗。三项 native/off/docker(见 sandboxModeOrder),
// sandboxModalIdx 是当前光标。
func (m model) sandboxModalBlock() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(highlightColor).Render(T("sandbox.title"))

	options := []string{
		T("sandbox.opt.native"),
		T("sandbox.opt.off"),
		T("sandbox.opt.docker"),
	}
	rows := make([]string, 0, len(options))
	for i, opt := range options {
		marker := "  "
		style := lipgloss.NewStyle().Foreground(softFgColor)
		if i == m.sandboxModalIdx {
			marker = "▸ "
			style = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10")).Background(lipgloss.Color("236"))
		}
		rows = append(rows, style.Render(marker+opt))
	}

	footer := lipgloss.NewStyle().Foreground(subtleColor).Render(T("lang.footer"))
	parts := []string{title, ""}
	parts = append(parts, rows...)
	parts = append(parts, "", footer)
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlightColor).
		Padding(1, 2).
		Width(42).
		Render(content)
}

// modelModalBlock 渲染 /model 选择弹窗。三项:auto / flash / pro,modelModalIdx 是当前光标。
func (m model) modelModalBlock() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(highlightColor).Render(T("model.modal.title"))

	options := []string{T("model.opt.auto"), T("model.opt.flash"), T("model.opt.pro")}
	rows := make([]string, 0, len(options))
	for i, opt := range options {
		marker := "  "
		style := lipgloss.NewStyle().Foreground(softFgColor)
		if i == m.modelModalIdx {
			marker = "▸ "
			style = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10")).Background(lipgloss.Color("236"))
		}
		rows = append(rows, style.Render(marker+opt))
	}

	footer := lipgloss.NewStyle().Foreground(subtleColor).Render(T("model.footer"))
	parts := []string{title, ""}
	parts = append(parts, rows...)
	parts = append(parts, "", footer)
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlightColor).
		Padding(1, 2).
		Width(52).
		Render(content)
}

// truncateReviewArgs 截断审核时显示的参数。
func truncateReviewArgs(args string, max int) string {
	if len(args) <= max {
		return args
	}
	return args[:max] + "…"
}

// rightPanelView 渲染右侧状态栏:Workspace / Models / Status / Usage / Commands / Plan。
// 每段标题用 ◆ 前缀 + 大写,内容缩进 2 空格。section 之间留 1 行空行做视觉分隔。
func (m model) rightPanelView() string {
	label := lipgloss.NewStyle().Foreground(dimColor).Render
	subtle := lipgloss.NewStyle().Foreground(subtleColor).Render

	// 路径压缩 ~/.../tail
	cwd := abbreviatePath(m.workspace, rightPanelWidth-2)

	// 权限模式(plan/auto/review)单独成段。任务执行状态(idle/streaming)已在输入框上方的
	// statusFooterLine 实时显示,右栏不再重复。
	modeStr := lipgloss.NewStyle().Foreground(highlightColor).Render(string(m.mode))

	// section 渲染助手:标题 + 缩进内容 + 末尾空行。
	section := func(title string, body []string) []string {
		out := make([]string, 0, len(body)+2)
		out = append(out,
			lipgloss.NewStyle().Foreground(highlightColor).Render("◆ ")+
				lipgloss.NewStyle().Foreground(accentColor).Bold(true).Render(strings.ToUpper(title)),
		)
		for _, line := range body {
			out = append(out, "  "+line)
		}
		out = append(out, "")
		return out
	}

	// inlineRow:标签和值放在同一行(◆ 标题  值),用于权限模式 / 计划 / 步骤这类单值项,不换行。
	inlineRow := func(title, value string) string {
		return lipgloss.NewStyle().Foreground(highlightColor).Render("◆ ") +
			lipgloss.NewStyle().Foreground(accentColor).Bold(true).Render(strings.ToUpper(title)) +
			"  " + value
	}

	rows := []string{}
	// 5 行文字 logo:上 `/` 修饰条 + 3 行 deepx ascii art + 下 `/` 修饰条。
	// 右栏宽 rightPanelWidth,Padding(0,1) 后内宽 rightPanelWidth-2,banner 渲到这个宽度。
	bannerInnerW := rightPanelWidth - 2
	if banner := renderBanner(bannerInnerW); banner != "" {
		rows = append(rows, strings.Split(banner, "\n")...)
	}

	// 版本行紧贴 banner 下:`v0.1.0` 或 `v0.1.0 ↑ 0.2.0`(有新版本时高亮)。
	rows = append(rows, m.versionLine(bannerInnerW), "")

	// Workspace section:标题尾接 session 哈希。不走 section() 是为了让哈希用 subtle 暗色。
	workspaceTitle := lipgloss.NewStyle().Foreground(highlightColor).Render("◆ ") +
		lipgloss.NewStyle().Foreground(accentColor).Bold(true).Render(strings.ToUpper(T("panel.workspace")))
	if m.session != nil {
		workspaceTitle += " " + subtle("("+m.session.SessionID()[:8]+")")
	}
	rows = append(rows, workspaceTitle, "  "+subtle(cwd), "")

	// 模型厂商 section:api host(去 scheme / path),host 即可标识厂商。
	endpoint := m.models.Flash.BaseURL
	if endpoint == "" {
		endpoint = m.models.Pro.BaseURL
	}
	if endpoint == "" {
		endpoint = "(not set)"
	}
	host := endpoint
	if idx := strings.Index(host, "://"); idx >= 0 {
		host = host[idx+3:]
	}
	if idx := strings.IndexAny(host, "/?"); idx >= 0 {
		host = host[:idx]
	}
	// 厂商段拆两个子标签:接口(host)+ 余额。余额:支持的供应商(DeepSeek/Kimi)显示金额(高亮),
	// 不支持显示 "-"(暗色),尚未探到(m.balance=="")显示 "…"(暗色)。
	var balanceVal string
	switch {
	case m.balance == "" || m.balance == "-":
		val := m.balance
		if val == "" {
			val = "…"
		}
		balanceVal = subtle(val)
	default:
		balanceVal = lipgloss.NewStyle().Foreground(highlightColor).Render(m.balance)
	}
	rows = append(rows, section(T("panel.vendor"), []string{
		label(T("panel.label.endpoint")) + " " + subtle(host),
		label(T("panel.label.balance")) + " " + balanceVal,
	})...)

	// 注:web dashboard 地址不在右栏显示 —— 启动时已在 chat 区给出可点击 / 已复制的提示,
	// 这里再放一份既重复又因面板窄被迫折行,反而没法点。

	// 模型路由:只显示 auto / flash / pro。
	routing := m.modelPin
	if routing == "" {
		routing = "auto"
	}
	rows = append(rows, inlineRow(T("panel.routing"), lipgloss.NewStyle().Foreground(highlightColor).Render(routing)), "")
	// 当前模型:只显示模型名(去掉 org/ 前缀,截断防溢出)。
	curModel := m.activeModelID
	if i := strings.LastIndexByte(curModel, '/'); i >= 0 {
		curModel = curModel[i+1:]
	}
	if curModel == "" {
		curModel = "—"
	}
	rows = append(rows, section(T("panel.curmodel"), []string{
		truncate(curModel, rightPanelWidth-4),
	})...)
	// 会话主题:模型每轮结尾自报(见 topic.go),按轮更新。摆在上下文用量上面 —— 这两项是
	// 一起看的:主题告诉你"这段对话在讲什么",用量告诉你"它占了多少",主题变了而用量又高
	// 就该 /new 了。还没识别到时用暗色占位,保持右栏行数稳定、不跳动。
	topicVal := subtle(T("panel.topic.none"))
	if m.topic != "" {
		topicVal = truncWidth(m.topic, rightPanelWidth-4)
	}
	rows = append(rows, section("🏷 "+T("panel.topic"), []string{topicVal})...)
	// 用量紧跟模型。首轮 API 调用结束才能拿到 lastUsage,没拿到前用 "—" 占位,保持布局一致。
	// 上下文占用:本轮发出的 prompt tokens / 压缩基准窗口。未配置(=0)则只显示已用 token,
	// 不编造分母/百分比。
	//
	// 分母必须与压缩用的窗口一致(compactCtxWin)。曾经按"当前活跃模型"取窗口,而压缩一律按 Pro 算 ——
	// 两边窗口配得不一样时(如 pro=512K / flash=128K),压缩明明把上下文砍到 17% 了,右栏却因为
	// 分母换成 flash 的 128K 而显示 71%,看起来像"压了没动",要等下一轮切回 pro 才跳变(issue #232)。
	// 这个数字的意义本来就是"离压缩触发线还有多远",分母自然该是压缩的基准。
	ctxWin := m.compactCtxWin()
	usedStr, outputStr, cacheStr := "—", "—", "—"
	if u := m.lastUsage; u != nil {
		if ctxWin > 0 && u.PromptTokens > 0 {
			usedStr = formatTokenCount(u.PromptTokens) + " / " + formatTokenCount(ctxWin) +
				" · " + strconv.Itoa(u.PromptTokens*100/ctxWin) + "%"
		} else {
			usedStr = formatTokenCount(u.PromptTokens) + " tok"
		}
		outputStr = formatTokenCount(u.CompletionTokens) + " tok"
		// 命中率:分母用 PromptTokens(DeepSeek 保证 = hit + miss;hit/miss 是其私有字段)。
		if u.PromptTokens > 0 {
			cacheStr = strconv.Itoa(u.PromptCacheHitTokens*100/u.PromptTokens) + "% hit"
		} else {
			cacheStr = "0% hit"
		}
	}
	rows = append(rows, section(T("panel.context"), []string{
		label(T("panel.label.used")) + " " + usedStr,
		label(T("panel.label.cache")) + " " + cacheStr,
		label(T("panel.label.output")) + " " + outputStr,
	})...)
	rows = append(rows, inlineRow(T("panel.permmode"), modeStr), "")
	// 代码图谱:单行只显示状态。
	cgState := tools.CodeGraphStatus()
	rows = append(rows, inlineRow(T("panel.codegraph"),
		lipgloss.NewStyle().Foreground(codegraphColor(cgState)).Render(T("codegraph."+cgState))), "")
	// 沙箱:显示当前模式 + native 的保护级别(OS 隔离 / 软策略),让用户清楚当前的边界强度。
	sbDesc := string(tools.CurrentSandboxMode())
	switch tools.CurrentSandboxMode() {
	case tools.SandboxOff:
		sbDesc += " (无防护)"
	case tools.SandboxNative:
		if tools.NativeIsolationActive() {
			sbDesc += " (OS隔离)"
		} else {
			sbDesc += " (软策略)"
		}
	}
	rows = append(rows, section("🛡 "+T("panel.sandbox"), []string{
		label(T("panel.label.sbmode")) + " " + sbDesc,
	})...)
	// 工作模式:kp / openspec / sp。
	rows = append(rows, section("🧭 "+T("panel.workmode"), []string{
		label(T("panel.label.wmode")) + " " + string(m.workingMode),
	})...)
	// 规划进度:始终显示(无规划时 0/0)。完整 plan 树在 chat 区展示,右栏只放摘要。
	// 待办(Todo)= Todo 工具(主 agent 顺序清单);计划(Plan)= CreatePlan(并发子 agent DAG)。
	// 二者共用 m.plan(同一时刻只一种活跃),按 planKind 分到两段显示,另一段为 0/0。
	var todoState, cpState *planState
	switch m.planKind {
	case "todo":
		todoState = m.plan
	case "createplan":
		cpState = m.plan
	}
	rows = append(rows,
		inlineRow(T("panel.todo"), renderPlanSummary(todoState, 0)[0]),
		"",
		inlineRow(T("panel.plan"), renderPlanSummary(cpState, 0)[0]),
		"")
	rows = append(rows, inlineRow(T("panel.help"), lipgloss.NewStyle().Foreground(highlightColor).Render("/help")))

	// 删掉最后那行多余空行
	if len(rows) > 0 && rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}

	return strings.Join(rows, "\n")
}
