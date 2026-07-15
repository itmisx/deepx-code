package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	// rightPanelWidth 右栏 section 区固定列宽。窄到能塞下两条 ◆ TITLE 行,宽到 model id
	// 不被 truncate 成 "deeps…"。32 跟 HEAD 一致,主流 IDE/终端默认宽度 (>= 100 列) 下
	// 至少给 chat 留 60+ 列。
	rightPanelWidth = 32
)

// 语义色变量。全部由 applyTheme 按终端背景明暗赋值;下方初始值 = 暗色档,
// 保证背景探测(BackgroundColorMsg)到达前的首帧按暗色渲染,收到后再切亮色并重绘。
//
// 只有 256-color 调色板里的具体色号(16-255)需要随背景切换 —— 基础 0-15 色由终端按
// 自身主题重映射,亮/暗都可读,故 subtle/accent 等仍用基础色,不进主题表。
//
// 各语义色的暗色 / 亮色取值见 darkPalette / lightPalette。
var (
	// 通用色:dim 给次要文本(label / 占位),subtle 给更暗的辅助信息,
	// accent / highlight 给右栏 section 标题和强调元素。
	dimColor         = lipgloss.Color("240")
	subtleColor      = lipgloss.Color("8")   // 基础色,亮暗自适应,不进主题表
	accentColor      = lipgloss.Color("12")  // 基础蓝:右栏 section 标题,亮暗自适应
	highlightColor   = lipgloss.Color("51")  // 强调元素(◆ / prompt / 光标)—— deepx 青蓝品牌色
	softFgColor      = lipgloss.Color("252") // 次前景:desc / label / 列表行等近白文本(亮底改深)
	strongFgColor    = lipgloss.Color("231") // 主前景:palette 命令名等需要醒目的文本(亮底改深黑)
	scrollThumbColor = lipgloss.Color("255") // 滚动条滑块

	// outerStyle 不再画外框 — 整个主 UI 直接贴终端边缘,视觉更干净。
	// 保留变量以兼容历史调用,但不带 border。
	outerStyle = lipgloss.NewStyle()

	dividerStyle = lipgloss.NewStyle().Foreground(subtleColor)

	// === 色条(quote bar)样式 ===
	//
	// 每条消息左侧画一根色条:首行 ╭、中间行 │、末行 ╰。
	// 视觉上像把消息"包"在一对括号里,身份靠色条颜色区分,不再用 inline emoji 前缀。
	//
	// 颜色挑选避开终端默认 0-15 色,用 256-color 调色板里饱和度低、背景下不刺眼的中间色:
	//   - user      cyan          消息从用户来,凉色
	//   - assistant violet/purple deepx 回复,跟终端常见绿色错开
	//   - tools     amber         工具组,暖色,跟 assistant 错开
	//   - system    gray          系统提示,最弱视觉权重
	userBarColor      = lipgloss.Color("39")  // 亮天蓝
	assistantBarColor = lipgloss.Color("141") // 淡紫
	toolsBarColor     = lipgloss.Color("215") // 琥珀
	systemBarColor    = lipgloss.Color("242") // 中灰
	thinkingBarColor  = lipgloss.Color("240") // 暗灰,思考内容最弱视觉权重

	// 用户回合做成"气泡":深底 + 白字 + 亮青块条,延续"用户=蓝"的语义,跟左对齐的 assistant/tools 错开。
	userBubbleBg  = lipgloss.Color("24")  // 深钢蓝底
	userBubbleFg  = lipgloss.Color("231") // 白字
	userBubbleBar = lipgloss.Color("51")  // 亮青块条

	// spinnerColor / cursorColor 供 initialModel 及 applyTheme 给 bubbles spinner / textarea 光标着色。
	spinnerColor = lipgloss.Color("99") // 紫
	cursorColor  = lipgloss.Color("51") // 亮青,跟 banner X 品牌符首色一致
)

const (
	// 色条总列宽 = 缩进 + 竖线 + 1 空格。
	// 一级(user/assistant/system):2 列 = ┃ + 空格;不缩进。
	// 二级(tools):4 列 = 2 列缩进 + │ + 空格;视觉上嵌入到一级段内部。
	barColWidthLevel1 = 2
	barColWidthLevel2 = 4
)

