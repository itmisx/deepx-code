package tui

import (
	"context"
	"deepx/agent"
	"deepx/mcp"
	"deepx/session"
	"deepx/skill"
	"deepx/tools"
	"deepx/web"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// imagePlaceholderRe 匹配输入框里 [Image #N] 形式的图片占位符。
var imagePlaceholderRe = regexp.MustCompile(`\[Image #(\d+)\]`)

type model struct {
	width  int
	height int

	chatViewport viewport.Model
	chatContent  *chatLog

	input textarea.Model

	// 整套连接配置 (per-role BaseURL/Model/APIKey),从 ~/.deepx/model.yaml 读取后传入。
	// 旧的 apiKey/baseURL 单独字段已合并到 models 里,通过 models.Flash.XX / models.Pro.XX 访问。
	models agent.ModelConfig

	// 配置 modal 状态:
	//   - showSetup       = true 时 View() 弹模态,Update() 路由按键给 setupInput
	//   - setupRequired   = true 表示无有效 yaml(首次启动);不允许 Esc 关闭,Ctrl+C 才退出
	//   - setupInput      = api key 输入框
	//   - setupErr        = 错误回显(保存失败 / 输入为空等)
	showSetup     bool
	setupRequired bool
	setupInput    textinput.Model
	setupErr      string

	// activeModelRole 是上一轮 / 当前流的实际生效角色 ("flash" / "pro")。
	// 每条新用户消息默认从 flash 起手;agent.ModelSwitchMsg 到达时更新为 pro。
	activeModelRole string
	activeModelID   string

	// proKeepRounds 是 pro 模型保持轮数计数器。
	// 每次收到 pro 的 ModelSwitchMsg 时设为 3,每轮用户消息递减 1。
	// 当值 > 0 时,下一轮强制走 pro(跳过 RouteByKeyword 关键词路由)。
	// 用户可输入 /flash 手动清 0 降回 flash。
	proKeepRounds int

	mode    agent.AgentMode
	history []agent.ChatMessage

	streamCh <-chan tea.Msg

	status    string
	streaming bool
	tokens    int

	currentReply *strings.Builder

	// 待发送的图片已落盘文件路径,Ctrl+V 累加,enter 后清空。
	// 不在内存里囤 PNG 字节是为了:(a) 让 img_ocr 工具能凭路径读;
	// (b) 历史 ChatMessage 之后再次发回 API 时不带巨大的 base64。
	attachedImagePaths []string

	// 当前活跃规划。nil = 无规划。pro 调用 CreatePlan 时初始化,
	// UpdatePlanStatus 通过 TaskStatusMsg 增量更新。每次新用户消息发起前清空。
	plan *planState

	// 鼠标 chat 矩形选区。selecting=true 表示左键在 chat 区按下后还没松开;
	// selAnchor / selEnd 是选区两端 (cellPos: 显示列 + wrapped 行号)。
	// 松开左键时:抠出选区内文本写到系统剪贴板,然后 selecting=false 高亮自动消失。
	selecting bool
	selAnchor cellPos
	selEnd    cellPos

	// 输入框全选态(Ctrl+A 触发)。true 时输入框 value 整段反色显示,
	// 下一次按键消费"全选"语义:输入字符 / 删除键 → 清空 value 后再 process;
	// Esc / 方向键 → 仅取消选择,不动 value;
	// Ctrl+A → 维持。
	inputAllSelected bool

	// app 侧光标 blink 状态。textarea 现在用真实终端光标(SetVirtualCursor(false)),
	// 终端光标 blink 走 DECSCUSR 指令但部分终端(如 VS Code 集成终端)不响应,
	// 所以这里 600ms tick 自己切换 cursor 可见性 —— View 看到 cursorBlinkOff=true 时
	// 不往 tea.View.Cursor 塞值,bubbletea 就把光标藏起来,下一拍切回来。
	// 用户按键时由 Update 重置成"亮"并 reset 计时,保持手感跟旧虚拟光标一致。
	cursorBlinkOff bool

	// 命令 palette 选中索引。是否打开不存字段 — 由 filterSlashCommands(input value)
	// 实时计算,避免状态同步问题。idx 越界时(value 一变 matches 变短)由消费方 clamp。
	commandPaletteIdx int

	// lastInputClickAt 记录最近一次落在输入框那一行的左键 click 时间戳。
	// 用来手动检测双击:两次 click 间隔 < 400ms 即视为双击,切换 inputAllSelected。
	// bubbletea v2 的 MouseClickMsg 不带 Clicks 计数,只能自己算。
	lastInputClickAt time.Time

	// inputDragging 表示左键在输入框区域按下后还没松开,用来实现"输入框拖拽全选":
	// 拖动中 → inputAllSelected=true 高亮整段;松手 → 复制输入框内容到剪贴板。
	inputDragging bool

	// copyHint 复制成功后的临时提示("✓ 已复制"),叠在鼠标松开的位置 (copyHintX/Y),
	// 1.5s 后由 copyHintClearMsg 清空。空串 = 不显示。
	copyHint  string
	copyHintX int
	copyHintY int

	// 思考动画。streaming=true 且当前没在接收 content tokens 时,在 chat 末尾追加 spinner 帧。
	// reasoning_content / tool_call 阶段都视为"思考中",content token 一到就停;
	// 下一轮工具结果或新 reasoning 到来时再次开启,实现"多轮思考折叠成一个动画"。
	spinner  spinner.Model
	thinking bool

	// activeTool 是当前正在执行的工具名(ToolCallStart 时置,TokenMsg/流结束时清空)。
	// 仅用于输入框上方的活动状态行,告诉用户"此刻在跑哪个工具"。
	activeTool string

	// session 是当前 workspace 的持久化句柄。启动时建/打开 ~/.deepx/sessions/{sid}/,
	// 写时机:user enter 后 + assistant 流结束(StreamDoneMsg)时各 append 一行。
	// Memory 工具通过 tools.SetMemorySession 拿到同一句柄,扫 jsonl 命中关键词。
	session *session.Manager

	// skill 系统:启动时扫 ~/.deepx/skills/ + session.SkillsDir() 两个目录的 SKILL.md。
	//   - skillLoader 注入 tools.LoadSkill,LLM 调工具时按名取正文
	//   - skillCatalog 在 system prompt 里挂"name + description"摘要,引导 LLM 决定要不要 LoadSkill
	// 进程内不刷新 — 用户增删 skill 后重启 deepx。
	skillLoader  *skill.Loader
	skillCatalog string

	// summary 是会话压缩摘要(内存态),每轮注入 system prompt 尾部(见 BuildSystemPrompt)。
	// 不再作为 history[0] 消息存在;持久化在 state.json 的 summary 字段。
	summary string

	// 重启缓存友好压缩:detectRestartCompaction 检测到前缀变化时暂存上次前缀快照,
	// Init 时用 restartCompactionCmd 在首请求前跑一次压缩(见 prefix_cache.go)。
	pendingCompactModel string
	pendingCompactSys   string
	pendingCompactTools string

	// compacting:压缩 Cmd 在飞时为 true。三个触发点(70%/重启/空闲)都 gate 在它上,
	// 防止并发压缩(否则第二个结果的 cutIdx 会越界已截断的 history → panic)。
	compacting bool

	// pendingUserText:本轮用户输入原文,发送时暂存、本轮成功后才写入 jsonl(连同 assistant)。
	// 这样 503/失败/取消的轮次不会在 jsonl 里留下没有回复的孤儿 user 记录。
	pendingUserText string

	// review 模式审核状态
	reviewPending  bool
	reviewCh       chan bool
	reviewToolName string
	reviewToolArgs string
	reviewYesNo    bool // true=YES, false=NO

	// /lang 选择 modal 状态。showLangModal=true 时全屏路由按键到 modal,
	// langModalIdx ∈ {0:zh, 1:en}。
	showLangModal bool
	langModalIdx  int

	// MCP:mcpMgr 管理外部 MCP server 连接与工具注入(启动时后台连接配置里的 server)。
	// /mcp-add 弹单行输入框(格式 "名称 命令 [参数...]");/mcp-delete 弹 server 列表选删。
	mcpMgr        *mcp.Manager
	showMcpAdd    bool
	mcpAddInput   textinput.Model
	mcpAddErr     string
	showMcpDelete bool
	mcpDelNames   []string
	mcpDelIdx     int

	// 版本信息。version 是 build 时注入的当前版本号(go build 默认 "dev")。
	// latestVersion 是异步检查得到的 GitHub latest release,空则没检查到 / 网络失败。
	// upgradeAvailable 由 versionNewer(latestVersion, version) 算出,渲染时用来决定是否
	// 在右栏显示"有新版本"提示。
	version          string
	latestVersion    string
	upgradeAvailable bool
	upgradeURL       string

	// 右栏仪表盘字段
	workspace       string        // os.Getwd() at startup,展示当前工作目录
	turnStartedAt   time.Time     // 本轮 Enter 时刻,用于实时计算 elapsed
	turnElapsed     time.Duration // 上一轮总耗时,streaming=false 时显示这个
	turnInputChars  int           // 本轮 user 发送时的 history 总字符数(快照)
	turnOutputChars int           // 本轮 assistant content 累计字符数(只算 content,跳过 reasoning)
	turnToolCalls   int           // 本轮工具调用次数,流结束时打进"完成"行

	// cancelAgent 取消后台 agent 的 context。ESC 中断时调用,真正终止 HTTP 请求和工具调用。
	cancelAgent context.CancelFunc

	// lastUsage 上一轮主 agent 的 API token 用量,含缓存命中信息。
	lastUsage *agent.UsageInfo

	// mdRenderers 按 wrap width 缓存 glamour renderer 实例。
	// window resize 时新 width 会触发新 renderer 创建,旧的进 cache 但短期不复用 — 不主动清理,
	// 内存占用极小(每个实例约几 KB,通常活跃 1-2 个 width)。
	mdRenderers map[int]*glamour.TermRenderer

	// web dashboard:hub 为 nil 表示 web 关闭(所有广播走 broadcast() 守卫跳过)。
	// webURL 非空时右栏显示 ◆ WEB 地址。
	hub    *web.Hub
	webURL string
}

// webInputMsg 是浏览器提交的输入,经 program.Send 注入,走和终端 Enter 完全相同的提交逻辑。
type webInputMsg struct{ text string }

// webReviewMsg 是浏览器的 review 确认,经 program.Send 注入,复用终端同一个 ReviewCh(先到先得)。
type webReviewMsg struct{ approve bool }

// reviewResultMsg 审核完成后从 goroutine 发回,恢复流监听。
type reviewResultMsg struct{}

// compressionResultMsg 会话压缩完成后的结果,由异步 tea.Cmd 发回 Update。
type compressionResultMsg struct {
	summary         string
	cutIdx          int // 从 snapshot 算出的截断位置
	compressedTurns int // 本次压缩的 user 轮数
	err             error
	manual          bool // true = /compact 手动触发:失败要给用户反馈,而非像后台触发那样静默
}

func initialModel(models agent.ModelConfig, needsSetup bool, version string, hub *web.Hub, webURL string) model {
	vp := viewport.New()

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("99"))

	ti := textarea.New()
	ti.Placeholder = T("misc.input_placeholder")
	ti.CharLimit = 4000
	ti.ShowLineNumbers = false
	ti.SetHeight(3)
	// 输入框样式定制:
	//   - 第一行显示 "> ",后续行只缩进 2 空格(对齐到内容列)避免每行重复 prompt
	//   - Prompt 染粉紫(同 banner 主色),focus / blur 状态都保留亮度
	//   - 内置 CursorLine 高亮(默认会给当前行加背景色)关掉,跟 chat 区无 chrome 风格一致
	tas := ti.Styles()
	tas.Focused.CursorLine = lipgloss.NewStyle()
	tas.Blurred.CursorLine = lipgloss.NewStyle()
	tas.Focused.Base = lipgloss.NewStyle()
	tas.Blurred.Base = lipgloss.NewStyle()
	// 光标样式:细竖条 + 粉紫色 + 缓慢 blink(600ms),跟 banner 主色一致,
	// 避免默认 block 光标在中文/emoji 行上把字符整块反色显得突兀。
	tas.Cursor.Shape = tea.CursorBar
	tas.Cursor.Color = lipgloss.Color("213") // 亮粉,跟 banner deepx 渐变首色一致
	tas.Cursor.Blink = true
	tas.Cursor.BlinkSpeed = 600 * time.Millisecond
	ti.SetStyles(tas)
	// 关掉 textarea 的虚拟光标 —— 虚拟光标把光标渲染成"反色的格子上字符"塞进
	// View 字符串里,跟正文字符一起进 bubbletea cellbuf 做帧差。CJK 宽字符 +
	// placeholder→content 跨帧 diff 时,这个反色 cell 会让首字符的格子算错位置,
	// 表现为首字符不显示。改用真实终端光标(由 tui/view.go wrapView 里 v.Cursor 注入),
	// 真实光标只是终端的位置 + 形状指令,完全不参与正文字符渲染。
	ti.SetVirtualCursor(false)
	// 不用 textarea 自带 prompt:"> " 由 tui/view.go 作为固定左侧 gutter 单独画(见 inputGutterWidth)。
	// 这样多行粘贴 / 滚动时 "> " 始终钉在输入框左上角,不会跟着内容滚走。
	ti.Prompt = ""

	si := textinput.New()
	si.Placeholder = "sk-..."
	si.CharLimit = 256
	si.SetWidth(50)

	mi := textinput.New()
	mi.Placeholder = "名称 命令 [参数...]"
	mi.CharLimit = 512
	mi.SetWidth(54)

	// 起手角色 = flash (若 flash 未配置则退化到 pro)
	role := "flash"
	activeID := models.Flash.Model
	if activeID == "" {
		role = "pro"
		activeID = models.Pro.Model
	}

	wd, _ := os.Getwd()

	// 当前 workspace 的会话持久化。建/打开 ~/.deepx/sessions/{sha1(wd)[:16]}/。
	// 失败(权限/磁盘满)不致命 —— sess=nil 时 appendChat 跳过持久化,Memory 工具返回禁用提示。
	sess, sessErr := session.New(wd)
	if sessErr == nil {
		tools.SetMemorySession(sess)
	}

	// Skill 加载器:多来源发现(兼容 Claude Code / opencode / cursor / agents 生态)。
	// workspace 优先于 global,同名覆盖。任一目录不存在不报错。
	// 用户没建任何 skill 时 catalog 为空,system prompt 不挂"Available Skills"段。
	home, _ := os.UserHomeDir()
	skill.ExtractBuiltins(home) // 解压内嵌 skill 到 ~/.deepx/skills/
	workspaceSkillDirs := []string{
		filepath.Join(wd, ".deepx", "skills"),
	}
	globalSkillDirs := []string{
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".deepx", "skills"),
	}
	loader := skill.New(workspaceSkillDirs, globalSkillDirs)
	tools.SetSkillLoader(loader)
	skillCatalog := buildSkillCatalog(loader)

	// 代码图谱:绑定到当前 workspace 根,懒构建(首次 CodeGraph 调用时才遍历解析)。
	tools.SetCodeGraphRoot(wd)

	// MCP:后台连接 ~/.deepx/mcp.json 里配置的 server,连上后把它们的工具注入给 LLM。
	// 失败只记状态、不影响启动;没配置则什么都不做。
	mcpMgr := mcp.NewManager()
	mcpMgr.ConnectAll()

	m := model{
		mcpMgr:          mcpMgr,
		mcpAddInput:     mi,
		chatContent:     newChatLog(maxChatBytes),
		currentReply:    &strings.Builder{},
		chatViewport:    vp,
		input:           ti,
		models:          models,
		activeModelRole: role,
		activeModelID:   activeID,
		version:         version,
		mode:            agent.AgentMode_Auto,
		status:          "idle",
		spinner:         sp,
		workspace:       wd,
		setupInput:      si,
		session:         sess,
		skillLoader:     loader,
		skillCatalog:    skillCatalog,
		hub:             hub,
		webURL:          webURL,
	}

	// 回填上轮 token 用量,Usage section 启动后立刻显示真实数字而非 "—"。
	if sess != nil {
		if u := sess.LoadUsage(); u != nil {
			m.lastUsage = &agent.UsageInfo{
				PromptTokens:          u.PromptTokens,
				CompletionTokens:      u.CompletionTokens,
				PromptCacheHitTokens:  u.PromptCacheHitTokens,
				PromptCacheMissTokens: u.PromptCacheMissTokens,
			}
		}
	}

	// 恢复会话历史:优先尝试二进制 gob 文件(含 tool_calls/tool_results/reasoning),
	// 失败时回退 JSONL 文本恢复(仅 user/assistant content)。
	if sess != nil {
		// 摘要是内存态,每轮注入 system prompt 尾部;不再作为 history 消息。
		m.summary = sess.LoadSummary()

		var gobOK bool
		var gobHistory []agent.ChatMessage
		if err := sess.LoadGob("history.gob", &gobHistory); err == nil && len(gobHistory) > 0 {
			gobOK = true
			// system prompt 不再存进 history(每轮由 BuildSystemPrompt 现建,确保 skill/摘要
			// 变化即时生效)。剥掉旧 gob 里残留的首条 system。
			if len(gobHistory) > 0 && gobHistory[0].Role == "system" {
				gobHistory = gobHistory[1:]
			}
			// 兼容旧格式:摘要曾作为首条 "## 会话摘要" assistant 消息存在,迁移进 m.summary。
			if len(gobHistory) > 0 && gobHistory[0].Role == "assistant" &&
				strings.HasPrefix(gobHistory[0].Content, "## 会话摘要") {
				if m.summary == "" {
					m.summary = strings.TrimPrefix(gobHistory[0].Content, "## 会话摘要\n")
				}
				gobHistory = gobHistory[1:]
			}
			m.history = gobHistory
			rebuildChatFromHistory(m.chatContent, gobHistory)
		}

		if !gobOK {
			summary := sess.LoadSummary()
			if summary != "" {
				// gob 失效兜底:有摘要时,从 jsonl 取固定最近若干轮接在摘要后。
				// jsonl 是全量的,可能和摘要轻微重叠,但这是罕见兜底路径,可接受。
				const fallbackTurns = 10
				entries := sess.LoadRecentTurns(fallbackTurns)
				m.summary = summary

				for _, e := range entries {
					m.history = append(m.history, agent.ChatMessage{
						Role:    e.Role,
						Content: e.Content,
					})
					kind := kindAssistant
					if e.Role == "user" {
						kind = kindUser
					}
					m.chatContent.Open(kind, e.Content)
				}
				if len(entries) > 0 {
					m.chatContent.Open(kindSystem, fmt.Sprintf(T("misc.history_suffix"), len(entries)))
				}
			} else {
				// 无压缩摘要:按对加载
				const resumeTurns = 20
				entries := sess.LoadRecentTurns(resumeTurns)
				for _, e := range entries {
					m.history = append(m.history, agent.ChatMessage{
						Role:    e.Role,
						Content: e.Content,
					})
					kind := kindAssistant
					if e.Role == "user" {
						kind = kindUser
					}
					m.chatContent.Open(kind, e.Content)
				}
				if len(entries) > 0 {
					m.chatContent.Open(kindSystem, fmt.Sprintf(T("misc.history_suffix"), len(entries)))
				}
			}

			// 声明当前模式,通知 LLM 当前状态。模式始终从 auto 起步(默认全工具)。
			// 注意:gob 恢复时跳过此步骤 — 历史已包含之前的 mode notification,
			// 重复追加会在每次重启时累积,污染 LLM 上下文。
			msg := modeNotification(m.mode, m.activeModelRole)
			m.history = append(m.history, agent.ChatMessage{Role: "assistant", Content: msg})
			m.appendChat("assistant", msg)
			if m.session != nil {
				_ = m.session.Append("assistant", msg)
			}
		}
	}

	// 首次启动:强制弹 modal,焦点在 setup 输入框
	if needsSetup {
		m.showSetup = true
		m.setupRequired = true
		m.setupInput.Focus()
	} else {
		m.input.Focus()
	}

	// web dashboard 启用时,在 chat 区给一条提示(含完整 URL)。不再自动复制到剪贴板 ——
	// 现在 chat 区可直接选中复制,终端支持的话也能点链接跳转,自动占用剪贴板反而打扰。
	if webURL != "" {
		m.appendChat("System", fmt.Sprintf(T("web.ready"), webURL))
	}

	// 重启检测:若 prompt/工具/mcp 相对上次会话变了、历史又够大,标记需在首请求前压缩
	//(此刻前缀已失效,趁机压缩,且复刻旧前缀命中热缓存 —— 见 prefix_cache.go)。
	m.detectRestartCompaction()

	// endpoint / 模型 / 模式信息全部移到右栏(rightPanelView 直接读 m.models / m.baseURL),
	// chat 区不再发开场 System 消息,保持干净
	return m
}

