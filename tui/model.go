package tui

import (
	"context"
	"deepx/agent"
	"deepx/config"
	"deepx/mcp"
	"deepx/session"
	"deepx/skill"
	"deepx/tools"
	"deepx/web"
	"deepx/workflow"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

var imagePlaceholderRe = regexp.MustCompile(`\[Image #(\d+)\]`)

// 长文本粘贴阈值:超过 800 字符,或超过一定行数(按视口高度动态)的文本,不直接塞进输入框,
// 而是存全文、在输入框放占位符 [Pasted text #N +X lines]。
// 与 free-code 一致:字符阈值用 PASTE_THRESHOLD=800;行数阈值 = min(height-10, pasteLineThresholdCap),
// 常态终端(height 够大)下即 >2 行触发(见 free-code PromptInput.tsx:1220 的 maxLines = Math.min(rows-10, 2))。
const pasteTextThreshold = 800
const pasteLineThresholdCap = 2

// pasteLineCap 与 free-code 一致:maxLines = min(rows-10, 2),常态终端下即 >2 行触发占位符。
func (m model) pasteLineCap() int {
	c := m.height - 10
	if pasteLineThresholdCap < c {
		c = pasteLineThresholdCap
	}
	return c
}

// formatPastedTextRef 生成长文本粘贴占位符,与 free-code 一致:[Pasted text #N +X lines]。
func formatPastedTextRef(id, numLines int) string {
	if numLines == 0 {
		return fmt.Sprintf("[Pasted text #%d]", id)
	}
	return fmt.Sprintf("[Pasted text #%d +%d lines]", id, numLines)
}

// expandPastedTextRefs 把输入框里的 [Pasted text #N +X lines] 占位符展开回全文(发送前调用)。
// 多个占位符反向替换(靠后的先替,保住前面偏移有效)。无对应存储的内容则保留占位符原样。
func expandPastedTextRefs(input string, store map[int]string) string {
	re := regexp.MustCompile(`\[Pasted text #(\d+)(?: \+\d+ lines)?\]`)
	matches := re.FindAllStringSubmatchIndex(input, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		m := matches[i]
		idStr := input[m[2]:m[3]]
		id := 0
		for _, c := range idStr {
			id = id*10 + int(c-'0')
		}
		full, ok := store[id]
		if !ok {
			continue
		}
		input = input[:m[0]] + full + input[m[1]:]
	}
	return input
}

// prunePastedTexts 回收已不再被引用的长文本粘贴全文,防止 pastedTexts 随会话无限增长(内存泄漏)。
// 只要某 id 的占位符还出现在输入框 / 待发原始输入(pendingInputOriginal) / 排队输入(queuedInput)中
// 就保留;否则该全文已随发送完成、打断回填等流程消费掉,可安全删除。(注意:不能"发送后无脑 delete",
// 因为 Esc 打断会把 pendingInputOriginal 的占位符重新塞回输入框、需再次展开,那时还得靠 store。)
func (m model) prunePastedTexts() {
	if len(m.pastedTexts) == 0 {
		return
	}
	refs := m.input.Value() + "\x00" + m.pendingInputOriginal
	for _, q := range m.queuedInput {
		refs += "\x00" + q
	}
	re := regexp.MustCompile(`\[Pasted text #(\d+)(?: \+\d+ lines)?\]`)
	keep := make(map[int]bool, len(m.pastedTexts))
	for _, sm := range re.FindAllStringSubmatch(refs, -1) {
		id := 0
		for _, c := range sm[1] {
			id = id*10 + int(c-'0')
		}
		keep[id] = true
	}
	for id := range m.pastedTexts {
		if !keep[id] {
			delete(m.pastedTexts, id)
		}
	}
}

// normalizeNewlines 把 Windows(\r\n) / 老 Mac(\r) 换行统一成 \n。
// 必须在文本进入 textarea 之前做:textarea 内置的 runeutil.Sanitizer 会把
// \r 与 \n 各自替换成 \n,导致 \r\n 变成 \n\n(行被翻倍、历史/输入框显示错乱)。
// 先归一化,textarea 就只会看到纯 \n,不再翻倍——与 free-code 在输入框组件层
// (.replace(/\r/g, "\n")) 的做法一致。
func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

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
	//   - setupProviderIdx= 当前选中的模型供应商下标(config.ProviderOptions),←/→ 切换
	//   - setupStep       = 0 选供应商 / 1 填配置(两步流程)
	//   - setupCustomFields= 「其它」自定义的 10 个字段输入框(flash/pro 各 5);setupFieldIdx 为焦点
	showSetup         bool
	setupRequired     bool
	setupInput        textinput.Model
	setupErr          string
	setupProviderIdx  int
	setupStep         int
	setupCustomFields []textinput.Model
	setupFieldIdx     int

	// activeModelRole 是上一轮 / 当前流的实际生效角色 ("flash" / "pro")。
	// 每条新用户消息默认从 flash 起手;agent.ModelSwitchMsg 到达时更新为 pro。
	activeModelRole string

	// modelPin 是用户用 /model 锁定的模型:"auto"(默认,走关键词路由) / "flash" / "pro"。
	// 锁定时作为 forceRole 传给 StartStream 绕过路由,并在压缩后重注锁定提示对。
	modelPin      string
	activeModelID string

	// visionByModel 是各模型(key=模型@base_url)是否支持视觉输入。启动时从 meta 缓存读入垫初值,
	// 随后由探针回执(visionCapMsg)和运行时自愈(VisionUnsupportedMsg)更新(见 vision.go)。
	// 取值缺省 false → 发图走 OCR;true → 发图渲染成 base64 内联,不走 OCR。
	visionByModel map[string]bool

	// balance 是右栏「模型厂商」段展示的账户剩余金额串(已含币种符号,如 "¥110.00")。
	// "" = 尚未探到(不显示);"-" = 该供应商不支持查询(见 balance.go / agent.ProbeBalance)。
	// 每次启动、改配置、切供应商、每轮回答结束时经 balanceMsg 回灌。
	balance string
	// balanceGen 是延迟重探的代数,每轮回答结束时 +1(见 balanceSettleTickMsg 的防抖)。
	balanceGen int

	mode        agent.AgentMode
	workingMode agent.WorkingMode // 工作模式 kp/openspec/sp(默认 kp);每轮注入对应 skill 引导;按 session 保存/恢复
	history     []agent.ChatMessage

	streamCh <-chan tea.Msg

	status    string
	streaming bool
	tokens    int

	// queuedInput 是流式进行中用户按 Enter 排队的消息(不打断当前轮)。
	// 流式无法往一个 in-flight 请求里追加内容,所以"边生成边输入"只能排队:本轮
	// StreamDoneMsg(或压缩完成)后按 FIFO 弹出一条作为新一轮发送,其余随后续轮次链式发出
	// —— 每条排队消息各成一轮。Esc / Ctrl+C 打断本轮时一并清空(用户在中止,不该再自动续发)。
	queuedInput []string

	// inputHistory 是已发送消息的历史记录(最新在末尾)，用于 ↑/↓ 翻阅。
	// inputHistoryIndex = -1 表示不在翻阅中；>=0 表示正在翻阅第几条。
	// inputDraft 是进入翻阅前输入框中的草稿，退出翻阅时恢复。
	inputHistory      []string
	inputHistoryIndex int
	inputDraft        string

	// pastedTexts 存"长文本粘贴"的全文(按 id 索引);输入框里只放 [Pasted text #N +X lines]
	// 占位符,避免超长文本被 textarea 的 CharLimit 截断、也不撑爆输入区。发送时再展开回全文。
	// 参考 free-code 的 pastedContents 机制。短粘贴(< 阈值)不走这里,直接进输入框。
	pastedTexts map[int]string
	nextPasteID  int

	currentReply *strings.Builder

	// 待发送的图片已落盘文件路径,Ctrl+V 累加,enter 后清空。
	// 不在内存里囤 PNG 字节是为了:(a) 让 img_ocr 工具能凭路径读;
	// (b) 历史 ChatMessage 之后再次发回 API 时不带巨大的 base64。
	attachedImagePaths []string

	// 当前活跃规划。nil = 无规划。pro 调用 CreatePlan 时初始化,
	// UpdatePlanStatus 通过 TaskStatusMsg 增量更新。每次新用户消息发起前清空。
	plan     *planState
	planKind string // 当前 plan 来源:"todo"(Todo)/ "createplan"(CreatePlan),右栏分段显示用

	// 鼠标 chat 矩形选区。selecting=true 表示左键在 chat 区按下后还没松开;
	// selAnchor / selEnd 是选区两端 (cellPos: 显示列 + wrapped 行号)。
	// 松开左键时:抠出选区内文本写到系统剪贴板,然后 selecting=false 高亮自动消失。
	selecting bool
	selAnchor cellPos
	selEnd    cellPos

	// 输入框选区态(鼠标拖拽触发的"部分选择",用于复制片段)。true 时按
	// inputSelAnchor/inputSelEnd 在输入框可见行上画反色。坐标口径与 chat 选区一致
	// (cellPos.col=显示列,line=textarea 可见行 taLines 的行号),复用 selection.go 的
	// applySelectionHighlight / extractSelectionText。它只是"标记待复制",不参与编辑:
	// 任何按键都会取消它、再走正常处理(不删 value)。
	inputSelecting              bool
	inputSelAnchor, inputSelEnd cellPos

	// app 侧光标 blink 状态。textarea 用真实终端光标(SetVirtualCursor(false)),
	// 而部分终端(VS Code 集成终端)不响应 DECSCUSR 的 blink 位,只好由 app 自己切
	// cursor 可见性:View 看到 cursorBlinkOff=true 时不往 tea.View.Cursor 塞值。
	// 但这条路径只在 appSideCursorBlink 为真时启用 —— 其余终端一律恒 false、光标常驻,
	// 由终端自己闪(原因见 appSideCursorBlink,issue #167)。
	cursorBlinkOff bool

	// 命令 palette 选中索引。是否打开不存字段 — 由 filterSlashCommands(input value)
	// 实时计算,避免状态同步问题。idx 越界时(value 一变 matches 变短)由消费方 clamp。
	commandPaletteIdx int

	// @ 文件提及选择器:选中索引 + 文件列表缓存。提及态由 fileMentionContext(input value, 光标)
	// 实时算;cache 在提及激活时懒构建、退出时清空(每次新 @ 会重新遍历,保证拾取新文件)。
	fileMentionIdx   int
	fileMentionCache []string

	// /reasoning 弹窗:showReasoningModal=true 时路由按键到弹窗;reasoningModalRow ∈ [0,4):
	// 0=flash.thinking, 1=flash.effort, 2=pro.thinking, 3=pro.effort。
	// 每次 ←/→ 立刻写盘,所以无 draft / cancel 概念,Enter / Esc 都是关闭。
	showReasoningModal bool
	reasoningModalRow  int

	// inputDragging 表示左键在输入框区域按下后还没松开,用来实现"输入框拖拽选择片段":
	// 按下记锚点,拖动中更新 inputSelEnd + inputSelecting=true 画高亮,松手复制选中片段。
	// inputDragStartX/Y 记按下时屏幕坐标(仅用于是否移动过的判定,真正选区走 cellPos)。
	inputDragging                    bool
	inputDragStartX, inputDragStartY int

	// scrollbarDragging 表示左键正按在 chat 滚动条上拖动:MouseMotion 时把光标 Y 映射成滚动偏移。
	scrollbarDragging bool

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

	// retryNotice 是 API 失败退避重试期间状态行显示的提示("重试 N/10 · HTTP 429 · 4s")。
	// RetryNoticeMsg 时置;请求成功(Token/ReasoningToken/ToolCallStart)或彻底结束(Done/Err)时清空。
	retryNotice string

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

	// workflowLoader 扫 .deepx/workflows/*.js,/workflow 命令据此加载并跑脚本。
	workflowLoader *workflow.Loader

	// summary 是会话压缩摘要(内存态),每轮注入 system prompt 尾部(见 BuildSystemPrompt)。
	// 不再作为 history[0] 消息存在;持久化在 state.json 的 summary 字段。
	summary string

	// 重启缓存友好压缩:detectRestartCompaction 检测到前缀变化时暂存上次前缀快照,
	// Init 时用 restartCompactionCmd 在首请求前跑一次压缩(见 prefix_cache.go)。
	pendingCompactModel string
	pendingCompactSys   string
	pendingCompactTools string

	// compacting:压缩 Cmd 在飞时为 true。三个触发点(80%/重启/空闲)都 gate 在它上,
	// 防止并发压缩(否则第二个结果的 cutIdx 会越界已截断的 history → panic)。
	compacting bool

	// compactingFG:仅手动 /compact 期间为 true —— 前台阻塞式压缩。footer 显示 spinner「压缩中…」,
	// 期间挡住普通消息提交,避免后台异步时用户又发消息、与压缩截断 history 抢同一段。自动压缩不置此位(仍后台)。
	compactingFG bool

	// 影子热压(shadow checkpoint):轮末上下文跨过 shadowPoints(30/45/60%)时,后台预算一份 checkpoint+cut
	// 存盘(不改实时态、不重建 system prompt),供 >1h 冷重启时用,省冷 prefill 全量历史。
	//   - shadowing:影子 Cmd 在飞时为 true,防并发影子。
	//   - shadowDonePct:已影子到的最高档(0/30/45/60);实时压缩落地后重置 0。
	//   - compactGen:实时压缩代数,影子结果带它回来比对,过期(实时压缩已发生)则丢弃。
	shadowing     bool
	shadowDonePct int
	compactGen    int

	// pendingUserText:本轮用户输入「展开后的全文」,发送时暂存、本轮成功后才写入 jsonl
	// (连同 assistant)。长文本粘贴在此已是展开态,落盘保存的是完整正文,正确。
	// 注意:jsonl 要全文,但输入框回填(打断/中断时)绝不能填全文 —— 4000 上限的输入框
	// 装不下 190 行,会被截断丢失。所以回填另用 pendingInputOriginal(见下)。
	pendingUserText string
	// pendingInputOriginal:本轮用户输入「原始形态」(含长文本粘贴占位符 [Pasted text #N +X lines],
	// 未展开)。仅供「打断后回填输入框」使用,保持占位符形态避免塞爆输入框被截断。
	// 与 pendingUserText 区分:后者展开后用于 jsonl 落盘,前者占位符用于输入框回填。
	pendingInputOriginal string

	// review 模式审核状态
	reviewPending  bool
	reviewCh       chan bool
	reviewToolName string
	reviewToolArgs string
	reviewYesNo    bool // true=YES, false=NO

	// AskUser 选择题弹窗状态(LLM 主动发起,复用 review 的阻塞-恢复骨架)。
	// askPending=true 时路由按键到弹窗。askSelected[题][选项] 是勾选态。
	askPending   bool
	askCh        chan string
	askQuestions []agent.AskQuestion
	askSelected  [][]bool
	askQIdx      int  // 当前题
	askOptIdx    int  // 当前题内的选项光标
	askWarn      bool // 当前题没选就按了回车 → 显示红色闪烁提示,提醒用空格选

	// /lang 选择 modal 状态。showLangModal=true 时全屏路由按键到 modal,
	// langModalIdx ∈ {0:zh, 1:en}。
	showLangModal bool
	langModalIdx  int

	// /working-mode 选择 modal 状态。workingModeModalIdx ∈ {0:karpathy, 1:openspec, 2:superpowers}。
	showWorkingModeModal bool
	workingModeModalIdx  int

	// /model 选择 modal 状态。showModelModal=true 时路由按键到 modal,
	// modelModalIdx ∈ {0:auto, 1:flash, 2:pro}。
	showModelModal bool
	modelModalIdx  int

	// /sandbox 选择 modal 状态。sandboxModalIdx ∈ {0:native, 1:off, 2:docker}(见 sandboxModeOrder)。
	showSandboxModal bool
	sandboxModalIdx  int

	// /provider 选择 modal 状态。showProviderModal=true 时路由按键到 modal;
	// providerNames 是 provider.yaml 里已存的供应商名,providerModalIdx 是当前光标。
	showProviderModal bool
	providerModalIdx  int
	providerNames     []string

	// MCP:mcpMgr 管理外部 MCP server 连接与工具注入(启动时后台连接配置里的 server)。
	// /mcp-add 弹单行输入框(格式 "名称 命令 [参数...]");/mcp-delete 弹 server 列表选删。
	mcpMgr        *mcp.Manager
	showMcpAdd    bool
	mcpAddInput   textinput.Model
	mcpAddErr     string
	showMcpDelete bool
	mcpDelNames   []string
	mcpDelIdx     int

	// /web-config 弹单行输入框设置 web 面板的绑定 IP + 端口,写入 meta.json。
	showWebConfig  bool
	webConfigInput textinput.Model
	webConfigErr   string

	// /skill-add 是 search-and-install 多阶段 modal:
	//   - skillAddInput  :阶段 1 输入(关键词 / URL / 本地路径)
	//   - skillAddSearching / skillAddInstalling:阶段 2/4 loading 标识
	//   - skillAddResults / skillAddIdx:阶段 3 搜索结果与当前选中
	// Esc 一律关 modal,异步搜索 / 安装结果通过 skillSearchDoneMsg / skillInstallDoneMsg 回 Update。
	showSkillAdd       bool
	skillAddInput      textinput.Model
	skillAddErr        string
	skillAddSearching  bool
	skillAddInstalling bool
	skillAddResults    []skill.RemoteSkillInfo
	skillAddIdx        int
	// /skill-delete 弹已装 skill 列表选删,跟原来一致。
	showSkillDelete bool
	skillDelNames   []string
	skillDelIdx     int

	// /sessions 历史对话列表模态。sessionListDelete=true 时是 /session-delete 的删除选择态
	// (复用同一弹窗,Enter 删除选中项而非切换)。
	showSessionList   bool
	sessionConvs      []session.ConvInfo
	sessionListIdx    int
	sessionListDelete bool

	// hideStatusPanel:隐藏右侧状态栏,chat 铺满整宽(Ctrl+B / /status 切换,记忆到 meta)。
	hideStatusPanel bool

	// showThinking:把模型 reasoning_content 暗显进对话流(/thinking 切换,记忆到 meta)。默认关。
	showThinking bool

	// docker 沙箱镜像拉取进度(/sandbox docker 切换时,镜像不在本地才拉)。
	dockerPulling    bool
	dockerPullImage  string
	dockerPullDots   int // 省略号动画计数(无百分比,只表示"进行中")
	dockerPullCancel context.CancelFunc

	// 版本信息。version 是 build 时注入的当前版本号(go build 默认 "dev")。
	// latestVersion 是异步检查得到的 GitHub latest release,空则没检查到 / 网络失败。
	// upgradeAvailable 由 versionNewer(latestVersion, version) 算出,渲染时用来决定是否
	// 在右栏显示"有新版本"提示。
	version          string
	latestVersion    string
	upgradeAvailable bool
	upgradeURL       string

	// Ctrl+C 双击退出保护。第一次按时间戳记到这里;ctrlcExitWindow 内再按才 quit。
	// streaming 中第一次也会取消流(行为同 Esc),避免要按两个键才能停一个跑得离谱的任务。
	lastCtrlCAt time.Time

	// Esc 双击中断保护(issue #120):vim/neovim 用户肌肉记忆常误按 Esc。
	// 第一次按时间戳记到这里;escInterruptWindow 内再按才真正中断 streaming。
	lastEscAt time.Time

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
	// webURL 非空时右栏显示 ◆ WEB 地址。srv 用于 /web-config 改完后热重绑监听。
	hub    *web.Hub
	srv    *web.Server
	webURL string

	// lastCgStatus 是最近一次广播给 web 的代码图谱状态;codegraph 在后台异步变(loading→ready),
	// 借 blink tick 轮询、变了才广播,让 web 状态跟上 TUI(TUI 是每帧直接读 atomic)。
	lastCgStatus string
}

