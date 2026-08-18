package tools

import "encoding/json"

// 模型角色常量:标识本轮用 flash 还是 pro。仅用于入口路由、SwitchModel、以及 UI 的活动模型
// 指示;不再用于工具可见性过滤(所有角色的工具表一致,保前缀缓存)。
const (
	RoleFlash = "flash" // 默认起手模型,便宜快
	RolePro   = "pro"   // 升级后的强模型
)

// Tool 工具定义
type Tool struct {
	Name        string                               `json:"name"`
	Description string                               `json:"description"`
	Parameters  ToolParam                            `json:"parameters"`
	Executor    func(args map[string]any) ToolResult `json:"-"`
	ReadOnly    bool                                 `json:"-"`
}

// ToolParam 工具参数 schema（OpenAI function calling 格式）
type ToolParam struct {
	Type       string             `json:"type"`
	Properties map[string]PropDef `json:"properties"`
	Required   []string           `json:"required,omitempty"`
}

// PropDef 参数定义
type PropDef struct {
	Type        string         `json:"type"`
	Description string         `json:"description,omitempty"`
	Items       map[string]any `json:"items,omitempty"`
	Enum        []string       `json:"enum,omitempty"`
}

// ToolResult 调用结果
type ToolResult struct {
	Output  string
	Success bool
}

// OpenAIToolSpec 用于序列化为 OpenAI function calling 协议要求的 JSON。
type OpenAIToolSpec struct {
	Type     string             `json:"type"` // 总是 "function"
	Function OpenAIFunctionSpec `json:"function"`
}

type OpenAIFunctionSpec struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Parameters  ToolParam `json:"-"`
	// RawParameters 非空时直接作为 "parameters" 发送(用于把任意 JSON Schema 原样当工具参数,
	// 如 structured_output 工具——schema 由用户脚本给出,形状任意,ToolParam 表达不了)。
	RawParameters json.RawMessage `json:"-"`
}

// MarshalJSON:RawParameters 非空则原样作 parameters,否则按 ToolParam 序列化(现有工具不变)。
func (f OpenAIFunctionSpec) MarshalJSON() ([]byte, error) {
	params := f.RawParameters
	if len(params) == 0 {
		b, err := json.Marshal(f.Parameters)
		if err != nil {
			return nil, err
		}
		params = b
	}
	return json.Marshal(struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	}{f.Name, f.Description, params})
}

// UnmarshalJSON:与 MarshalJSON 对称。Parameters/RawParameters 都是 json:"-",
// 若无此方法,反序列化(如压缩时从快照还原 specs)会丢掉 parameters,导致
// 重新 marshal 出 {"type":"","properties":null},API 报 "null is not of type object"。
// 这里把 parameters 原样收进 RawParameters,重新 marshal 时逐字节还原,保前缀缓存命中。
func (f *OpenAIFunctionSpec) UnmarshalJSON(data []byte) error {
	var aux struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	f.Name = aux.Name
	f.Description = aux.Description
	f.RawParameters = aux.Parameters
	return nil
}

// ToOpenAISpec 转换成 OpenAI tools 协议。
func (t Tool) ToOpenAISpec() OpenAIToolSpec {
	return OpenAIToolSpec{
		Type: "function",
		Function: OpenAIFunctionSpec{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		},
	}
}

// Find 按名查找工具，找不到返回 nil。先查静态工具，再兜底查动态注入的 MCP 工具。
func Find(name string) *Tool {
	for i := range Tools {
		if Tools[i].Name == name {
			return &Tools[i]
		}
	}
	return findMCPTool(name)
}

// ParseArgs 把 LLM 返回的 JSON 字符串参数解析成 map。
func ParseArgs(raw string) (map[string]any, error) {
	args := map[string]any{}
	if raw == "" || raw == "null" {
		return args, nil
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, err
	}
	return args, nil
}