// cursorBlinkTickMsg 是 app 侧 600ms 一次的 cursor blink 信号。
// 每次到达时 Update 切 cursorBlinkOff,然后返回下一拍 tick。
type cursorBlinkTickMsg struct{}

// cursorBlinkInterval = 半周期。亮 600ms,灭 600ms,跟旧虚拟光标的节奏对齐。
const cursorBlinkInterval = 600 * time.Millisecond

func cursorBlinkTick() tea.Cmd {
	return tea.Tick(cursorBlinkInterval, func(time.Time) tea.Msg {
		return cursorBlinkTickMsg{}
	})
}

// broadcast 把事件发给 web hub(关闭时 hub==nil,直接跳过)。
func (m model) broadcast(ev web.Event) {
	if m.hub != nil {
		m.hub.Broadcast(ev)
	}
}

// submitUserInput 是终端 Enter 和 web 输入共用的提交入口:
// 斜杠命令直接执行;否则构造 user 消息、落盘、广播 user_message,并启动 agent stream。
// 不碰输入框(textarea 清空由 Enter 分支自己做)。
func (m model) submitUserInput(input string) (model, tea.Cmd) {
	input = strings.TrimSpace(input)
	if input == "" && len(m.attachedImagePaths) == 0 {
		return m, nil
	}
	// 斜杠命令:仅匹配已知命令,粘贴的路径类文本不误触
	if matches := filterSlashCommands(input); len(matches) > 0 {
		cmd := m.handleSlashCommand(matches[0].name)
		return m, cmd
	}
	// 流式中再提交(主要是 web 端可能在生成时点发送)→ 丢弃,避免并发两个 stream。
	if m.streaming {
		return m, nil
	}

	userMsg := m.buildUserMessage(input)
	// 聊天窗口里仍显示用户输入的原文(含占位符),路径替换只发生在发给 LLM 的消息体中。
	m.appendChat("You", input)
	m.history = append(m.history, userMsg)
	// 用户输入先暂存,本轮成功后(StreamDoneMsg)才写 jsonl —— 失败的轮次不留孤儿记录。
	m.pendingUserText = input
	m.broadcast(web.Event{Kind: "user_message", Text: input})
	m.attachedImagePaths = nil

	m.status = "thinking"
	m.streaming = true
	m.thinking = true
	m.currentReply.Reset()

	// 仪表盘:开计时,快照本轮输入字符数,输出归零
	m.turnStartedAt = time.Now()
	m.turnInputChars = sumHistoryChars(m.history)
	m.turnOutputChars = 0
	m.turnToolCalls = 0
	m.activeTool = ""

	m.refreshViewport()

	var cmds []tea.Cmd
	cmds = append(cmds, m.spinner.Tick)

	// 每次新用户消息开始,角色重置回 flash;agent 内部 keyword router 决定本轮真实模型。
	m.activeModelRole = "flash"
	m.activeModelID = m.models.Flash.Model
	if m.activeModelID == "" {
		m.activeModelRole = "pro"
		m.activeModelID = m.models.Pro.Model
	}
	// 上一轮的 plan 清空
	m.plan = nil

	// B: pro 状态保持 — 如果上轮升了 pro,接下来 N 轮默认保持 pro
	forceRole := ""
	if m.proKeepRounds > 0 {
		m.proKeepRounds--
		forceRole = "pro"
	}

	workspace, _ := os.Getwd()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelAgent = cancel
	cmd, ch := agent.StartStream(
		ctx,
		m.models,
		m.history, maxTokens,
		m.mode,
		workspace,
		m.skillCatalog,
		m.summary,
		forceRole,
	)
	m.streamCh = ch
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m model) Init() tea.Cmd {
	// textarea 的光标 blink 由 Focus() 返回,启动时一并发起。
	// checkForUpgradeCmd 异步打 GitHub Releases API,完成后通过 upgradeCheckResult 回送 Update。
	// cursorBlinkTick 自己驱动真实光标的明灭节奏。
	cmds := []tea.Cmd{textinput.Blink, m.input.Focus(), checkForUpgradeCmd(m.version), cursorBlinkTick()}
	// 重启检测到前缀变化且历史够大时,在首请求前先跑一次缓存友好压缩。
	if m.pendingCompactSys != "" {
		cmds = append(cmds, m.restartCompactionCmd())
	}
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// web 镜像:agent 的每条流式消息恰好过一次 Update,这里统一映射并广播给浏览器。
	// 非 agent 消息(按键 / tick 等)ToWebEvent 返回 false,不广播。hub==nil 时 broadcast 跳过。
	if ev, ok := web.ToWebEvent(msg); ok {
		m.broadcast(ev)
	}

	switch msg := msg.(type) {

	case webInputMsg:
		// 浏览器提交的输入,走和终端 Enter 完全相同的提交逻辑。
		var cmd tea.Cmd
		m, cmd = m.submitUserInput(msg.text)
		return m, cmd

	case webReviewMsg:
		// 浏览器的 review 确认,复用终端同一个 ReviewCh(先到先得)。
		if m.reviewPending {
			m.reviewCh <- msg.approve
			m.reviewPending = false
			m.broadcast(web.Event{Kind: "review_resolved"})
			m.refreshViewport()
			return m, func() tea.Msg { return reviewResultMsg{} }
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		leftW, vpH := m.layout()
		m.chatViewport.SetWidth(leftW)
		m.chatViewport.SetHeight(vpH)
		// 输入区 = 左侧固定 gutter("> ")+ 右侧 textarea。textarea 占整宽 m.width-gutter
		//(分隔线只到 body 底,输入区横跨整行)。gutter 由 view.go 单独画。
		m.input.SetWidth(m.width - inputGutterWidth)
		m.input.SetHeight(inputAreaHeight - 2) // 减去上下各 1 行居中留白
		// 窗口尺寸变了 → wrap 重算 → 老 line 号失效,必须清选区
		m.selecting = false
		m.refreshViewport()

	case tea.MouseWheelMsg:
		// modal 期间忽略
		if m.showSetup || m.showLangModal {
			return m, nil
		}
		// 滚轮: 转给 viewport,顺便取消选区
		if m.selecting {
			m.selecting = false
		}
		var c tea.Cmd
		m.chatViewport, c = m.chatViewport.Update(msg)
		return m, c

	case tea.MouseClickMsg:
		if m.showSetup || m.showLangModal {
			return m, nil
		}
		if msg.Button != tea.MouseLeft {
			return m, nil
		}
		leftW, vpH := m.layout()
		// chat 区:X 从 0 起,Y 从 0 起(无顶栏);chatRight 由 layout() 算的 leftW 决定。
		chatLeft, chatTop := 0, 0
		chatRight := chatLeft + leftW
		chatBottom := chatTop + vpH
		inChat := msg.X >= chatLeft && msg.X < chatRight &&
			msg.Y >= chatTop && msg.Y < chatBottom
		// 输入区:body 下方整块(空白行 + textarea 行),Y ∈ [vpH, m.height)。
		inInput := msg.Y >= vpH && msg.Y < m.height && msg.X >= 0 && msg.X < m.width

		if inInput {
			// 双击切换全选;单击进入输入区:清 chat 选区 + 起一次"拖拽全选"。
			now := time.Now()
			if !m.lastInputClickAt.IsZero() && now.Sub(m.lastInputClickAt) < 400*time.Millisecond {
				if m.input.Value() != "" {
					m.inputAllSelected = !m.inputAllSelected
				}
				m.lastInputClickAt = time.Time{} // 清零,避免三击当成第二次双击
			} else {
				m.lastInputClickAt = now
				m.inputDragging = true // 后续 MouseMotion 一动就全选
				if m.selecting {
					m.selecting = false
				}
			}
			m.refreshViewport()
			return m, nil
		}

		if inChat {
			col := msg.X - chatLeft
			line := m.chatViewport.YOffset() + (msg.Y - chatTop)
			m.selAnchor = cellPos{col: col, line: line}
			m.selEnd = m.selAnchor
			m.selecting = true
			m.refreshViewport()
		} else if m.selecting {
			m.selecting = false
			m.refreshViewport()
		}
		return m, nil

	case tea.MouseMotionMsg:
		if m.showSetup || m.showLangModal {
			return m, nil
		}
		if msg.Button != tea.MouseLeft {
			return m, nil
		}
		leftW, vpH := m.layout()
		// chat 区:X 从 0 起,Y 从 0 起(无顶栏);chatRight 由 layout() 算的 leftW 决定。
		chatLeft, chatTop := 0, 0
		chatRight := chatLeft + leftW
		chatBottom := chatTop + vpH

		// 输入框拖拽:一旦移动就全选高亮(输入框内容通常一行/几行,直接整段选)。
		if m.inputDragging {
			if m.input.Value() != "" && !m.inputAllSelected {
				m.inputAllSelected = true
				m.refreshViewport()
			}
			return m, nil
		}

		if m.selecting {
			scrolled := false
			if msg.Y < chatTop && !m.chatViewport.AtTop() {
				m.chatViewport.ScrollUp(1)
				scrolled = true
			} else if msg.Y >= chatBottom && !m.chatViewport.AtBottom() {
				m.chatViewport.ScrollDown(1)
				scrolled = true
			}
			cx := msg.X
			if cx < chatLeft {
				cx = chatLeft
			}
			if cx >= chatRight {
				cx = chatRight - 1
			}
			cy := msg.Y
			if cy < chatTop {
				cy = chatTop
			}
			if cy >= chatBottom {
				cy = chatBottom - 1
			}
			col := cx - chatLeft
			line := m.chatViewport.YOffset() + (cy - chatTop)
			if scrolled || m.selEnd.col != col || m.selEnd.line != line {
				m.selEnd = cellPos{col: col, line: line}
				m.refreshViewport()
			}
		}
		return m, nil

	case tea.MouseReleaseMsg:
		if m.showSetup || m.showLangModal {
			return m, nil
		}
		if msg.Button != tea.MouseLeft {
			return m, nil
		}
		// 输入框拖拽全选松手:复制整段输入框内容(不弹"已复制"提示)。
		if m.inputDragging {
			m.inputDragging = false
			if m.inputAllSelected {
				if text := m.input.Value(); text != "" {
					_ = writeClipboardText(text)
					return m, tea.SetClipboard(text)
				}
			}
			return m, nil
		}
		if m.selecting {
			// 双路写剪贴板(对齐 crush):pbcopy 本地必中,OSC52 兼容更多终端 + 跨 SSH。
			// 不清 selecting:保留高亮,直到用户点别处 / 滚轮 / 改尺寸 / 开始新选择。
			if cmd := m.copySelection(); cmd != nil {
				// 记下松开位置,"✓ 已复制"提示就叠在这里。
				m.copyHintX = msg.X
				m.copyHintY = msg.Y
				return m, cmd
			}
		}
		return m, nil

	case tea.PasteMsg:
		// 配置 modal 期间,转发给 setupInput(允许粘贴 API key)
		if m.showSetup {
			var c tea.Cmd
			m.setupInput, c = m.setupInput.Update(msg)
			return m, c
		}
		// mcp-add modal 期间,转发给 mcpAddInput(允许粘贴命令)
		if m.showMcpAdd {
			var c tea.Cmd
			m.mcpAddInput, c = m.mcpAddInput.Update(msg)
			return m, c
		}
		// 空 paste 内容 = 终端有 paste 事件但 PTY 里没文本 → 大概率剪贴板只有图片,主动读 PNG。
		// 非空 paste 内容 = 普通文本粘贴,放掉让 textinput 自己接(它有 PasteMsg 处理)。
		if msg.Content == "" {
			if m.inputAllSelected {
				m.input.SetValue("")
				m.attachedImagePaths = nil
				m.inputAllSelected = false
			}
			if data, err := readClipboardImage(); err == nil {
				path, ferr := saveAttachedImage(data, len(m.attachedImagePaths)+1)
				if ferr != nil {
					m.appendChat("System", "保存粘贴图片失败: "+ferr.Error())
					return m, nil
				}
				m.attachedImagePaths = append(m.attachedImagePaths, path)
				m.insertImagePlaceholder(len(m.attachedImagePaths))
			}
			return m, nil
		}
		// 文本粘贴:让 textinput 处理
		var c tea.Cmd
		m.input, c = m.input.Update(msg)
		return m, c

	case tea.KeyPressMsg:
		// 配置 modal 处于活动状态时,按键全部路由到 setupInput,绕过主界面所有处理。
		if m.showSetup {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "enter":
				cmd := m.submitSetup()
				return m, cmd
			case "esc":
				// 首次启动 modal 不允许 Esc 关闭(还没有配置可用);
				// 否则正常关闭返回主界面。
				if m.setupRequired {
					m.setupErr = T("setup.error.required")
					return m, nil
				}
				m.showSetup = false
				m.setupErr = ""
				m.setupInput.Reset()
				m.setupInput.Blur()
				m.input.Focus()
				return m, nil
			}
			var c tea.Cmd
			m.setupInput, c = m.setupInput.Update(msg)
			return m, c
		}

		// /lang modal:↑/↓ 切换中英,Enter 确认,Esc 取消
		if m.showLangModal {
			switch msg.String() {
			case "up", "k":
				if m.langModalIdx > 0 {
					m.langModalIdx--
				}
				return m, nil
			case "down", "j":
				if m.langModalIdx < 1 {
					m.langModalIdx++
				}
				return m, nil
			case "enter":
				langs := []Lang{LangZH, LangEN}
				picked := langs[m.langModalIdx]
				SetLang(picked)
				m.showLangModal = false
				// 切换后右栏 / palette 的 desc 都需要刷新一遍
				m.refreshViewport()
				// 同步语言给 web 端
				m.broadcast(web.Event{Kind: "lang", Text: string(picked)})
				name := "中文"
				if picked == LangEN {
					name = "English"
				}
				m.appendChat("System", fmt.Sprintf(T("lang.switched"), name))
				return m, nil
			case "esc", "ctrl+c":
				m.showLangModal = false
				return m, nil
			}
			return m, nil
		}

		// /mcp-add modal:单行输入,Enter 保存连接,Esc 取消
		if m.showMcpAdd {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "enter":
				m.submitMcpAdd()
				return m, nil
			case "esc":
				m.showMcpAdd = false
				m.mcpAddErr = ""
				m.mcpAddInput.Blur()
				m.input.Focus()
				return m, nil
			}
			var c tea.Cmd
			m.mcpAddInput, c = m.mcpAddInput.Update(msg)
			return m, c
		}

		// /mcp-delete modal:↑/↓ 选,Enter 删,Esc 取消
		if m.showMcpDelete {
			switch msg.String() {
			case "up", "k":
				if m.mcpDelIdx > 0 {
					m.mcpDelIdx--
				}
				return m, nil
			case "down", "j":
				if m.mcpDelIdx < len(m.mcpDelNames)-1 {
					m.mcpDelIdx++
				}
				return m, nil
			case "enter":
				m.submitMcpDelete()
				return m, nil
			case "esc", "ctrl+c":
				m.showMcpDelete = false
				m.input.Focus()
				return m, nil
			}
			return m, nil
		}

		// review 审核态:↑/↓ 切换 YES/NO, Enter 确认, Esc 拒绝
		if m.reviewPending {
			switch msg.String() {
			case "up", "down":
				m.reviewYesNo = !m.reviewYesNo
				m.refreshViewport()
				return m, nil
			case "enter":
				m.reviewCh <- m.reviewYesNo
				m.reviewPending = false
				m.broadcast(web.Event{Kind: "review_resolved"})
				m.refreshViewport()
				return m, func() tea.Msg {
					return reviewResultMsg{}
				}
			case "esc", "ctrl+c":
				m.reviewCh <- false
				m.reviewPending = false
				m.broadcast(web.Event{Kind: "review_resolved"})
				m.refreshViewport()
				return m, func() tea.Msg {
					return reviewResultMsg{}
				}
			}
			return m, nil
		}

		// 命令 palette 导航键拦截。palette 由 input value 的 "/" 前缀触发,
		// 这里只在 palette 打开时消费 ↑/↓/Tab,其他键继续往下走交给 textinput。
		if matches := filterSlashCommands(m.input.Value()); len(matches) > 0 {
			// clamp idx 避免 value 缩短后越界
			if m.commandPaletteIdx >= len(matches) {
				m.commandPaletteIdx = len(matches) - 1
			}
			if m.commandPaletteIdx < 0 {
				m.commandPaletteIdx = 0
			}
			switch msg.String() {
			case "up":
				if m.commandPaletteIdx > 0 {
					m.commandPaletteIdx--
				}
				return m, nil
			case "down":
				if m.commandPaletteIdx < len(matches)-1 {
					m.commandPaletteIdx++
				}
				return m, nil
			case "tab":
				// 用选中命令替换 input value;光标移到末尾,palette 仍会因 value 完全等于命令
				// 而匹配它自己一条(只剩 1 项),用户可以再按 Enter 执行,或者继续编辑加参数。
				// textarea 没有 SetCursor 线性接口,SetValue 后用 CursorEnd() 跳到末尾。
				m.input.SetValue(matches[m.commandPaletteIdx].name)
				m.input.CursorEnd()
				m.commandPaletteIdx = 0
				return m, nil
			case "enter":
				// palette 打开时 Enter = 直接执行当前选中命令。
				// 不走原 enter 分支的 input value(可能只是 "/sk" 这种半成品 → handleSlashCommand
				// 会报"未知命令")。先把 value 清掉,再执行完整命令。
				chosen := matches[m.commandPaletteIdx].name
				m.input.SetValue("")
				m.commandPaletteIdx = 0
				return m, m.handleSlashCommand(chosen)
			}
		} else {
			// palette 没在显示,idx 复位避免下次打开时停在过去位置
			m.commandPaletteIdx = 0
		}

		// Ctrl+A 全选态预处理:按下 Ctrl+A 后,任何其他按键都要先消费"全选"语义。
		// 放在外层 switch 之前,确保所有后续 case 都看不到带 selected 的状态。
		if m.inputAllSelected {
			switch msg.String() {
			case "ctrl+shift+a", "super+shift+a":
				return m, nil // 已全选,继续全选
			case "esc":
				m.inputAllSelected = false
				return m, nil
			case "ctrl+c":
				// 保留原"非流式时退出"语义,不被 selected 改变
			case "left", "right", "home", "end", "up", "down",
				"pgup", "pgdown", "pageup", "pagedown":
				// 方向 / 翻页 → 仅取消选择,光标移动交给后续 textinput.Update
				m.inputAllSelected = false
			case "backspace", "delete", "ctrl+w", "ctrl+u":
				m.input.SetValue("")
				m.attachedImagePaths = nil
				m.inputAllSelected = false
				return m, nil
			case "enter":
				// 全选状态按 Enter:按原 enter 流程走(发送当前 value),同时清除选择标记
				m.inputAllSelected = false
				// 不 return,继续走外层 enter case
			default:
				// 可打印字符或其他键:先清空 value,再让 textinput 处理这次按键(就把新字符填入空 value)
				m.input.SetValue("")
				m.attachedImagePaths = nil
				m.inputAllSelected = false
			}
		}
		switch msg.String() {
		// 输入框全选。两个 key 都注册:
		//   - "ctrl+shift+a"  — 跨平台通用,跟 Claude Code 一致 (issue #52912)
		//   - "super+shift+a" — macOS Cmd+Shift+A,Kitty Keyboard Protocol 透传 (iTerm2 /
		//     kitty / WezTerm / Ghostty 默认未占用此组合,自然送达;Terminal.app 不支持
		//     Kitty Protocol,那边用户用 Ctrl+Shift+A 即可)
		//
		// 不用 Cmd+A / Ctrl+A 是因为前者被 macOS 终端 GUI 拦截做"全选窗口文本"、
		// 字节不进 PTY;后者是 readline LineStart 的默认绑定 — Anthropic 的 Claude Code
		// (issue #14789 not_planned) 也确认 Cmd+A 物理不可达,所有 TUI 都用 Ctrl 组合键。
		case "ctrl+shift+a", "super+shift+a":
			if m.input.Value() != "" {
				m.inputAllSelected = true
			}
			return m, nil
		case "ctrl+c":
			// Ctrl+C 退出程序。若正在流式,先取消后台任务。
			if m.streaming {
				if m.cancelAgent != nil {
					m.cancelAgent()
					m.cancelAgent = nil
				}
				if m.streamCh != nil {
					drainAndDiscard(m.streamCh)
				}
			}
			return m, tea.Quit
		case "esc":
			// Esc 中断当前对话。取消 context 真正终止后台 HTTP 请求和工具调用,
			// 然后 drain channel 防止 goroutine 阻塞。
			if m.streaming && m.streamCh != nil {
				if m.cancelAgent != nil {
					m.cancelAgent()
					m.cancelAgent = nil
				}
				drainAndDiscard(m.streamCh)
				m.streamCh = nil
				m.streaming = false
				m.thinking = false
				m.status = "idle"
				m.chatContent.Append(T("misc.interrupted"))
				m.refreshViewport()
				return m, nil
			}
			return m, nil
		case "up", "down", "pgup", "pgdown", "pageup", "pagedown", "home", "end", "ctrl+u", "ctrl+d":
			// chat 区滚动键全部拦截,textarea 不再消费方向键 — 用户明确要求 ↑/↓
			// 滚 chat,代价是多行 input 不能用方向键移光标(仍可 Left/Right + Backspace 编辑)。
			var c tea.Cmd
			m.chatViewport, c = m.chatViewport.Update(msg)
			return m, c
		case "alt+enter", "alt+\r", "ctrl+enter", "ctrl+\r", "shift+enter":
			// 在光标处插入换行,实现多行输入。Enter 仍走下方 submit 分支。
			// 同时接 Alt+Enter / Ctrl+Enter / Shift+Enter — 不同终端 / OS 上的"换行"组合键各异,
			// macOS Terminal.app 多用 Alt+Enter,iTerm2 / VSCode / Linux 用户更习惯 Ctrl+Enter / Shift+Enter。
			if m.streaming {
				return m, nil
			}
			m.input.InsertRune('\n')
			return m, nil
		case "ctrl+v":
			// 先看剪贴板有没有图,有就落盘 + 把 [Image #N] 占位符插到输入框光标处。
			// 没图则继续下落到 textinput,走文本粘贴。
			if data, err := readClipboardImage(); err == nil {
				path, err := saveAttachedImage(data, len(m.attachedImagePaths)+1)
				if err != nil {
					m.appendChat("System", "保存粘贴图片失败: "+err.Error())
					return m, nil
				}
				m.attachedImagePaths = append(m.attachedImagePaths, path)
				m.insertImagePlaceholder(len(m.attachedImagePaths))
				return m, nil
			}
		case "enter":
			if m.streaming {
				return m, nil
			}
			input := strings.TrimSpace(m.input.Value())
			if input == "" && len(m.attachedImagePaths) == 0 {
				return m, nil
			}
			m.input.SetValue("")
			var cmd tea.Cmd
			m, cmd = m.submitUserInput(input)
			return m, cmd
		}

	case upgradeCheckResult:
		// 后台升级检查回执:有错就静默忽略,有结果就跟当前版本比一下。
		// 有新版本时:右栏 banner 下方版本行常驻显示 ↑ 提示;chat 区追加一条 System 消息
		// 给一次明显提醒(每次启动一次,不打扰)。
		if msg.Err == nil && msg.LatestVersion != "" {
			m.latestVersion = msg.LatestVersion
			m.upgradeURL = msg.URL
			m.upgradeAvailable = versionNewer(msg.LatestVersion, m.version)
			if m.upgradeAvailable {
				cur := m.version
				if cur == "" {
					cur = "dev"
				}
				m.appendChat("System", fmt.Sprintf(T("upgrade.available"), cur, msg.LatestVersion, UpgradeHint))
			}
		}
		return m, nil

	case agent.ReasoningTokenMsg:
		// streamCh 已 nil 说明 ESC/Ctrl+C 中断过了,丢弃残留消息
		if m.streamCh == nil {
			return m, nil
		}
		// 模型在思考。文字不进 chat,只确保 spinner 在转。
		m.status = "thinking"
		if !m.thinking {
			m.thinking = true
			cmds = append(cmds, m.spinner.Tick)
		}
		return m, tea.Batch(append(cmds, agent.ListenToStream(m.streamCh))...)

	case agent.TokenMsg:
		if m.streamCh == nil {
			return m, nil
		}
		// 助手正式回复开始,停止 spinner,把文本写进 chat
		m.status = "streaming"
		m.thinking = false
		m.activeTool = ""
		text := string(msg)
		m.currentReply.WriteString(text)
		// 上一段若是 tools(刚执行完工具,模型继续说话),切回 assistant 段。
		m.chatContent.EnsureKind(kindAssistant, "")
		m.chatContent.Append(text)
		m.tokens += len([]rune(text))
		m.turnOutputChars += len([]rune(text))
		m.refreshViewport()
		return m, agent.ListenToStream(m.streamCh)

	case agent.ToolCallStartMsg:
		if m.streamCh == nil {
			return m, nil
		}
		m.status = "tool"
		// Todo 是可见清单的「全量快照更新」,不是一次干活动作:清单本身由下方 live overlay
		// 实时勾选,每次更新再往 chat 打一行 "📌 Todo" 是纯噪音。所以 Todo 不留 chat 行、
		// 也不计入"N 次工具调用"。其余工具照旧:紧凑单行 <icon> Name (主参数)。
		if msg.Name != "Todo" {
			m.turnToolCalls++
			m.activeTool = msg.Name
			line := formatToolCallLine(msg.Name, msg.Args)
			// 切到 tools 段。tools 段不走 markdown 渲染(refreshViewport 里特判),
			// 所以单 \n 直接换行,无需 hard break trick。连续 tool_call 同段归并 → 一组色条。
			if m.chatContent.LastKind() == kindTools {
				if !m.chatContent.EndsWithNewline() {
					m.chatContent.Append("\n")
				}
				m.chatContent.Append(line + "\n")
			} else {
				m.chatContent.Open(kindTools, line+"\n")
			}
		}
		m.refreshViewport()
		// review 模式:暂停流,等待用户确认。web 端也弹确认层。
		if msg.ReviewCh != nil {
			m.reviewPending = true
			m.reviewCh = msg.ReviewCh
			m.reviewToolName = msg.Name
			m.reviewToolArgs = msg.Args
			m.reviewYesNo = true // 默认 YES
			m.broadcast(web.Event{Kind: "review_request", Name: msg.Name, Args: msg.Args})
			return m, nil
		}
		// 工具执行期间继续转 spinner,等结果回来后才可能切到 content
		if !m.thinking {
			m.thinking = true
			cmds = append(cmds, m.spinner.Tick)
		}
		return m, tea.Batch(append(cmds, agent.ListenToStream(m.streamCh))...)

	case agent.ToolCallResultMsg:
		if m.streamCh == nil {
			return m, nil
		}
		// 结果不再原样打印长输出。失败时短提示一行;成功默默吞掉(LLM 会用结果接着干)。
		// 这样多轮工具调用看到的就是清爽的工具列表,不被几百行输出淹没。
		if !msg.Success {
			out := msg.Output
			if len(out) > 200 {
				out = out[:200] + "…"
			}
			m.chatContent.Append("  ✗ " + msg.Name + " 失败: " + out + "\n")
		}
		m.currentReply.Reset()
		m.refreshViewport()
		// 工具结果后,LLM 还要继续思考(或最终回复),保持 spinner 状态
		if !m.thinking {
			m.thinking = true
			cmds = append(cmds, m.spinner.Tick)
		}
		return m, tea.Batch(append(cmds, agent.ListenToStream(m.streamCh))...)

	case spinner.TickMsg:
		// spinner 自驱动:每次 tick 来都更新 frame,只要还在 thinking 就继续 Tick
		var c tea.Cmd
		m.spinner, c = m.spinner.Update(msg)
		if m.thinking {
			cmds = append(cmds, c)
		}
		// thinking 状态下需要重渲染让 spinner 帧切换可见
		m.refreshViewport()
		return m, tea.Batch(cmds...)

	case cursorBlinkTickMsg:
		// app 侧 cursor blink:每 600ms 切一次可见,View 那边读 cursorBlinkOff
		// 决定要不要把光标塞进 tea.View.Cursor。继续返回下一拍 tick,自驱动。
		m.cursorBlinkOff = !m.cursorBlinkOff
		return m, cursorBlinkTick()

	case copyHintClearMsg:
		// "已复制"提示到点清空(View 下一帧就不显示了)。
		m.copyHint = ""
		return m, nil

	case agent.HistoryUpdateMsg:
		if m.streamCh == nil {
			return m, nil
		}
		// system prompt 不入存储 history(每轮由 BuildSystemPrompt 现建,含最新摘要)。
		h := msg.History
		if len(h) > 0 && h[0].Role == "system" {
			h = h[1:]
		}
		m.history = h
		// 持久化完整 history(含 tool_calls / tool results)到 binary gob 文件,
		// 重启时可直接反序列化恢复,无需重建。
		if m.session != nil {
			_ = m.session.SaveGob("history.gob", m.history)
		}
		return m, agent.ListenToStream(m.streamCh)

	case agent.PrefixSnapshotMsg:
		// 持久化本轮实际发送的前缀 + 当前签名(供重启检测与缓存友好压缩)。
		m.onPrefixSnapshot(msg)
		if m.streamCh != nil {
			return m, agent.ListenToStream(m.streamCh)
		}
		return m, nil

	case agent.ModelSwitchMsg:
		if m.streamCh == nil {
			return m, nil
		}
		m.activeModelRole = msg.Role
		m.activeModelID = msg.ModelID
		if msg.Reason != "" {
			// 升级类的切换在聊天流里留一行可见痕迹,便于用户察觉为什么变贵了
			m.chatContent.Append(fmt.Sprintf("\n[已升级到 %s 模型] 原因: %s\n", msg.Role, msg.Reason))
			m.refreshViewport()
			// B: pro 保持 — 升 pro 后接下来 3 轮默认保持,避免每轮重新关键词路由掉回 flash
			if msg.Role == "pro" {
				m.proKeepRounds = 3
			}
		}
		return m, agent.ListenToStream(m.streamCh)

	case agent.PlanCreatedMsg:
		if m.streamCh == nil {
			return m, nil
		}
		m.plan = &planState{items: msg.Plans}
		// 不写 chatContent — plan 通过 refreshViewport 的 live overlay 实时渲染,
		// 每次 TaskStatusMsg 自然刷新出新 checkbox。
		// 流结束时(StreamDoneMsg)再固化一次进 chatContent 留作历史。
		m.refreshViewport()
		return m, agent.ListenToStream(m.streamCh)

	case agent.TaskStatusMsg:
		if m.streamCh == nil {
			return m, nil
		}
		// 只有 plan 已就位才有意义;否则可能是模型乱调,丢弃
		if m.plan != nil {
			m.plan.apply(msg)
			m.refreshViewport() // 触发 live overlay 重渲染,checkbox 立刻更新
		}
		return m, agent.ListenToStream(m.streamCh)

	case agent.UsageMsg:
		if m.streamCh == nil {
			return m, nil
		}
		// 主 agent 单次 API 用量,仅记录最新一轮(子 agent 的调用不发送 UsageMsg)。
		m.lastUsage = &msg.Usage
		// 持久化到 state.json,重启后立刻就有数据,Usage section 不再显示 "—"。
		if m.session != nil {
			m.session.SaveUsage(
				msg.Usage.PromptTokens,
				msg.Usage.CompletionTokens,
				msg.Usage.PromptCacheHitTokens,
				msg.Usage.PromptCacheMissTokens,
			)
		}
		return m, agent.ListenToStream(m.streamCh)

	case agent.StreamDoneMsg:
		if m.streamCh == nil {
			return m, nil
		}
		// 流结束时不再把 plan 固化进 chatContent —— plan 跑完后用户更关心模型的后续
		// 总结输出,checkbox 列表留着只会和后续 token 混在一起视觉嘈杂。右栏 "X/Y done"
		// 摘要仍在,要看完整步骤就翻 session 历史。
		// 持久化助手最终回复。currentReply 在流式过程中累加,这里一次性落盘。
		// 注意只存"主对话内容" —— tool_call / tool_result / reasoning 都不进 session 文件。
		if m.session != nil {
			// 本轮成功,才把用户消息写入 jsonl(连同 assistant 配对);total_turns 也随之 +1。
			if m.pendingUserText != "" {
				_ = m.session.Append("user", m.pendingUserText)
				m.pendingUserText = ""
			}
			final := m.currentReply.String()
			if strings.TrimSpace(final) != "" {
				_ = m.session.Append("assistant", final)
			}
		}
		m.status = "idle"
		m.streaming = false
		m.thinking = false
		m.activeTool = ""
		m.streamCh = nil
		m.cancelAgent = nil
		m.turnElapsed = time.Since(m.turnStartedAt)
		// 完成态(用时 + 工具次数)由输入框上方的活动状态行展示(statusFooterLine 空闲分支),
		// 不再单独往 chat 里打一行,避免和底部"就绪/完成"指示重复。
		m.refreshViewport()

		// 显示区按字节预算自动裁剪 (chatLog.Append/Open 内部已调 trim),
		// 这里无需额外动作 — 旧的 trimDisplayTurns 按"10 轮"裁的逻辑已被 chatLog 取代。

		// 检查是否需要触发会话压缩：估算 token 数接近窗口的 70% 时触发。
		ctxWin := m.models.Pro.ContextWindow
		if ctxWin <= 0 {
			ctxWin = 65536
		}
		if m.session != nil && m.models.Pro.Model != "" && !m.compacting && m.lastPromptTokens() >= ctxWin*70/100 {
			// 拷贝当前 history 快照,异步执行压缩
			snapshot := make([]agent.ChatMessage, len(m.history))
			copy(snapshot, m.history)
			// 上次实际发送的 model + system + tools(刚结束的这轮已写入快照),复刻它命中热缓存。
			_, lastModel, lastSys, lastTools := m.session.LoadPrefixSnapshot()
			entry := m.entryForModel(lastModel)
			m.compacting = true
			return m, func() tea.Msg {
				summary, cutIdx, compressedTurns, err := agent.RunCompression(lastSys, lastTools, snapshot, entry, ctxWin)
				return compressionResultMsg{
					summary:         summary,
					cutIdx:          cutIdx,
					compressedTurns: compressedTurns,
					err:             err,
				}
			}
		}

		return m, nil

	case agent.StreamErrMsg:
		if m.streamCh == nil {
			return m, nil
		}
		m.chatContent.Append("\n[Error: " + msg.Err.Error() + "]\n\n")
		m.status = "error"
		m.streaming = false
		m.thinking = false
		m.activeTool = ""
		m.streamCh = nil
		m.cancelAgent = nil
		m.turnElapsed = time.Since(m.turnStartedAt)
		m.refreshViewport()
		return m, nil

	case reviewResultMsg:
		// 审核完成,恢复流监听继续工具循环
		return m, agent.ListenToStream(m.streamCh)

	case compressionResultMsg:
		m.compacting = false
		if msg.err != nil {
			// 后台自动触发(70%/重启/空闲)失败静默 —— 不往聊天区打扰用户,下次触发会重试。
			// 但 /compact 是用户主动发起的,压不动(历史太小/轮数不足)要明确告知,否则像没反应。
			if msg.manual {
				m.chatContent.Open(kindSystem, "**压缩跳过**:"+msg.err.Error())
				m.refreshViewport()
			}
			return m, nil
		}
		// 兜底:cutIdx 基于触发时的快照算;若期间 history 已被(另一次压缩/新消息)改动到
		// 比 cutIdx 短,直接丢弃这个过期结果,避免切片越界 panic。
		if msg.cutIdx < 0 || msg.cutIdx > len(m.history) {
			return m, nil
		}
		// 摘要进 m.summary(每轮注入 system prompt 尾部),不再作为 history 消息;
		// history 只截断保留尾部。
		m.summary = msg.summary
		m.history = append([]agent.ChatMessage(nil), m.history[msg.cutIdx:]...)

		_ = m.session.SaveSummary(msg.summary)
		// 压缩后 history 已截断,写回 gob 保持下轮启动一致性
		if m.session != nil {
			_ = m.session.SaveGob("history.gob", m.history)
		}

		// 状态栏 prompt 直接读 m.lastUsage.PromptTokens(上轮真实 API 返回值),压缩截断 history
		// 后它仍是压缩前的大数,直到下一轮请求才更新 —— 视觉上像"压了但没动"。这里压缩成功后
		// 立即用本地估算重算下一次 prompt 的量级写回,状态栏即时反映缩小后的上下文。
		// 前缀已变,下一轮缓存命中未知,旧 hit 数会让 cache% 失真(甚至 >100%),一并清零。
		if m.lastUsage != nil {
			m.lastUsage.PromptTokens = m.estimatePromptTokens()
			m.lastUsage.PromptCacheHitTokens = 0
		}

		m.chatContent.Open(kindSystem, fmt.Sprintf("**已压缩会话历史（%d 轮→摘要）**", msg.compressedTurns))
		m.refreshViewport()
		return m, nil
	}

	var inputCmd tea.Cmd
	m.input, inputCmd = m.input.Update(msg)
	cmds = append(cmds, inputCmd)

	// 用户敲键时强制 cursor 立刻亮,跟旧虚拟光标"打字 = 光标snappy显形"的手感一致。
	// 下一拍 600ms tick 仍按既有节奏 toggle,不 reset 时钟。
	if _, ok := msg.(tea.KeyPressMsg); ok {
		m.cursorBlinkOff = false
	}

	return m, tea.Batch(cmds...)
}