// webInputMsg 是浏览器提交的输入,经 program.Send 注入,走和终端 Enter 完全相同的提交逻辑。
type webInputMsg struct{ text string }

// webReviewMsg 是浏览器的 review 确认,经 program.Send 注入,复用终端同一个 ReviewCh(先到先得)。
type webReviewMsg struct{ approve bool }

// webAskAnswerMsg 是浏览器对 AskUser 选择题的作答(answer = 答案 JSON),复用终端同一个 askCh(先到先得)。
type webAskAnswerMsg struct{ answer string }

// webInterruptMsg 是浏览器点"停止":中断当前执行,等价于终端按 Esc。
type webInterruptMsg struct{}

// 左栏操作:压缩会话 / MCP 增删(经 program.Send 注入,走相同 Update)。
type webCompactMsg struct{}
type webMcpAddMsg struct{ cfg mcp.ServerConfig }
type webMcpDeleteMsg struct{ name string }

// interruptStream 中断当前后台 agent:取消 context(真正终止 HTTP 请求与工具调用)、
// 清理 review/ask 的等待通道防 goroutine 泄漏、drain channel、复位为 idle,并广播 interrupted
// 让浏览器端同步停下(取消时 agent 因 context.Canceled 静默 return,不会自己发 done/error)。
// 终端 Esc 与浏览器"停止"共用这一段。调用前应确认 m.streaming && m.streamCh != nil。
func (m model) interruptStream() model {
	if m.cancelAgent != nil {
		m.cancelAgent()
		m.cancelAgent = nil
	}
	if m.reviewPending && m.reviewCh != nil {
		m.reviewCh <- false
		m.reviewPending = false
		m.reviewCh = nil
	}
	if m.askPending && m.askCh != nil {
		m.askCh <- ""
		m.askPending = false
		m.askCh = nil
	}
	if m.streamCh != nil {
		drainAndDiscard(m.streamCh)
		m.streamCh = nil
	}
	m.streaming = false
	m.thinking = false
	m.status = "idle"
	m.chatContent.Append(T("misc.interrupted"))
	m.queuedInput = nil // 中止本轮 → 丢弃排队消息,不再自动续发
	m.broadcast(web.Event{Kind: "interrupted"})
	return m
}

// 控制类 web 消息:浏览器点按钮经 program.Send 注入,复用终端同一套切换逻辑。
type webNewSessionMsg struct{}                      // 新建会话
type webSwitchSessionMsg struct{ id string }        // 切换到某会话
type webRenameSessionMsg struct{ id, title string } // 重命名会话
type webDeleteSessionMsg struct{ id string }        // 删除会话
type webSetModelMsg struct{ role string }           // 路由 auto/flash/pro
type webSetModeMsg struct{ mode string }            // 权限模式 plan/auto/review
type webSetSandboxMsg struct{ mode string }         // 沙箱 off/native/docker
type webSetWorkingModeMsg struct{ mode string }     // 工作模式
type webSetLangMsg struct{ lang string }            // 界面语言 zh/en

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

// shadowResultMsg 影子热压完成后的结果。只存盘、不改实时态;gen 用于丢弃过期结果。
type shadowResultMsg struct {
	checkpoint string
	cut        int
	gen        int // 触发时的 compactGen;回来时若与当前不符,说明期间发生过实时压缩,丢弃
	err        error
}

// compactTriggerPct 实时会话压缩触发阈值(上下文用量占窗口的百分比)。与 agent 包轮内 auto-compact
// 对齐到 80%:留 20% 给本轮输出;1M 窗口下也有充足 headroom。
const compactTriggerPct = 80

// shadowPoints 影子热压的触发档位(上下文用量百分比)。30 起步而非 20:keepTarget 也是 20%,
// 在 20% 处 history < keepTarget 会被 RunCompression 拒(白跑);30% 起 history 才够压。
var shadowPoints = []int{30, 45, 60}