// darkBackground 记录当前终端是否暗色背景(由 tea.BackgroundColorMsg 探测)。默认 true。
var darkBackground = true

// applyTheme 按背景明暗把全部语义色切到对应档,并重建依赖它们的 style 变量。
// 背景探测后在 Update 里调用一次;bubbletea 单 goroutine 内重赋包级变量安全。
// 返回是否发生了切换(用于决定要不要清 markdown renderer 缓存 + 重绘)。
func applyTheme(dark bool) bool {
	if dark == darkBackground && themeApplied {
		return false
	}
	darkBackground = dark
	themeApplied = true
	if dark {
		dimColor = lipgloss.Color("240")
		highlightColor = lipgloss.Color("51")
		softFgColor = lipgloss.Color("252")
		strongFgColor = lipgloss.Color("231")
		scrollThumbColor = lipgloss.Color("255")
		userBarColor = lipgloss.Color("39")
		assistantBarColor = lipgloss.Color("141")
		toolsBarColor = lipgloss.Color("215")
		systemBarColor = lipgloss.Color("242")
		thinkingBarColor = lipgloss.Color("240")
		userBubbleBg = lipgloss.Color("24")
		userBubbleFg = lipgloss.Color("231")
		userBubbleBar = lipgloss.Color("51")
		spinnerColor = lipgloss.Color("99")
		cursorColor = lipgloss.Color("51")
		bannerSuffixColor = lipgloss.Color("250")
		bannerDecoColor = lipgloss.Color("67")
		deepxLetterColors = darkLetterColors
	} else {
		// 亮色档:近白前景全部压深,亮青/亮紫/琥珀换成同色系的深调,保证白底对比度。
		dimColor = lipgloss.Color("245")
		highlightColor = lipgloss.Color("31") // 深青(品牌色的亮底变体)
		softFgColor = lipgloss.Color("238")
		strongFgColor = lipgloss.Color("232") // 近黑
		scrollThumbColor = lipgloss.Color("244")
		userBarColor = lipgloss.Color("32")
		assistantBarColor = lipgloss.Color("97")
		toolsBarColor = lipgloss.Color("172")
		systemBarColor = lipgloss.Color("245")
		thinkingBarColor = lipgloss.Color("245")
		userBubbleBg = lipgloss.Color("254")
		userBubbleFg = lipgloss.Color("236")
		userBubbleBar = lipgloss.Color("31")
		spinnerColor = lipgloss.Color("61")
		cursorColor = lipgloss.Color("31")
		bannerSuffixColor = lipgloss.Color("244")
		bannerDecoColor = lipgloss.Color("67")
		deepxLetterColors = lightLetterColors
	}
	rebuildStyles()
	return true
}

// themeApplied 记录 applyTheme 是否已跑过至少一次(首次即使 dark==默认也要落地 rebuildStyles)。
var themeApplied = false

// rebuildStyles 重建那些在包级 var 里就把颜色 bake 进去的 style,使其跟上当前主题色。
func rebuildStyles() {
	dividerStyle = lipgloss.NewStyle().Foreground(subtleColor)
	inputPromptStyle = lipgloss.NewStyle().Foreground(highlightColor).Bold(true)
	scrollbarThumbStyle = lipgloss.NewStyle().Foreground(scrollThumbColor)
	paletteNameStyle = lipgloss.NewStyle().Foreground(strongFgColor).Bold(true)
}

// barColorFor 根据 segment kind 返回对应色条颜色。
// 未知 kind 兜底用 system 灰色 — 不致命,只是视觉上跟系统提示同列。
func barColorFor(kind string) color.Color {
	switch kind {
	case kindUser:
		return userBarColor
	case kindAssistant:
		return assistantBarColor
	case kindTools:
		return toolsBarColor
	case kindSystem:
		return systemBarColor
	case kindThinking:
		return thinkingBarColor
	}
	return systemBarColor
}

// dimThinking 把思考内容渲染成次级暗显(暗灰 + 斜体),并按 width 软换行。
// 不走 glamour:思考是模型的内部独白,弱化显示避免抢正式回复的视觉焦点。
func dimThinking(s string, width int) string {
	if s == "" {
		return ""
	}
	st := lipgloss.NewStyle().Foreground(thinkingBarColor).Italic(true)
	if width > 0 {
		st = st.Width(width)
	}
	return st.Render(s)
}