// buildUserMessage 把输入文本中的 [Image #N] 占位符替换成已落盘的图片绝对路径,
// 让不支持多模态输入的 LLM (例如 DeepSeek) 也能拿到图片信息——
// 模型可通过 img_ocr 工具按路径识别图片内容。
//
// 设计:图片在 Ctrl+V 时就落盘到 ~/.deepx/ocr/cache/<ts>-<N>.png,
// 这里只做"占位符 → 路径"的文本替换,不再产出多模态 ContentParts。
// 用户手动删掉的占位符对应的图片不会出现在最终消息里。
func (m model) buildUserMessage(text string) agent.ChatMessage {
	if len(m.attachedImagePaths) == 0 {
		return agent.ChatMessage{Role: "user", Content: text}
	}
	replaced := imagePlaceholderRe.ReplaceAllStringFunc(text, func(match string) string {
		// match 形如 "[Image #1]";提取数字索引
		sub := imagePlaceholderRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		idx, _ := strconv.Atoi(sub[1])
		if idx < 1 || idx > len(m.attachedImagePaths) {
			return match
		}
		return m.attachedImagePaths[idx-1]
	})
	return agent.ChatMessage{Role: "user", Content: replaced}
}

// saveAttachedImage 把刚粘贴的 PNG 字节写到 ~/.deepx/ocr/cache/ 下的新文件,
// 返回该文件的绝对路径。失败时返回空串 + 错误,调用方决定怎么提示用户。
func saveAttachedImage(data []byte, index int) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	cacheDir := filepath.Join(home, ".deepx", "ocr", "cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", err
	}
	// 时间戳精确到纳秒,避免连续粘贴的文件名冲突
	name := fmt.Sprintf("%s-%d.png", time.Now().Format("20060102-150405.000000000"), index)
	path := filepath.Join(cacheDir, name)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", err
	}
	return path, nil
}