func initialModel(models agent.ModelConfig, needsSetup bool, version string, hub *web.Hub, srv *web.Server, webURL string) model {
	vp := viewport.New()
	vp.MouseWheelDelta = mouseWheelDelta()

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = lipgloss.NewStyle().Foreground(spinnerColor)

	ti := textarea.New()
	ti.Placeholder = T("misc.input_placeholder")
	ti.CharLimit = 4000
	ti.ShowLineNumbers = false
	// 输入框固定 inputRows 行高。内容多于此时,靠 ↑/↓ 移动光标带动 textarea 内部滚动,
	// 把多出的行"移出来"看(见按键处理里 ↑/↓ 落到 input.Update 的注释)。
	ti.SetHeight(inputTextRows)
	// 输入框样式定制:
	//   - 第一行显示 "> ",后续行只缩进 2 空格(对齐到内容列)避免每行重复 prompt
	//   - Prompt 染亮青(同 banner 品牌主色),focus / blur 状态都保留亮度
	//   - 内置 CursorLine 高亮(默认会给当前行加背景色)关掉,跟 chat 区无 chrome 风格一致
	tas := ti.Styles()
	tas.Focused.CursorLine = lipgloss.NewStyle()
	tas.Blurred.CursorLine = lipgloss.NewStyle()
	tas.Focused.Base = lipgloss.NewStyle()
	tas.Blurred.Base = lipgloss.NewStyle()
	// 光标样式:细竖条 + 亮青色 + 缓慢 blink(600ms),跟 banner 品牌主色一致,
	// 避免默认 block 光标在中文/emoji 行上把字符整块反色显得突兀。
	tas.Cursor.Shape = tea.CursorBar
	tas.Cursor.Color = cursorColor // 品牌青,亮底自适应为深青(见 applyTheme)
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

	ski := textinput.New()
	ski.Placeholder = "关键词 或 https://github.com/owner/repo 或 ~/path"
	ski.CharLimit = 512
	ski.SetWidth(54)

	wci := textinput.New()
	wci.Placeholder = "0.0.0.0 8080"
	wci.CharLimit = 64
	wci.SetWidth(54)

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
	skill.CleanupExtractedBuiltins(home) // 一次性清掉旧版解压到 ~/.deepx/skills 的内置副本(现改为从 embed 加载)
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

	// workflow:.deepx/workflows/*.js(项目级覆盖全局),/workflow 命令加载执行。
	wfProjectDirs, wfGlobalDirs := workflow.DefaultDirs(wd, home)
	workflowLoader := workflow.New(wfProjectDirs, wfGlobalDirs)

	// 代码图谱:绑定到当前 workspace 根,懒构建(首次 CodeGraph 调用时才遍历解析)。
	tools.SetCodeGraphRoot(wd)

	// 沙箱:绑定 workspace(docker 挂载用)+ 恢复上次模式/镜像。docker 模式下首条命令惰性起容器;
	// 若彼时 docker 不可用,EnsureDockerContainer 会给清晰错误,用户可 /sandbox native 切回。
	tools.SetSandboxWorkspace(wd)
	if mm := metaGet(); mm.SandboxDockerImage != "" {
		tools.SetSandboxDockerImage(mm.SandboxDockerImage)
	}
	switch metaGet().Sandbox {
	case string(tools.SandboxDocker):
		tools.SetSandboxMode(tools.SandboxDocker)
	case string(tools.SandboxOff):
		tools.SetSandboxMode(tools.SandboxOff)
	} // 其余(空 / native)走默认 native,无需设置

	// MCP:后台连接 ~/.deepx/mcp.json 里配置的 server,连上后把它们的工具注入给 LLM。
	// 失败只记状态、不影响启动;没配置则什么都不做。
	mcpMgr := mcp.NewManager()
	mcpMgr.ConnectAll()

	// 视觉能力:先用缓存里上次的值给本会话垫个初值;每次启动都会重探(见 Init → visionProbeCmds),
	// 探针结果经 visionCapMsg 回灌当前会话并覆盖缓存。
	visionByModel := loadVisionCaps(models)
	// 粘贴图片缓存:跟 OCR 解耦后改由这里按时效清理(超过 7 天的旧图删掉),不阻塞启动。
	go tools.SweepPasteCache(7 * 24 * time.Hour)

	m := model{
		mcpMgr:          mcpMgr,
		mcpAddInput:     mi,
		skillAddInput:   ski,
		webConfigInput:  wci,
		chatContent:     newChatLog(maxChatBytes),
		currentReply:    &strings.Builder{},
		chatViewport:    vp,
		input:           ti,
		models:          models,
		visionByModel:   visionByModel,
		activeModelRole: role,
		activeModelID:   activeID,
		modelPin:        "auto",
		version:         version,
		mode:            agent.AgentMode_Auto,
		workingMode:     agent.WorkingModeDefault, // 默认 kp;下方从 session 恢复
		status:          "idle",
		hideStatusPanel: metaGet().HideStatus,   // 记忆上次的状态栏显隐
		showThinking:    metaGet().ShowThinking, // 记忆上次的思考显隐
		spinner:         sp,
		workspace:       wd,
		setupInput:      si,
		session:         sess,
		skillLoader:     loader,
		skillCatalog:    skillCatalog,
		workflowLoader:  workflowLoader,
		hub:             hub,
		srv:             srv,
		webURL:          webURL,
		inputHistoryIndex: -1,
	}

	// 恢复本会话的工作模式(空 = 默认 kp)与 /model 锁定(子会话级,issue #43)。
	if sess != nil {
		m.workingMode = agent.NormalizeWorkingMode(sess.LoadWorkingMode())
		m.restoreModelPin(sess.LoadModelPin())
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
			// 老对话(升级前就有 history、没 conv.json)首次进 /sessions 别显示"(未命名)":
			// 用第一条用户消息回填标题。session 包自己解码不了 history.gob,放这儿做。
			if sess.ConvTitle() == "" {
				if t := firstUserText(gobHistory); t != "" {
					sess.SetConvTitle(truncTitle(t, 40))
				}
			}
		}

		// 注:">1h 缓存已凉则套用影子"统一在「提交新一轮」时判断(见 applyStaleShadow)——
		// 重启只是"产生 >1h 间隔"的一种情况,和挂机回来走同一逻辑;且不到真发请求不截断。

		// 只有默认对话(= rootDir)才用 workspace 级 JSONL 兜底恢复;/new 出来的新对话没自己的
		// history.gob 时就该是空的,不能去捞共享 JSONL 里别的对话的内容(否则新会话显示旧内容)。
		if !gobOK && sess.OnDefaultConversation() {
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

	// 每次启动的欢迎语。
	m.appendChat("System", T("welcome"))

	// web 控制面板启用时,在 chat 区给出可点击 / 可复制的地址 —— 浏览器里能新建会话、
	// 切会话、切权限/沙箱/工作模式,状态与终端实时对齐。
	if webURL != "" {
		m.appendChat("System", fmt.Sprintf(T("web.ready"), webURL))
		// 绑定到非回环地址(0.0.0.0 / 局域网 IP)→ 面板对外可达,给一条安全警告。
		if webURLExposed(webURL) {
			m.appendChat("System", T("web.ready.lan"))
		}
	}

	// 剪贴板工具检测:Linux 上没装 wl-clipboard / xclip / xsel 时 Ctrl+V 文本粘贴会静默
	// 失败(atotto/clipboard 找不到二进制就返错,bubbles textarea 啥也不做),用户调一天
	// 也猜不到根因。启动直接告诉他怎么装(macOS / Windows 系统自带,各自的 hint 返空)。
	if hint := clipboardTextHint(); hint != "" {
		m.appendChat("System", hint)
	}

	// 偏好快照:启动时读一次 AGENTS.md 冻结进缓存,本会话每轮复用(只在压缩/重启时再刷新)。
	// 必须在 detectRestartCompaction 之前 —— 它算前缀签名要包含当前偏好。
	agent.RefreshPreferences(m.workspace)

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

// ctrlcExitWindow 两次 Ctrl+C 之间允许的最大时间差。第一次按下若 streaming 中则取消流并
// 提示再按退出;窗口内第二次 → 真退。
const ctrlcExitWindow = 1 * time.Second

// escInterruptWindow 两次 Esc 之间允许的最大时间差。streaming 中第一次按只提示、不中断;
// 窗口内第二次 → 真中断。防止 vim 用户误按一下 Esc 就打断生成(issue #120)。
const escInterruptWindow = 1 * time.Second

func cursorBlinkTick() tea.Cmd {
	return tea.Tick(cursorBlinkInterval, func(time.Time) tea.Msg {
		return cursorBlinkTickMsg{}
	})
}

// appSideCursorBlink 决定光标由谁来闪:app 侧(切 tea.View.Cursor 的有无)还是终端自己。
//
// 默认交给终端(false)。因为 app 侧闪烁是靠把 tea.View.Cursor 置 nil 再塞回来实现的,
// 而 bubbletea 只要发现光标样式变了就写一条 DECSCUSR(cursed_renderer.go):
// nil → 样式 0、有值 → 样式 5,于是每 600ms 往终端写一次 "\x1b[5 q" / "\x1b[0 q"。
// 这个序列的 q 前面有个空格(中间字节),CSI 解析器不健全的终端会在空格处就结束序列、
// 把末尾的 q 当普通字符打印出来 —— 又因 bubbletea 是差分渲染不重画旧格子,这些野生的 q
// 会从输入框光标处一路堆积(issue #167:每秒吐一个 q,拔键盘也不停)。
// 光标常驻不变样式后,DECSCUSR 全程只在启停各写一次,问题消失。
//
// 例外:VS Code 集成终端默认忽略 DECSCUSR 的 blink 位(光标闪不闪由它自己的设置说了算),
// 不 app 侧闪就是死光标;而它的 xterm.js 能正确吃掉中间字节、不会漏 q,所以只对它开这条路径。
var appSideCursorBlink = detectAppSideCursorBlink()

func detectAppSideCursorBlink() bool {
	return os.Getenv("TERM_PROGRAM") == "vscode"
}

// broadcast 把事件发给 web hub(关闭时 hub==nil,直接跳过)。
func (m model) broadcast(ev web.Event) {
	if m.hub != nil {
		m.hub.Broadcast(ev)
	}
}

// broadcastQueued 把当前排队待发送列表整份推给 web,让浏览器在输入框上方显示"待发送"区
// (对齐 TUI 的 queuedDisplayLines)。queuedInput 每次增删后调用;空列表即清空前端展示。
func (m model) broadcastQueued() {
	m.broadcast(web.Event{Kind: "queued", Queued: append([]string(nil), m.queuedInput...)})
}

// applyMode 切换权限模式(plan/auto/review),落一条系统提示进历史/会话,并广播给 web。
// 终端 /plan /auto /review 与 web 按钮共用此入口。非法 mode 直接忽略。
func (m *model) applyMode(mode agent.AgentMode) {
	switch mode {
	case agent.AgentMode_Plan, agent.AgentMode_Auto, agent.AgentMode_Review:
	default:
		return
	}
	m.mode = mode
	msg := modeNotification(mode, m.activeModelRole)
	m.history = append(m.history, agent.ChatMessage{Role: "assistant", Content: msg})
	m.appendChat("assistant", msg)
	if m.session != nil {
		_ = m.session.Append("assistant", msg)
	}
	m.broadcast(web.Event{Kind: "mode", Text: string(mode)})
}

// applyLang 切换界面语言并广播给 web。终端 /lang 弹窗与 web 语言下拉共用此入口。
func (m *model) applyLang(picked Lang) {
	SetLang(picked)
	m.refreshViewport() // 右栏 / palette 的 desc 等都要按新语言重画
	m.broadcast(web.Event{Kind: "lang", Text: string(picked)})
	name := "中文"
	if picked == LangEN {
		name = "English"
	}
	m.appendChat("System", fmt.Sprintf(T("lang.switched"), name))
}

// endpointHost 从模型配置的 BaseURL 里取出 api host(去 scheme / path),用作"模型厂商"展示。
// 与 view.go 右栏的取法一致。
func endpointHost(models agent.ModelConfig) string {
	host := models.Flash.BaseURL
	if host == "" {
		host = models.Pro.BaseURL
	}
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.IndexAny(host, "/?"); i >= 0 {
		host = host[:i]
	}
	return host
}

// onOff 把布尔映射成 web 控制事件用的 "on"/"off" 文本。
func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// broadcastControlState 把全部控制态(权限模式 / 沙箱 / 工作模式 / 代码图谱 + 会话列表)
// 推给 web,使浏览器状态栏与 TUI 对齐。启动时与每次切会话后调用。
func (m model) broadcastControlState() {
	if m.hub == nil {
		return
	}
	m.broadcast(web.Event{Kind: "vendor", Text: endpointHost(m.models)})
	m.broadcast(web.Event{Kind: "routing", Text: m.modelPin})
	m.broadcast(web.Event{Kind: "mode", Text: string(m.mode)})
	m.broadcast(web.Event{Kind: "sandbox", Text: string(tools.CurrentSandboxMode())})
	m.broadcast(web.Event{Kind: "working_mode", Text: string(m.workingMode)})
	m.broadcast(web.Event{Kind: "codegraph", Text: tools.CodeGraphStatus()})
	m.broadcast(web.Event{Kind: "show_thinking", Text: onOff(m.showThinking)})
	m.broadcastSessions()
}

// broadcastSessions 把会话列表(对齐 TUI /sessions)推给 web,供前端点击切换。
func (m model) broadcastSessions() {
	if m.hub == nil || m.session == nil {
		return
	}
	convs := m.session.ListConversations()
	// web 会话列表按创建时间排序(新建在前),保持稳定 —— 不随聊天 last_seen 重排。
	sort.Slice(convs, func(i, j int) bool { return convs[i].CreatedAt.After(convs[j].CreatedAt) })
	out := make([]web.SessionInfo, 0, len(convs))
	for _, c := range convs {
		title := c.Title
		if title == "" {
			title = T("session.untitled")
		}
		out = append(out, web.SessionInfo{ID: c.ID, Title: title, Active: c.Active})
	}
	m.broadcast(web.Event{Kind: "sessions", Sessions: out})
}

// broadcastSessionLoaded 在切换 / 新建会话后,把当前会话的 user/assistant 消息推给 web,
// 让浏览器聊天区也跟着切过去(也用于启动时把已恢复的历史镜像给晚连接的浏览器)。
func (m model) broadcastSessionLoaded() {
	if m.hub == nil {
		return
	}
	// 工具结果:历史里 tool 角色消息按 ToolCallID 携带输出,先建映射供工具条还原。
	toolOut := map[string]string{}
	for _, h := range m.history {
		if h.Role == "tool" && h.ToolCallID != "" {
			toolOut[h.ToolCallID] = h.Content
		}
	}
	msgs := make([]web.Message, 0, len(m.history))
	for _, h := range m.history {
		switch h.Role {
		case "user":
			if strings.TrimSpace(h.Content) == "" {
				continue
			}
			msgs = append(msgs, web.Message{Role: "user", Content: h.Content})
		case "assistant":
			if strings.TrimSpace(h.Content) != "" {
				msgs = append(msgs, web.Message{Role: "assistant", Content: h.Content})
			}
			// 还原该 assistant 消息发起的工具调用,内联到对话流。
			// 历史不存 success 标志,统一按已完成("done")展示。
			for _, tc := range h.ToolCalls {
				msgs = append(msgs, web.Message{
					Role:   "tool",
					ID:     tc.ID,
					Name:   tc.Function.Name,
					Args:   tc.Function.Arguments,
					Status: "done",
					Output: toolOut[tc.ID],
				})
			}
		}
		// 其余(tool 结果 / system)不单独成行:tool 结果已并入上面的工具条。
	}
	m.broadcast(web.Event{Kind: "session_loaded", Messages: msgs})
}

// submitUserInput 是终端 Enter 和 web 输入共用的提交入口:
// 斜杠命令直接执行;否则构造 user 消息、落盘、广播 user_message,并启动 agent stream。
// 不碰输入框(textarea 清空由 Enter 分支自己做)。
// popQueuedInput 弹出一条排队输入(FIFO)并作为新一轮提交,返回 ok=true 表示确有排队被发出。
// 只在 streaming 已置 false 后调用(StreamDoneMsg / 压缩完成),此时可安全开下一轮;剩余排队
// 随它这一轮的 StreamDoneMsg 再次触发,链式逐条发出。
func (m model) popQueuedInput() (model, tea.Cmd, bool) {
	if len(m.queuedInput) == 0 {
		return m, nil, false
	}
	next := m.queuedInput[0]
	m.queuedInput = m.queuedInput[1:]
	m.broadcastQueued() // 弹出一条 → 同步缩短前端待发送区(submitUserInput 随后广播 user_message)
	var cmd tea.Cmd
	m, cmd = m.submitUserInput(next)
	return m, cmd, true
}

// applyStaleShadow 在「提交新一轮」前调用:若距上次请求 > cacheWarmWindow(DeepSeek 前缀缓存大概率
// 已凉),且有会话中趁热预存的影子 checkpoint(且尚未套用),就把它落地——m.summary 换成影子、
// history 截到切点后,省掉冷 prefill 整段历史。<1h 不动(全量蹭服务端热缓存)。
//
// 缓存失效只看"距上次请求多久"(服务端、与重启无关),所以重启后首条消息和挂机回来走同一逻辑。
// 幂等守卫 cp != m.summary 防重复套用(扛清理失败 / 崩溃 / 连续触发)。显示区(m.chatContent)仍全量,
// 只截发给模型的 history(与压缩行为一致)。
func (m *model) applyStaleShadow() {
	if m.session == nil || len(m.history) == 0 {
		return
	}
	t, ok := m.session.PrefixSnapshotTime()
	if !ok || time.Since(t) <= cacheWarmWindow {
		return // 没发过请求 / 缓存还热 → 用全量(命中热缓存更快)
	}
	cp, cut := m.session.LoadShadow()
	if cp == "" || cp == m.summary || cut <= 0 || cut > len(m.history) {
		return
	}
	m.summary = cp
	m.history = append([]agent.ChatMessage(nil), m.history[cut:]...)
	_ = m.session.SaveSummary(cp)
	_ = m.session.SaveGob("history.gob", m.history)
	m.session.ClearShadow()
}

func (m model) submitUserInput(input string) (model, tea.Cmd) {
	// 暂存「原始输入」(含长文本粘贴占位符、未展开):专供打断后回填输入框,
	// 必须保持占位符形态 —— 否则 190 行全文塞回 4000 上限的输入框会被截断、丢失。
	// 展开后的全文另存 pendingUserText(下方),只用于本轮成功后的 jsonl 落盘。
	m.pendingInputOriginal = input
	// 发送前先回收已无引用的长文本全文(避免 pastedTexts 随会话累积泄漏)。
	m.prunePastedTexts()
	// 发送前把长文本粘贴占位符 [Pasted text #N +X lines] 展开回全文(参考 free-code)。
	// 还原后可能远超过 textarea 的 CharLimit,但此时已离开输入框、直接送模型,不受限。
	input = expandPastedTextRefs(input, m.pastedTexts)
	// 粘贴文本可能含 Windows 换行 \r\n 或单独的 \r。\r\n 在后续渲染链里会被
	// chatViewport.SetContentLines 拆成多行,但单独的 \r 会留在行尾被终端当回车符执行,
	// 导致光标跳回行首、后续字符覆盖前面 → 视觉错乱(只在顶部旧消息出现、底部正常)。
	// 输入时(PasteMsg 处理)已统一过一次,这里再兜一层,从源头消除。
	input = normalizeNewlines(input)
	input = strings.TrimSpace(input)
	// 删掉输入框里的 [Image #N] = 想移除那张图:据残留占位符裁剪待发图片列表,
	// 否则删了占位符图却照发、非视觉模型还会反复 OCR(issue #146 的「删不掉」)。
	m.attachedImagePaths = reconcileAttachedImages(input, m.attachedImagePaths)
	if input == "" && len(m.attachedImagePaths) == 0 {
		return m, nil
	}
	// 流式中 / 压缩前台期间再提交(主要是 web 端在生成时点发送)→ 排队而非丢弃,本轮(或压缩)
	// 结束后由 popQueuedInput 自动发出,与终端 Enter 完全一致:不开新 stream(杜绝并发两个 stream /
	// 与压缩截断 history 的竞态)、不丢字。终端 Enter 已在键处理处排队;这里兜 web 等其它入口
	// (issue #161:web 生成中发送直接消失)。排队原文含斜杠命令时,popQueuedInput 回灌本函数时
	// streaming 已置 false,会照常走下面的斜杠解析——与终端排队原文再解析的行为一致。
	if m.streaming || m.compactingFG {
		// 排「原始输入」(占位符),不是已展开的全文——否则排队再回灌本函数时
		// pendingInputOriginal 会拿到展开态,打断回填输入框又会塞爆被截断。
		m.queuedInput = append(m.queuedInput, m.pendingInputOriginal)
		m.broadcastQueued()
		m.refreshViewport()
		return m, nil
	}
	// 斜杠命令(带参数,如 /model flash):首 token 精确命中已知命令名 → 转发完整输入。
	// 必须在 filterSlashCommands 之前 —— 后者遇空格会返回 nil,会把带参命令误当普通消息。
	if isExactSlashCommand(input) {
		return m, m.handleSlashCommand(input)
	}
	// 无参数命令 + 前缀补全(如 /au → /auto):走 palette 解析,用解析出的命令名。
	if matches := filterSlashCommands(input); len(matches) > 0 {
		cmd := m.handleSlashCommand(matches[0].name)
		return m, cmd
	}

	// 距上次请求 > 1h → 缓存大概率已凉,套用会话中预存的影子 checkpoint,缩小这轮的冷 prefill。
	// 重启后首条消息与挂机回来共用此逻辑(统一在"发新一轮"判断,不在启动时)。
	m.applyStaleShadow()

	userMsg := m.buildUserMessage(input)
	m.appendChat("You", input)
	m.history = append(m.history, userMsg)
	// 对话还没标题时,用首条用户输入当标题(给 /sessions 列表显示)。
	// 设了新标题就立刻把会话列表推给 web,否则浏览器一直显示"未命名",要切回来才更新。
	if m.maybeSetConvTitle(input) {
		m.broadcastSessions()
	}
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
	m.planKind = ""

	workspace, _ := os.Getwd()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelAgent = cancel
	// 把各模型的视觉能力塞进传给 agent 的配置:agent 发带图请求前据此渲染 base64 / 路径+OCR。
	models := m.models
	models.Flash.Vision = m.visionByModel[modelCapKey(models.Flash)]
	models.Pro.Vision = m.visionByModel[modelCapKey(models.Pro)]
	cmd, ch := agent.StartStream(
		ctx,
		models,
		m.history,
		m.mode,
		workspace,
		m.skillCatalog,
		m.summary,
		m.modelPin,
		m.workingMode,
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
	// 探测终端背景明暗:回应经 tea.BackgroundColorMsg 到 Update,据此切亮/暗主题(issue #163)。
	cmds = append(cmds, tea.RequestBackgroundColor)
	// 重启检测到前缀变化且历史够大时,在首请求前先跑一次缓存友好压缩。
	if m.pendingCompactSys != "" {
		cmds = append(cmds, m.restartCompactionCmd())
	}
	// 视觉能力探测:每次启动对各模型重探一次(见 vision.go),结果经 visionCapMsg 回灌。
	cmds = append(cmds, visionProbeCmds(m.models)...)
	// 账户余额:仅 DeepSeek/Kimi 可凭 Key 查,其它回 "-"(见 balance.go)。
	if cmd := balanceProbeCmd(m.models); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := ForceGraphemeCmd(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	// 启动即把控制态与已恢复的历史推进 hub 快照,晚连接的浏览器据此与 TUI 对齐。
	m.broadcastControlState()
	m.broadcastSessionLoaded()
	return tea.Batch(cmds...)
}

func ForceGraphemeCmd() tea.Cmd {
	if !graphemeWidthMode {
		return nil
	}
	return func() tea.Msg {
		return tea.ModeReportMsg{Mode: ansi.ModeUnicodeCore, Value: ansi.ModeSet}
	}
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

	case webAskAnswerMsg:
		// 浏览器对 AskUser 选择题的作答,复用终端同一个 askCh(先到先得)。
		if m.askPending {
			m.askCh <- msg.answer
			m.askPending = false
			m.broadcast(web.Event{Kind: "ask_resolved"})
			m.refreshViewport()
			return m, func() tea.Msg { return reviewResultMsg{} }
		}
		return m, nil

	case webInterruptMsg:
		// 浏览器点"停止":等价于终端按 Esc,中断当前执行。
		if m.streaming && m.streamCh != nil {
			m = m.interruptStream()
			m.refreshViewport()
			return m, nil
		}
		return m, nil

	case webCompactMsg:
		// 浏览器点"压缩会话":等价于 /compact。
		return m, m.startManualCompaction()

	case webMcpAddMsg:
		// 浏览器添加 MCP server:落盘 + 后台连接,刷新工具集。
		if err := mcp.AddServer(msg.cfg); err != nil {
			m.appendChat("System", "添加 MCP 失败:"+err.Error())
		} else {
			m.appendChat("System", fmt.Sprintf("已添加 MCP server「%s」,正在连接…", msg.cfg.Name))
			if m.mcpMgr != nil {
				cfg := msg.cfg
				go m.mcpMgr.Connect(cfg)
			}
		}
		return m, nil

	case webMcpDeleteMsg:
		// 浏览器删除 MCP server:落盘删除 + 断连。
		if _, err := mcp.DeleteServer(msg.name); err != nil {
			m.appendChat("System", "删除 MCP 失败:"+err.Error())
		} else {
			m.appendChat("System", fmt.Sprintf("已删除 MCP server「%s」。", msg.name))
			if m.mcpMgr != nil {
				m.mcpMgr.Disconnect(msg.name)
			}
		}
		return m, nil

	case webNewSessionMsg:
		// 浏览器点"新建会话":复用终端 /new 逻辑;loadCurrentConversation 会广播新会话态。
		m.startNewConversation()
		m.refreshViewport() // 确保终端侧立即重绘出新会话(含 session.new 欢迎行)
		return m, nil

	case webSwitchSessionMsg:
		// 浏览器点会话列表:切到该 id;流式中拒绝(同终端)。
		if !m.streaming && m.session != nil {
			if err := m.session.SwitchConversation(msg.id); err == nil {
				m.loadCurrentConversation()
			}
		}
		return m, nil

	case webRenameSessionMsg:
		if m.session != nil {
			_ = m.session.RenameConversation(msg.id, msg.title)
			m.broadcastSessions()
		}
		return m, nil

	case webDeleteSessionMsg:
		// 删会话:流式中拒绝。删的是当前会话则删后切回默认并重载。
		// 失败必须回传提示(issue #141):默认会话(rootDir 老会话)不可删,
		// 此前 web 路径静默吞掉错误 → 用户点删除无反应、无报错,以为"删不掉"。
		if m.streaming {
			m.broadcast(web.Event{Kind: "notice", Text: T("session.streaming")})
		} else if m.session != nil {
			wasCurrent := m.session.CurrentConversation() == msg.id
			if err := m.session.DeleteConversation(msg.id); err != nil {
				m.broadcast(web.Event{Kind: "notice", Text: T("session.delete.cant_default")})
			} else if wasCurrent {
				m.loadCurrentConversation() // 内部广播 session_loaded + 控制态(含会话列表)
			} else {
				m.broadcastSessions()
			}
		}
		return m, nil

	case webSetModelMsg:
		m.applyModelPin(msg.role) // 内部广播 routing
		return m, nil

	case webSetModeMsg:
		m.applyMode(agent.AgentMode(msg.mode))
		return m, nil

	case webSetSandboxMsg:
		// applySandboxMode 内部已广播 sandbox 态(off/native 即时,docker 拉完在 activate 时)。
		return m, m.applySandboxMode(tools.SandboxMode(msg.mode))

	case webSetWorkingModeMsg:
		m.applyWorkingMode(agent.NormalizeWorkingMode(msg.mode)) // 内部广播 working_mode
		return m, nil

	case webSetLangMsg:
		switch msg.lang {
		case string(LangZH):
			m.applyLang(LangZH)
		case string(LangEN):
			m.applyLang(LangEN)
		}
		return m, nil

	case tea.BackgroundColorMsg:
		// 终端背景明暗探测结果:切亮/暗主题。applyTheme 返回 false = 主题未变(且已初始化过),
		// 直接跳过。切换后清 markdown renderer 缓存(旧实例 bake 的是旧主题的 glamour 样式)+
		// 重着色 spinner / 光标 + 重绘。见 applyTheme(issue #163)。
		if !applyTheme(msg.IsDark()) {
			return m, nil
		}
		m.mdRenderers = nil          // 旧实例 bake 的是旧主题的 glamour 样式,丢弃重建
		m.chatContent.InvalidateRender() // 清掉每段旧主题的 ANSI 缓存,否则宽度没变时 Render 复用旧色(需拖窗口才刷新)
		m.spinner.Style = lipgloss.NewStyle().Foreground(spinnerColor)
		tas := m.input.Styles()
		tas.Cursor.Color = cursorColor
		m.input.SetStyles(tas)
		m.refreshViewport()
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		leftW, vpH := m.layout()
		m.chatViewport.SetWidth(leftW)
		m.chatViewport.SetHeight(vpH)
		// 输入区收进左列(与对话同宽):textarea 宽 = leftW - gutter。
		// 状态栏占满全高、分隔线贯穿到底,所以输入不再横跨整宽。gutter 由 view.go 单独画。
		m.input.SetWidth(leftW - inputGutterWidth)
		// 窗口尺寸变了 → wrap 重算 → 老 line 号失效,必须清选区
		m.selecting = false
			m.inputSelecting = false
		m.refreshViewport()

	case tea.MouseWheelMsg:
		// modal 期间忽略
		if m.showSetup || m.showLangModal || m.showWorkingModeModal || m.showModelModal || m.showSandboxModal || m.showReasoningModal || m.showSessionList || m.showProviderModal {
			return m, nil
		}
		// 滚轮:转给 viewport,顺便取消选区
		if m.selecting {
			m.selecting = false
		}
		var c tea.Cmd
		m.chatViewport, c = m.chatViewport.Update(msg)
		return m, c

	case tea.MouseClickMsg:
		if m.showSetup || m.showLangModal || m.showWorkingModeModal || m.showModelModal || m.showSandboxModal || m.showReasoningModal || m.showSessionList || m.showProviderModal {
			return m, nil
		}
		if msg.Button != tea.MouseLeft {
			return m, nil
		}
		leftW, vpH := m.layout()
		// chat 区:X 从 0 起,Y 从 0 起(无顶栏)。滚动条/分隔线占 [leftW, leftW+scrollbarWidth) 三列,内容列 [0, leftW)。
		chatLeft, chatTop := 0, 0
		chatBottom := chatTop + vpH

		// 先判滚动条/分隔线区(三列):命中就进入拖拽态并按 Y 滚动,return —— 不落到选区逻辑,绝不影响拖拽选中。
		if msg.X >= leftW && msg.X < leftW+scrollbarWidth && msg.Y >= chatTop && msg.Y < chatBottom {
			m.scrollbarDragging = true
			m.scrollChatToTrackRow(msg.Y-chatTop, vpH)
			return m, nil
		}

		// 选区只认内容列 [chatLeft, leftW),分隔线列按 X 与上面互斥。
		inChat := msg.X >= chatLeft && msg.X < leftW &&
			msg.Y >= chatTop && msg.Y < chatBottom
		// 输入区:左列 body 下方那块(空白行 + textarea 行),Y ∈ [vpH, m.height) 且 X ∈ [0, leftW)。
		// X 上界收到 leftW —— 右侧是全高状态栏,点那里不该进输入编辑。
		inInput := msg.Y >= vpH && msg.Y < m.height && msg.X >= 0 && msg.X < leftW

		if inInput {
			// 按下进入输入区:清 chat 选区 + 记拖拽锚点,准备"拖拽选择片段"。
			// 同时把上一次的输入选区清掉:若接着是真拖动,MouseMotion 会重新点亮;
			// 若只是单击松手,就等价于"点一下取消"(#188)。
			m.inputSelecting = false
			if a, ok := m.inputCellAt(msg.X, msg.Y); ok {
				m.inputSelAnchor = a
				m.inputSelEnd = a
			}
			m.inputDragging = true
			m.inputDragStartX, m.inputDragStartY = msg.X, msg.Y
			if m.selecting {
				m.selecting = false
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
		if m.showSetup || m.showLangModal || m.showWorkingModeModal || m.showModelModal || m.showSandboxModal || m.showReasoningModal || m.showSessionList || m.showProviderModal {
			return m, nil
		}
		if msg.Button != tea.MouseLeft {
			return m, nil
		}
		leftW, vpH := m.layout()
		// chat 区:X 从 0 起,Y 从 0 起(无顶栏)。内容列 [0, leftW),分隔线列在 leftW。
		chatLeft, chatTop := 0, 0
		chatRight := chatLeft + leftW // 选区夹取到内容列
		chatBottom := chatTop + vpH

		// 滚动条拖拽优先:正按在滚动条上 → 按光标 Y 滚动,return,不进选区逻辑。
		if m.scrollbarDragging {
			m.scrollChatToTrackRow(msg.Y-chatTop, vpH)
			return m, nil
		}

		// 输入框拖拽选择片段:把光标映射成输入可见行的 cellPos,只要落到与锚点不同的
		// 单元格就开始/更新选区高亮(同格内的抖动不触发,避免单击误选)。
		if m.inputDragging {
			if e, ok := m.inputCellAt(msg.X, msg.Y); ok && m.input.Value() != "" {
				if e.col != m.inputSelAnchor.col || e.line != m.inputSelAnchor.line {
					m.inputSelEnd = e
					m.inputSelecting = true
				} else {
					// 拖回锚点同格 → 取消选区(选区收拢为空)。
					m.inputSelEnd = e
					m.inputSelecting = false
				}
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
		if m.showSetup || m.showLangModal || m.showWorkingModeModal || m.showModelModal || m.showSandboxModal || m.showReasoningModal || m.showSessionList || m.showProviderModal {
			return m, nil
		}
		if msg.Button != tea.MouseLeft {
			return m, nil
		}
		// 滚动条拖拽松手:仅清除拖拽态,不做别的(不影响选区复制等)。
		if m.scrollbarDragging {
			m.scrollbarDragging = false
			return m, nil
		}
		// 输入框拖拽松手:若真的选了一段(inputSelecting),复制选中片段并弹"已复制"提示
		// (与 chat 区一致)。纯单击(无选区)→ 什么都不做 = 点一下取消。
		if m.inputDragging {
			m.inputDragging = false
			if m.inputSelecting {
				if text := m.inputSelectionText(); text != "" {
					m.copyHint = T("copy.done")
					m.copyHintX = msg.X
					m.copyHintY = msg.Y
					return m, tea.Batch(clipboardWriteCmd(text), clearCopyHintCmd())
				}
			}
			return m, nil
		}
		if m.selecting {
			// 写剪贴板:本地原生优先、远程/失败才 OSC52(见 copySelection / clipboardWriteCmd)。
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
		// 配置 modal 期间:custom 表单转发给焦点字段,预设供应商转发给 setupInput(允许粘贴 key)
		if m.showSetup {
			if m.setupStep == 1 && m.curProvider() == config.ProviderCustom {
				if m.setupFieldIdx >= 0 && m.setupFieldIdx < len(m.setupCustomFields) {
					var c tea.Cmd
					m.setupCustomFields[m.setupFieldIdx], c = m.setupCustomFields[m.setupFieldIdx].Update(msg)
					return m, c
				}
				return m, nil
			}
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
		// web-config modal 期间,转发给 webConfigInput(允许粘贴 IP)
		if m.showWebConfig {
			var c tea.Cmd
			m.webConfigInput, c = m.webConfigInput.Update(msg)
			return m, c
		}
		// skill-add modal 期间,转发给 skillAddInput(允许粘贴 URL / 路径)
		// 仅阶段 1(无结果且非 loading 时)接管 paste;阶段 3 列表态忽略 paste
		if m.showSkillAdd && !m.skillAddSearching && !m.skillAddInstalling && len(m.skillAddResults) == 0 {
			var c tea.Cmd
			m.skillAddInput, c = m.skillAddInput.Update(msg)
			return m, c
		}
		// 空 paste 内容 = 终端有 paste 事件但 PTY 里没文本 → 大概率剪贴板只有图片,主动读 PNG。
		// 非空 paste 内容 = 普通文本粘贴,放掉让 textinput 自己接(它有 PasteMsg 处理)。
		if msg.Content == "" {
			m.inputSelecting = false // 选区只是复制标记,粘贴图片不清空 value
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
		// 文本粘贴:超阈值(>800 字符,或行数超 pasteLineCap=min(height-10,2)、常态 >2 行)走占位符,否则让 textarea 正常接。
		// 占位符方案避免超长文本被 textarea 的 CharLimit(4000)截断、也不撑爆输入区。
		// 进入 textarea 之前先把 \r\n 归一为 \n:否则 textarea 的 Sanitizer 会把
		// \r\n 拆成两个 \n(\r 一个、\n 一个),造成行翻倍、历史/输入框错乱(见 normalizeNewlines)。
		text := msg.Content
		text = normalizeNewlines(text)
		numLines := strings.Count(text, "\n")
		if len(text) > pasteTextThreshold || numLines > m.pasteLineCap() {
			// 先回收上一轮已无引用的全文,再存新的;避免反复大段粘贴时 map 只增不减。
			m.prunePastedTexts()
			if m.pastedTexts == nil {
				m.pastedTexts = make(map[int]string)
			}
			id := m.nextPasteID
			m.nextPasteID++
			m.pastedTexts[id] = text
			m.input.InsertString(formatPastedTextRef(id, numLines))
			return m, nil
		}
		var c tea.Cmd
		m.input, c = m.input.Update(tea.PasteMsg{Content: text})
		return m, c

	case tea.KeyPressMsg:
		// 配置 modal 处于活动状态时,按键全部在这里处理,绕过主界面。两步:选供应商 / 填配置。
		if m.showSetup {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "esc":
				// 第二步 Esc → 退回选供应商;第一步 Esc → 关闭(首次启动不许关)。
				if m.setupStep == 1 {
					m.setupStep = 0
					m.setupErr = ""
					m.setupInput.Blur()
					for i := range m.setupCustomFields {
						m.setupCustomFields[i].Blur()
					}
					return m, nil
				}
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

			// 第一步:选供应商(↑/↓ 竖排切换,←/→ 同效;Enter 进入填配置)。
			if m.setupStep == 0 {
				switch msg.String() {
				case "up", "left", "k":
					n := len(config.ProviderOptions)
					if n > 0 {
						m.setupProviderIdx = (m.setupProviderIdx - 1 + n) % n
					}
					return m, nil
				case "down", "right", "j":
					n := len(config.ProviderOptions)
					if n > 0 {
						m.setupProviderIdx = (m.setupProviderIdx + 1) % n
					}
					return m, nil
				case "enter":
					m.setupStep = 1
					m.setupErr = ""
					if m.curProvider() == config.ProviderCustom {
						m.setupCustomFields = newSetupCustomFields()
						m.setupFieldIdx = 0
					} else {
						m.setupInput.SetValue("")
						m.setupInput.Focus()
					}
					return m, nil
				}
				return m, nil
			}

			// 配置输入统一禁空格:url / model / api_key / 数字都不含空格,误触/粘错直接挡掉
			//(保存时另有 strings.TrimSpace 兜底首尾)。
			if s := msg.String(); s == " " || s == "space" {
				return m, nil
			}

			// 第二步 · custom:10 字段表单(Tab/↑↓ 切字段,Enter 保存)。
			if m.curProvider() == config.ProviderCustom {
				switch msg.String() {
				case "enter":
					return m, m.submitSetup()
				case "tab", "down":
					m.focusCustomField(m.setupFieldIdx + 1)
					return m, nil
				case "shift+tab", "up":
					m.focusCustomField(m.setupFieldIdx - 1)
					return m, nil
				}
				if m.setupFieldIdx >= 0 && m.setupFieldIdx < len(m.setupCustomFields) {
					var c tea.Cmd
					m.setupCustomFields[m.setupFieldIdx], c = m.setupCustomFields[m.setupFieldIdx].Update(msg)
					return m, c
				}
				return m, nil
			}

			// 第二步 · 预设供应商:单 api_key 字段(Enter 保存)。
			if msg.String() == "enter" {
				return m, m.submitSetup()
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
				picked := []Lang{LangZH, LangEN}[m.langModalIdx]
				m.showLangModal = false
				m.applyLang(picked)
				return m, nil
			case "esc", "ctrl+c":
				m.showLangModal = false
				return m, nil
			}
			return m, nil
		}

		// /provider 弹窗:↑/↓ 切供应商(provider.yaml 里已存的),Enter 应用(写回 model.yaml + 重载),Esc 取消。
		if m.showProviderModal {
			switch msg.String() {
			case "up", "k":
				if m.providerModalIdx > 0 {
					m.providerModalIdx--
				}
				return m, nil
			case "down", "j":
				if m.providerModalIdx < len(m.providerNames)-1 {
					m.providerModalIdx++
				}
				return m, nil
			case "enter":
				m.showProviderModal = false
				if m.providerModalIdx >= 0 && m.providerModalIdx < len(m.providerNames) {
					return m, m.applyProvider(m.providerNames[m.providerModalIdx])
				}
				return m, nil
			case "esc", "ctrl+c":
				m.showProviderModal = false
				return m, nil
			}
			return m, nil
		}

		// /working-mode 弹窗:↑/↓ 切行(3 行:karpathy/openspec/superpowers),Enter 应用,Esc 取消。
		if m.showWorkingModeModal {
			switch msg.String() {
			case "up", "k":
				if m.workingModeModalIdx > 0 {
					m.workingModeModalIdx--
				}
				return m, nil
			case "down", "j":
				if m.workingModeModalIdx < len(workingModeOrder)-1 {
					m.workingModeModalIdx++
				}
				return m, nil
			case "enter":
				m.showWorkingModeModal = false
				m.input.Focus()
				m.applyWorkingMode(workingModeOrder[m.workingModeModalIdx])
				return m, nil
			case "esc", "ctrl+c":
				m.showWorkingModeModal = false
				m.input.Focus()
				return m, nil
			}
			return m, nil
		}

		// /reasoning 弹窗:↑/↓ 切行(4 行:flash/pro × thinking/effort),←/→ 在当前行切值并
		// 立即写盘,Enter / Esc 关闭(无 cancel,改了就入盘了)。
		if m.showReasoningModal {
			switch msg.String() {
			case "up", "k":
				if m.reasoningModalRow > 0 {
					m.reasoningModalRow--
				}
				return m, nil
			case "down", "j":
				if m.reasoningModalRow < 3 {
					m.reasoningModalRow++
				}
				return m, nil
			case "left", "h":
				m.reasoningStepRow(m.reasoningModalRow, -1)
				return m, nil
			case "right", "l":
				m.reasoningStepRow(m.reasoningModalRow, 1)
				return m, nil
			case "enter", "esc", "ctrl+c":
				m.showReasoningModal = false
				return m, nil
			}
			return m, nil
		}

		// /model modal:↑/↓ 选 auto/flash/pro,Enter 确认,Esc 取消
		if m.showModelModal {
			switch msg.String() {
			case "up", "k":
				if m.modelModalIdx > 0 {
					m.modelModalIdx--
				}
				return m, nil
			case "down", "j":
				if m.modelModalIdx < 2 {
					m.modelModalIdx++
				}
				return m, nil
			case "enter":
				opts := []string{"auto", tools.RoleFlash, tools.RolePro}
				m.showModelModal = false
				m.applyModelPin(opts[m.modelModalIdx])
				return m, nil
			case "esc", "ctrl+c":
				m.showModelModal = false
				return m, nil
			}
			return m, nil
		}

		// /sandbox 弹窗:↑/↓ 切行(3 行:native/off/docker),Enter 应用(docker 可能触发异步拉镜像),Esc 取消。
		if m.showSandboxModal {
			switch msg.String() {
			case "up", "k":
				if m.sandboxModalIdx > 0 {
					m.sandboxModalIdx--
				}
				return m, nil
			case "down", "j":
				if m.sandboxModalIdx < len(sandboxModeOrder)-1 {
					m.sandboxModalIdx++
				}
				return m, nil
			case "enter":
				m.showSandboxModal = false
				m.input.Focus()
				return m, m.applySandboxMode(sandboxModeOrder[m.sandboxModalIdx])
			case "esc", "ctrl+c":
				m.showSandboxModal = false
				m.input.Focus()
				return m, nil
			}
			return m, nil
		}

		// /mcp-add modal:单行输入,Enter 保存连接,Esc/Ctrl+C 取消
		// Ctrl+C 在 modal 里改成"关 modal"(不退程序);需要真退用 Esc 关 modal 后再 Ctrl+C 两次。
		if m.showMcpAdd {
			switch msg.String() {
			case "enter":
				m.submitMcpAdd()
				return m, nil
			case "esc", "ctrl+c":
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

		// /web-config modal:Enter 保存,Esc 取消
		if m.showWebConfig {
			switch msg.String() {
			case "enter":
				m.submitWebConfig()
				return m, nil
			case "esc", "ctrl+c":
				m.showWebConfig = false
				m.webConfigErr = ""
				m.webConfigInput.Blur()
				m.input.Focus()
				return m, nil
			}
			var c tea.Cmd
			m.webConfigInput, c = m.webConfigInput.Update(msg)
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

		// /skill-add modal:四阶段
		//   - searching/installing:只接 Esc 关 modal,其他键吞掉
		//   - 结果列表态:↑↓ 选 / Enter 装(异步)/ Esc 关
		//   - 输入态:textinput 编辑 / Enter 提交(异步搜索 or 直装)/ Esc 关
		if m.showSkillAdd {
			switch msg.String() {
			case "esc", "ctrl+c":
				// Ctrl+C 在 modal 内只关 modal(同 Esc),不退程序;真退要回主输入再按两次。
				m.showSkillAdd = false
				m.skillAddErr = ""
				m.skillAddInput.Blur()
				m.skillAddResults = nil
				m.skillAddSearching = false
				m.skillAddInstalling = false
				m.input.Focus()
				return m, nil
			}
			// loading 阶段:键盘吞掉等异步消息
			if m.skillAddSearching || m.skillAddInstalling {
				return m, nil
			}
			// 阶段 3:有搜索结果时
			if len(m.skillAddResults) > 0 {
				switch msg.String() {
				case "up", "k":
					if m.skillAddIdx > 0 {
						m.skillAddIdx--
					}
					return m, nil
				case "down", "j":
					if m.skillAddIdx < len(m.skillAddResults)-1 {
						m.skillAddIdx++
					}
					return m, nil
				case "enter":
					return m, m.installSelectedSkillResult()
				}
				return m, nil
			}
			// 阶段 1:输入态
			if msg.String() == "enter" {
				return m, m.submitSkillAdd()
			}
			var c tea.Cmd
			m.skillAddInput, c = m.skillAddInput.Update(msg)
			return m, c
		}

		// /skill-delete modal:↑/↓ 选,Enter 删,Esc 取消(完全镜像 /mcp-delete)
		if m.showSkillDelete {
			switch msg.String() {
			case "up", "k":
				if m.skillDelIdx > 0 {
					m.skillDelIdx--
				}
				return m, nil
			case "down", "j":
				if m.skillDelIdx < len(m.skillDelNames)-1 {
					m.skillDelIdx++
				}
				return m, nil
			case "enter":
				m.submitSkillDelete()
				return m, nil
			case "esc", "ctrl+c":
				m.showSkillDelete = false
				m.input.Focus()
				return m, nil
			}
			return m, nil
		}

		// /sessions 历史对话列表:↑/↓ 选,Enter 切换,Esc 取消
		if m.showSessionList {
			switch msg.String() {
			case "up", "k":
				if m.sessionListIdx > 0 {
					m.sessionListIdx--
				}
				return m, nil
			case "down", "j":
				if m.sessionListIdx < len(m.sessionConvs)-1 {
					m.sessionListIdx++
				}
				return m, nil
			case "enter":
				if m.sessionListDelete {
					m.submitSessionDelete()
				} else {
					m.submitSessionSwitch()
				}
				return m, nil
			case "esc", "ctrl+c":
				m.showSessionList = false
				m.sessionListDelete = false
				m.input.Focus()
				return m, nil
			}
			return m, nil
		}

		// AskUser 选择题:↑↓ 移光标,空格勾选(单选互斥),←→ 切题,Enter 下一题/最后一题提交,Esc 取消。
		// 选中只认空格;没选就按回车 → 红色闪烁提示,不前进/提交,逼用户记住用空格选。
		if m.askPending {
			q := m.askQuestions[m.askQIdx]
			switch msg.String() {
			case "up", "k":
				if m.askOptIdx > 0 {
					m.askOptIdx--
				}
				m.refreshViewport()
				return m, nil
			case "down", "j":
				if m.askOptIdx < len(q.Options)-1 {
					m.askOptIdx++
				}
				m.refreshViewport()
				return m, nil
			case " ", "space":
				if q.Multiple {
					m.askSelected[m.askQIdx][m.askOptIdx] = !m.askSelected[m.askQIdx][m.askOptIdx]
				} else {
					for i := range m.askSelected[m.askQIdx] {
						m.askSelected[m.askQIdx][i] = false
					}
					m.askSelected[m.askQIdx][m.askOptIdx] = true
				}
				m.askWarn = false // 已用空格选,撤掉提示
				m.refreshViewport()
				return m, nil
			case "left", "h":
				if m.askQIdx > 0 {
					m.askQIdx--
					m.askOptIdx = 0
					m.askWarn = false
				}
				m.refreshViewport()
				return m, nil
			case "right", "l":
				if m.askQIdx < len(m.askQuestions)-1 {
					m.askQIdx++
					m.askOptIdx = 0
					m.askWarn = false
				}
				m.refreshViewport()
				return m, nil
			case "enter":
				// 当前题必须先用空格选;没选就回车 → 亮红提示,原地不动。
				if !askQuestionAnswered(m.askSelected[m.askQIdx]) {
					m.askWarn = true
					m.refreshViewport()
					return m, m.spinner.Tick // 确保后续 tick 驱动闪烁
				}
				m.askWarn = false
				if m.askQIdx < len(m.askQuestions)-1 {
					m.askQIdx++
					m.askOptIdx = 0
					m.refreshViewport()
					return m, nil
				}
				// 折叠成 kindSystem 档案段留在 scrollback:问题 + 已选答案,作答有痕(issue #134)。
				m.chatContent.Open(kindSystem, m.askRecord(false))
				m.askCh <- m.buildAskAnswer()
				m.askPending = false
				m.broadcast(web.Event{Kind: "ask_resolved"})
				m.refreshViewport()
				return m, func() tea.Msg { return reviewResultMsg{} }
			case "esc", "ctrl+c":
				m.chatContent.Open(kindSystem, m.askRecord(true))
				m.askCh <- ""
				m.askPending = false
				m.broadcast(web.Event{Kind: "ask_resolved"})
				m.refreshViewport()
				return m, func() tea.Msg { return reviewResultMsg{} }
			case "pgup", "pgdown", "pageup", "pagedown", "home", "end", "ctrl+u", "ctrl+d":
				// 卡片已内联进对话流:翻页/顶底键照常滚动 chat 回看历史,不被选项交互吞掉(issue #134)。
				var c tea.Cmd
				m.chatViewport, c = m.chatViewport.Update(msg)
				return m, c
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
				m.commandPaletteIdx = 0
				// 需要参数的命令(无弹窗兜底)→ 不直接执行,补全进输入框 + 末尾空格,等用户补参数。
				if slashCommandNeedsArg(chosen) {
					m.input.SetValue(chosen + " ")
					m.input.CursorEnd()
					return m, nil
				}
				m.input.SetValue("")
				return m, m.handleSlashCommand(chosen)
			case "esc":
				// 取消:清掉正在敲的 "/命令",面板随 value 清空而消失。
				m.input.SetValue("")
				m.attachedImagePaths = nil
				m.commandPaletteIdx = 0
				return m, nil
			}
		} else {
			// palette 没在显示,idx 复位避免下次打开时停在过去位置
			m.commandPaletteIdx = 0
		}

		// @ 文件提及选择器导航键拦截。与 / palette 互斥(/ 起手的情况已在上面消费过导航键)。
		// 提及态由光标处的 "@query" 片段决定;cache 由下方 syncFileMention 在提及激活时构建。
		if start, end, query, active := fileMentionContext(m.input.Value(), m.input.Line(), m.input.Column()); active && !strings.HasPrefix(m.input.Value(), "/") {
			matches := filterWorkspaceFiles(query, m.fileMentionCache, fileMentionMaxRows)
			if len(matches) > 0 {
				if m.fileMentionIdx >= len(matches) {
					m.fileMentionIdx = len(matches) - 1
				}
				if m.fileMentionIdx < 0 {
					m.fileMentionIdx = 0
				}
				switch msg.String() {
				case "up":
					if m.fileMentionIdx > 0 {
						m.fileMentionIdx--
					}
					return m, nil
				case "down":
					if m.fileMentionIdx < len(matches)-1 {
						m.fileMentionIdx++
					}
					return m, nil
				case "tab", "enter":
					// 用选中路径替换光标处的 "@query" 为 "@<相对路径>"。分工对齐 / palette:
					//   - Enter = 确认选择并关闭(总补尾随空格,文件 / 目录都直接选定)
					//   - Tab   = 对目录"下钻"(不补空格、留在提及态继续筛子路径);对文件等同 Enter
					// 与 / palette 的 Tab 同样用 SetValue + CursorEnd —— 光标落末尾,提及在句中时
					// 光标不在插入点是已知小瑕疵,常见的"句尾 @" 场景表现正确。
					chosen := matches[m.fileMentionIdx]
					suffix := " "
					if msg.String() == "tab" && strings.HasSuffix(chosen, "/") {
						suffix = "" // 仅 Tab 选目录才下钻
					}
					runes := []rune(m.input.Value())
					m.input.SetValue(string(runes[:start]) + "@" + chosen + suffix + string(runes[end:]))
					m.input.CursorEnd()
					m.fileMentionIdx = 0
					return m, nil
				}
			}
		}

		// 输入框选区(复制标记)预处理:它不参与编辑,任何按键都先取消它、再走正常处理。
		// Esc 只取消不做别的;其余键取消后继续(打字/删除/移动光标照常,不再"清空 value")。
		if m.inputSelecting {
			m.inputSelecting = false
			if msg.String() == "esc" {
				return m, nil
			}
		}
		switch msg.String() {
		case "ctrl+c":
			// 输入框有内容(或挂了图)→ 单次 Ctrl+C 清空,符合 shell / REPL 习惯。
			// 不计入退出双击(重置计时),清完再按才进入下面的退出逻辑。
			if strings.TrimSpace(m.input.Value()) != "" || len(m.attachedImagePaths) > 0 {
				m.input.SetValue("")
				m.attachedImagePaths = nil
				m.inputSelecting = false
				m.lastCtrlCAt = time.Time{}
				m.input.SetHeight(inputTextRows) // 多行清空后把视口/高度复位
				return m, nil
			}
			// 输入框为空时才走"双击退出"保护:防止误触一下就退。
			//   - streaming 中第一次:先取消(同 Esc 行为)+ 提示再按退出
			//   - idle 中第一次:只提示,不退
			//   - 任何时候窗口内第二次:tea.Quit
			now := time.Now()
			if !m.lastCtrlCAt.IsZero() && now.Sub(m.lastCtrlCAt) <= ctrlcExitWindow {
				// 第二次,窗口内,退
				if m.streaming && m.cancelAgent != nil {
					m.cancelAgent()
					m.cancelAgent = nil
				}
				// 清理 reviewCh,防止 agent goroutine 泄漏
				if m.reviewPending && m.reviewCh != nil {
					m.reviewCh <- false
					m.reviewPending = false
					m.reviewCh = nil
				}
				if m.streamCh != nil {
					drainAndDiscard(m.streamCh)
				}
				return m, tea.Quit
			}
			// 第一次(或上一次已过期)
			m.lastCtrlCAt = now
			if m.streaming {
				// 顺手把流取消了(等同 Esc)。这样用户单按一次就停,不需要先 Esc 再 Ctrl+C。
				if m.cancelAgent != nil {
					m.cancelAgent()
					m.cancelAgent = nil
				}
				// 清理 reviewCh,防止 agent goroutine 泄漏
				if m.reviewPending && m.reviewCh != nil {
					m.reviewCh <- false
					m.reviewPending = false
					m.reviewCh = nil
				}
				if m.streamCh != nil {
					drainAndDiscard(m.streamCh)
					m.streamCh = nil
				}
				m.streaming = false
				m.thinking = false
				m.status = "idle"
				m.chatContent.Append(T("misc.interrupted"))
				m.queuedInput = nil // 中止本轮 → 丢弃排队消息,不再自动续发
				m.broadcastQueued() // 同步清空 web 待发送区
			}
			m.appendChat("System", T("misc.ctrlc_again_to_quit"))
			m.refreshViewport()
			return m, nil
		case "esc":
			// 正在拉 docker 镜像 → Esc 取消拉取,保持 native。
			if m.dockerPulling {
				if m.dockerPullCancel != nil {
					m.dockerPullCancel()
					m.dockerPullCancel = nil
				}
				m.dockerPulling = false
				m.appendChat("System", T("sandbox.pull_canceled"))
				m.refreshViewport()
				return m, nil
			}
			// Esc 中断当前对话 —— 双击保护(issue #120):vim/neovim 用户常误按 Esc,
			// 单按一次只提示、不打断;escInterruptWindow 内再按一次才真正中断。
			// 想单按即停的用户仍可用 Ctrl+C(空输入时单按即取消生成)。
			if m.streaming && m.streamCh != nil {
				now := time.Now()
				if m.lastEscAt.IsZero() || now.Sub(m.lastEscAt) > escInterruptWindow {
					// 第一次(或上次已过期):只记时间 + 提示,不中断。
					m.lastEscAt = now
					m.appendChat("System", T("misc.esc_again_to_interrupt"))
					m.refreshViewport()
					return m, nil
				}
				// 窗口内第二次:真正中断。
				m.lastEscAt = time.Time{}
				m = m.interruptStream()
			// 打断后把这一轮的用户输入回填到空输入框,方便改一下重发。
			// 用 pendingInputOriginal(原始形态、含粘贴占位符),不要用 pendingUserText
			// (已展开的全文):190 行全文塞回 4000 上限的输入框会被截断、丢失。
			// 仅当输入框为空时回填,不覆盖用户已敲入的新内容。
			if strings.TrimSpace(m.input.Value()) == "" && m.pendingInputOriginal != "" {
				m.input.SetValue(m.pendingInputOriginal)
			}
				m.refreshViewport()
				return m, nil
			}
			return m, nil
		case "pgup", "pgdown", "pageup", "pagedown", "home", "end", "ctrl+u", "ctrl+d":
			// 翻页 / 顶底键滚 chat。↑/↓ 不在此列 —— 它们落到下方 input.Update 移动输入框光标:
			// 输入区固定 3 行,光标移过可视区会带动 textarea 内部滚动,把多出的行"移出来"看。
			// chat 滚动仍可用 PgUp/PgDn、鼠标滚轮、拖动右侧滚动条。
			var c tea.Cmd
			m.chatViewport, c = m.chatViewport.Update(msg)
			return m, c
		case "up":
			// ↑ 键：光标在「内容最顶端」（或输入框为空）时，翻阅上一条历史。
			// 顶端必须同时满足：逻辑行 == 0 且处于该行顶部虚拟行（RowOffset == 0）。
			// 否则（如首行是长行被软折成多个虚拟行、光标在其中下移过）交给 textarea
			// 在折行内上移光标，不能误判为「已在首行」而触发历史翻阅 —— 否则长文本里
			// 键盘 ↑ 完全失效、只能靠鼠标滚轮（见 up_wrap 回归测试）。
			// 有弹窗/流式中时不拦截，交给 textarea 处理光标移动。
			if m.showSetup || m.showMcpAdd || m.showReasoningModal ||
				m.showSkillAdd || m.showWebConfig || m.askPending || m.reviewPending {
				// 弹窗打开时不拦截，让 textarea 处理
			} else if m.streaming || m.compactingFG {
				// 流式中不拦截
			} else if m.input.Value() == "" ||
				(m.input.Line() == 0 && m.input.LineInfo().RowOffset == 0) {
				m.navigateHistoryUp()
				return m, nil
			}
		case "down":
			// ↓ 键：正在翻阅历史时，翻到下一条（更新的）消息；翻到底则恢复草稿。
			// 与 ↑ 对称：只有光标已到当前历史项的「底部」（最后逻辑行 + 最后虚拟行）才翻下一条，
			// 否则下移光标 —— 否则翻阅一条多行历史时,在中间行按 ↓ 会直接翻走、跳过多行内容
			// （详见 down_history_nav 回归测试）。
			if m.showSetup || m.showMcpAdd || m.showReasoningModal ||
				m.showSkillAdd || m.showWebConfig || m.askPending || m.reviewPending {
				// 弹窗打开时不拦截
			} else if m.streaming || m.compactingFG {
				// 流式中不拦截
			} else if m.inputHistoryIndex >= 0 {
				li := m.input.LineInfo()
				lastLine := strings.Count(m.input.Value(), "\n") // 最后逻辑行号(0-indexed)
				atBottom := m.input.Line() == lastLine && li.RowOffset == li.Height-1
				if atBottom {
					m.navigateHistoryDown()
					return m, nil
				}
				// 未到底部: 下放给 textarea 下移光标, 不翻历史
			}
		case "ctrl+j":
			// 在光标处插入换行,实现多行输入。Enter 仍走下方 submit 分支。
			// 换行键统一为 ctrl+j:它就是 LF(\n),终端原生支持、三平台一致、不被 OS 拦截,
			// bubbletea 报成 "ctrl+j",与 Enter(CR→"enter")天然区分 —— 终端里唯一可靠的换行键。
			// (不再接 Alt/Ctrl/Shift+Enter:Win11 下 Alt+Enter 被系统吃掉切全屏 #124,
			//  Ctrl/Shift+Enter 在 Terminal.app 等又跟 Enter 不可区分,留着只会平台不一致。)
			if m.streaming {
				return m, nil
			}
			m.input.InsertRune('\n')
			// InsertRune 直调不会重定位 textarea 内部视口(只有走 textarea.Update 末尾才会),
			// 在已满 3 行时插入新行后光标会跑到可视区外、被钳回行首。SetHeight(同值)内部会
			// 调 repositionView 把视口滚到光标行,且不动光标——以此手动触发一次重定位。
			m.input.SetHeight(inputTextRows)
			return m, nil
		case "ctrl+b":
			// 显示/隐藏右侧状态栏(chat 铺满整宽);记忆到 meta。
			m.toggleStatusPanel()
			return m, nil
		case "ctrl+v":
			// 剪贴板有图就落盘并插入到输入框;没图则下落到 textinput 走文本粘贴。
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
			if m.streaming || m.compactingFG {
				// 流式中 / 压缩中:不打断,把这条排队,本轮(或压缩)结束后自动发送
				// (见 queuedInput / StreamDoneMsg / compressionResultMsg)。压缩期间排队同样杜绝
				// "新开 stream 与压缩截断 history 抢同一段"的竞态——安全和拒绝式一样,但不丢字、不硬挡。
				// 只排文本;此刻挂着的图片留在 attachedImagePaths,由下一次真正提交时一并消费。
				q := strings.TrimSpace(m.input.Value())
				if q == "" {
					return m, nil
				}
				m.queuedInput = append(m.queuedInput, q)
				m.broadcastQueued() // 终端排队也同步到 web 的待发送区
				m.pushInputHistory(q)
				m.input.SetValue("")
				m.refreshViewport()
				return m, nil
			}
			input := strings.TrimSpace(m.input.Value())
			if input == "" && len(m.attachedImagePaths) == 0 {
				return m, nil
			}
			// 需要参数的命令但没带参数 → 不发送,补全进输入框 + 末尾空格,等用户补参数。
			if name, ok := bareSlashNeedsArg(input); ok {
				m.input.SetValue(name + " ")
				m.input.CursorEnd()
				return m, nil
			}
			m.input.SetValue("")
			m.pushInputHistory(input)
			var cmd tea.Cmd
			m, cmd = m.submitUserInput(input)
			return m, cmd
		}

	case visionCapMsg:
		// 视觉能力探测回执:更新当前会话的能力表(立刻影响后续发图走 base64 还是 OCR)+ 覆盖缓存。
		m.applyVisionCap(msg)
		return m, nil

	case balanceMsg:
		// 余额查询回执:更新右栏「模型厂商」段展示的剩余金额,并广播给 web 端动态更新。
		m.applyBalance(msg)
		m.broadcast(web.Event{Kind: "balance", Text: m.balance})
		return m, nil

	case balanceSettleTickMsg:
		// 计费结算后的延迟重探到点(issue #182)。代数对不上 = 期间又答了新的一轮、
		// 已有更晚的钟在排队 → 丢弃本次,避免连续对话攒一堆钟白打接口。
		if msg.gen != m.balanceGen {
			return m, nil
		}
		return m, balanceProbeCmd(m.models)

	case agent.VisionUnsupportedMsg:
		// 运行时自愈:某模型实际拒绝图片(agent 已自动改 OCR 重发)→ 把它标记为无视觉、纠正缓存,
		// 下次发图不再对它用 base64。
		m.applyVisionCap(visionCapMsg{key: msg.Model + "@" + msg.BaseURL, vision: false})
		return m, nil

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

	case agent.RetryNoticeMsg:
		if m.streamCh == nil {
			return m, nil
		}
		// API 失败退避重试:状态行实时显示「重试 N/10」,spinner 保持转动(对标 Claude Code 的 attempt 计数,issue #147)。
		m.status = "thinking"
		if !m.thinking {
			m.thinking = true
			cmds = append(cmds, m.spinner.Tick)
		}
		m.retryNotice = fmt.Sprintf("重试 %d/%d · %s · %ds", msg.Attempt, msg.Max, msg.Reason, int(msg.Delay.Seconds()+0.5))
		return m, tea.Batch(append(cmds, agent.ListenToStream(m.streamCh))...)

	case agent.ReasoningTokenMsg:
		// streamCh 已 nil 说明 ESC/Ctrl+C 中断过了,丢弃残留消息
		if m.streamCh == nil {
			return m, nil
		}
		// 模型在思考。默认只驱动 spinner;/thinking 打开后把 reasoning 增量暗显进 chat。
		m.retryNotice = ""
		m.status = "thinking"
		if !m.thinking {
			m.thinking = true
			cmds = append(cmds, m.spinner.Tick)
		}
		if m.showThinking {
			m.chatContent.EnsureKind(kindThinking, "")
			m.chatContent.Append(string(msg))
			m.refreshViewport()
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
		m.retryNotice = "" // 流式开始 = 重试成功,清掉提示
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
		m.retryNotice = "" // 工具开始 = 上游请求已成功,清掉重试提示
		// Todo 是可见清单的「全量快照更新」,不是一次干活动作:清单本身由下方 live overlay
		// 实时勾选,每次更新再往 chat 打一行 "📌 Todo" 是纯噪音。所以 Todo 不留 chat 行、
		// 也不计入"N 次工具调用"。其余工具照旧:紧凑单行 <icon> Name (主参数)。
		if msg.Name != "Todo" && msg.Name != "AskUser" {
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

	case agent.AskUserMsg:
		if m.streamCh == nil {
			return m, nil
		}
		// 暂停流监听,弹选择框等用户勾选(同 review 的暂停-恢复骨架)。
		m.askPending = true
		m.askCh = msg.ResponseCh
		m.askQuestions = msg.Questions
		m.askQIdx = 0
		m.askOptIdx = 0
		m.askWarn = false
		m.askSelected = make([][]bool, len(msg.Questions))
		for i, q := range msg.Questions {
			m.askSelected[i] = make([]bool, len(q.Options))
		}
		m.status = "tool"
		// 同步给浏览器:web 端也弹同样的选择框。
		m.broadcast(web.Event{Kind: "ask_request", Questions: msg.Questions})
		m.refreshViewport()
		return m, nil

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
		// spinner 自驱动:每次 tick 来都更新 frame,只要还在 thinking 就继续 Tick。
		// AskUser 的红色提示在闪烁时也需要持续 tick 驱动重渲染。
		var c tea.Cmd
		m.spinner, c = m.spinner.Update(msg)
		if m.thinking || m.compactingFG || (m.askPending && m.askWarn) {
			cmds = append(cmds, c)
		}
		// thinking 状态下需要重渲染让 spinner 帧切换可见
		m.refreshViewport()
		return m, tea.Batch(cmds...)

	case cursorBlinkTickMsg:
		// app 侧 cursor blink:每 600ms 切一次可见,View 那边读 cursorBlinkOff
		// 决定要不要把光标塞进 tea.View.Cursor。继续返回下一拍 tick,自驱动。
		// 仅 appSideCursorBlink 的终端走这条;其余恒 false,光标常驻交终端自己闪(issue #167)。
		// 注意这拍 tick 不能停 —— 下面的 codegraph 状态轮询和 workflow 计时重画都搭它的车。
		if appSideCursorBlink {
			m.cursorBlinkOff = !m.cursorBlinkOff
		}
		// 顺带轮询代码图谱状态:后台异步变化(loading→ready),变了才广播给 web,保持与 TUI 对齐。
		if m.hub != nil {
			if cg := tools.CodeGraphStatus(); cg != m.lastCgStatus {
				m.lastCgStatus = cg
				m.broadcast(web.Event{Kind: "codegraph", Text: cg})
			}
		}
		// workflow 运行中:子 agent 跑的过程没有 token 刷新,借这 600ms 心跳重画一次,
		// 让步骤/阶段的耗时实时跳秒(其余情况不必,token 自带刷新)。
		if m.streaming && m.plan != nil && !m.plan.allFinished() {
			m.refreshViewport()
		}
		return m, cursorBlinkTick()

	case copyHintClearMsg:
		// "已复制"提示到点清空(View 下一帧就不显示了)。
		m.copyHint = ""
		return m, nil

	case dockerPullDoneMsg:
		if !m.dockerPulling {
			return m, nil // 已被取消
		}
		m.dockerPulling = false
		m.dockerPullCancel = nil
		if msg.err != nil {
			m.appendChat("System", fmt.Sprintf(T("sandbox.pull_failed"), msg.err))
		} else {
			m.activateDockerSandbox(m.dockerPullImage) // 镜像就绪,正式切 docker
		}
		m.refreshViewport()
		return m, nil

	case dockerPullTickMsg:
		if !m.dockerPulling {
			return m, nil // 拉取已结束/取消,停止动画
		}
		m.dockerPullDots++
		m.refreshViewport()
		return m, dockerPullTickCmd() // 续帧

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

	case agent.CompactedMsg:
		// 单个长 turn 内自动压缩:把新摘要存进 session(每轮由 BuildSystemPrompt 注入 system 尾部)。
		// system 不入 history(会被 HistoryUpdateMsg 剥掉),不靠这条 session.summary 就丢上下文。
		// history 的截断由 agent 随后发的 HistoryUpdateMsg 同步,这里只管摘要。
		if m.streamCh == nil {
			return m, nil
		}
		m.summary = msg.Summary
		if m.session != nil {
			_ = m.session.SaveSummary(msg.Summary)
		}
		// 前缀已变,旧缓存命中数会让 cache% 失真,清零(同 compressionResultMsg)。
		if m.lastUsage != nil {
			m.lastUsage.PromptCacheHitTokens = 0
		}
		if msg.Turns > 0 {
			m.chatContent.Open(kindSystem, fmt.Sprintf("**已自动压缩会话历史（%d 轮→摘要）**", msg.Turns))
			m.refreshViewport()
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
		}
		return m, agent.ListenToStream(m.streamCh)

	case agent.PlanCreatedMsg:
		if m.streamCh == nil {
			return m, nil
		}
		m.plan = &planState{items: msg.Plans, phases: msg.Phases}
		m.planKind = msg.Kind
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

		// 检查是否需要触发会话压缩：估算 token 数接近窗口的 compactTriggerPct(80%)时触发。
		ctxWin := m.models.Pro.ContextWindow
		if ctxWin <= 0 {
			ctxWin = 65536
		}
		if m.session != nil && m.models.Pro.Model != "" && !m.compacting && m.lastPromptTokens() >= ctxWin*compactTriggerPct/100 {
			m.compacting = true
			m.compactingFG = true // 前台阻塞:同手动 /compact,footer 转 spinner + 期间挡输入。子 agent 走 runSubAgent 不经此处,天然例外。
			// 前台阻塞 + spinner;排队输入推迟到 compressionResultMsg(压缩完成后)再发。
			return m, tea.Batch(m.compactCmd(false), m.spinner.Tick)
		}

		// 影子热压:上下文跨过 shadowPoints(30/45/60%)某档时,后台预算一份 checkpoint+cut 存盘
		// (不改实时态、不阻塞用户)。仅供 >1h 冷重启用,省冷 prefill 全量历史;新覆盖旧;实时压缩落地会清掉它。
		// 不用 20% 起步:keepTarget 也是 20%,那点 history < keepTarget 必被 RunCompression 拒(白跑)。
		var shadowCmd tea.Cmd
		if m.session != nil && m.models.Pro.Model != "" && !m.compacting && !m.shadowing && ctxWin > 0 {
			// 取"已达到的最高未做档位"一次触发(pct 一跳跨过多档时不逐格补,直接打最高那档)。
			pct := m.lastPromptTokens() * 100 / ctxWin
			next := 0
			for _, p := range shadowPoints {
				if p > m.shadowDonePct && p <= pct {
					next = p
				}
			}
			if next > 0 {
				snapshot := append([]agent.ChatMessage(nil), m.history...)
				_, lastModel, lastSys, lastTools := m.session.LoadPrefixSnapshot()
				entry := m.entryForModel(lastModel)
				gen := m.compactGen
				m.shadowDonePct = next
				m.shadowing = true
				shadowCmd = func() tea.Msg {
					cp, cut, _, err := agent.RunCompression(lastSys, lastTools, snapshot, entry, ctxWin)
					return shadowResultMsg{checkpoint: cp, cut: cut, gen: gen, err: err}
				}
			}
		}

		// 本轮花了 token,余额变了:后台重探一次(仅 DeepSeek/Kimi 实际打接口,其它是 no-op)。
		balCmd := balanceProbeCmd(m.models)
		// 但供应商计费是滞后结算的,上面这次多半拿到的还是扣费前的数(issue #182:右栏比网页大、
		// 显得「特别省钱」)。所以再挂一次 balanceSettleDelay 之后的重探来校准到真实值。
		// balanceGen++ 即防抖:连续对话时每轮都重新挂钟,只有最后一轮那口钟会真的去查。
		m.balanceGen++
		settleCmd := balanceSettleCmd(m.balanceGen)

		// 没触发实时压缩:有排队输入就发下一条;影子 Cmd(若有)并行后台跑,不阻塞用户。
		if next, qcmd, ok := m.popQueuedInput(); ok {
			return next, tea.Batch(shadowCmd, qcmd, balCmd, settleCmd)
		}
		return m, tea.Batch(shadowCmd, balCmd, settleCmd)

	case agent.StreamErrMsg:
		if m.streamCh == nil {
			return m, nil
		}
		m.chatContent.Append("\n[Error: " + msg.Err.Error() + "]\n\n")
		m.status = "error"
		m.streaming = false
		m.thinking = false
		m.activeTool = ""
		m.retryNotice = "" // 重试耗尽/不可重试,落到这里报错,清掉重试提示
		m.streamCh = nil
		m.cancelAgent = nil
		m.turnElapsed = time.Since(m.turnStartedAt)
		m.refreshViewport()
		// 出错也把排队的下一条发出去(给它一次机会):瞬时错误下能继续,持续错误下级联有界、可 Esc 终止。
		if next, qcmd, ok := m.popQueuedInput(); ok {
			return next, qcmd
		}
		return m, nil

	case reviewResultMsg:
		// 审核完成,恢复流监听继续工具循环。
		// review 等待期间 API 连接断开时 streamCh 已被置 nil,此时应转入 idle。
		if m.streamCh == nil {
			return m, nil
		}
		return m, agent.ListenToStream(m.streamCh)

	case skillSearchDoneMsg:
		// /skill-add 阶段 2 完成:转阶段 3 列表(或回阶段 1 报错)
		m.skillAddSearching = false
		if msg.err != nil {
			m.skillAddErr = "搜索失败:" + msg.err.Error()
			return m, nil
		}
		if len(msg.results) == 0 {
			m.skillAddErr = fmt.Sprintf("没找到关于 %q 的 skill", msg.query)
			return m, nil
		}
		m.skillAddResults = msg.results
		m.skillAddIdx = 0
		return m, nil

	case skillInstallDoneMsg:
		// /skill-add 阶段 4 完成:关 modal,chat 出结果
		m.skillAddInstalling = false
		m.showSkillAdd = false
		m.skillAddInput.Blur()
		m.skillAddResults = nil
		m.input.Focus()
		if msg.err != nil {
			m.appendChat("System", "✗ 安装失败:"+msg.err.Error())
		} else {
			m.appendChat("System", fmt.Sprintf("✓ 已安装 skill「%s」到 ~/.deepx/skills/%s/", msg.skillName, msg.skillName))
		}
		return m, nil

	case compressionResultMsg:
		m.compacting = false
		m.compactingFG = false // 解除前台阻塞(成功/失败都解,spinner 随之停、输入恢复)
		if msg.err != nil {
			// 后台自动触发(70%/重启/空闲)失败静默 —— 不往聊天区打扰用户,下次触发会重试。
			// 但 /compact 是用户主动发起的,压不动(历史太小/轮数不足)要明确告知,否则像没反应。
			if msg.manual {
				m.chatContent.Open(kindSystem, "**压缩跳过**:"+msg.err.Error())
				m.refreshViewport()
				m.broadcast(web.Event{Kind: "notice", Text: "压缩跳过:" + msg.err.Error()})
			}
			// 压缩失败也别把排队消息卡住,照常发出下一条。
			if next, qcmd, ok := m.popQueuedInput(); ok {
				return next, qcmd
			}
			return m, nil
		}
		// 兜底:cutIdx 基于触发时的快照算;若期间 history 已被(另一次压缩/新消息)改动到
		// 比 cutIdx 短,直接丢弃这个过期结果,避免切片越界 panic。
		if msg.cutIdx < 0 || msg.cutIdx > len(m.history) {
			if next, qcmd, ok := m.popQueuedInput(); ok {
				return next, qcmd
			}
			return m, nil
		}
		// 摘要进 m.summary(每轮注入 system prompt 尾部),不再作为 history 消息;
		// history 只截断保留尾部。
		m.summary = msg.summary
		// 压缩点重建系统提示词(缓存本就会 miss),顺势刷新 AGENTS.md 快照:会话中途写入的偏好在此生效。
		agent.RefreshPreferences(m.workspace)
		m.history = append([]agent.ChatMessage(nil), m.history[msg.cutIdx:]...)

		// 锁定态:压缩可能把 /model 锁定提示对压掉,在截断后历史最前重注一对,
		// 让模型压缩后仍知道锁的是哪个模型(锁本身靠 m.modelPin,不依赖这对消息)。
		if m.modelPin == tools.RoleFlash || m.modelPin == tools.RolePro {
			lockPair := []agent.ChatMessage{
				{Role: "user", Content: "/model " + m.modelPin},
				{Role: "assistant", Content: lockedModelMsg(m.modelPin)},
			}
			m.history = append(lockPair, m.history...)
		}

		_ = m.session.SaveSummary(msg.summary)
		// 实时压缩落地:实时态(summary + 截断 history)已是更新更全的 checkpoint+tail,影子作废。
		// 清掉影子、代数 +1(丢弃期间在飞的过期影子结果)、影子档位归零(history 重新长起再积累)。
		m.compactGen++
		m.shadowDonePct = 0
		m.session.ClearShadow()
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
		m.broadcast(web.Event{Kind: "notice", Text: fmt.Sprintf("已压缩会话历史（%d 轮 → 摘要）", msg.compressedTurns)})
		// 压缩完成,现在在更小的上下文上发出排队的下一条(StreamDoneMsg 把它推迟到了这里)。
		if next, qcmd, ok := m.popQueuedInput(); ok {
			return next, qcmd
		}
		return m, nil

	case shadowResultMsg:
		// 影子热压回来:只存盘、绝不动实时态(m.history/m.summary/system prompt 都不碰)。
		m.shadowing = false
		// 失败,或期间发生过实时压缩(gen 变了 → 影子 cut 已对不上、且实时态已更优)→ 丢弃不存。
		if msg.err != nil || msg.gen != m.compactGen {
			return m, nil
		}
		// cut 落在当前 history 内(history 只追加未截断时恒成立;防御性再判)→ 存为影子。
		if msg.cut > 0 && msg.cut <= len(m.history) && m.session != nil {
			m.session.SaveShadow(msg.checkpoint, msg.cut)
		}
		return m, nil
	}

	beforeVal := m.input.Value()
	var inputCmd tea.Cmd
	m.input, inputCmd = m.input.Update(msg)
	cmds = append(cmds, inputCmd)

	// 输入框内容已更新,据新值同步 @ 文件提及选择器的缓存(激活时懒构建、退出时清空)。
	m.syncFileMention()

	// 用户敲键时强制 cursor 立刻亮,跟旧虚拟光标"打字 = 光标snappy显形"的手感一致。
	// 下一拍 600ms tick 仍按既有节奏 toggle,不 reset 时钟。
	if _, ok := msg.(tea.KeyPressMsg); ok {
		m.cursorBlinkOff = false
		// issue #113:行中插入宽字符(把右边内容向右挤)会触发 ultraviolet 行 diff 双写,
		// 在不能自愈的终端(Windows conhost/PowerShell)上残留半个旧字。强制整屏重绘绕开
		// 有问题的增量 diff。仅在「输入值实际变化 且 宽字符行中编辑态」时触发:日常行尾打字、
		// 纯光标移动都不满足,零影响。
		if m.input.Value() != beforeVal && m.inputWideMidLine() {
			cmds = append(cmds, tea.ClearScreen)
		}
	}

	return m, tea.Batch(cmds...)
}

// inputWideMidLine 判断输入框是否处于会触发 ultraviolet 宽字符 diff 双写的状态:
// 当前行含宽字符,且光标右边同一行还有内容(即行中编辑,而非行尾追加)。
// 这是 issue #113 残影的必要条件;满足时配合强制重绘规避。
func (m model) inputWideMidLine() bool {
	val := m.input.Value()
	if val == "" {
		return false
	}
	lines := strings.Split(val, "\n")
	li := m.input.Line()
	if li < 0 || li >= len(lines) {
		return false
	}
	line := lines[li]
	// 本行无宽字符(显示宽度 == rune 数)→ 不可能触发。
	if ansi.StringWidthWc(line) <= len([]rune(line)) {
		return false
	}
	// 光标右边同行还有内容(行尾追加不触发 bug)。
	return m.input.Column() < len([]rune(line))
}

// syncFileMention 据当前输入值同步 @ 文件提及选择器状态:
//   - 处于提及态且缓存为空 → 遍历 workspace 构建文件列表缓存
//   - 退出提及态 → 清空缓存与选中索引(下次 @ 重新遍历,自动拾取新增文件)
func (m *model) syncFileMention() {
	_, _, _, active := fileMentionContext(m.input.Value(), m.input.Line(), m.input.Column())
	if !active || strings.HasPrefix(m.input.Value(), "/") {
		m.fileMentionCache = nil
		m.fileMentionIdx = 0
		return
	}
	if m.fileMentionCache == nil {
		wd, err := os.Getwd()
		if err == nil {
			m.fileMentionCache = listWorkspaceFiles(wd)
		}
	}
}

// buildUserMessage 构造发给 LLM 的用户消息:解析 @ 文件引用,并附带已落盘图片的路径。
// webURLExposed 判断 web 面板地址是否对外可达(非回环):用于决定是否打安全警告。
// localhost / 127.x / ::1 视为仅本机;其余 IP(局域网/公网)视为已暴露。
func webURLExposed(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "" || host == "localhost" {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false // 非 IP(自定义域名)无从判断,不打警告,避免误报
	}
	return !ip.IsLoopback()
}

func (m model) buildUserMessage(text string) agent.ChatMessage {
	if wd, err := os.Getwd(); err == nil {
		text = resolveFileMentions(text, wd)
	}
	// 钉上提交当轮的工作模式:发送时按这个标签渲染后缀,切模式不改写历史 → 前缀缓存稳定。
	// gob 持久化,重启后原样恢复(见 ChatMessage.WorkingMode / renderWorkingMode)。
	if len(m.attachedImagePaths) == 0 {
		return agent.ChatMessage{Role: "user", Content: text, WorkingMode: m.workingMode}
	}
	return agent.ChatMessage{
		Role:        "user",
		Content:     text,
		ImagePaths:  append([]string(nil), m.attachedImagePaths...),
		WorkingMode: m.workingMode,
	}
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

// insertImagePlaceholder 在输入框当前光标位置插入第 n 张图的引用。
func (m *model) insertImagePlaceholder(n int) {
	m.input.InsertString(fmt.Sprintf(" [Image #%d] ", n))
}

// reconcileAttachedImages 按输入框文本里还残留哪些 [Image #N] 占位符,裁剪待发图片列表。
// 占位符编号 N 对应 attached[N-1](insertImagePlaceholder 插入时 N = 该图在列表里的序号,
// 列表只增不减直到发送,故编号与下标始终对齐)。用户删掉某个 [Image #N] 文本 = 想移除那张图,
// 这里把没了占位符的图从待发列表剔除 —— 让「删占位符」真正等于「不发那张图」(issue #146)。
func reconcileAttachedImages(text string, attached []string) []string {
	if len(attached) == 0 {
		return attached
	}
	present := make(map[int]bool)
	for _, mt := range imagePlaceholderRe.FindAllStringSubmatch(text, -1) {
		var n int
		if _, err := fmt.Sscanf(mt[1], "%d", &n); err == nil {
			present[n] = true
		}
	}
	kept := make([]string, 0, len(attached))
	for i, p := range attached {
		if present[i+1] {
			kept = append(kept, p)
		}
	}
	return kept
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
// chatDisplayText 取一条消息用于"对话区显示"的文本:优先 Content,否则从 ContentParts 拼出文本、图片用 [图片] 表示。
func chatDisplayText(msg agent.ChatMessage) string {
	if msg.Content != "" {
		return msg.Content
	}
	var sb strings.Builder
	for _, p := range msg.ContentParts {
		var seg string
		switch p.Type {
		case "text":
			seg = p.Text
		case "image_url":
			seg = "[图片]"
		}
		if seg == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString(" ")
		}
		sb.WriteString(seg)
	}
	return sb.String()
}

func rebuildChatFromHistory(cl *chatLog, history []agent.ChatMessage) {
	for _, msg := range history {
		switch msg.Role {
		case "user":
			if t := chatDisplayText(msg); t != "" {
				cl.Open(kindUser, t)
			}
		case "assistant":
			if t := chatDisplayText(msg); t != "" {
				cl.Open(kindAssistant, t)
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
	if strings.HasPrefix(cmd, "/model") { // 带参数,单独解析
		return m.handleModelCommand(cmd)
	}
	if strings.HasPrefix(cmd, "/sandbox") { // 带参数(native/docker [image]),传原文(镜像名别被小写化)
		return m.handleSandboxCommand(input)
	}
	if strings.HasPrefix(cmd, "/working-mode") { // 带参数(kp/openspec/sp)
		return m.handleWorkingModeCommand(cmd)
	}
	if strings.HasPrefix(cmd, "/session-rename") { // 带参数:新标题(保留大小写,传原文)
		return m.handleSessionRenameCommand(input)
	}
	if strings.HasPrefix(cmd, "/session-delete") { // 删当前会话
		return m.handleSessionDeleteCommand()
	}
	if strings.HasPrefix(cmd, "/ultracode") { // /ultracode <描述> → 让模型创建一个 workflow(对齐 Claude Code)
		return m.handleUltracodeCommand(input)
	}
	if strings.HasPrefix(cmd, "/workflow") { // /workflows(列表) 或 /workflow <名字> [k=v…](保留原文大小写)
		return m.handleWorkflowCommand(input)
	}
	if strings.HasPrefix(cmd, "/provider") { // 裸 /provider 弹选择器,或 /provider <名字> 直切
		return m.handleProviderCommand(cmd)
	}
	switch cmd {
	case "/plan":
		m.applyMode(agent.AgentMode_Plan)
	case "/auto":
		m.applyMode(agent.AgentMode_Auto)
	case "/review":
		m.applyMode(agent.AgentMode_Review)
	case "/mode":
		m.appendChat("assistant", fmt.Sprintf(T("mode.show"), m.mode))
	case "/config":
		m.openSetupModal()
	case "/skills":
		m.appendChat("assistant", m.skillsListMessage())
	case "/skill-add":
		m.openSkillAddModal()
	case "/skill-delete":
		m.openSkillDeleteModal()
	case "/mcp-list":
		m.appendChat("System", m.mcpListMessage())
	case "/mcp-add":
		m.openMcpAddModal()
	case "/mcp-delete":
		m.openMcpDeleteModal()
	case "/web-config":
		m.openWebConfigModal()
	case "/reasoning":
		m.openReasoningModal()
	case "/lang":
		m.showLangModal = true
		// 默认光标停在当前语言上
		m.langModalIdx = 0
		if CurrentLang() == LangEN {
			m.langModalIdx = 1
		}
	case "/compact":
		return m.startManualCompaction()
	case "/status":
		m.toggleStatusPanel()
	case "/thinking":
		m.toggleThinking()
	case "/new":
		m.startNewConversation()
	case "/sessions":
		m.openSessionListModal()
	case "/undo":
		m.undoLastTurn()
	case "/help":
		m.appendChat("assistant", T("help.body"))
	case "/exit":
		return tea.Quit // 优雅退出:会话状态每轮已落盘,直接退即可
	default:
		m.appendChat("assistant", fmt.Sprintf(T("mode.unknown_cmd"), cmd))
	}
	return nil
}

// undoLastTurn 撤销最近一轮对话(类似 Claude Code 的 /undo):从 history 里删掉最后一条
// user 消息及其后的所有消息(assistant 回复 / 工具调用),重建显示并落盘 gob,再把被撤销的
// 用户输入回填到空输入框,方便编辑后重发。
//   - 流式进行中拒绝(先 Esc 停)——否则会和正在写 history 的流竞争。
//   - jsonl 是 append-only 的检索/兜底通道,不回写;权威恢复源 history.gob 已更新即可。
func (m *model) undoLastTurn() {
	if m.streaming {
		m.appendChat("System", T("undo.streaming"))
		return
	}
	last := -1
	for i := len(m.history) - 1; i >= 0; i-- {
		if m.history[i].Role == "user" {
			last = i
			break
		}
	}
	if last < 0 {
		m.appendChat("System", T("undo.nothing"))
		return
	}
	undone := m.history[last].Content
	m.history = m.history[:last]
	if m.session != nil {
		_ = m.session.SaveGob("history.gob", m.history)
	}
	// 显示重建:清空后按截断后的 history 重放,再叠一行撤销提示。
	m.chatContent.Reset()
	rebuildChatFromHistory(m.chatContent, m.history)
	// 原输入回填(仅当输入框为空,不覆盖用户已敲入的新内容)。
	if strings.TrimSpace(m.input.Value()) == "" && undone != "" {
		m.input.SetValue(undone)
	}
	m.appendChat("System", T("undo.done"))
	m.refreshViewport()
}

// activateDockerSandbox 正式切到 docker 沙箱:设模式 + 记忆 + 提示。镜像已就绪后调用。
func (m *model) activateDockerSandbox(image string) {
	tools.SetSandboxMode(tools.SandboxDocker)
	metaUpdate(func(mm *meta) {
		mm.Sandbox = string(tools.SandboxDocker)
		mm.SandboxDockerImage = image
	})
	m.appendChat("assistant", fmt.Sprintf(T("sandbox.switched_docker"), image))
	m.broadcast(web.Event{Kind: "sandbox", Text: string(tools.SandboxDocker)})
}

// handleWorkingModeCommand 处理 /working-mode [kp|openspec|sp]:
// 无参 → 显示当前模式;有参 → 切换、存进当前 session(切会话会同步)、给出提示。
func (m *model) handleWorkingModeCommand(cmd string) tea.Cmd {
	fields := strings.Fields(cmd)
	if len(fields) < 2 {
		// 无参 → 弹窗选择,光标落在当前模式上。
		m.showWorkingModeModal = true
		m.workingModeModalIdx = workingModeIndex(m.workingMode)
		m.input.Blur()
		return nil
	}
	switch strings.ToLower(fields[1]) {
	case "kp", "karpathy", "spec", "openspec", "sp", "superpowers":
		m.applyWorkingMode(agent.NormalizeWorkingMode(fields[1]))
	default:
		m.appendChat("assistant", fmt.Sprintf(T("workingmode.unknown"), fields[1]))
	}
	return nil
}

// handleSessionRenameCommand 处理 /session-rename <新标题>:重命名当前会话(保留大小写)。
func (m *model) handleSessionRenameCommand(input string) tea.Cmd {
	rest := strings.TrimSpace(input)
	if i := strings.IndexAny(rest, " \t"); i >= 0 {
		rest = strings.TrimSpace(rest[i:])
	} else {
		rest = ""
	}
	if rest == "" {
		m.appendChat("assistant", T("session.rename.usage"))
		return nil
	}
	if m.session != nil {
		_ = m.session.RenameConversation(m.session.CurrentConversation(), rest)
		m.broadcastSessions()
		m.appendChat("System", fmt.Sprintf(T("session.renamed"), rest))
	}
	return nil
}

// handleSessionDeleteCommand 处理 /session-delete:弹出会话列表,选中后删除(而非直接删当前)。
func (m *model) handleSessionDeleteCommand() tea.Cmd {
	if m.streaming {
		m.appendChat("System", T("session.streaming"))
		return nil
	}
	m.openSessionListModal()
	m.sessionListDelete = true // 复用 /sessions 弹窗,Enter 改为删除选中项
	return nil
}

// submitSessionDelete 删除弹窗里选中的会话(默认会话不可删;删的是当前会话则切回默认并重载)。
func (m *model) submitSessionDelete() {
	defer func() { m.showSessionList = false; m.sessionListDelete = false; m.input.Focus() }()
	if m.streaming {
		m.appendChat("System", T("session.streaming"))
		return
	}
	if m.sessionListIdx < 0 || m.sessionListIdx >= len(m.sessionConvs) {
		return
	}
	target := m.sessionConvs[m.sessionListIdx]
	wasCurrent := target.Active
	if err := m.session.DeleteConversation(target.ID); err != nil {
		m.appendChat("System", T("session.delete.cant_default"))
		return
	}
	if wasCurrent {
		m.loadCurrentConversation() // DeleteConversation 已切回默认,重载并广播
	} else {
		m.broadcastSessions()
	}
	title := target.Title
	if title == "" {
		title = T("session.untitled")
	}
	m.appendChat("System", fmt.Sprintf(T("session.deleted_named"), title))
}

// workingModeOrder 是弹窗里工作模式的展示顺序,与 workingModeModalIdx 对应。
var workingModeOrder = []agent.WorkingMode{
	agent.WorkingModeKarpathy,
	agent.WorkingModeOpenSpec,
	agent.WorkingModeSuperpowers,
}

// workingModeIndex 返回某模式在弹窗里的行号,未知则落到默认(0)。
func workingModeIndex(mode agent.WorkingMode) int {
	for i, mm := range workingModeOrder {
		if mm == mode {
			return i
		}
	}
	return 0
}

// applyWorkingMode 切换工作模式、写进当前 session,给一条提示,并广播给 web。
// 终端命令 / 弹窗 / web 按钮共用此入口,故两边状态对齐。
func (m *model) applyWorkingMode(mode agent.WorkingMode) {
	m.workingMode = mode
	if m.session != nil {
		m.session.SaveWorkingMode(string(mode))
	}
	m.appendChat("assistant", fmt.Sprintf(T("workingmode.switched"), mode))
	m.broadcast(web.Event{Kind: "working_mode", Text: string(mode)})
}

// handleSandboxCommand 处理 /sandbox [off|native|docker [image]]:
// 无参 → 显示当前模式;off → 关闭沙箱;native → OS 隔离/软策略;docker [image] → 探测 Docker 后切容器。
// input 是原始输入(未小写化),镜像名大小写敏感。
func (m *model) handleSandboxCommand(input string) tea.Cmd {
	// 去掉前缀 /sandbox(大小写不敏感),其余按空白切。
	rest := strings.TrimSpace(input)
	if i := strings.IndexAny(rest, " \t"); i >= 0 {
		rest = strings.TrimSpace(rest[i:])
	} else {
		rest = ""
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		// 无参 → 弹窗选择,光标落在当前模式上。
		m.openSandboxModal()
		return nil
	}
	switch strings.ToLower(fields[0]) {
	case "off":
		return m.applySandboxMode(tools.SandboxOff)
	case "native":
		return m.applySandboxMode(tools.SandboxNative)
	case "docker":
		if len(fields) >= 2 { // /sandbox docker <image>
			tools.SetSandboxDockerImage(fields[1])
		}
		return m.applySandboxMode(tools.SandboxDocker)
	default:
		m.appendChat("assistant", fmt.Sprintf(T("sandbox.unknown"), fields[0]))
	}
	return nil
}

// sandboxModeOrder 是弹窗里沙箱模式的展示顺序,与 sandboxModalIdx 对应。
var sandboxModeOrder = []tools.SandboxMode{
	tools.SandboxNative,
	tools.SandboxOff,
	tools.SandboxDocker,
}

// sandboxModeIndex 返回某模式在弹窗里的行号,未知则落到默认(0:native)。
func sandboxModeIndex(mode tools.SandboxMode) int {
	for i, mm := range sandboxModeOrder {
		if mm == mode {
			return i
		}
	}
	return 0
}

// openSandboxModal 打开沙箱选择弹窗,光标停在当前模式。
func (m *model) openSandboxModal() {
	m.sandboxModalIdx = sandboxModeIndex(tools.CurrentSandboxMode())
	m.showSandboxModal = true
	m.input.Blur()
}

// applySandboxMode 切到指定沙箱模式。off/native 即时生效;docker 需探测 + (按需异步拉镜像),
// 拉取时返回进度动画 cmd。命令行带参和弹窗两条路径共用此函数。
func (m *model) applySandboxMode(mode tools.SandboxMode) tea.Cmd {
	switch mode {
	case tools.SandboxOff:
		tools.SetSandboxMode(tools.SandboxOff)
		metaUpdate(func(mm *meta) { mm.Sandbox = string(tools.SandboxOff) })
		m.appendChat("assistant", T("sandbox.switched_off"))
		m.broadcast(web.Event{Kind: "sandbox", Text: string(tools.SandboxOff)})
	case tools.SandboxNative:
		tools.SetSandboxMode(tools.SandboxNative)
		metaUpdate(func(mm *meta) { mm.Sandbox = string(tools.SandboxNative) })
		if tools.NativeIsolationActive() {
			m.appendChat("assistant", T("sandbox.switched_native_os"))
		} else {
			m.appendChat("assistant", T("sandbox.switched_native_soft"))
		}
		m.broadcast(web.Event{Kind: "sandbox", Text: string(tools.SandboxNative)})
	case tools.SandboxDocker:
		if err := tools.DockerAvailable(); err != nil {
			m.appendChat("System", fmt.Sprintf(T("sandbox.docker_unavailable"), err))
			return nil
		}
		image := tools.SandboxDockerImage()
		if tools.ImagePresent(image) {
			// 镜像已在本地 → 直接切,无需拉取/进度条
			m.activateDockerSandbox(image)
			return nil
		}
		// 镜像不在本地 → 异步拉取,对话区显示拉取动画;拉完才正式切到 docker
		if m.dockerPulling {
			return nil // 已在拉,别重复
		}
		ctx, cancel := context.WithCancel(context.Background())
		m.dockerPullCancel = cancel
		m.dockerPulling = true
		m.dockerPullImage = image
		m.dockerPullDots = 0
		m.refreshViewport()
		// 一边等拉取结果,一边跑省略号动画
		return tea.Batch(waitDockerPull(tools.PullImage(ctx, image)), dockerPullTickCmd())
	}
	return nil
}

// handleModelCommand 处理 /model:无参数(或参数不认识)→ 弹窗选择;
// /model auto|flash|pro 带有效参数 → 直接应用(快捷方式)。
func (m *model) handleModelCommand(cmd string) tea.Cmd {
	fields := strings.Fields(cmd)
	if len(fields) < 2 {
		m.openModelModal()
		return nil
	}
	switch fields[1] {
	case "auto", tools.RoleFlash, tools.RolePro:
		m.applyModelPin(fields[1])
	default:
		m.openModelModal() // 参数不认识也走弹窗,弹窗本身就是引导
	}
	return nil
}

// openModelModal 打开模型选择弹窗,光标停在当前锁定项。
func (m *model) openModelModal() {
	m.modelModalIdx = 0 // auto
	switch m.modelPin {
	case tools.RoleFlash:
		m.modelModalIdx = 1
	case tools.RolePro:
		m.modelModalIdx = 2
	}
	m.showModelModal = true
}

// applyModelPin 应用模型选择:设 m.modelPin;锁定 flash/pro 时插入一对 user/assistant
// 锁定提示(让模型从上下文知道当前锁的是哪个),auto 恢复关键词路由。
// 锁的强制力在 StartStream(forceRole)+SwitchModel 闸门,不依赖这对消息。
func (m *model) applyModelPin(arg string) {
	switch arg {
	case "auto":
		m.modelPin = "auto"
		// auto 起手走 flash,状态区先反映 flash;下一轮按 ModelSwitchMsg 再校正。
		m.setActiveModel(tools.RoleFlash)
		msg := T("model.unlocked")
		m.history = append(m.history, agent.ChatMessage{Role: "assistant", Content: msg})
		m.appendChat("assistant", msg)
		if m.session != nil {
			_ = m.session.Append("assistant", msg)
		}
	case tools.RoleFlash, tools.RolePro:
		m.modelPin = arg
		m.setActiveModel(arg) // 状态区即时切到锁定的模型
		m.appendModelLockPair(arg)
	default:
		return
	}
	// 持久化到**当前子会话**的 state.json(issue #43:/new 各记各的,/sessions 切换时恢复)。
	// auto 也写,覆盖旧的 flash/pro 锁定。
	if m.session != nil {
		m.session.SaveModelPin(m.modelPin)
	}
	m.broadcast(web.Event{Kind: "routing", Text: m.modelPin})
}

// restoreModelPin 把从 session 读出的 /model 选择应用到内存(不写盘、不插提示对)。
// flash/pro 且对应模型已配置 → 锁定并即时切右栏显示;否则回退 auto(右栏起手 flash,
// flash 未配置则 pro)。供 initialModel 启动恢复与 loadCurrentConversation 切会话恢复共用。
func (m *model) restoreModelPin(pin string) {
	switch {
	case pin == tools.RoleFlash && m.models.Flash.Model != "":
		m.modelPin = tools.RoleFlash
		m.setActiveModel(tools.RoleFlash)
	case pin == tools.RolePro && m.models.Pro.Model != "":
		m.modelPin = tools.RolePro
		m.setActiveModel(tools.RolePro)
	default:
		m.modelPin = "auto"
		if m.models.Flash.Model != "" {
			m.setActiveModel(tools.RoleFlash)
		} else {
			m.setActiveModel(tools.RolePro)
		}
	}
}

// setActiveModel 立即把状态区显示的活跃模型切到 role,使 /model 选择即时反映在右栏。
func (m *model) setActiveModel(role string) {
	m.activeModelRole = role
	if role == tools.RolePro {
		m.activeModelID = m.models.Pro.Model
	} else {
		m.activeModelID = m.models.Flash.Model
	}
}

// appendModelLockPair 写入一对锁定提示(user: /model X + assistant: 已锁定 X),
// 同步进 history(LLM 上下文)、聊天显示、session。压缩后由 compressionResultMsg 重注。
func (m *model) appendModelLockPair(role string) {
	userMsg := "/model " + role
	botMsg := lockedModelMsg(role)
	m.history = append(m.history,
		agent.ChatMessage{Role: "user", Content: userMsg},
		agent.ChatMessage{Role: "assistant", Content: botMsg},
	)
	m.appendChat("user", userMsg)
	m.appendChat("assistant", botMsg)
	if m.session != nil {
		_ = m.session.Append("user", userMsg)
		_ = m.session.Append("assistant", botMsg)
	}
}

// lockedModelMsg 返回"已锁定 X 模型"的提示文案。
func lockedModelMsg(role string) string {
	return fmt.Sprintf(T("model.locked"), role)
}

// compactCmd 构造一次"压缩 history → checkpoint"的后台 Cmd —— 手动 /compact 与自动 80% 触发共用。
// 拍 history 快照、复刻上次实际发送的 model/system/tools(命中热缓存);manual 供结果处理区分
// (失败时是否提示用户)。调用方负责置 m.compacting/m.compactingFG 与启动 spinner。
func (m model) compactCmd(manual bool) tea.Cmd {
	ctxWin := m.models.Pro.ContextWindow
	if ctxWin <= 0 {
		ctxWin = 65536
	}
	snapshot := append([]agent.ChatMessage(nil), m.history...)
	_, lastModel, lastSys, lastTools := m.session.LoadPrefixSnapshot()
	entry := m.entryForModel(lastModel)
	return func() tea.Msg {
		summary, cutIdx, turns, err := agent.RunCompression(lastSys, lastTools, snapshot, entry, ctxWin)
		return compressionResultMsg{summary: summary, cutIdx: cutIdx, compressedTurns: turns, err: err, manual: manual}
	}
}

// startManualCompaction 处理 /compact:手动触发会话压缩,仍按 context_window × 20% 保留尾部。
// 与自动 80% 触发(StreamDoneMsg 里)走同一套 compactCmd + compressionResultMsg 流程,
// 区别只在于不看 token 阈值——用户敲了就压。压不动(历史太小)由 RunCompression 返回 err,
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
	m.compacting = true
	m.compactingFG = true // 前台阻塞:footer 转 spinner、期间挡输入(compressionResultMsg 里清)
	m.appendChat("System", "正在压缩会话历史…")
	// 启动 spinner tick,让 footer 的「压缩中…」动起来(TickMsg 处理处会在 compactingFG 时续 tick)。
	return tea.Batch(m.compactCmd(true), m.spinner.Tick)
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

// workflowsListMessage 列出可用 workflow(name + scope + 描述 + 路径);空则给建目录提示。
func (m *model) workflowsListMessage() string {
	if m.workflowLoader == nil {
		return "workflow 系统未启用"
	}
	metas := m.workflowLoader.List()
	if len(metas) == 0 {
		var dirs strings.Builder
		for _, d := range m.workflowLoader.AllDirs() {
			fmt.Fprintf(&dirs, "  - %s\n", d)
		}
		return "暂无 workflow。在以下任一目录创建 <name>.js 即可:\n" + dirs.String() +
			"\n脚本格式:\n" +
			"  export const meta = { name: \"my-flow\", description: \"做什么\" };\n" +
			"  export default async function main(args) {\n" +
			"    phase(\"Step\"); return await agent(\"做点事\");\n" +
			"  }\n" +
			"\n运行:/workflow my-flow key=value"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "可用 workflow (%d 个),用 /workflow <名字> [键=值 …] 运行:\n\n", len(metas))
	for _, wm := range metas {
		fmt.Fprintf(&sb, "**%s** _(%s)_\n  %s\n  %s\n\n", wm.Name, wm.Scope, wm.Description, wm.Path)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// handleWorkflowCommand 分发 /workflows(列表)与 /workflow <名字> [k=v …](运行)。
func (m *model) handleWorkflowCommand(input string) tea.Cmd {
	fields := strings.Fields(strings.TrimSpace(input))
	head := strings.ToLower(fields[0])
	// /workflows 或裸 /workflow(没给名字)→ 列表
	if head == "/workflows" || len(fields) < 2 {
		m.appendChat("assistant", m.workflowsListMessage())
		return nil
	}
	name := fields[1]
	args := parseWorkflowArgs(fields[2:])
	return m.startWorkflowTurn(input, name, args)
}

// handleUltracodeCommand 处理 /ultracode <描述>:起一个普通回合,让模型按 creating-workflows 技能
// 为该需求创建并保存一个 workflow(对齐 Claude Code 的 ultracode)。之后用 /workflow <名字> 运行。
func (m *model) handleUltracodeCommand(input string) tea.Cmd {
	fields := strings.Fields(strings.TrimSpace(input))
	desc := strings.TrimSpace(strings.Join(fields[1:], " "))
	if desc == "" {
		m.appendChat("System", "用法:/ultracode <描述>(据此生成并保存一个 workflow,之后用 /workflow <名字> 运行)")
		return nil
	}
	prompt := "使用 `creating-workflows` 技能,为下面的需求创建一个 workflow:取一个合适的 kebab-case 名字," +
		"调用 Workflow 工具(action=create)保存。创建完成后,用一句话告诉我 workflow 名字以及用 `/workflow <名字>` 运行。\n\n需求:" + desc
	nm, cmd := m.submitUserInput(prompt)
	*m = nm
	return cmd
}

// parseWorkflowArgs 把 ["k=v", "a=b"] 解析成 map;若只有一个无 = 的 token,则原样作为字符串 args。
func parseWorkflowArgs(toks []string) any {
	if len(toks) == 0 {
		return nil
	}
	if len(toks) == 1 && !strings.Contains(toks[0], "=") {
		return toks[0]
	}
	out := map[string]string{}
	for _, t := range toks {
		if i := strings.IndexByte(t, '='); i > 0 {
			out[t[:i]] = t[i+1:]
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// startWorkflowTurn 加载并运行一个 workflow,作为一个普通助手回合(复用流式 + 落盘)。
func (m *model) startWorkflowTurn(rawInput, name string, args any) tea.Cmd {
	if m.streaming || m.compactingFG {
		m.appendChat("System", "正忙,稍后再运行 workflow")
		return nil
	}
	script, err := m.workflowLoader.Load(name)
	if err != nil {
		m.appendChat("System", "加载 workflow 失败:"+err.Error())
		return nil
	}

	cmdText := strings.TrimSpace(rawInput)
	m.appendChat("You", cmdText)
	m.history = append(m.history, m.buildUserMessage(cmdText))
	m.pendingUserText = cmdText

	m.plan = nil
	m.planKind = ""
	m.status = "thinking"
	m.streaming = true
	m.thinking = true
	m.currentReply.Reset()
	m.turnStartedAt = time.Now()
	m.turnInputChars = sumHistoryChars(m.history)
	m.turnOutputChars = 0
	m.turnToolCalls = 0
	m.activeTool = ""

	workspace, _ := os.Getwd()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelAgent = cancel
	models := m.models
	models.Flash.Vision = m.visionByModel[modelCapKey(models.Flash)]
	models.Pro.Vision = m.visionByModel[modelCapKey(models.Pro)]

	cmd, ch := agent.StartWorkflow(ctx, models, m.history, m.mode, workspace, m.skillCatalog, script, args)
	m.streamCh = ch
	m.refreshViewport()
	return tea.Batch(m.spinner.Tick, cmd)
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
// backslashSentinel 是渲染期临时替换 `\.` 中反斜杠的私有区码点(U+E000),goldmark 不会把它当转义符,
// 宽度也是 1(与 `\` 一致,不影响换行计算),渲染后再还原。正文里几乎不可能出现该码点。
const backslashSentinel = "\uE000"

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
	// 只保护 `\.`:goldmark 会把 `\.` 当转义点吃掉反斜杠,Windows 路径
	// C:\Users\…\.deepx 会丢成 C:\Users\….deepx。送进渲染器前把 `\.` 里的反斜杠换成私有区哨兵
	// (goldmark 原样透传、不触发转义),渲染完再换回。其它转义(`\*` 等)保持 markdown 原义。
	content = strings.ReplaceAll(content, "\\.", backslashSentinel+".")
	out, err := r.Render(content)
	if err != nil {
		return strings.ReplaceAll(content, backslashSentinel, "\\")
	}
	out = strings.ReplaceAll(out, backslashSentinel, "\\")
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
	// 亮色终端用 glamour 的 LightStyleConfig(深色前景),暗色用 DarkStyleConfig,
	// 否则助手 markdown 在白底上是浅色前景、几乎看不见(issue #163)。
	style := styles.DarkStyleConfig // value copy
	if !darkBackground {
		style = styles.LightStyleConfig
	}
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
		// 用户回合走气泡(左块条 + 整段底色),不走 glamour / 色条,见 renderUserBubble。
		if kind == kindUser {
			return renderUserBubble(stripVS16(strings.TrimRight(ensureEmojiSpacing(raw), "\n")), width)
		}
		var inner string
		switch kind {
		case kindTools:
			inner = colorizeDiffBlock(ensureEmojiSpacingANSI(ensureEmojiSpacing(raw)))
		case kindThinking:
			inner = dimThinking(ensureEmojiSpacing(raw), barInnerWidth(width, kind))
		default:
			inner = ensureEmojiSpacingANSI(m.renderMarkdown(ensureEmojiSpacing(raw), barInnerWidth(width, kind)))
		}
		inner = stripVS16(strings.TrimRight(inner, "\n"))
		return applyQuoteBar(inner, kind)
	})

	// plan / spinner 是临时态(只在 streaming 期间显示),不另外画色条避免跟最后一段
	// 的 ╰ 视觉重复。简单缩进 2 列,让它视觉上像是当前段的"延续"。
	// 一旦所有节点跑完,overlay 就藏起来,让屏幕让给模型后续的总结/继续输出 ——
	// 否则 checkbox 列表会和流式 token 视觉上混在一起。
	if m.plan != nil && m.streaming && !m.plan.allFinished() {
		content += "\n" + indentBlock(renderPlanForChat(m.plan), "  ")
	}
	// docker 镜像拉取动画:拉取期间挂在对话区末尾,随 tick 刷新省略号;拉完即撤(dockerPulling 置回 false)。
	if m.dockerPulling {
		content += "\n" + dockerPullText(m.dockerPullImage, m.dockerPullDots)
	}
	// AskUser 选择题:待答时把交互卡片挂在对话区末尾(同 plan/docker 套路),不再用居中浮层。
	// 这样卡片随会话一起滚动,用户可 PgUp/滚轮回看历史;答完后会折叠成 kindSystem 档案段留痕(issue #134)。
	if m.askPending {
		content += "\n" + m.askUserBlock()
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

// pushInputHistory 将一条已发送的消息加入输入历史。
// 去重：与上一条相同则跳过；上限 100 条防止内存无限增长。
func (m *model) pushInputHistory(text string) {
	if len(m.inputHistory) > 0 && m.inputHistory[len(m.inputHistory)-1] == text {
		return
	}
	m.inputHistory = append(m.inputHistory, text)
	if len(m.inputHistory) > 100 {
		m.inputHistory = m.inputHistory[len(m.inputHistory)-100:]
	}
	m.inputHistoryIndex = -1
	m.inputDraft = ""
}

// navigateHistoryUp 翻阅上一条（更旧的）历史消息。
func (m *model) navigateHistoryUp() {
	if len(m.inputHistory) == 0 {
		return
	}
	if m.inputHistoryIndex < 0 {
		// 首次进入翻阅：保存当前草稿
		m.inputDraft = m.input.Value()
		m.inputHistoryIndex = len(m.inputHistory) - 1
	} else if m.inputHistoryIndex > 0 {
		m.inputHistoryIndex--
	} else {
		return // 已是最旧的一条
	}
	m.input.SetValue(m.inputHistory[m.inputHistoryIndex])
	m.input.CursorEnd()
}

// navigateHistoryDown 翻阅下一条（更新的）历史消息；到底则恢复草稿并退出翻阅。
func (m *model) navigateHistoryDown() {
	if m.inputHistoryIndex < 0 {
		return
	}
	if m.inputHistoryIndex < len(m.inputHistory)-1 {
		m.inputHistoryIndex++
		m.input.SetValue(m.inputHistory[m.inputHistoryIndex])
		m.input.CursorEnd()
	} else {
		// 已翻到最新（或超出）：恢复草稿，退出翻阅态
		m.input.SetValue(m.inputDraft)
		m.input.CursorEnd()
		m.inputHistoryIndex = -1
		m.inputDraft = ""
	}
}