// 工具定义列表
var Tools = []Tool{
	{
		Name:        "List",
		Description: "列出指定目录下的文件和子目录。",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"path": {Type: "string", Description: "目录路径（绝对或相对路径）"},
			},
			Required: []string{"path"},
		},
		Executor: ListDir,
		ReadOnly: true,
	},
	{
		Name: "CodeGraph",
		Description: "代码图谱:对当前 workspace 做符号级导航,查代码符号(函数/类型/方法)的定义、调用关系、实现者、继承关系，影响范围,要优先于read,glob,grep工具调用。" +
			"\n\n**何时调用**(优先于 Grep):" +
			"\n- 某个函数/类型/方法定义在哪 → op=def" +
			"\n- 谁调用了某函数(改它的影响面)→ op=callers;某函数调用了哪些 → op=callees" +
			"\n- 谁实现了某接口(Go 隐式接口,grep 查不到)→ op=implementers;谁继承/嵌入某类型 → op=subtypes;某类型派生自什么 → op=supertypes" +
			"(注:继承/实现边覆盖 Go 及主流 OOP 语言,空结果会列出已覆盖范围、不代表无关系)" +
			"\n- 改某符号会牵连哪些下游(影响面/blast radius,传递闭包)→ op=impact(可给 depth)" +
			"\n- 谁引用/用到了某个名字 → op=refs" +
			"\n- 按名字找符号、或列某类符号 → op=symbols(可加 kind 过滤)" +
			"\n- 看一个文件有哪些符号 → op=outline;它 import 了什么 → op=imports(给 path)" +
			"\n\n**何时不要**:搜的是注释/字符串/任意文本(用 Grep);非代码文件。" +
			"\n\n结果是 `文件:行`(+签名/调用方),已截断分页。注:调用关系按名解析,同名会合并。" +
			"\n支持语言:Go(stdlib 精确)+ TS/JS/Python/Java/Rust/C/C++/C#/Ruby/PHP/Kotlin/Swift/Scala/Dart/Vue/Svelte。其它(shell/css/json/md 等)用 Grep。",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"op":    {Type: "string", Enum: []string{"symbols", "def", "refs", "callers", "callees", "implementers", "subtypes", "supertypes", "impact", "imports", "outline", "reindex"}, Description: "操作类型"},
				"name":  {Type: "string", Description: "符号名;def/refs/callers/callees/implementers/subtypes/supertypes/impact 必填,支持 \"Type.Method\" 限定名;symbols 作模糊过滤"},
				"path":  {Type: "string", Description: "outline/imports 用:相对 workspace 的文件路径"},
				"kind":  {Type: "string", Enum: []string{"func", "method", "type", "var", "const", "field"}, Description: "可选,按符号种类过滤"},
				"depth": {Type: "integer", Description: "impact 用:传递闭包跳数,默认 3"},
				"max":   {Type: "integer", Description: "最多返回几条,默认 60"},
			},
			Required: []string{"op"},
		},
		Executor: CodeGraph,
		ReadOnly: true,
	},
	{
		Name:        "Read",
		Description: "读取文本文件内容，可选 offset/limit 指定行范围。",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"path":   {Type: "string", Description: "文件路径"},
				"offset": {Type: "integer", Description: "起始行号(从1开始)"},
				"limit":  {Type: "integer", Description: "最多读取多少行,默认2000"},
			},
			Required: []string{"path"},
		},
		Executor: ReadFile,
		ReadOnly: true,
	},
	{
		Name: "Write",
		Description: "写入（覆盖）文本文件。父目录会自动创建。\n\n" +
			"⚠️ 超大文件分块写:content 会整体计入本次输出,一次写太多可能撞上单次输出上限被截断、" +
			"导致本次调用失败——且对话越长、可用输出预算越小,越容易触发。稳妥做法:先用 Write 写入" +
			"开头一部分,再用 Update 逐段追加余下内容,把每次写入拆小。",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"path":    {Type: "string", Description: "文件路径"},
				"content": {Type: "string", Description: "要写入的全部内容;超大时请分块(见工具说明)"},
			},
			Required: []string{"path", "content"},
		},
		Executor: WriteFile,
		ReadOnly: false,
	},
	{
		Name: "Update",
		Description: "编辑已有文件:用 old_string 精确匹配要替换的内容,new_string 为替换后的内容。\n\n" +
			"old_string 必须与文件内容逐字符一致(含缩进 Tab/空格、空行)。\n" +
			"从 Read 输出复制 old_string 时,只复制「│」之后的内容,不包含行号前缀。\n" +
			"选取 3-5 行上下文确保唯一性。若 old_string 出现多次且只想替换一处,加更多上下文让其唯一;\n" +
			"replace_all=true 可替换全部出现。\n\n" +
			"注意: old_string/new_string 中若有双引号必须转义为 \\\"。",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"path":        {Type: "string", Description: "文件路径（绝对或相对路径）"},
				"old_string":  {Type: "string", Description: "要替换的精确文本,需逐字符匹配"},
				"new_string":  {Type: "string", Description: "替换后的文本"},
				"replace_all": {Type: "boolean", Description: "替换所有匹配项,默认 false"},
			},
			Required: []string{"path", "old_string", "new_string"},
		},
		Executor: EditFile,
		ReadOnly: false,
	},
	{
		Name:        "Glob",
		Description: "按 glob 模式查找文件，支持 ** 递归通配，结果按修改时间倒序返回。",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"pattern": {Type: "string", Description: "glob 模式，如 **/*.go"},
				"path":    {Type: "string", Description: "搜索根目录（可选）"},
			},
			Required: []string{"pattern"},
		},
		Executor: GlobFile,
		ReadOnly: true,
	},
	{
		Name:        "Grep",
		Description: "在文件中按正则查找匹配行，结果为 path:line:content 格式。",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"pattern": {Type: "string", Description: "正则表达式"},
				"path":    {Type: "string", Description: "搜索路径（文件或目录）"},
				"glob":    {Type: "string", Description: "文件名模式过滤，如 *.go"},
			},
			Required: []string{"pattern"},
		},
		Executor: GrepFile,
		ReadOnly: true,
	},
	{
		Name:        "Tree",
		Description: "以树状结构显示目录。",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"path":  {Type: "string", Description: "根目录路径"},
				"depth": {Type: "integer", Description: "最大深度，默认 3"},
			},
		},
		Executor: FileTree,
		ReadOnly: true,
	},
	{
		Name: "Bash",
		Description: "在 shell 中执行命令并返回 stdout/stderr。可指定 cwd 与超时秒数(默认 60)。" +
			"\nWrite/Update 因目标在 workspace 外被拒时,由用户确认或自行处理,不要自作主张绕过。" +
			"\n\n**常驻进程**(开发服务器 / watch / daemon,如 npm run dev、vite、python -m http.server、tail -f)" +
			"不会主动退出 —— 默认(前台)调用会一直阻塞到 timeout 才返回,并把子进程甩成孤儿。" +
			"启动这类进程时**必须传 `run_in_background: true`**:立即返回一个句柄 id(形如 bash_1),不阻塞。" +
			"随后用 `BashOutput(id)` 读输出/查是否就绪,用完用 `KillBash(id)` 结束。" +
			"\n⚠️ **不要用 shell 的 `&` 或 `nohup` 在前台模式里自己后台化**(如 `./server &`):那样救不了," +
			"Go 仍会等子进程继承的输出管道关闭而卡死到 timeout,还会留孤儿;要后台跑就用 `run_in_background: true`。" +
			"\n前台模式(默认)只用来跑会主动退出的命令:构建 / 单测 / lint / grep / git / ls / 安装依赖 / 一次性脚本。",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"command":           {Type: "string", Description: "完整命令行（通过 sh -c 执行）"},
				"cwd":               {Type: "string", Description: "工作目录（可选）"},
				"timeout":           {Type: "integer", Description: "超时秒数，默认 60（仅前台模式生效）"},
				"run_in_background": {Type: "boolean", Description: "true 则后台启动常驻进程,立即返回句柄 id,不阻塞;配合 BashOutput / KillBash 使用"},
			},
			Required: []string{"command"},
		},
		Executor: RunCommand,
		ReadOnly: false,
	},
	{
		Name:        "BashOutput",
		Description: "读取某个后台进程(Bash run_in_background 启动)自上次读取以来的新输出,并报告其运行/退出状态。用于等待服务就绪、查看日志、确认是否报错。",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"id": {Type: "string", Description: "后台进程句柄 id（Bash run_in_background 返回的，形如 bash_1）"},
			},
			Required: []string{"id"},
		},
		Executor: BashOutput,
		ReadOnly: true, // 只读输出,不动文件
	},
	{
		Name:        "KillBash",
		Description: "结束一个后台进程(连同其子进程树),释放端口/资源。常驻服务验证完务必调用,避免孤儿进程堆积。",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"id": {Type: "string", Description: "后台进程句柄 id（形如 bash_1）"},
			},
			Required: []string{"id"},
		},
		Executor: KillBash,
		ReadOnly: false,
	},
	{
		Name: "Search",
		Description: "搜索公开互联网,返回标题/URL/摘要列表。" +
			"\n\n**何时调用**:用户问到时效性强的信息(新闻、版本号、近期事件)、需要最新文档/教程、" +
			"或代码里某个依赖/API 的官方说明你不确定。本工具只是检索摘要,要看完整内容仍需后续访问 URL。" +
			"\n\n**何时不要调用**:用户问的是当前 workspace 的代码细节(用 Read/Grep)、" +
			"通用编程常识、或你已经能给出可靠回答的问题 — 别为了凑 token 滥用搜索。" +
			"\n\n**后端**:Bing HTML 抓取,零配置、无需 API key,国内可用 (cn.bing.com 直连)。",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"query":       {Type: "string", Description: "搜索关键词,直接用自然语言或英文都行"},
				"max_results": {Type: "integer", Description: "最多返回几条 (1-15, 默认 5)"},
			},
			Required: []string{"query"},
		},
		Executor: WebSearch,
		ReadOnly: true, // 仅查询,不动本地文件
	},
	{
		Name: "Fetch",
		Description: "抓取单个 URL 的内容,HTML 自动转纯文本(去 script/style/nav,优先抽 main/article)。" +
			"\n\n**何时调用**:" +
			"\n- 用户直接给定 URL 让你读 / 总结 / 评审" +
			"\n- Search 找到 URL 后需要看正文(snippet 不够)" +
			"\n- 跟踪官方文档 / GitHub issue / 技术博客等具体页面" +
			"\n\n**何时别调**:" +
			"\n- 用户问的是 workspace 本地内容 (用 Read)" +
			"\n- 你已经能凭训练数据答好(避免无谓 fetch)" +
			"\n\n输出含 URL/Content-Type/长度元数据 + 正文。超过 max_chars 自动截断。" +
			"\n非 HTML(JSON/Markdown/纯文本)直接返回原文不做处理。",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"url":       {Type: "string", Description: "完整 URL,必须以 http:// 或 https:// 开头"},
				"max_chars": {Type: "integer", Description: "输出字符上限 (1-30000, 默认 10000)"},
			},
			Required: []string{"url"},
		},
		Executor: WebFetch,
		ReadOnly: true,
	},
	{
		Name: "Memory",
		Description: "检索当前 workspace 历史对话(跨日期,仅当前 workspace 的 session)。" +
			"\n\n**何时调用**:" +
			"\n- 用户说\"上次\"、\"之前\"、\"以前我们聊过\"等指向过往对话的表达" +
			"\n- 你需要回忆之前讨论过的设计决策、做过的修改、报错的根因" +
			"\n- 任务延续:用户接着之前未完成的事情往下做" +
			"\n\n**怎么用**:你给一组关键词(建议 2-4 个,关键名词/动词,避免停用词)。" +
			"mode=and(默认)要求全部命中、最精准;mode=or 任一命中、召回更广,关键词少且独立时用。" +
			"\n\n命中后返回 [日期 | 时刻 | 角色 | 内容前 400 字]。如果未命中,换近义词或拆短再试。",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"keywords": {
					Type:        "array",
					Description: "检索关键词列表,1-6 个",
					Items:       map[string]any{"type": "string"},
				},
				"mode": {
					Type:        "string",
					Description: "and=全部命中(默认),or=任一命中",
					Enum:        []string{"and", "or"},
				},
				"max_results": {Type: "integer", Description: "返回上限 (1-50, 默认 10)"},
			},
			Required: []string{"keywords"},
		},
		Executor: Memory,
		ReadOnly: true,
	},
	{
		Name: "LoadSkill",
		Description: "读取一个 skill 的完整内容。" +
			"当 system prompt 的 Available Skills 列表中，有 skill 的 description 与当前任务匹配时调用。" +
			"同一回合内可连续调用多个相关 skill。" +
			"没有匹配的 skill 时不要调用。" +
			"同一 skill 在本会话内只调用一次。",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"name": {Type: "string", Description: "skill 名,从 system prompt 的 Available Skills 列表中选"},
			},
			Required: []string{"name"},
		},
		Executor: LoadSkill,
		ReadOnly: true,
	},
	{
		Name:        "OCR",
		Description: "对本地图片做 OCR 识别,返回图片中的全部文字 (基于 PaddleOCR PP-OCRv5,支持中英文)。**仅当对话里给的是图片文件路径、而你无法直接看到这张图时**才调用本工具(供不支持视觉的模型读图)。**如果图片已经直接内联显示在消息里(你能看到图),绝对不要调用本工具,直接看图作答即可。** path 必须用消息里直接给你的那条图片路径原样传入;绝不要自己去文件系统里搜索、ls 或猜测图片文件。首次调用会自动下载 ~37MB 模型,后续秒级响应。",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"path": {Type: "string", Description: "图片的本地文件路径,支持 PNG/JPEG/GIF;直接用消息里给出的那条路径,不要自己搜索或猜测"},
			},
			Required: []string{"path"},
		},
		Executor: ImgOCR,
		ReadOnly: true, // 仅读取本地图片,不修改任何东西
	},
	{
		Name: "SwitchModel",
		Description: "在模型路由的 4 级(Level0/1/2/3)之间升级或降级。\n\n" +
			"**路由等级背景**(见系统提示): Level0(simple,flash)/Level1(medium,flash+thinking)/Level2(complex,pro+medium)/Level3(deep,pro+high)。\n\n" +
			"**target_level 参数**: 模型判断当前任务应路由到的目标级别(0-3)。\n\n" +
			"**升级规则(跨 2 级才升级)**:\n" +
			"- 仅当 target_level - 当前级别 >= 2 时才实际升级\n" +
			"- 例如: 当前 Level0 → target_level=2 或 3 才升级; 当前 Level1 → target_level=3 才升级\n" +
			"- 若只差 1 级(如当前 Level1 → target_level=2), 不升级(差距小, 收益低)\n\n" +
			"**升级时必须明确 reason_type**:\n" +
			"- `context`: 因上下文压力升级(当前级别窗口不够用, 需要更大上下文窗口)\n" +
			"- `complexity`: 因任务复杂度被低估升级(任务实际需要更深的推理/分析, 当前级别路由偏低)\n\n" +
			"**降级规则(跨 1 级就降级, 省钱)**:\n" +
			"- 仅当 当前级别 - target_level >= 1 时就降级\n" +
			"- 例如: 当前 Level3 → target_level=2 或更低就降级; 当前 Level2 → target_level=1 或更低就降级\n" +
			"- 降级不必给 reason_type(默认语义降级: 任务实际比路由级别简单)\n\n" +
			"**reason 参数**: 一句话说明为什么切换。升级时格式: \"上下文压力(使用率约XX%)\" 或 \"复杂度被低估: 需要Level[N](原因)\"。\n\n" +
			"**绝对不要调的情况**:\n" +
			"- 简单任务(读文件、改一行代码、查定义、grep) — 即使上下文很长也只用 flash\n" +
			"- 已在目标级别 — 调用纯属多余\n" +
			"- 用户用 /model 锁定了模型",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"target_level": {
					Type:        "integer",
					Description: "目标路由级别 0-3。升级需跨 2 级, 降级跨 1 级即可",
				},
				"reason_type": {
					Type:        "string",
					Enum:        []string{"context", "complexity"},
					Description: "升级原因: context=上下文压力 / complexity=任务复杂度被低估。降级时省略",
				},
				"reason": {
					Type:        "string",
					Description: "一句话切换理由。升级格式: \"上下文压力(使用率约XX%)\" 或 \"复杂度被低估: 需要Level[N](原因)\"",
				},
			},
			Required: []string{"target_level", "reason"},
		},
		// Executor 为 nil:本工具在 agent/llm.go 工具循环里被拦截,不走默认 Executor。
		// 拦截后直接修改 agent 内部 currentEntry/role,通过 ModelSwitchMsg 通知 UI。
		Executor: nil,
		ReadOnly: true,
	},
	{
		Name: "CreatePlan",
		Description: "把任务拆解成多个步骤的 DAG 执行。\n\n" +
			"**何时调用**:\n" +
			"- 用户明确要求规划\n" +
			"- 开发/重构/修复复杂业务时\n\n" +
			"**每个节点 model 字段**:\n" +
			"  • `flash` — 机械步骤:读文件 / grep / ls / git status / 统计行数\n" +
			"  • `pro` — 思考步骤:分析 / 代码评审 / 根因排查 / 最终汇总\n\n" +
			"**并发原则**: 默认不写 depends_on，只有某步骤确实必须等另一步完成才加。优先并发，而非串行。\n\n" +
			"**执行 & 返回值**: 按 DAG 依赖关系并发执行所有节点,返回每个节点的执行汇总(每行一个节点 + 状态 + 简短结果)。拿到汇总后只需给用户写一段简洁的最终总结,不要再做实际工作(状态由 deepx 自维护)。",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"plans": {
					Type:        "array",
					Description: "顶层规划节点列表。每项含 id(plan1, plan2 ...)、title、model、可选 depends_on。",
					Items: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id":         map[string]any{"type": "string", "description": "plan1, plan2 ..."},
							"title":      map[string]any{"type": "string", "description": "一句话说明这一步做什么"},
							"model":      map[string]any{"type": "string", "enum": []string{"flash", "pro"}, "description": "执行该 plan 使用哪个模型"},
							"depends_on": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "依赖的其他 plan id;无依赖留空(允许并发)"},
						},
						"required": []string{"id", "title", "model"},
					},
				},
			},
			Required: []string{"plans"},
		},
		Executor: CreatePlan,
		ReadOnly: true, // 仅做规划登记,不动文件
		// 入口由 keyword router 路由,模型自行判断要不要拆 plan;
		// flash 起手时也允许它把复杂任务拆成 DAG(其中可指定 pro 节点跑深度部分)。
		// 子 agent 不该用本工具(防递归),但不按角色隐藏(各角色工具表一致、保前缀缓存):靠系统提示词禁止 +
		// Executor 为 nil 兜底(同 Todo / SwitchModel)。见 subagent.go。
	},
	{
		Name: "UpdatePlanStatus",
		Description: "更新某个 plan 节点的执行状态。每开始一个 plan 前调用一次(status=running),完成或失败时再调一次(status=done/failed)。summary 是可选的一段简短结论。" +
			"deepx 用这些状态实时驱动右栏 Current Plan 区的显示。",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"id":      {Type: "string", Description: "plan 的 id,如 plan1"},
				"status":  {Type: "string", Enum: []string{"pending", "running", "done", "failed", "blocked"}, Description: "新状态"},
				"summary": {Type: "string", Description: "可选,完成/失败时一段简短的结论或错误描述"},
			},
			Required: []string{"id", "status"},
		},
		Executor: UpdatePlanStatus,
		ReadOnly: true,
		// 主要给 CreatePlan 拆出的 DAG 子 agent 写中间状态(实际被吞,以 scheduler 为准)。
		// 主对话用 Todo 维护可见清单、不靠它;但不再按角色隐藏(各角色工具表一致,保前缀缓存),
		// 主 agent 真调了也只是发一条 TaskStatusMsg,apply 找不到 id 会静默忽略,无害。
	},
	{
		Name: "Todo",
		Description: "维护一个对用户可见的待办清单,展示并随进度勾选——这是给用户的进度透明度,不派子 agent、你自己一步步执行。\n\n" +
			"**何时用**:任务有 ≥3 个有先后顺序的步骤、值得让用户看到进度(从零搭应用、跨多文件改动、调试修复链路)。开工前先列出全部步骤,然后每开始/完成一步就重发整张清单更新状态。\n\n" +
			"**何时不用**:单步或简单任务(直接做);真正可并行、互相独立的扇出任务用 CreatePlan(那会派并发子 agent),搭一个连贯的应用不要用 CreatePlan。\n\n" +
			"**用法(全量快照)**:每次调用都传完整 todos 列表,每项含 content + status。新增 / 标记进行中 / 完成 / 调整,都重发整张表(不是增量)。同一时刻最多一项 in_progress。",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"todos": {
					Type:        "array",
					Description: "完整的待办列表(全量快照,按执行顺序排列)。",
					Items: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"content": map[string]any{"type": "string", "description": "这一步做什么(一句话)"},
							"status":  map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed"}, "description": "pending=未开始 / in_progress=进行中 / completed=已完成"},
						},
						"required": []string{"content", "status"},
					},
				},
			},
			Required: []string{"todos"},
		},
		// Executor 为 nil:在 agent/llm.go 工具循环里被拦截,转成 PlanCreatedMsg 更新 UI,
		// 不走默认 Executor、不派子 agent。
		Executor: nil,
		ReadOnly: true,
		// Roles 留空 = 所有角色可见;子 agent 由其系统提示词禁止使用(同 CreatePlan)。
	},
	{
		Name: "Workflow",
		Description: "创建 / 运行 / 列出可复用的 workflow(用 JavaScript 编排多个子 agent 的固定流程)。\n\n" +
			"**何时用**:用户想把一套多步、可复用、会重复跑的多子-agent 流程固化下来(多视角审查、扇出研究、流水线、对抗验证等)。一次性简单任务别用。\n\n" +
			"**action**:\n" +
			"  • `create` — 保存一个新 workflow,需 `saveAs`(kebab-case 名)+ `script`(完整 JS 源码)。写脚本前先遵循 `creating-workflows` 技能了解格式与 API(parallel 才有并发)。\n" +
			"  • `run` — 运行,需 `name`,可选 `args`。**运行前会请用户确认**。\n" +
			"  • `list` — 列出可用 workflow。\n\n" +
			"不要用 Write 工具手写 workflow 文件,统一走本工具的 create。",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"action": {Type: "string", Enum: []string{"create", "run", "list"}, Description: "要执行的操作"},
				"name":   {Type: "string", Description: "run:要运行的 workflow 名"},
				"saveAs": {Type: "string", Description: "create:保存名(kebab-case,需等于脚本 meta.name)"},
				"script": {Type: "string", Description: "create:完整的 workflow JavaScript 源码"},
				"args":   {Type: "string", Description: "run:可选参数(JSON 对象或 key=value)"},
			},
			Required: []string{"action"},
		},
		// Executor 为 nil:在 agent/llm.go 工具循环里被拦截(handleWorkflowTool);
		// run 动作还会触发跑前确认。子 agent 不该用本工具,靠系统提示词禁止 + nil 兜底。
		Executor: nil,
		ReadOnly: false,
	},
	{
		Name: "Explore",
		Description: "派一个只读的「探索」子 agent 去大范围搜索/定位,它在自己的独立上下文里翻查,只把**结论**回给你——大量原文噪音不会进你的上下文。能探两类目标:\n" +
			"  • 本地代码库:跨多文件 Grep/Glob/Read/CodeGraph 定位,带回相关位置 file:line + 关键发现。\n" +
			"  • 外部仓库/网页:用 Search + Fetch 抓 GitHub 仓库主页/README/raw 源码/文档,搞清一个外部项目或链接是干嘛的、怎么用、关键实现在哪,带回用途/关键事实 + 来源链接。\n\n" +
			"**何时用**:回答问题需要**跨多个未知位置、多轮搜索才能定位**时。例如:\"本项目在哪里处理 X / Y 涉及哪些文件 / Z 的调用链\";\"调查某个 GitHub 仓库/网页是干嘛的、它的某功能怎么实现\"。会话已经很长、不想把几十个文件或几页网页塞进当前上下文时,尤其该用它。\n\n" +
			"**何时不用**:你已经知道确切的文件或符号——直接用 Read / Grep / CodeGraph 更快更省;只抓一个已知 URL 用 Fetch 即可。它每次是独立上下文(不命中你当前会话缓存),**只在真的需要大范围探索时叫它**,别替代单次搜索/抓取。\n\n" +
			"**用法**:task 写清楚要查什么、要带回什么结论(越具体越省事);可选 thoroughness 控制深度。它只读(不改文件、不执行命令),完成后返回一段精炼结论。",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"task":         {Type: "string", Description: "要探索的问题 / 要定位的东西 / 期望带回的结论(尽量具体,例如\"找出鉴权中间件在哪定义、被哪些路由用到,带回 file:line\";或\"调查 github.com/x/y 是什么项目、核心功能怎么实现\")"},
				"thoroughness": {Type: "string", Enum: []string{"quick", "medium", "thorough"}, Description: "探索深度:quick=基本搜索够用就停 / medium=适度展开(默认) / thorough=多位置多措辞全面排查。越深越慢越费,按需选。"},
			},
			Required: []string{"task"},
		},
		// Executor 为 nil:在 agent/llm.go 工具循环里被拦截,派只读探索子 agent(runExplore)。
		// 子 agent 自己的工具集里不含 Explore(防递归套娃),见 agent/explore.go。
		Executor: nil,
		ReadOnly: true,
	},
	{
		Name: "AskUser",
		Description: "向用户发起一道或多道选择题,在终端弹窗里让用户直接勾选(单选/多选),用户选完把结果回传给你——比让用户敲一长串文字省事得多。\n\n" +
			"**何时用**:需求确认(可一次列多个需求点,每个给几个选项)、在有限且明确的取舍里让用户拍板(技术选型、是否包含某功能、A/B 方案二选一)。\n\n" +
			"**何时不用**:开放性问题、需要用户自由表达或填具体内容(路径/命名/数值)的场景——那些仍用普通文字提问。别把每句话都做成弹窗。\n\n" +
			"**用法**:questions 数组,每题含 question(题面)+ options(2~6 个选项,各有 label;value 省略则等于 label)+ 可选 multiple(true=多选,默认单选)。用户取消时本工具返回失败,届时改用普通对话继续。",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"questions": {
					Type:        "array",
					Description: "选择题列表,可一次问多道(如多个需求点的确认)。",
					Items: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"question": map[string]any{"type": "string", "description": "题面/需求点描述"},
							"multiple": map[string]any{"type": "boolean", "description": "是否多选;省略=单选"},
							"options": map[string]any{
								"type":        "array",
								"description": "2~6 个候选项",
								"items": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"label": map[string]any{"type": "string", "description": "显示给用户的文字"},
										"value": map[string]any{"type": "string", "description": "选中后回传给你的标识;省略则等于 label"},
									},
									"required": []string{"label"},
								},
							},
						},
						"required": []string{"question", "options"},
					},
				},
			},
			Required: []string{"questions"},
		},
		// Executor 为 nil:在 agent/llm.go 工具循环里被拦截,弹 TUI 选择框、阻塞等用户选完再回传。
		Executor: nil,
		ReadOnly: true,
	},
	{
		Name: "Remember",
		Description: "把用户的**持久性**偏好 / 约定写入记忆文件(AGENTS.md),下次启动 / 新对话时自动注入,长期生效。\n\n" +
			"**何时用**:用户表达跨轮、长期有效的偏好或约定时(措辞如「以后都…」「记住…」「不要再…」「我习惯…」「这个项目用…」)。写完向用户确认记了什么、记到哪一级。\n\n" +
			"**何时不用**:一次性指令、显而易见的事、代码/配置本身已表达的——这些别记,免得污染记忆。\n\n" +
			"**scope 判定**:与「人 / 工作习惯」相关、跨项目都成立 → global(写 ~/.deepx/AGENTS.md);与「这个代码库 / 技术栈 / 目录规范」相关、仅本仓库成立 → project(写 <workspace>/AGENTS.md)。判据:这条偏好**依不依赖当前代码库**——依赖=project,不依赖=global。拿不准就先用 AskUser 问用户记成哪一级。\n\n" +
			"content 写成一句清晰、可执行的偏好(简洁,别长篇)。",
		Parameters: ToolParam{
			Type: "object",
			Properties: map[string]PropDef{
				"scope":   {Type: "string", Enum: []string{"global", "project"}, Description: "global=全局(所有项目)/ project=仅当前项目"},
				"content": {Type: "string", Description: "要记住的偏好,一句话"},
			},
			Required: []string{"scope", "content"},
		},
		// Executor 为 nil:在 agent/llm.go 工具循环里拦截(需要 workspace 解析项目级路径)。
		Executor: nil,
		ReadOnly: false, // 会写 AGENTS.md;plan 只读模式下不可用
	},
}