// insertImagePlaceholder 在输入框当前光标位置插入 [Image #n]。
// textarea 自带 InsertString,前后补一个空格直接交给它处理光标——简化版,
// 不再判断光标两侧字符。多一两个空格无碍 LLM 解析。
func (m *model) insertImagePlaceholder(n int) {
	m.input.InsertString(fmt.Sprintf(" [Image #%d] ", n))
}

// roleKind 把内部 role 名映射成 chatLog 段 kind。
// 决定渲染时套哪根色条 — 不再在 raw 里加任何 "**emoji 名**: " 前缀,
// 身份完全由色条承载,正文跟用户输入保持原貌。
func roleKind(role string) string {
	switch strings.ToLower(role) {
	case "user", "you":
		return kindUser
	case "deepx", "assistant":
		return kindAssistant
	case "tools", "tool":
		return kindTools
	}
	return kindSystem
}

func (m *model) appendChat(role, text string) {
	m.chatContent.Open(roleKind(role), text)
	m.refreshViewport()
}

// rebuildChatFromHistory 把完整 []ChatMessage 按"显示块"粒度写入 chatLog。
//
// **为什么不返回一个大字符串再 Open 一次**:chatLog.trim 是按 segment 粒度丢最旧段的,
// 且 `len(segments) > 1` 才会触发裁剪。如果整个历史塞进单一 segment,首次新消息一旦
// 新开 segment 就把整段历史(可能数百 KB)作为最旧段一次性丢掉 —— 用户体验就是
// "重启看得到历史,发一条新消息历史全没了"。
// 改成每条消息(及 assistant 的每个 tool_call、每条 tool result)一次 Open 后,
// trim 能从最旧消息逐条丢,屏幕上保留尽可能多的近期上下文。
//
// 显示规则:
//   - user: 非空 content 显示为用户消息
//   - assistant: 非空 content 显示为助手回复;有 ToolCalls 时每个调用各自渲染成一段
//   - tool: **跳过** —— 跟直播流对齐。运行时 ToolCallResultMsg 成功时静默
//     (model.go:820 附近),仅失败写 `  ✗ ...`。但 history 里的 tool message 不带
//     成功/失败标记,无法在恢复时区分,索性全部不渲染。结果是否成功用户能从
//     紧随其后的 assistant 回复推断 —— 不影响理解,且跟运行时观感一致。
//   - system: 跳过
//
// 不写 m.history、不写 session.gob —— 仅是显示通道,改这里不影响 LLM 缓存。
func rebuildChatFromHistory(cl *chatLog, history []agent.ChatMessage) {
	for _, msg := range history {
		switch msg.Role {
		case "user":
			if msg.Content != "" {
				cl.Open(kindUser, msg.Content)
			}
		case "assistant":
			if msg.Content != "" {
				cl.Open(kindAssistant, msg.Content)
			}
			// 工具调用行:同 ToolCallStartMsg,连续 tool_call 归并到同一段(同 kind)。
			// tools 段跳过 markdown 渲染,单 \n 即可保证每条单独一行。
			for _, tc := range msg.ToolCalls {
				if tc.Function.Name == "Todo" {
					continue // Todo 不留 chat 行(同运行时),清单由 live overlay 渲染
				}
				line := formatToolCallLine(tc.Function.Name, tc.Function.Arguments)
				if cl.LastKind() == kindTools {
					if !cl.EndsWithNewline() {
						cl.Append("\n")
					}
					cl.Append(line + "\n")
				} else {
					cl.Open(kindTools, line+"\n")
				}
			}
		}
	}
}

