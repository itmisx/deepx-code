package tui

import (
	"deepx/agent"
	"deepx/config"
	"deepx/tools"
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// 自定义表单的字段下标。顺序必须与 setupCustomFieldDefs 一致 —— buildCustomConfig 按下标取值,
// 用常量而不是裸数字,免得以后往表里插字段时漏改某处。
const (
	fCustomName = iota // 供应商名:存进 provider.yaml 的 key,/provider <名字> 按它切换
	fCustomFlashBaseURL
	fCustomFlashModel
	fCustomFlashAPIKey
	fCustomFlashMaxTokens
	fCustomFlashCtxWindow
	fCustomProBaseURL
	fCustomProModel
	fCustomProAPIKey
	fCustomProMaxTokens
	fCustomProCtxWindow
)

// setupCustomFieldDefs 定义「其它」自定义表单的字段顺序与元信息。
// group 是分组标题:与上一字段的 group 不同时,渲染时插一行组头(Provider / Flash / Pro)。
var setupCustomFieldDefs = []struct {
	group       string
	label       string
	placeholder string
	isInt       bool
}{
	{"Provider", "name", "openrouter", false},
	{"Flash", "base_url", "https://api.openai.com/v1", false},
	{"Flash", "model", "gpt-4o-mini", false},
	{"Flash", "api_key", "sk-...", false},
	{"Flash", "max_tokens", "8192", true},
	{"Flash", "context_window", "131072", true},
	{"Pro", "base_url", "https://api.openai.com/v1", false},
	{"Pro", "model", "gpt-4o", false},
	{"Pro", "api_key", "sk-...", false},
	{"Pro", "max_tokens", "8192", true},
	{"Pro", "context_window", "131072", true},
}

// newSetupCustomFields 按 setupCustomFieldDefs 创建一组空的自定义字段输入框(只留 placeholder 提示,
// 不预填当前配置 —— 预填后用户改的时候还得先删,反而麻烦)。焦点放第 0 个。
func newSetupCustomFields() []textinput.Model {
	fields := make([]textinput.Model, len(setupCustomFieldDefs))
	for i, def := range setupCustomFieldDefs {
		ti := textinput.New()
		ti.Prompt = "" // 去掉默认 "> ",避免和外层 [ ] 重复、并节省列宽
		ti.Placeholder = def.placeholder
		ti.CharLimit = 256
		ti.SetWidth(40)
		fields[i] = ti
	}
	if len(fields) > 0 {
		fields[0].Focus()
	}
	return fields
}

// atoiOr 把字符串解析为正整数;空 / 非法 / 非正 → 回退 def。
func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
		return n
	}
	return def
}