// applyQuoteBar 在已渲染 ANSI 文本左侧画一根色条,做两级引用。
//
//	一级(user / assistant / system):每行前缀 "┃ " (U+2503 粗竖线 + 空格),不缩进
//	二级(tools):每行前缀 "  │ " (2 空格缩进 + U+2502 细竖线 + 空格)
//
// 所有行统一前缀,不再用 ╭/╰ 端点 — 单行段也不会出现"半截括号"错位的视觉。
// 色条字符按 kind 着色,正文 1 cell 间距。
//
// 输入 content 应该已经按 barInnerWidth(width, kind) 软换行 — 这里只加前缀,不再 wrap。
func applyQuoteBar(content, kind string) string {
	if content == "" {
		return ""
	}
	var (
		barChar string
		indent  string
	)
	switch kind {
	case kindTools, kindThinking:
		barChar = "│" // 细线,作为二级 quote
		indent = "  " // 2 列缩进,视觉上嵌入到上方 assistant 段
	default:
		barChar = "┃" // 粗线,user/assistant/system 一级 quote
		indent = ""
	}
	bar := lipgloss.NewStyle().Foreground(barColorFor(kind))
	prefix := indent + bar.Render(barChar) + " "
	lines := strings.Split(content, "\n")
	for i, ln := range lines {
		lines[i] = prefix + ln
	}
	return strings.Join(lines, "\n")
}

// renderUserBubble 把用户回合渲染成气泡:左侧 1 列 ┃ 色条(与 LLM 一级 quote 同字符,多行连续)
// + 整段浅色底,按视口宽度铺满。不走 glamour —— 用户输入是纯文本(路径 / 代码片段居多),
// 气泡里按字面显示反而更清晰,也省掉 markdown 转义带来的意外(见 backslashSentinel)。
// lipgloss 的 Width 负责按宽换行 + 背景铺满。
func renderUserBubble(text string, viewportW int) string {
	if viewportW <= 0 || text == "" {
		return text
	}
	boxW := viewportW - 1 // 左侧色条占 1 列
	if boxW < 1 {
		boxW = 1
	}
	// 暂时去掉钢蓝色底(userBubbleBg),只保留左色条 + 白字 + 按宽换行,避免整片底色显乱。
	box := lipgloss.NewStyle().
		Foreground(userBubbleFg).
		Width(boxW).
		Padding(0, 1)
	bar := lipgloss.NewStyle().Foreground(userBubbleBar).Render("┃")
	// lipgloss 的 Width 只做按词边界的软折行:超长无空格单行(日志/JSON/minified 代码)
	// 找不到断词点就不硬断,会横向溢出 chat 区。再套一层 ansi.Wrap 做硬折行兜底,
	// 任何超宽行都被强制断到内容区宽内(box 带左右 Padding(0,1) 各 1 列,内容宽 = boxW-2),
	// 与模型回复走 glamour 的一致,避免粘贴长文本后错乱。
	innerW := boxW - 2
	if innerW < 1 {
		innerW = 1
	}
	rendered := ansi.Wrap(box.Render(text), innerW, "")
	lines := strings.Split(rendered, "\n")
	for i, ln := range lines {
		lines[i] = bar + ln
	}
	return strings.Join(lines, "\n")
}

// barInnerWidth 计算正文区可用宽度(扣掉色条占用的列)。
// tools 段比一级段额外扣 2 列缩进。
func barInnerWidth(viewportW int, kind string) int {
	bar := barColWidthLevel1
	if kind == kindTools || kind == kindThinking {
		bar = barColWidthLevel2
	}
	w := viewportW - bar
	if w < 1 {
		w = 1
	}
	return w
}

// indentBlock 给多行内容的每一行前面加 prefix(典型用法:缩进 2 个空格)。
// 用于 plan / spinner 等临时 overlay 内容,跟上方色条段视觉对齐。
func indentBlock(content, prefix string) string {
	if content == "" {
		return content
	}
	lines := strings.Split(content, "\n")
	for i, ln := range lines {
		lines[i] = prefix + ln
	}
	return strings.Join(lines, "\n")
}