func modeNotification(mode agent.AgentMode, modelRole string) string {
	modelPart := ""
	if modelRole != "" {
		modelPart = T("mode.model_suffix") + modelRole
	}
	var name string
	switch mode {
	case agent.AgentMode_Plan:
		name = T("mode.plan")
	case agent.AgentMode_Review:
		name = T("mode.review")
	default:
		name = T("mode.auto")
	}
	return T("mode.current_prefix") + name + modelPart
}

// handleSlashCommand 处理本地斜杠命令。多数命令只改本地状态、返回 nil;
// 异步命令(如 /compact)返回 tea.Cmd 交给 bubbletea 调度。
func (m *model) handleSlashCommand(input string) tea.Cmd {
	cmd := strings.ToLower(strings.TrimSpace(input))
	switch cmd {
	case "/plan":
		m.mode = agent.AgentMode_Plan
		msg := modeNotification(agent.AgentMode_Plan, m.activeModelRole)
		m.history = append(m.history, agent.ChatMessage{Role: "assistant", Content: msg})
		m.appendChat("assistant", msg)
		if m.session != nil {
			_ = m.session.Append("assistant", msg)
		}
	case "/auto":
		m.mode = agent.AgentMode_Auto
		msg := modeNotification(agent.AgentMode_Auto, m.activeModelRole)
		m.history = append(m.history, agent.ChatMessage{Role: "assistant", Content: msg})
		m.appendChat("assistant", msg)
		if m.session != nil {
			_ = m.session.Append("assistant", msg)
		}
	case "/review":
		m.mode = agent.AgentMode_Review
		msg := modeNotification(agent.AgentMode_Review, m.activeModelRole)
		m.history = append(m.history, agent.ChatMessage{Role: "assistant", Content: msg})
		m.appendChat("assistant", msg)
		if m.session != nil {
			_ = m.session.Append("assistant", msg)
		}
	case "/mode":
		m.appendChat("assistant", fmt.Sprintf(T("mode.show"), m.mode))
	case "/config":
		m.openSetupModal()
	case "/skills":
		m.appendChat("assistant", m.skillsListMessage())
	case "/mcp-list":
		m.appendChat("System", m.mcpListMessage())
	case "/mcp-add":
		m.openMcpAddModal()
	case "/mcp-delete":
		m.openMcpDeleteModal()
	case "/lang":
		m.showLangModal = true
		// 默认光标停在当前语言上
		m.langModalIdx = 0
		if CurrentLang() == LangEN {
			m.langModalIdx = 1
		}
	case "/compact":
		return m.startManualCompaction()
	case "/flash":
		m.proKeepRounds = 0
		msg := fmt.Sprintf("已降级到 flash 模型(%s)", m.models.Flash.Model)
		m.history = append(m.history, agent.ChatMessage{Role: "assistant", Content: msg})
		m.appendChat("assistant", msg)
		if m.session != nil {
			_ = m.session.Append("assistant", msg)
		}
	case "/help":
		m.appendChat("assistant", T("help.body"))
	default:
		m.appendChat("assistant", fmt.Sprintf(T("mode.unknown_cmd"), cmd))
	}
	return nil
}