// overlayCentered 把 fg(modal)叠在 bg(主 UI)上居中显示。
// 实现:
//  1. 拆 bg 和 fg 成行;算出 fg 的最大显示宽度(以 ansi.StringWidth 测,跟终端实际渲染一致)
//  2. 居中位置:startY = (height - fgHeight)/2, startX = (width - fgWidth)/2
//  3. 对每一行 fg,用 ansi.Cut 把对应 bg 行的 [startX, startX+fgW) 区间挖掉换成 fg 内容
//  4. 重新 join 输出
//
// bg 太短(行数少于 startY+fgH)时,缺失行不补,modal 会被截断。这种情况下终端高度不够,
// 不在 modal 区也没什么意义。
func overlayCentered(bg, fg string, width, height int) string {
	fgLines := strings.Split(strings.TrimRight(fg, "\n"), "\n")
	fgH := len(fgLines)
	fgW := 0
	for _, ln := range fgLines {
		if w := ansi.StringWidth(ln); w > fgW {
			fgW = w
		}
	}

	startY := (height - fgH) / 2
	startX := (width - fgW) / 2
	if startY < 0 {
		startY = 0
	}
	if startX < 0 {
		startX = 0
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

// spliceLineCells 把 fg 的所有 cell 拼到 bg 的 [atCol, atCol+fgW) 区间,
// 保留 bg 在该区间前后的内容(连同 ANSI 转义)。
// 用 ansi.Cut 处理 ANSI 边界,避免 bg 的 SGR 状态污染 fg 或 fg 之后内容。
func spliceLineCells(bg, fg string, atCol, fgW int) string {
	pre := ansi.Cut(bg, 0, atCol)
	// bg 在 atCol 之前太短 → 补空格到 atCol 列,保证 fg 起始位置对齐
	if preW := ansi.StringWidth(pre); preW < atCol {
		pre += strings.Repeat(" ", atCol-preW)
	}
	post := ""
	if bgW := ansi.StringWidth(bg); atCol+fgW < bgW {
		post = ansi.Cut(bg, atCol+fgW, bgW)
	}
	return pre + fg + post
}

// providerDisplay 把供应商 id 映射成展示名(custom → 其它/Other)。
func providerDisplay(p string) string {
	if p == config.ProviderCustom {
		return T("setup.provider.custom")
	}
	return p
}

// refreshSetupProviders 重建第一步的候选列表:
//
//	config.ProviderOptions(deepseek / mimo / kimi / qwen / 其它(自定义)=新建)
//	++ provider.yaml 里已存的自定义名(按 ProviderNames 的顺序,custom 垫底)
//
// 后半段就是「二次配置」的入口:选中已存的名字 → 第二步表单预填它当前的值,改完 Enter 覆盖同名槽。
// setupSavedStart 记下分界线,给渲染插分隔标题、给 Enter 判断该不该预填。
func (m *model) refreshSetupProviders() {
	list := append([]string(nil), config.ProviderOptions...)
	m.setupSavedStart = len(list)
	if names, err := config.ProviderNames(); err == nil {
		for _, n := range names {
			// 预设名走上面那段(选中只填 api_key),这里只收自定义存档。
			if !config.IsPresetProvider(n) {
				list = append(list, n)
			}
		}
	}
	m.setupProviders = list
	// 列表变短(删了一项)时把光标贴到末尾,而不是弹回顶部。
	if m.setupProviderIdx >= len(list) {
		m.setupProviderIdx = len(list) - 1
	}
	if m.setupProviderIdx < 0 {
		m.setupProviderIdx = 0
	}
}

// setupDeleteSelected 处理第一步的删除键:第一次按只记下待删的名字并要求再按一次确认,
// 第二次按才真正从 provider.yaml 移除 —— 存档里有 api key,误删的代价是重新去后台抄一遍。
//
// 只能删「已保存的自定义」那一段:预设供应商和「其它(自定义)」是固定入口而不是存档,删不掉。
// 删除只动 provider.yaml,当前生效的 model.yaml 原样不动(哪怕删的正是当前在用的那份配置)。
func (m *model) setupDeleteSelected() {
	if !m.setupEditingSaved() {
		m.setupErr = T("setup.error.delete_not_saved")
		m.setupDeleteConfirm = ""
		return
	}
	name := m.curProvider()
	if m.setupDeleteConfirm != name {
		m.setupDeleteConfirm = name // 第一次:亮确认提示,等第二下
		m.setupErr = ""
		return
	}
	m.setupDeleteConfirm = ""
	if err := config.DeleteProvider(name); err != nil {
		m.setupErr = fmt.Sprintf(T("setup.error.delete"), err)
		return
	}
	m.setupErr = ""
	m.refreshSetupProviders()
	m.appendChat("System", fmt.Sprintf(T("setup.deleted"), name))
}

// curProvider 返回当前选中的供应商名(预设 id、"custom",或已存的自定义名)。
func (m model) curProvider() string {
	if m.setupProviderIdx >= 0 && m.setupProviderIdx < len(m.setupProviders) {
		return m.setupProviders[m.setupProviderIdx]
	}
	return config.ProviderOptions[0]
}

// setupIsCustomForm 报告第二步该走哪套表单:预设供应商 → 单 api_key 输入框;
// 其余(新建自定义 / 编辑已存的自定义)→ 多字段表单。
func (m model) setupIsCustomForm() bool {
	return !config.IsPresetProvider(m.curProvider())
}

// setupEditingSaved 报告当前选中的是不是「已保存的自定义」分段里的项(即二次修改,而非新建)。
func (m model) setupEditingSaved() bool {
	return m.setupSavedStart > 0 && m.setupProviderIdx >= m.setupSavedStart &&
		m.setupProviderIdx < len(m.setupProviders)
}

// setupProviderRows 渲染第一步的候选列表:预设段用展示名(custom → 其它/Other),
// 已保存段用原始名(它就是 /provider 要敲的字符串),两段之间插一行标题。
func (m model) setupProviderRows() []string {
	rows := make([]string, 0, len(m.setupProviders)+1)
	for i, p := range m.setupProviders {
		if i == m.setupSavedStart {
			rows = append(rows, lipgloss.NewStyle().Foreground(dimColor).Render("  "+T("setup.saved_section")))
		}
		label := p
		if i < m.setupSavedStart {
			label = providerDisplay(p)
		}
		if i == m.setupProviderIdx {
			rows = append(rows, lipgloss.NewStyle().Foreground(highlightColor).Bold(true).Render("  ▸ "+label))
		} else {
			rows = append(rows, lipgloss.NewStyle().Foreground(subtleColor).Render("    "+label))
		}
	}
	return rows
}

// prefillCustomFields 把已存供应商的配置回填进自定义表单,供二次修改。
// name 字段一并填上原名:直接 Enter 就是覆盖同名槽,改掉它则是改名(见 submitSetup)。
// max_tokens / context_window 为 0(未设,走模型默认)时留空,免得把一个凭空的数字固化进配置。
func (m *model) prefillCustomFields(name string) {
	cfg, ok, err := config.LoadProvider(name)
	if err != nil || !ok {
		return
	}
	set := func(i int, v string) {
		if i >= 0 && i < len(m.setupCustomFields) {
			m.setupCustomFields[i].SetValue(v)
		}
	}
	num := func(n int) string {
		if n <= 0 {
			return ""
		}
		return strconv.Itoa(n)
	}
	set(fCustomName, name)
	set(fCustomFlashBaseURL, cfg.Flash.BaseURL)
	set(fCustomFlashModel, cfg.Flash.Model)
	set(fCustomFlashAPIKey, cfg.Flash.APIKey)
	set(fCustomFlashMaxTokens, num(cfg.Flash.MaxTokens))
	set(fCustomFlashCtxWindow, num(cfg.Flash.ContextWindow))
	set(fCustomProBaseURL, cfg.Pro.BaseURL)
	set(fCustomProModel, cfg.Pro.Model)
	set(fCustomProAPIKey, cfg.Pro.APIKey)
	set(fCustomProMaxTokens, num(cfg.Pro.MaxTokens))
	set(fCustomProCtxWindow, num(cfg.Pro.ContextWindow))
}

// enterSetupStep2 从第一步进入第二步:按选中项准备对应的输入控件。
// 选中「已保存的自定义」→ 预填该存档;选中「其它(自定义)」→ 空表单新建;预设 → 清空的 api_key 输入框。
func (m *model) enterSetupStep2() {
	m.setupStep = 1
	m.setupErr = ""
	if m.setupIsCustomForm() {
		m.setupCustomFields = newSetupCustomFields()
		m.setupFieldIdx = 0
		if m.setupEditingSaved() {
			m.prefillCustomFields(m.curProvider())
		}
		return
	}
	m.setupInput.SetValue("")
	m.setupInput.Focus()
}

// setupModalBlock 只渲染 modal 本身(不放置),供 overlay 使用。
// 两步:setupStep==0 选供应商;==1 填配置(预设供应商单填 api_key,custom 填 10 字段表单)。
func (m model) setupModalBlock() string {
	// 标题 + 括号写明保存路径(去掉所有说明性提示文字)。
	savePath := "~/.deepx/model.yaml"
	if p, err := config.Path(); err == nil {
		savePath = abbreviatePath(p, 48)
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(highlightColor).Render(T("setup.title")) +
		lipgloss.NewStyle().Foreground(dimColor).Render("  ("+fmt.Sprintf(T("setup.save_path_hint"), savePath)+")")

	var body, footer string
	if m.setupStep == 0 {
		// 供应商竖排,选中项行首高亮标记。
		providerLabel := lipgloss.NewStyle().Foreground(dimColor).Render(T("setup.provider_label"))
		body = providerLabel + "\n" + strings.Join(m.setupProviderRows(), "\n")
		footer = T("setup.footer.step_provider")
		if m.setupEditingSaved() {
			footer += T("setup.footer.delete_hint") // 只有停在已保存的自定义上才提删除
		}
	} else {
		provName := lipgloss.NewStyle().Foreground(subtleColor).Render(T("setup.cur_provider") + " " + providerDisplay(m.curProvider()))
		if m.setupEditingSaved() {
			provName += lipgloss.NewStyle().Foreground(dimColor).Render("  " + T("setup.editing_hint"))
		}
		if m.setupIsCustomForm() {
			body = provName + "\n\n" + m.setupCustomFormBlock()
			footer = T("setup.footer.step_custom")
		} else {
			inputLabel := lipgloss.NewStyle().Foreground(dimColor).Render(T("setup.input_label"))
			body = provName + "\n\n" + inputLabel + "\n  " + m.setupInput.View()
			footer = T("setup.footer.step_preset")
		}
	}

	parts := []string{title, "", body}
	if m.setupErr != "" {
		parts = append(parts, "", lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render("✗ "+m.setupErr))
	}
	if m.setupDeleteConfirm != "" {
		parts = append(parts, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).
			Render(fmt.Sprintf(T("setup.delete_confirm"), m.setupDeleteConfirm)))
	}
	parts = append(parts, "", lipgloss.NewStyle().Foreground(dimColor).Render(footer))

	content := lipgloss.JoinVertical(lipgloss.Left, parts...)

	// 整框 72(含边框+内边距);内容区 = 72-2-4 = 66,容得下自定义表单每行(标签+方括号输入框)不换行。
	modalWidth := 72
	if maxW := m.width - 4; modalWidth > maxW {
		modalWidth = maxW
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlightColor).
		Padding(1, 2).
		Width(modalWidth).
		Render(content)
}

// setupCustomFormBlock 渲染「其它」自定义的 10 字段表单:标签右对齐 + 方括号输入框,
// 焦点字段行首 ▸ 并高亮括号,flash/pro 分组。常规表单观感。
func (m model) setupCustomFormBlock() string {
	const labelW = 14
	var b strings.Builder
	lastGroup := ""
	for i, def := range setupCustomFieldDefs {
		if def.group != lastGroup {
			if lastGroup != "" {
				b.WriteString("\n") // 组间空一行
			}
			b.WriteString(lipgloss.NewStyle().Foreground(dimColor).Bold(true).Render(def.group) + "\n")
			lastGroup = def.group
		}
		focused := i == m.setupFieldIdx

		marker := "  "
		labelStyle := lipgloss.NewStyle().Foreground(subtleColor).Width(labelW).Align(lipgloss.Right)
		bracketStyle := lipgloss.NewStyle().Foreground(dimColor)
		if focused {
			marker = lipgloss.NewStyle().Foreground(highlightColor).Render("▸ ")
			labelStyle = labelStyle.Foreground(highlightColor)
			bracketStyle = lipgloss.NewStyle().Foreground(highlightColor)
		}

		view := ""
		if i < len(m.setupCustomFields) {
			view = m.setupCustomFields[i].View()
		}
		field := bracketStyle.Render("[ ") + view + bracketStyle.Render(" ]")
		b.WriteString(marker + labelStyle.Render(def.label) + "  " + field + "\n")
	}
	return b.String()
}

// buildCustomConfig 从自定义表单构造 Config,并解出这份配置要存进 provider.yaml 的供应商名。
//
// name 留空 → 回退 "custom"(老行为:不起名字就还是占那一个槽);起了名字则该名字成为
// provider.yaml 的 key,之后 `/provider <名字>` 就能切回来 —— 多份自定义配置各占各的槽,互不覆盖。
// flash 必须填全 base_url/model/api_key;pro 的 base_url/api_key 留空则继承 flash、model 留空则同 flash。
// max_tokens / context_window 留空用通用默认。返回 (cfg, name, "") 成功,(nil, "", errMsg) 失败。
func (m *model) buildCustomConfig() (*config.Config, string, string) {
	v := func(i int) string {
		if i < len(m.setupCustomFields) {
			return strings.TrimSpace(m.setupCustomFields[i].Value())
		}
		return ""
	}
	name := config.NormalizeProviderName(v(fCustomName))
	if !config.ValidProviderName(name) {
		return nil, "", T("setup.error.custom_name")
	}
	if config.IsPresetProvider(name) {
		return nil, "", fmt.Sprintf(T("setup.error.custom_name_reserved"), name)
	}
	flash := config.ModelEntry{
		BaseURL:       v(fCustomFlashBaseURL),
		Model:         v(fCustomFlashModel),
		APIKey:        v(fCustomFlashAPIKey),
		MaxTokens:     atoiOr(v(fCustomFlashMaxTokens), config.CustomDefaultMaxTokens),
		ContextWindow: atoiOr(v(fCustomFlashCtxWindow), config.CustomDefaultContextWindow),
	}
	if flash.BaseURL == "" || flash.Model == "" || flash.APIKey == "" {
		return nil, "", T("setup.error.custom_flash")
	}
	pro := config.ModelEntry{
		BaseURL:       v(fCustomProBaseURL),
		Model:         v(fCustomProModel),
		APIKey:        v(fCustomProAPIKey),
		MaxTokens:     atoiOr(v(fCustomProMaxTokens), config.CustomDefaultMaxTokens),
		ContextWindow: atoiOr(v(fCustomProCtxWindow), config.CustomDefaultContextWindow),
	}
	if pro.BaseURL == "" {
		pro.BaseURL = flash.BaseURL
	}
	if pro.APIKey == "" {
		pro.APIKey = flash.APIKey
	}
	if pro.Model == "" {
		pro.Model = flash.Model
	}
	return &config.Config{Flash: flash, Pro: pro}, name, ""
}

// focusCustomField 把焦点移到第 idx 个自定义字段(环绕越界),其余 Blur。
func (m *model) focusCustomField(idx int) {
	n := len(m.setupCustomFields)
	if n == 0 {
		return
	}
	idx = (idx%n + n) % n
	for i := range m.setupCustomFields {
		m.setupCustomFields[i].Blur()
	}
	m.setupCustomFields[idx].Focus()
	m.setupFieldIdx = idx
}

// submitSetup 处理 modal 内 Enter 的提交逻辑:
//   - 校验输入非空
//   - 按选中的供应商用 config.DefaultFor 构造 yaml
//   - 落盘
//   - 重新 Load(保证内存版本和磁盘一致)
//   - 把 model 内的 m.models 替换为新配置
//   - 关闭 modal,把焦点交回主输入框
//
// 失败时设置 setupErr,modal 留着等用户重试。
func (m *model) submitSetup() tea.Cmd {
	provider := m.curProvider()
	// 是否在二次修改某个已存的自定义供应商(决定改名时要不要清掉旧槽)。
	editing := m.setupEditingSaved()

	// 存进 provider.yaml 的名字:预设供应商就是它自己的 id;自定义则取表单里用户起的名字。
	archiveName := provider

	var cfg *config.Config
	if m.setupIsCustomForm() {
		built, name, errMsg := m.buildCustomConfig()
		if errMsg != "" {
			m.setupErr = errMsg
			return nil
		}
		cfg = built
		archiveName = name
	} else {
		val := strings.TrimSpace(m.setupInput.Value())
		if val == "" {
			m.setupErr = T("setup.error.empty")
			return nil
		}
		cfg = config.DefaultFor(provider, val) // 预设供应商:套 modelConfig 默认 + 该 key
	}
	if err := config.Save(cfg); err != nil {
		m.setupErr = fmt.Sprintf(T("setup.error.save"), err)
		return nil
	}
	// 同步存档到 provider.yaml(按供应商名),供 /provider 快捷切换。存档失败不致命,不挡 /config。
	_ = config.SaveProvider(archiveName, cfg)
	// 二次修改时把 name 改成了别的 → 当改名处理,清掉旧槽,免得留一份同内容的孤儿存档。
	renamedFrom := ""
	if editing && provider != archiveName {
		_ = config.DeleteProvider(provider)
		renamedFrom = provider
	}
	loaded, err := config.Load()
	if err != nil {
		m.setupErr = fmt.Sprintf(T("setup.error.reload"), err)
		return nil
	}
	m.models = agent.ModelConfig{
		Flash: agent.ModelEntry(loaded.Flash),
		Pro:   agent.ModelEntry(loaded.Pro),
	}
	m.activeModelRole = "flash"
	m.activeModelID = m.models.Flash.Model
	if m.activeModelID == "" {
		m.activeModelRole = "pro"
		m.activeModelID = m.models.Pro.Model
	}
	// 模型换了:视觉能力可能变,重置 —— 先用新模型的缓存值垫初值,下面返回探测命令立刻重探。
	m.visionByModel = loadVisionCaps(m.models)
	// 窗口也可能变了:单次 Write 上限随窗口自适应,重算注入(见 agent.WriteContentLimitFor)。
	tools.SetWriteContentLimit(agent.WriteContentLimitFor(m.models))
	// 重置 modal 状态
	m.showSetup = false
	m.setupRequired = false
	m.setupErr = ""
	m.setupStep = 0
	m.setupCustomFields = nil
	m.setupFieldIdx = 0
	m.setupInput.Reset()
	m.setupInput.Blur()
	m.input.Focus()
	m.refreshSetupProviders() // 新建/改名的槽下次打开 /config 就在列表里

	path, _ := config.Path()
	// 反斜杠转义已在 renderMarkdown 渲染层统一处理(见 backslashSentinel),这里不必再包反引号。
	m.appendChat("System", T("setup.saved_to")+path)
	// 告诉用户这份配置存成了哪个供应商名 —— 否则自定义命名后没人知道 /provider 该敲什么。
	m.appendChat("System", fmt.Sprintf(T("setup.archived_as"), archiveName, archiveName))
	if renamedFrom != "" {
		m.appendChat("System", fmt.Sprintf(T("setup.renamed_from"), renamedFrom, archiveName))
	}
	// 对新配置的模型重探视觉能力(结果经 visionCapMsg 回灌当前会话 + 覆盖缓存)。
	// 余额也重探:换了供应商/Key,旧值已无意义,先清空待新值回灌。
	m.balance = ""
	cmds := visionProbeCmds(m.models)
	if cmd := balanceProbeCmd(m.models); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

// openSetupModal 给 /config 命令用:把当前面板切到 modal(从选供应商那步开始),允许 Esc 取消。
func (m *model) openSetupModal() {
	m.refreshSetupProviders() // 每次打开都重读 provider.yaml,已存的自定义名才是最新的
	m.showSetup = true
	m.setupRequired = false
	m.setupErr = ""
	m.setupDeleteConfirm = ""
	m.setupStep = 0
	m.setupFieldIdx = 0
	m.setupCustomFields = nil
	m.setupInput.SetValue("")
	m.setupInput.Blur()
	m.input.Blur()
}

// handleProviderCommand 分发 /provider(弹选择器)与 /provider <名字>(直切)。
// 供应商名来自 provider.yaml(已 /config 过的才在);没有任何存档则提示先 /config。
func (m *model) handleProviderCommand(input string) tea.Cmd {
	names, err := config.ProviderNames()
	if err != nil {
		m.appendChat("System", fmt.Sprintf(T("provider.error.load"), err))
		return nil
	}
	if len(names) == 0 {
		m.appendChat("System", T("provider.empty"))
		return nil
	}
	// /provider <名字> → 直切。走和存档时同一套规范化(去空白 + 转小写),免得大小写敲错就查无此名。
	if fields := strings.Fields(strings.TrimSpace(input)); len(fields) >= 2 {
		return m.applyProvider(config.NormalizeProviderName(fields[1]))
	}
	// 裸 /provider → 弹选择器,光标默认停在与当前 model.yaml 匹配的供应商上(匹配不到停 0)。
	m.providerNames = names
	m.providerModalIdx = 0
	for i, n := range names {
		if cfg, ok, _ := config.LoadProvider(n); ok &&
			cfg.Flash.Model == m.models.Flash.Model && cfg.Pro.Model == m.models.Pro.Model {
			m.providerModalIdx = i
			break
		}
	}
	m.showProviderModal = true
	return nil
}

// applyProvider 把 provider.yaml 中指定供应商的 flash/pro 写回 model.yaml 并热切换:
// 重载、刷新 m.models、重探视觉。返回视觉探测命令。逻辑与 submitSetup 的模型更新段一致。
func (m *model) applyProvider(name string) tea.Cmd {
	cfg, ok, err := config.LoadProvider(name)
	if err != nil {
		m.appendChat("System", fmt.Sprintf(T("provider.error.load"), err))
		return nil
	}
	if !ok {
		m.appendChat("System", fmt.Sprintf(T("provider.unknown"), name))
		return nil
	}
	if err := config.Save(cfg); err != nil {
		m.appendChat("System", fmt.Sprintf(T("provider.error.save"), err))
		return nil
	}
	loaded, err := config.Load()
	if err != nil {
		m.appendChat("System", fmt.Sprintf(T("provider.error.save"), err))
		return nil
	}
	m.models = agent.ModelConfig{
		Flash: agent.ModelEntry(loaded.Flash),
		Pro:   agent.ModelEntry(loaded.Pro),
	}
	m.activeModelRole = "flash"
	m.activeModelID = m.models.Flash.Model
	if m.activeModelID == "" {
		m.activeModelRole = "pro"
		m.activeModelID = m.models.Pro.Model
	}
	// 换了供应商 → 视觉能力可能变,先用缓存垫初值,再返回探测命令重探。
	m.visionByModel = loadVisionCaps(m.models)
	// 窗口也可能变了:单次 Write 上限随窗口自适应,重算注入(见 agent.WriteContentLimitFor)。
	tools.SetWriteContentLimit(agent.WriteContentLimitFor(m.models))
	m.appendChat("System", fmt.Sprintf(T("provider.switched"), name, m.models.Flash.Model, m.models.Pro.Model))
	m.refreshViewport()
	// /provider 切供应商后同步 Web 面板模型名(Hub 快照的 Models 只在启动时设过一次,
	// 不广播的话浏览器右栏仍显示旧模型)。
	if m.hub != nil {
		m.hub.SetModels(m.models.Flash.Model, m.models.Pro.Model, m.activeModelRole)
	}
	// 换供应商 → 余额变了,先清空待重探回灌。
	m.balance = ""
	cmds := visionProbeCmds(m.models)
	if cmd := balanceProbeCmd(m.models); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}