// startManualCompaction 处理 /compact:手动触发会话压缩,仍按 context_window × 20% 保留尾部。
// 与 70% 自动触发(StreamDoneMsg 里)走同一套 runCompression + compressionResultMsg 流程,
// 区别只在于不看 token 阈值——用户敲了就压。压不动(历史太小)由 runCompression 返回 err,
// 经 manual 标记在结果处理处反馈给用户。
func (m *model) startManualCompaction() tea.Cmd {
	if m.session == nil || m.models.Pro.Model == "" {
		m.appendChat("System", "无可用会话或 Pro 模型,无法压缩")
		return nil
	}
	if m.compacting {
		m.appendChat("System", "压缩正在进行中,请稍候")
		return nil
	}
	ctxWin := m.models.Pro.ContextWindow
	if ctxWin <= 0 {
		ctxWin = 65536
	}
	// 拷贝 history 快照,异步执行(同 70% 触发逻辑)。
	snapshot := make([]agent.ChatMessage, len(m.history))
	copy(snapshot, m.history)
	// 复刻上次实际发送的 model + system + tools,命中热缓存。
	_, lastModel, lastSys, lastTools := m.session.LoadPrefixSnapshot()
	entry := m.entryForModel(lastModel)
	m.compacting = true
	m.appendChat("System", "正在压缩会话历史…")
	return func() tea.Msg {
		summary, cutIdx, compressedTurns, err := agent.RunCompression(lastSys, lastTools, snapshot, entry, ctxWin)
		return compressionResultMsg{
			summary:         summary,
			cutIdx:          cutIdx,
			compressedTurns: compressedTurns,
			err:             err,
			manual:          true,
		}
	}
}

// === Skill 辅助 ===

// buildSkillCatalog 把所有可用 skill 拼成给 LLM 看的简短目录。
// 格式:每行 "- <name> (<scope>): <description>"。空 loader / 空目录返回空串。
func buildSkillCatalog(l *skill.Loader) string {
	if l == nil {
		return ""
	}
	metas := l.List()
	if len(metas) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, m := range metas {
		desc := m.Description
		if desc == "" {
			desc = "(无 description)"
		}
		fmt.Fprintf(&sb, "- %s (%s): %s\n", m.Name, m.Scope, desc)
	}
	return sb.String()
}

// skillsListMessage 给 /skills slash 命令的输出。
// 列出当前两个目录里所有 skill,显示 scope / name / description / 路径。
// 没有任何 skill 时给个友好引导。
func (m *model) skillsListMessage() string {
	if m.skillLoader == nil {
		return "skill 系统未启用"
	}
	metas := m.skillLoader.List()
	if len(metas) == 0 {
		var dirsBlock strings.Builder
		for _, d := range m.skillLoader.AllDirs() {
			fmt.Fprintf(&dirsBlock, "  - %s\n", d)
		}
		return "暂无 skill。在以下任一目录创建 <name>/SKILL.md 即可:\n" +
			dirsBlock.String() +
			"\nSKILL.md 格式:\n" +
			"  ---\n" +
			"  name: my-skill\n" +
			"  description: 一句话说明做什么 + 何时用\n" +
			"  ---\n" +
			"  <markdown 正文>"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "可用 skill (%d 个):\n\n", len(metas))
	for _, sm := range metas {
		fmt.Fprintf(&sb, "**%s** _(%s)_\n  %s\n  %s\n\n",
			sm.Name, sm.Scope, sm.Description, sm.Path)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// collectSelectionText 从当前显示的渲染内容里抠出选区文本,用于 mouse-release 时写入剪贴板。
// 走跟 refreshViewport 同源的 renderChatBaseContent — 行号跟用户在屏幕上看到的一一对应。
func (m *model) collectSelectionText() string {
	w := m.chatViewport.Width()
	if w <= 0 {
		return ""
	}
	content := m.renderChatBaseContent(w)
	return extractSelectionText(content, m.selAnchor, m.selEnd, w)
}

// renderMarkdown 用 glamour 渲染 markdown 到 styled ANSI。
//
// 关键预处理:ansi.Strip 剥掉 chatContent 里已有的 ANSI 转义,避免下游再次被当 literal。
// glamour 提供完整 GFM(table / fence / list / heading / link)+ chroma 语法高亮 + 内置主题。
//
// 渲染器按 width 缓存:同样 width 复用同一个 TermRenderer 实例,window resize 时
// 新 width 触发新实例创建(旧实例进 cache,内存占用极小)。glamour 的 wordWrap 在
// renderer 创建时固定,所以 width 必须 = key。
func (m *model) renderMarkdown(content string, width int) string {
	if width <= 0 || content == "" {
		return content
	}
	content = ansi.Strip(content)
	r := m.mdRenderer(width)
	if r == nil {
		// 极端情况(glamour 初始化失败)兜底返回 raw,不让 chat 区空白
		return content
	}
	out, err := r.Render(content)
	if err != nil {
		return content
	}
	// glamour 输出末尾常带 \n,trim 掉避免段间多 1 空行
	return strings.TrimSuffix(out, "\n")
}

// mdRenderer 按 width lazy 获取/创建 glamour renderer。
// 基于 dark 主题但收紧 document margin / block-prefix / block-suffix —— 默认 margin 2
// 会让每段内容左偏 2 列,跟我们外层的色条加起来视觉太松散;BlockPrefix/Suffix 的 "\n"
// 会让段首/段尾各多出 1 空行。这里全压成 0,让 glamour 输出紧贴色条。
//
// WithEmoji 开 :smile: → 😄 之类的 shortcode 转换,LLM 输出常用。
func (m *model) mdRenderer(width int) *glamour.TermRenderer {
	if m.mdRenderers == nil {
		m.mdRenderers = make(map[int]*glamour.TermRenderer)
	}
	if r, ok := m.mdRenderers[width]; ok {
		return r
	}
	style := styles.DarkStyleConfig // value copy
	style.Document.BlockPrefix = ""
	style.Document.BlockSuffix = ""
	zero := uint(0)
	style.Document.Margin = &zero
	// chroma 解析失败的 token 会被当 "error" 渲染,dark 主题默认给 error 加红粉色背景
	// (#F05B5B),整块代码段就成大块红斑。LLM 输出经常是 ASCII art、无 lang 标签的代码块
	// 或者 chroma 没有 lexer 的小众语言,触发率高 — 把背景清掉,只留前景色即可。
	if style.CodeBlock.Chroma != nil {
		style.CodeBlock.Chroma.Error.BackgroundColor = nil
	}

	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(width),
		glamour.WithEmoji(),
	)
	if err != nil {
		return nil
	}
	m.mdRenderers[width] = r
	return r
}

// renderChatBaseContent 渲染 chat 区基础内容(含色条 / 思考动画 / plan),不含选区高亮。
// refreshViewport 和 collectSelectionText 共享这份输出,保证"屏幕显示"和"复制到剪贴板"基于
// 同一份文本(避免之前 collectSelectionText 走 ansi.Wrap raw,行号跟 markdown 渲染对不上的 bug)。
func (m *model) renderChatBaseContent(w int) string {
	// 按 segment 渲染:每段独立缓存 ANSI,流式期间只有最后一段(被 Append 清过 ansi)
	// 真正走重渲;前面段直接复用。resize 时所有段的 ansiWidth 不匹配,会整体重渲。
	//
	// kind == kindTools 时跳过 glamour:tools 段是结构化的工具调用列表(每行一条),
	// 走 markdown 会把多 tool 行的单 \n 当 soft break 拼成一行 — 不是想要的。
	// 直接保留 raw \n + emoji spacing,再加缩进色条即可。
	// Update 工具的 ~~~diff ... ~~~ 块单独走 colorizeDiffBlock 染色,fence 行不显示,
	// `-` 行染红、`+` 行染绿、"... (N more lines)" 染暗 —— 跟 markdown diff 渲染观感一致。
	content := m.chatContent.Render(w, func(raw, kind string, width int) string {
		var inner string
		if kind == kindTools {
			inner = colorizeDiffBlock(ensureEmojiSpacingANSI(ensureEmojiSpacing(raw)))
		} else {
			inner = ensureEmojiSpacingANSI(m.renderMarkdown(ensureEmojiSpacing(raw), barInnerWidth(width, kind)))
		}
		inner = strings.TrimRight(inner, "\n")
		return applyQuoteBar(inner, kind)
	})

	// plan / spinner 是临时态(只在 streaming 期间显示),不另外画色条避免跟最后一段
	// 的 ╰ 视觉重复。简单缩进 2 列,让它视觉上像是当前段的"延续"。
	// 一旦所有节点跑完,overlay 就藏起来,让屏幕让给模型后续的总结/继续输出 ——
	// 否则 checkbox 列表会和流式 token 视觉上混在一起。
	if m.plan != nil && m.streaming && !m.plan.allFinished() {
		content += "\n" + indentBlock(renderPlanForChat(m.plan), "  ")
	}
	// 思考动画不再画在 chat 末尾 —— 已统一移到输入框上方的活动状态行(statusFooterLine),
	// 避免一次 thinking 出现在两个地方。
	return content
}

func (m *model) refreshViewport() {
	atBottom := m.chatViewport.AtBottom()
	w := m.chatViewport.Width()

	content := m.renderChatBaseContent(w)
	if m.selecting && w > 0 {
		content = applySelectionHighlight(content, m.selAnchor, m.selEnd, w)
	}
	m.chatViewport.SetContent(content)
	if atBottom {
		m.chatViewport.GotoBottom()
	}
}
