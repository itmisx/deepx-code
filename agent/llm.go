package agent

import (
	"bufio"
	"bytes"
	"context"
	"deepx/tools"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type AgentMode string

const (
	AgentMode_Plan   AgentMode = "plan"
	AgentMode_Auto   AgentMode = "auto"
	AgentMode_Review AgentMode = "review"

	// maxNoProgressRounds:连续这么多轮工具调用「全部失败、无任何成功」即判定卡死/空转,暂停。
	// 这是主 agent 唯一的"主动"熔断,拦反复改同一处失败、反复跑同一条报错命令这类失败循环;
	// 只要某轮有任一工具成功就重置计数,productive 的长任务不受影响。
	//
	// 刻意不设"绝对轮数上限"(对标 Claude Code 交互式主循环):长任务跑到模型自己停为止,
	// 不在第 N 轮硬性中断。终止由三道天然边界保证 —— ① convo 每轮只增,上下文单调增长,
	// 迟早撞模型上下文窗口让 streamOnce 优雅报错退出;② 本断路器拦失败循环;③ 用户 ESC 随时中断。
	// (见 issue #84:旧的固定 100 轮上限会把合法长任务在中途打断、需手动继续。)
	maxNoProgressRounds = 15

	// compactTriggerPct:单个 turn 内,上一轮真实 prompt tokens 占模型上下文窗口达到这个百分比,
	// 就在下次请求前自动压缩历史(对标 Claude Code 的 auto-compact:压缩+继续,而非熔断停)。
	// 取 80 留 ~20% 给本轮输出;压缩后历史缩到 ~20% 窗口,不会立刻反复触发。
	compactTriggerPct = 80

	// maxAPIRetries:streamOnce 在「进入流式之前」失败(网络错 / 429 限流 / 5xx 服务端错误)时的最大重试次数。
	// 只重试这类瞬时错误,且只在还没吐任何 token 给 UI 时重试(进流式后的中途断不在此列,重试会重复吐字)。
	// 退避 = 指数 + 抖动,429 优先听 Retry-After;每次重试前发 RetryNoticeMsg 让状态行显示「重试 N/10」(issue #147)。
	maxAPIRetries = 10
)

// ModelEntry 单个 role 的完整连接配置 — base_url / model id / api_key 三件套。
// 设计目标:flash 和 pro 可以指向不同 provider(比如 flash 用本地 vllm,pro 用 DeepSeek 云端)。
type ModelEntry struct {
	BaseURL       string
	Model         string
	APIKey        string
	ContextWindow int // 上下文窗口大小(tokens)
	MaxTokens     int // 单次生成的 completion 上限(tokens);字段顺序需与 config.ModelEntry 一致
	// 推理参数(跨供应商通用,空值不发送)。详见 config.ModelEntry 同名字段注释。
	ReasoningEffort string
	Thinking        string
	// Vision 表示该模型是否支持图片输入(由启动探测的缓存填入,见 tui)。决定带图消息发请求时
	// 渲染成 base64 image_url(true)还是路径文本走 OCR(false)。
	Vision bool
}

// ModelConfig 双模型配置。Flash 处理简单/查询型任务,Pro 处理复杂/规划型任务。
// 入口路由(keyword_router.go)决定本轮起手用哪个;每个 plan 节点也可以独立指定 model 字段。
// 两个 entry 可以共用同一个 BaseURL/APIKey,只 Model 不同(常见场景);也可以完全分离。
type ModelConfig struct {
	Flash ModelEntry
	Pro   ModelEntry
}

// === 给 TUI 的事件 ===

type TokenMsg string                  // 助手正式回复(content)的文本增量,会展示到 chat
type ReasoningTokenMsg string         // 模型思考过程(reasoning_content)增量,TUI 用它驱动 spinner,不展示文字
type StreamErrMsg struct{ Err error } // 错误
type StreamDoneMsg struct{}           // 整个会话回合结束

// RetryNoticeMsg:API 请求失败、进入退避重试前发给 UI,驱动状态行显示「重试 N/Max」(issue #147)。
type RetryNoticeMsg struct {
	Attempt int           // 第几次重试(从 1 起)
	Max     int           // 重试次数上限
	Delay   time.Duration // 本次退避等待时长
	Reason  string        // 触发原因,如 "HTTP 429" / 网络错误
}
type ToolCallStartMsg struct {        // 即将调用工具
	Name     string
	Args     string
	ReviewCh chan bool // review 模式下的审核通道,nil = 无需审核
}
type ToolCallResultMsg struct { // 工具调用返回
	Name    string
	Output  string
	Success bool
}

// ModelSwitchMsg 通知 UI 本轮起手选择的模型。每轮仅在开头发一次,本轮不再变化。
type ModelSwitchMsg struct {
	Role    string // "flash" or "pro"
	ModelID string // 实际 model id
	Reason  string // 可选,描述路由依据(目前为空,B 方案静默路由)
}

// HistoryUpdateMsg 让 UI 用最新的 history 替换本地副本(包含 assistant tool_calls / tool 结果)
type HistoryUpdateMsg struct {
	History []ChatMessage
}

// CompactedMsg 在「单个长 turn 内」自动压缩后发给 UI:摘要存在 session(每轮注入 system 尾部),
// 而 system 不入 history(HistoryUpdateMsg 会剥掉)——必须用这条把新摘要带出来,否则下轮上下文丢失。
// history 截断由随后的 HistoryUpdateMsg 同步。Turns = 本次被压掉的 user 轮数,仅供 UI 提示。
type CompactedMsg struct {
	Summary string
	Turns   int
}

// VisionUnsupportedMsg:本以为支持视觉的模型,实际发图被端点拒(如 404 "no image input")。
// agent 已自动改用 OCR 重发,这里通知 TUI 把该模型标记为无视觉、纠正缓存,后续不再发 base64。
type VisionUnsupportedMsg struct {
	Model   string
	BaseURL string
}

// PrefixSnapshotMsg 携带本轮"实际发送"的前缀(system 文本 + tool specs JSON)。
// TUI 持久化它,用于重启变化检测与缓存友好压缩复刻旧前缀。每轮发一次。
type PrefixSnapshotMsg struct {
	Model         string // 本轮实际使用的 model ID(缓存按模型分,压缩需同模型才命中)
	SystemPrompt  string
	ToolSpecsJSON string
}

// === OpenAI 协议结构 ===

// ChatMessage 是历史记录与请求体共用的消息结构。
// 文本消息走 Content (string),包含图片的多模态消息走 ContentParts (array)。
// 两个字段都是内存表示, JSON 序列化由 MarshalJSON 统一处理。
type ChatMessage struct {
	Role             string        `json:"-"`
	Content          string        `json:"-"`
	ContentParts     []ContentPart `json:"-"`
	ReasoningContent string        `json:"-"`
	ToolCalls        []ToolCall    `json:"-"`
	ToolCallID       string        `json:"-"`
	Name             string        `json:"-"`
	// ImagePaths 是这条消息附带的图片绝对路径(粘贴落盘的图)。**规范形态只存路径、不存 base64**
	// (历史小、缓存友好)。发请求前由 renderConvoImages 按"当轮模型支不支持视觉"即时渲染:
	// 支持 → 读成 base64 image_url;不支持 → 路径替回文本走 OCR。gob 持久化(导出字段)。
	ImagePaths []string `json:"-"`
	// WorkingMode 记录这条 user 消息**提交当轮所处的工作模式**(只对 user 消息有意义)。
	// 钉死不变:发请求前由 renderWorkingMode 按**每条消息自己的** mode 确定性渲染后缀,
	// 切换当前模式不会改写历史消息的后缀 → 历史逐字节稳定、前缀缓存不 miss。空值兜底为默认 kp。
	// 同 ImagePaths 走"规范形态只存标签、发送那刻才渲染"的思路。gob 持久化(导出字段)。
	WorkingMode WorkingMode `json:"-"`
}

// ContentPart 是 OpenAI 多模态消息里 content 数组的一个元素。
// Type 取值: "text" | "image_url"。
type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

type ImageURL struct {
	URL string `json:"url"`
}

// MarshalJSON 根据是否带图,把 content 序列化成 string 或 array。
// 同时保证 tool 消息 / 纯 assistant 工具调用消息 在 content 为空时不出现该字段。
func (m ChatMessage) MarshalJSON() ([]byte, error) {
	type wire struct {
		Role             string     `json:"role"`
		Content          any        `json:"content,omitempty"`
		ReasoningContent any        `json:"reasoning_content,omitempty"`
		ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
		ToolCallID       string     `json:"tool_call_id,omitempty"`
		Name             string     `json:"name,omitempty"`
	}
	w := wire{
		Role:       m.Role,
		ToolCalls:  m.ToolCalls,
		ToolCallID: m.ToolCallID,
		Name:       m.Name,
	}
	switch {
	case m.ReasoningContent != "":
		w.ReasoningContent = m.ReasoningContent
	case m.Role == "assistant":
		w.ReasoningContent = ""
	}
	switch {
	case len(m.ContentParts) > 0:
		w.Content = m.ContentParts
	case m.Content != "":
		w.Content = m.Content
	case m.Role == "assistant" && len(m.ToolCalls) == 0:
		// DeepSeek (和部分严格的 OpenAI 兼容实现) 要求 assistant 消息至少含 content 或 tool_calls。
		// 当模型只输出 reasoning_content 时,两者都缺会导致下轮请求被 API 400 拒绝。
		// 这里兜底发个空字符串 content 满足契约;omitempty 对非 nil interface(空字符串包裹后)不生效。
		w.Content = ""
	}
	return json.Marshal(w)
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Index    int          `json:"index,omitempty"`
	Function ToolCallFunc `json:"function"`
}

type ToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatRequest struct {
	Model string `json:"model"`
	// omitempty:max_tokens=0 时不发这个字段,让模型走自己的默认输出上限
	// (不同模型默认上限差很多,config 里某些供应商故意留 0 用默认,见 config.modelConfig)。
	MaxTokens     int                    `json:"max_tokens,omitempty"`
	Stream        bool                   `json:"stream"`
	StreamOptions *streamOptions         `json:"stream_options,omitempty"`
	Messages      []ChatMessage          `json:"messages"`
	Tools         []tools.OpenAIToolSpec `json:"tools,omitempty"`
	// 推理参数 —— **两个并列的顶层字段**(对照 DeepSeek 官方文档):
	//
	//   {"thinking": {"type": "enabled"}, "reasoning_effort": "high"}
	//
	// 不要写成嵌套(reasoning_effort 不是 thinking 的子字段)。
	// 空值严格 omitempty —— 用户不设就完全没有对应 JSON 键,任何不支持的模型
	// (MiMo / 未来 OpenAI-兼容新模型)都不会被多余字段炸 400。
	Thinking        *thinkingOption `json:"thinking,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
}

// thinkingOption 是 DeepSeek 思考开关的请求体格式:`{"type": "enabled"}` 或 `{"type": "disabled"}`。
// DeepSeek 默认 enabled,MiMo 默认 disabled。
type thinkingOption struct {
	Type string `json:"type"`
}

// buildThinkingOption 把 ModelEntry.Thinking 字符串转成请求体 thinking 对象。
// 空 / 未识别值返回 nil → omitempty 整个键消失。
func buildThinkingOption(v string) *thinkingOption {
	switch v {
	case "enabled", "disabled":
		return &thinkingOption{Type: v}
	}
	return nil
}

// validateReasoningEffort 把 ModelEntry.ReasoningEffort 过一遍白名单,未识别值
// (yaml 笔误、未来废弃档等)返回 "" → omitempty 不发,防止脏值送到服务端导致 400。
//
// 取值(DeepSeek 文档):
//   - canonical: high (默认) | max
//   - 兼容别名:  low / medium → high;xhigh → max
//
// 白名单纳入全部 5 个,既覆盖 DeepSeek canonical,也覆盖 OpenAI o1/o3 风格(low/medium/high)
// —— 后者拼到 DeepSeek 自动映射,拼到 OpenAI-兼容端就是合法标准取值。
func validateReasoningEffort(v string) string {
	switch v {
	case "low", "medium", "high", "max", "xhigh":
		return v
	}
	return ""
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// UsageInfo 单次 API 调用的 token 用量,含缓存命中信息。
//
// 缓存命中字段各供应商口径不同:DeepSeek 直接给 prompt_cache_hit_tokens;
// OpenAI 标准(mimo 等)放在嵌套的 prompt_tokens_details.cached_tokens。
// normalize() 把后者回填到 PromptCacheHitTokens,使下游显示逻辑只认一套字段。
type UsageInfo struct {
	PromptTokens          int `json:"prompt_tokens"`
	CompletionTokens      int `json:"completion_tokens"`
	TotalTokens           int `json:"total_tokens"`
	PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`  // DeepSeek 专有
	PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"` // DeepSeek 专有
	PromptTokensDetails   struct {
		CachedTokens int `json:"cached_tokens"` // OpenAI 标准(mimo 等)
	} `json:"prompt_tokens_details"`
}

// normalize 统一缓存命中口径:DeepSeek 字段缺失而 OpenAI 标准字段存在时,
// 用 cached_tokens 回填 hit,并据 prompt_tokens 推出 miss。
func (u *UsageInfo) normalize() {
	if u == nil {
		return
	}
	if u.PromptCacheHitTokens == 0 && u.PromptTokensDetails.CachedTokens > 0 {
		u.PromptCacheHitTokens = u.PromptTokensDetails.CachedTokens
		if miss := u.PromptTokens - u.PromptCacheHitTokens; miss > 0 {
			u.PromptCacheMissTokens = miss
		}
	}
}

// UsageMsg 从 agent goroutine 发给 TUI 的单次 API 用量。
type UsageMsg struct {
	Usage UsageInfo
}

type sseChunk struct {
	Choices []struct {
		Delta struct {
			Content          string     `json:"content"`
			ReasoningContent string     `json:"reasoning_content"`
			ToolCalls        []ToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *UsageInfo `json:"usage,omitempty"`
}

// chatResponse 非流式响应的完整结构。
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// CallOnce 发起一次非流式 chat completion 调用,直接返回 content 文本。
// 不带 tools 参数,适用于摘要生成等一次性文本生成场景。
func CallOnce(ctx context.Context, apiKey, baseURL, modelID string, convo []ChatMessage, maxTokens int) (string, error) {
	body, err := json.Marshal(chatRequest{
		Model:     modelID,
		MaxTokens: maxTokens,
		Stream:    false,
		Messages:  convo,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}

	var result chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	return result.Choices[0].Message.Content, nil
}

// CallWithTools 与 CallOnce 类似(非流式、返回 content),但额外带上 tools 参数。
// 用于缓存友好的压缩:摘要请求复刻会话的 [system][tools][history] 前缀,只在末尾追加压缩指令,
// 从而命中已缓存的前缀(tools 必须和被缓存的那次逐字节一致才命中,故由调用方传入旧 specs)。
func CallWithTools(ctx context.Context, apiKey, baseURL, modelID string, convo []ChatMessage, toolSpecs []tools.OpenAIToolSpec, maxTokens int) (string, error) {
	body, err := json.Marshal(chatRequest{
		Model:     modelID,
		MaxTokens: maxTokens,
		Stream:    false,
		Messages:  convo,
		Tools:     toolSpecs,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	var result chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	return result.Choices[0].Message.Content, nil
}

// MarshalToolSpecs 把工具 specs 序列化成 JSON 字符串,供快照持久化(逐字节)。
func MarshalToolSpecs(toolSpecs []tools.OpenAIToolSpec) string {
	b, err := json.Marshal(toolSpecs)
	if err != nil {
		return ""
	}
	return string(b)
}

// UnmarshalToolSpecs 从快照 JSON 还原工具 specs,供压缩复刻旧前缀。空串/失败返回 nil。
func UnmarshalToolSpecs(s string) []tools.OpenAIToolSpec {
	if s == "" {
		return nil
	}
	var out []tools.OpenAIToolSpec
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

// === 入口 ===

// StartStream 启动一个对话回合。入口由 RouteByKeyword 决定起手模型(flash/pro),
// 本轮锁定该模型不再切换。复杂任务由模型主动调 CreatePlan 拆分,plan 节点的 model 字段
// 由 sub-agent 按需路由,实现细粒度的模型选择。
// coreSystemPrompt 是主 agent 与子 agent **共用**的稳定头部:身份 + 行为规则 + workspace + skill 目录。
// 主/子在同一 workspace、同一 skill 目录下逐字节一致 —— 这是缓存前缀共享的基础。
// 主 agent 在其后接「会话摘要」,子 agent 在其后接「节点目标」等专属尾部(见各自构造处)。
func coreSystemPrompt(workspace, skillCatalog string) string {
	base := fmt.Sprintf(`你是 DeepX,一个自主编码 agent,跑在用户的本地开发环境里。

通过工具帮用户:理解代码 · 编辑文件 · 写代码 · 调试 · 执行 shell 命令 · 拆任务 · 推理架构。1

# 核心原则
- 准确、简洁,行动优先于解释
- 增量解决问题
- 不假装做过没做的事,不编造文件内容 / 命令输出 / 工具结果
- 用工具拿事实,不要猜

# 工具使用
- 改代码前先 inspect 相关文件、理解上下文,改动最小化。编辑时保持现有风格,不顺手做不相关的重构,默认保持向后兼容(除非用户明确要求)。
- 查代码符号(函数/类型/方法)的定义、调用关系、实现者、继承请优先用 CodeGraph工具(更准、不误命中注释/字符串)。
- 需要用户在**有限、明确的选项**里做选择或拍板时(需求确认、技术选型、A/B 方案、是否包含某功能等),**必须调用 AskUser 工具弹窗让用户勾选**,可一次问多道;不要把选项写成文字列表让用户敲字回复。开放性、需要自由表达的问题才用文字提问。
- 用户表达**持久性**偏好/约定时(「以后都…」「记住…」「不要再…」「我习惯…」「这个项目用…」),调用 **Remember** 工具写入 AGENTS.md(跨项目的习惯=global,本仓库的约定=project),长期生效;一次性指令不要记。

# 技能skill使用
- 实现功能、修复 bug、重构或 review 代码时,遵循本轮用户消息尾部「工作模式」指明的方法论 skill(加载其正文并执行),不要使用未指明的其它工作模式 skill。

# 任务规划
- 简单/单步任务:直接做,不要过度规划。
- 多步顺序任务(≥3 步且有先后,如从零搭应用 / 跨多文件改动 / 调试修复链路):动手前先用 Todo 列出全部步骤,之后每开始或完成一步就重发整张 todos 更新状态,让用户看到进度。你自己逐步执行,不派子 agent。
- **别提前收尾**:任务没真正完成前(尤其 todo 里还有未完成项),不要只回一段总结就停下——继续调用工具推进到底。只在全部做完、或确实卡住需要用户提供信息时才结束。像"分析/梳理 XX 流程"这类调研任务,要把相关文件都查透、给出完整结论,不能查两三个文件就收。
- 真正可并行、彼此独立的扇出任务才用 CreatePlan 拆 DAG(会派并发子 agent 各自跑);搭一个连贯的应用别用 CreatePlan。

# Shell 安全
- 不主动执行破坏性命令(rm -rf / drop / force push 等)
- 优先可逆操作,destructive 操作先确认
- Write/Update 因目标在 workspace 外被拒时,由用户确认或自行处理,不要自作主张绕过。
- docker 沙箱模式下(命令在 Linux 容器里跑、~ 解析为 /root、宿主路径如 /Users/… 不存在):只有项目 workspace 挂载在 /workspace 且持久化,写到容器其它位置(含 ~ 与宿主绝对路径)是临时文件、容器销毁即丢。此时要在 workspace 外建/改宿主文件,别在容器里写一份就声称成功——直接告诉用户该路径在 docker 沙箱不可达、只有项目目录可用,需要的话切到 native/off。

# 模式限制
- plan 模式:禁止 Write / Update / Bash,其余工具均可使用。
- auto 模式:全部工具均可使用,无需人工审核。
- review 模式:所有工具均可使用,但 Write / Update / Bash 需要人工审核确认后才执行,其余工具自动执行。
- 每次模式切换时会有一条系统通知明确告诉你当前处于什么模式,严格遵守。
- 如果当前模式禁止了你需要的工具,告诉用户"当前是 plan 模式,该操作不允许,请用 /auto 切换到 auto 模式"。不要试图绕过限制。

# 响应风格
- 简短、技术性,列表优于长段落
- 避免营销话术/重复显而易见的信息
- 只在必要时解释

# 失败处理
- 信息不足: 继续inspect文件,必要时问一个聚焦问题
- 任务模糊: 陈述假设,按最安全解读 proceed

# 运行时
- 当前工作目录:%s`,
		workspace,
	)
	if skillCatalog != "" {
		base += "\n\n**Available Skills**(用户预定义的指令包,description 跟当前任务对得上就调 `LoadSkill` 加载正文)\n" + skillCatalog
	}
	return base
}

// BuildSystemPrompt 主 agent 的 system prompt = 共用核心 + 会话摘要尾部。
// 摘要垫在最后:核心 + skill 那段会话内字节不变,即使摘要每次压缩都变,前缀仍命中,
// 失效点只从摘要开始(详见前缀缓存优化设计)。
func BuildSystemPrompt(workspace, skillCatalog, summary string) string {
	base := coreSystemPrompt(workspace, skillCatalog)
	// 持久偏好/项目约定:读会话内冻结的快照(currentPrefs),只在启动 / 压缩时由 RefreshPreferences 刷新,
	// 中途每轮复用同一份 → 前缀稳定、缓存命中。会话中途写入的 AGENTS.md 下次压缩/重启才生效。
	if prefs := currentPrefs(); prefs != "" {
		base += "\n\n# 用户偏好 / 项目约定(持久记忆,需严格遵循)\n" + prefs
	}
	if summary != "" {
		base += "\n\n# 当前工作状态(checkpoint,此前对话的压缩,延续上下文)\n" + summary
	}
	return base
}

func StartStream(
	ctx context.Context,
	models ModelConfig,
	history []ChatMessage,
	mode AgentMode,
	workspace string,
	skillCatalog string, // 见下方 system prompt 注入逻辑;空串表示当前没有 skill
	summary string, // 会话压缩摘要,垫在 system prompt 末尾;空串表示尚未压缩
	forceRole string, // 用户锁定的模型角色("flash"/"pro");空串或 "auto" 表示走关键词路由
	workingMode WorkingMode, // 工作模式:每轮把对应 skill 引导追加到最后一条 user 消息(renderWorkingMode)
) (tea.Cmd, <-chan tea.Msg) {
	ch := make(chan tea.Msg, 128)

	go func() {
		defer close(ch)

		convo := append([]ChatMessage(nil), history...)
		// 从 history 里找最后一条 user 消息,作为派给子 agent 的"任务背景"
		latestUserTask := ""
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].Role == "user" {
				latestUserTask = history[i].Content
				break
			}
		}
		if workspace != "" {
			// 在首轮注入 system 提示:当前工作目录 + 任务拆解 + plan 节点的 model 选择指南。
			// 入口模型已经由 keyword router 决定(flash 或 pro);模型自行判断要不要 CreatePlan 拆任务。
			if len(convo) == 0 || convo[0].Role != "system" {
				sysBase := BuildSystemPrompt(workspace, skillCatalog, summary)
				convo = append([]ChatMessage{{Role: "system", Content: sysBase}}, convo...)
			}
		}

		// 当前活跃角色,起手 flash。升级到 pro 后不回头。
		role := tools.RoleFlash
		currentEntry := models.Flash
		if currentEntry.Model == "" {
			currentEntry = models.Pro // 退化:flash 未设时,直接用 pro
			role = tools.RolePro
		}

		// 起手模型选择:
		//   - forceRole=flash/pro:用户用 /model 锁定,直接定死,绕过关键词路由;
		//   - 否则(""/auto):入口关键词路由(纯本地、零延迟、无 LLM)——命中复杂关键词 /
		//     消息 > 500 字 → pro,否则 flash。
		// 无论哪种,本轮锁定该模型,主循环不再自动切换。
		switch forceRole {
		case tools.RoleFlash:
			if models.Flash.Model != "" {
				role, currentEntry = tools.RoleFlash, models.Flash
			}
		case tools.RolePro:
			if models.Pro.Model != "" {
				role, currentEntry = tools.RolePro, models.Pro
			}
		default:
			if latestUserTask != "" && models.Pro.Model != "" {
				if RouteByKeyword(latestUserTask) == "pro" {
					role, currentEntry = tools.RolePro, models.Pro
				}
			}
		}
		ch <- ModelSwitchMsg{Role: role, ModelID: currentEntry.Model}

		toolSpecs := buildToolSpecs(mode)

		// 发出本轮"实际发送"的前缀快照(system 文本 + tool specs JSON),供 TUI 持久化:
		// 重启变化检测 + 缓存友好压缩复刻旧前缀。tool specs 随 mode/role 变,故必须存实际值。
		{
			sysContent := ""
			if len(convo) > 0 && convo[0].Role == "system" {
				sysContent = convo[0].Content
			}
			ch <- PrefixSnapshotMsg{Model: currentEntry.Model, SystemPrompt: sysContent, ToolSpecsJSON: MarshalToolSpecs(toolSpecs)}
		}

		// 完成度门禁状态:lastTodo = 最近一次 Todo 快照(判断是否还有未完成项);
		// gateNudges = 连续被门禁挡回的次数(死循环保护,见 completionGate)。
		var lastTodo []PlanItem
		gateNudges := 0

		// lastFile = 本轮最近操作的文件路径,给 Update 漏 path 时兜底回填(issue #81)。
		var lastFile string

		// noProgressRounds = 连续「全部工具调用失败」的轮数;到 maxNoProgressRounds 判定卡死中止。
		// 任一轮有工具成功就归零。无绝对轮数上限(见 maxNoProgressRounds 注释),跑到模型自己停为止。
		noProgressRounds := 0

		// 循环内 auto-compact 状态:lastPromptTokens = 上一轮真实 prompt tokens(判是否该压缩);
		// inLoopCompactOff = 本轮压缩失败后置位,退回"撞窗口优雅报错",避免反复压缩刷请求。
		lastPromptTokens := 0
		inLoopCompactOff := false

		for {
			// 检查 context 是否取消(ESC/退出),提前退出不卡后台
			if ctx.Err() != nil {
				return
			}

			// 循环内 auto-compact:上一轮 prompt 接近上下文窗口就先压缩历史腾空间再继续(不熔断停)。
			// 对标 Claude Code:压缩 convo[1:] 成摘要、重建 [system(新摘要)]+尾部,新摘要经 CompactedMsg
			// 回传 TUI 存 session(否则被剥的 system 摘要会丢失),history 截断经 HistoryUpdateMsg 同步。
			if ctxWin := currentEntry.ContextWindow; !inLoopCompactOff && ctxWin > 0 &&
				lastPromptTokens >= ctxWin*compactTriggerPct/100 &&
				len(convo) > 0 && convo[0].Role == "system" {
				hist := convo[1:]
				sum, cutIdx, turns, cerr := RunCompression(convo[0].Content, MarshalToolSpecs(toolSpecs), hist, currentEntry, ctxWin)
				if cerr != nil {
					inLoopCompactOff = true // 压不动(历史太短/摘要失败)→ 本轮不再尝试,退回撞窗口报错
				} else {
					summary = sum
					kept := append([]ChatMessage(nil), hist[cutIdx:]...)
					convo = append([]ChatMessage{{Role: "system", Content: BuildSystemPrompt(workspace, skillCatalog, summary)}}, kept...)
					lastPromptTokens = 0 // 压完归零,等下一轮真实 usage 再判
					// 防死循环:若压缩后历史仍超阈值(最近 5 轮本身就超窗口,RunCompression 切点缩不动),
					// 再压也无效 → 本轮关掉循环内压缩,退回撞窗口优雅报错,避免反复压缩刷请求。
					if EstimatePromptTokens(workspace, skillCatalog, summary, kept) >= ctxWin*compactTriggerPct/100 {
						inLoopCompactOff = true
					}
					ch <- CompactedMsg{Summary: summary, Turns: turns}
					ch <- HistoryUpdateMsg{History: convo}
				}
			}
			// 按本轮模型支不支持视觉,即时把带图消息渲染成 base64 或 路径+OCR(见 renderConvoImages)。
			// 只渲染发出去的副本,convo 规范形态(只存路径)不变。
			// 渲染后的副本才是真正发出的输入 —— max_tokens 夹取按它估算(渲染会追加 OCR 文本等,
			// 比规范 convo 大;按规范估会低估输入、夹不住,仍可能爆窗)。
			rendered := renderConvoImages(renderWorkingMode(convo, workingMode), currentEntry.Vision)
			assistantContent, reasoning, toolCalls, finishReason, usage, err := streamOnce(
				ctx,
				currentEntry.APIKey, currentEntry.BaseURL, currentEntry.Model,
				rendered, clampMaxTokens(currentEntry.MaxTokens, currentEntry.ContextWindow, rendered), toolSpecs,
				currentEntry.ReasoningEffort, currentEntry.Thinking,
				ch,
			)
			// 自愈兜底:被端点以"不支持图片输入"拒掉(无论 base64 是探测误判发的、还是历史里混进来的)→
			// 把该模型降级为无视觉(本轮后续也生效),用"剥图"渲染重发一次,并通知 TUI 纠正缓存。
			// 不限定 currentEntry.Vision —— base64 可能从别处混入,撞到就无条件回退。用户看不到这个 404。
			if err != nil && isImageInputUnsupported(err) {
				currentEntry.Vision = false
				ch <- VisionUnsupportedMsg{Model: currentEntry.Model, BaseURL: currentEntry.BaseURL}
				rendered := renderConvoImages(renderWorkingMode(convo, workingMode), false)
				assistantContent, reasoning, toolCalls, finishReason, usage, err = streamOnce(
					ctx,
					currentEntry.APIKey, currentEntry.BaseURL, currentEntry.Model,
					rendered, clampMaxTokens(currentEntry.MaxTokens, currentEntry.ContextWindow, rendered), toolSpecs,
					currentEntry.ReasoningEffort, currentEntry.Thinking,
					ch,
				)
			}
			if err != nil {
				// context 取消是主动中断,不报 Error 给 UI。
				if errors.Is(err, context.Canceled) {
					return
				}
				ch <- StreamErrMsg{err}
				return
			}
			// 主 agent 的 token 用量发给 TUI 显示。
			if usage != nil {
				ch <- UsageMsg{Usage: *usage}
				lastPromptTokens = usage.PromptTokens // 供下一轮判断是否触发循环内 auto-compact
			}

			// 截断判定双信号(后端可能不给 finish_reason,尤其代理/自建池子):
			// finish_reason==length 或 生成 token 撞上 max_tokens 上限。
			// 提前算 —— 下面 tool_call 截断分支也要用它。
			truncated := finishReason == "length" ||
				(usage != nil && currentEntry.MaxTokens > 0 && usage.CompletionTokens >= currentEntry.MaxTokens)

			// --- 落历史前拦截两类"看似正常结束、实为异常"的响应(issue #169)---
			// 分类见 classifyStreamResult;两类都不落这条 assistant 进历史,催模型自愈,
			// 连续到 maxGateNudges 上限仍无法自愈则回一条可读错误,不再空转。
			switch classifyStreamResult(assistantContent, reasoning, toolCalls, truncated) {
			case outcomeTruncatedTool:
				// tool_call 被输出长度上限截断:最后一个 tool_call 的 arguments 是残缺 JSON,
				// 执行必然 "unexpected end of JSON input";模型只当普通 parse 错、重试同一个
				// 超大调用 → 再截断 → 死循环,对话卡死无法恢复(会话 A)。催其改用分块重试。
				if gateNudges < maxGateNudges {
					gateNudges++
					convo = append(convo, ChatMessage{Role: "user", Content: truncatedToolNudge})
					continue
				}
				ch <- StreamErrMsg{errTruncatedToolLoop}
				return
			case outcomeEmpty:
				// 空响应:供应商返回了空(限流 / 不稳定,免费 / 代理端点常见)。别静默当成
				// "完成"(否则用户只见"无响应 / 卡死",会话 B),催模型重新回复。
				if gateNudges < maxGateNudges {
					gateNudges++
					convo = append(convo, ChatMessage{Role: "user", Content: emptyResponseNudge})
					continue
				}
				ch <- StreamErrMsg{errEmptyResponseLoop}
				return
			}

			// 把本轮 assistant 回复写入历史(含 reasoning_content,thinking 模型下轮需要)
			convo = append(convo, ChatMessage{
				Role:             "assistant",
				Content:          assistantContent,
				ReasoningContent: reasoning,
				ToolCalls:        toolCalls,
			})

			if len(toolCalls) == 0 {
				// 完成度门禁:别把"这轮没工具调用"直接当成"任务完成"。
				// 纯文本被长度截断、或还有未完成 todo 时,注入一条提示再跑一轮,催它继续。
				if nudge := completionGate(truncated, lastTodo, &gateNudges); nudge != "" {
					convo = append(convo, ChatMessage{Role: "user", Content: nudge})
					continue
				}
				ch <- HistoryUpdateMsg{History: convo}
				ch <- StreamDoneMsg{}
				return
			}
			gateNudges = 0 // 有工具调用 = 有进展,重置门禁计数

			// roundProgress = 本轮是否有任一工具调用成功;决定 noProgressRounds 是归零还是累加。
			roundProgress := false
			// turnToolBytes = 本轮已计入历史的工具结果合计字节;clampTurnToolOutput 据此做「本轮合计上限」,
			// 防止一轮并发多个 tool call、单条都 ≤96KB 但合计撑爆上下文(issue #135 遗留缺口)。每轮(此处)归零。
			turnToolBytes := 0

			// 执行每个工具调用,把结果加进 convo。
			// 这些工具被 deepx 拦截 (不走 Executor):
			//   - CreatePlan         → 解析后产 PlanCreatedMsg,触发 DAG 调度(派并发子 agent)
			//   - Todo               → 解析后产 PlanCreatedMsg 刷新可见清单,主 agent 自己执行,不派子 agent
			//   - UpdatePlanStatus   → 解析后产 TaskStatusMsg,UI 更新单项状态
			//   - SwitchModel        → 改本轮 currentEntry / role,通过 ModelSwitchMsg 通知 UI
			// 拦截后仍要给 LLM 一个 fake tool result,让 OpenAI 工具循环能正常推进。
			for _, tc := range toolCalls {
				// review 模式:对 Write/Update/Bash 发起审核。
				// Workflow(run) 无论何种模式都强制确认:它会执行模型生成的脚本(进而派子 agent)。
				var reviewCh chan bool
				if (mode == AgentMode_Review && isReviewable(tc.Function.Name)) || isWorkflowRun(tc) {
					reviewCh = make(chan bool, 1)
				}
				ch <- ToolCallStartMsg{Name: tc.Function.Name, Args: tc.Function.Arguments, ReviewCh: reviewCh}
				if reviewCh != nil {
					select {
					case approved := <-reviewCh:
						if !approved {
							ch <- ToolCallResultMsg{Name: tc.Function.Name, Output: "操作已被用户拒绝 (review 模式)", Success: false}
							convo = append(convo, ChatMessage{
								Role:       "tool",
								ToolCallID: tc.ID,
								Name:       tc.Function.Name,
								Content:    "操作已被用户拒绝 (review 模式)",
							})
							continue
						}
					case <-ctx.Done():
						return
					}
				}

				var result tools.ToolResult
				switch tc.Function.Name {
				case "AskUser":
					// 弹 TUI 选择框,阻塞等用户选完(同 review 的 channel 骨架)。
					questions, perr := parseAskUserArgs(tc.Function.Arguments)
					if perr != nil {
						result = tools.ToolResult{Output: perr.Error(), Success: false}
					} else {
						respCh := make(chan string, 1)
						ch <- AskUserMsg{Questions: questions, ResponseCh: respCh}
						select {
						case answer := <-respCh:
							if answer == "" {
								result = tools.ToolResult{Output: "用户取消了选择(或希望改用文字回答)。请改用普通对话继续询问,不要重复弹窗。", Success: false}
							} else {
								result = tools.ToolResult{Output: answer, Success: true}
							}
						case <-ctx.Done():
							return
						}
					}
				case "Remember":
					// 持久化用户偏好到 AGENTS.md(global=~/.deepx,project=workspace),下次启动注入。
					scope, content, perr := parseRememberArgs(tc.Function.Arguments)
					if perr != nil {
						result = tools.ToolResult{Output: perr.Error(), Success: false}
					} else if path, serr := saveMemory(scope, content, workspace); serr != nil {
						result = tools.ToolResult{Output: "记忆保存失败:" + serr.Error(), Success: false}
					} else {
						level := "全局"
						if scope == "project" {
							level = "本项目"
						}
						result = tools.ToolResult{
							Output:  fmt.Sprintf("已记住(%s):%s\n(写入 %s;本会话内我已知晓,正式注入系统提示词在下次压缩 / 重启后)", level, content, path),
							Success: true,
						}
					}
				case "Workflow":
					// 创建/运行/列出 workflow。run 已在上方经 reviewCh 确认;run 期间的进度
					// 经 ch 以 TokenMsg 流式呈现,结果回给模型续写总结。
					result = handleWorkflowTool(ctx, tc, models, mode, workspace, skillCatalog, ch)
				case "CreatePlan":
					plans, perr := parseCreatePlanArgs(tc.Function.Arguments)
					if perr != nil {
						result = tools.ToolResult{Output: perr.Error(), Success: false}
					} else {
						// 1. 通知 UI 渲染 plan 树
						ch <- PlanCreatedMsg{Plans: plans, Kind: "createplan"}
						// 2. 拍平成 DAG 节点并同步执行
						nodes := flattenPlans(plans)
						exec := func(n *schedulerNode, preds map[string]string) (string, error) {
							res := runSubAgent(ctx, subAgentInput{
								Models:       models,
								Entry:        resolveModelEntry(n.Model, models),
								NodeID:       n.ID,
								NodeTitle:    n.Title,
								UserTask:     latestUserTask,
								Predecessors: preds,
								Workspace:    workspace,
								SkillCatalog: skillCatalog,
								Mode:         mode,
							})
							if res.Err != nil {
								return "", res.Err
							}
							return res.Summary, nil
						}
						final := runDAG(ctx, nodes, exec, ch)
						// 3. 拼汇总 ToolResult 给 pro,让它写最终给用户的总结
						var summary strings.Builder
						summary.WriteString(fmt.Sprintf("已执行完毕,共 %d 个节点。\n", len(final)))
						successCount := 0
						for _, n := range final {
							icon := "?"
							switch n.Status {
							case PlanStatusDone:
								icon = "✓"
								successCount++
							case PlanStatusFailed:
								icon = "✗"
							case PlanStatusBlocked:
								icon = "⏸"
							}
							summary.WriteString(fmt.Sprintf("  %s [%s] %s — %s\n", icon, n.ID, n.Title, n.Summary))
						}
						summary.WriteString(fmt.Sprintf("\n%d/%d 成功。请基于以上结果给用户写一段简洁的最终总结。", successCount, len(final)))
						result = tools.ToolResult{
							Output:  summary.String(),
							Success: successCount > 0,
						}
					}
				case "Todo":
					// 主 agent 自驱动的可见待办清单:全量快照覆盖当前 planState,不派子 agent。
					// 复用 PlanCreatedMsg 让 UI 直接按各项 status 渲染 checkbox。
					items, perr := parseTodoArgs(tc.Function.Arguments)
					if perr != nil {
						result = tools.ToolResult{Output: perr.Error(), Success: false}
					} else {
						lastTodo = items // 记录最新快照,供完成度门禁判断是否还有未完成项
						ch <- PlanCreatedMsg{Plans: items, Kind: "todo"}
						done := 0
						for _, it := range items {
							if it.Status == PlanStatusDone {
								done++
							}
						}
						result = tools.ToolResult{
							Output:  fmt.Sprintf("待办已更新:%d/%d 完成。继续按清单执行,每开始/完成一步就重发整张 todos 更新状态。", done, len(items)),
							Success: true,
						}
					}
				case "UpdatePlanStatus":
					id, st, summary, perr := parseUpdatePlanStatusArgs(tc.Function.Arguments)
					if perr != nil {
						result = tools.ToolResult{Output: perr.Error(), Success: false}
					} else {
						ch <- TaskStatusMsg{ID: id, Status: st, Summary: summary}
						result = tools.ToolResult{
							Output:  fmt.Sprintf("已记录: %s = %s", id, st),
							Success: true,
						}
					}
				case "SwitchModel":
					// 单向升级到 pro。已经在 pro 是 no-op,flash → pro 实际换 currentEntry。
					// 切换立即生效:本轮工具循环下一次 streamOnce 用新 entry。
					reason := parseSwitchModelReason(tc.Function.Arguments)
					if forceRole == tools.RoleFlash {
						// 用户用 /model flash 锁定,模型无权越权升级。
						result = tools.ToolResult{
							Output:  "用户已锁定 flash 模型(/model flash),忽略本次升级,继续用 flash 完成任务。",
							Success: true,
						}
					} else if role == tools.RolePro {
						result = tools.ToolResult{
							Output:  "已经在 pro 模型,无需切换。继续完成任务即可。",
							Success: true,
						}
					} else if models.Pro.Model == "" {
						result = tools.ToolResult{
							Output:  "pro 模型未配置(model.yaml 里 pro.model 为空),无法升级。继续用 flash 处理。",
							Success: false,
						}
					} else {
						role = tools.RolePro
						currentEntry = models.Pro
						// 工具表不随角色变(各角色一致),无需重算 toolSpecs。
						ch <- ModelSwitchMsg{Role: role, ModelID: currentEntry.Model, Reason: reason}
						result = tools.ToolResult{
							Output:  fmt.Sprintf("已切到 pro 模型 (%s)。本轮剩余请求 + reasoning 用 pro 处理。", currentEntry.Model),
							Success: true,
						}
					}
				case "OCR":
					// 视觉模型本就能看图。它对"已经内联给它的那张图"还调 OCR(mimo 甚至会先 ls 缓存目录
					// 再 OCR),纯属冗余绕路 —— base64 都喂到嘴边了还去翻文件。软提醒(消息备注/工具描述)
					// 压不住这个模型,这里在执行层硬拦:不真跑 OCR,把它怼回去直接看图。不改工具表,缓存安全。
					// 只拦"对已内联图的 OCR";OCR 一个没内联的文件路径(视觉模型确实看不到的)照常放行。
					if currentEntry.Vision && ocrTargetsInlinedImage(tc.Function.Arguments, convo) {
						result = tools.ToolResult{
							Output:  "你是视觉模型,这张图已经以图片形式内联在当前对话里了,请直接查看图片作答 —— 不要调用 OCR,也不要用 ls/find 去文件系统查找图片文件。",
							Success: false,
						}
					} else if prev, done := priorOCRResult(tc.Function.Arguments, convo); done {
						// 同一图片路径已 OCR 过 → 别重复:路径赖在历史里,模型会反复 OCR、用户喊停也拦不住(issue #146)。
						// 回上次结果 + 硬性提示别再调,Success=false 让 maxNoProgressRounds 卡死断路器能介入。
						result = tools.ToolResult{
							Output:  "这张图之前已经 OCR 过了,结果是:\n" + prev + "\n\n请勿对同一张图重复 OCR。若仍需要图中信息,请直接询问用户图里写了什么,或让用户改用支持视觉的模型。",
							Success: false,
						}
					} else {
						result = executeTool(tc, mode, &lastFile)
					}
				case "Explore":
					// 派只读探索子 agent:在独立上下文里搜索本地代码库或外部仓库/网页,只回结论。
					// 用 flash 跑(便宜,搜索不是推理);独立上下文不污染主会话。详见 agent/explore.go。
					task, thoroughness := parseExploreArgs(tc.Function.Arguments)
					if strings.TrimSpace(task) == "" {
						result = tools.ToolResult{Output: "Explore 需要 task 参数(要探索的问题 / 要带回的结论)。", Success: false}
					} else {
						summary, eerr := runExplore(ctx, exploreInput{
							Entry:        resolveModelEntry("flash", models),
							Task:         task,
							Thoroughness: thoroughness,
							Workspace:    workspace,
						})
						if eerr != nil {
							result = tools.ToolResult{Output: "探索失败:" + eerr.Error(), Success: false}
						} else {
							result = tools.ToolResult{Output: summary, Success: true}
						}
					}
				default:
					result = executeTool(tc, mode, &lastFile)
				}

				if result.Success {
					roundProgress = true
				}
				ch <- ToolCallResultMsg{
					Name:    tc.Function.Name,
					Output:  result.Output,
					Success: result.Success,
				}
				convo = append(convo, ChatMessage{
					Role:       "tool",
					ToolCallID: tc.ID,
					Name:       tc.Function.Name,
					// 本轮合计上限:单条已被 clampToolOutput 限到 96KB,这里再按「本轮所有工具结果合计」收口,
					// 防止一轮并发多条把上下文顶爆(issue #135)。只截入历史的内容,UI(上方 ToolCallResultMsg)仍展示完整结果。
					Content: clampTurnToolOutput(tc.Function.Name, result.Output, &turnToolBytes),
				})
			}
			ch <- HistoryUpdateMsg{History: convo}

			// 无进展断路器:本轮工具全失败则累加,任一成功则归零;连续卡死到上限就暂停。
			if roundProgress {
				noProgressRounds = 0
			} else {
				noProgressRounds++
				if noProgressRounds >= maxNoProgressRounds {
					ch <- StreamErrMsg{fmt.Errorf("连续 %d 轮工具调用均未成功,疑似卡死或反复失败,已暂停。可输入「继续」让它接着尝试,或换个说法。", maxNoProgressRounds)}
					return
				}
			}
		}
	}()

	return ListenToStream(ch), ch
}

// streamOnce 发起一次 chat/completions 请求,返回 (content, reasoning_content, tool_calls, usage, error)。
//
// reasoningEffort / thinking 是跨供应商通用的推理参数,**空字符串严格不发送**(走各家 API 默认),
// 这是兼容 MiMo 等不支持这俩字段的模型的关键 —— 任何不主动启用的模型都不会被多余字段炸 400。
// maxGateNudges 是完成度门禁连续催继续的上限:催够这么多次模型仍不动工具,就放行结束,防死循环/空转。
const maxGateNudges = 3

// streamOutcome 是对一次 stream 结果的分类,决定主循环该如何处置(issue #169)。
type streamOutcome int

const (
	outcomeNormal        streamOutcome = iota // 正常:落历史 / 执行工具
	outcomeTruncatedTool                      // tool_call 被长度截断:丢弃、催分块重试
	outcomeEmpty                              // 空响应:催重试
)

// classifyStreamResult 分类一次 stream 结果。纯函数,便于单测。
//   - tool_call 被截断(truncated 且有 tool_calls):arguments 残缺,执行必失败并诱发死循环。
//   - 空响应(无 tool_calls 且 content / reasoning 全空):供应商返回了空。
//
// 两者都不能当成正常结束。其余情形(有内容 / 有完整工具调用 / 纯文本被截断)均按正常处理,
// 纯文本截断由下游 completionGate 负责催继续。
func classifyStreamResult(assistantContent, reasoning string, toolCalls []ToolCall, truncated bool) streamOutcome {
	if truncated && len(toolCalls) > 0 {
		return outcomeTruncatedTool
	}
	if len(toolCalls) == 0 && assistantContent == "" && reasoning == "" {
		return outcomeEmpty
	}
	return outcomeNormal
}

// truncatedToolNudge / emptyResponseNudge:回传给模型的催继续提示(issue #169)。
// 刻意不带 "Error:" / "错误:" 前缀,给模型可自纠的空间(见工具调用 harness 审计),
// 而不是把它当成一次硬失败。
const (
	truncatedToolNudge = "(你上一次的工具调用因输出长度上限被截断,参数 JSON 不完整、无法执行——" +
		"通常是单次写入的内容过大。请把它拆成更小的多次写入重试:先用 Write 写入前面一部分" +
		"(比如上次内容的一半或更少),再用 Update 逐段追加剩下的;不要再整段重复大写入。)"
	emptyResponseNudge = "(你上一条回复是空的,没有任何文字内容,也没有工具调用。" +
		"请重新回复:继续完成当前任务,该调用工具就调用工具,不要返回空响应。)"
)

// errTruncatedToolLoop / errEmptyResponseLoop:连续催到上限仍无法自愈时回给 UI 的可读错误,
// 避免"静默假装完成"或无限空转(issue #169)。
var (
	errTruncatedToolLoop = errors.New("模型连续多次因输出长度上限截断工具调用(通常是单次写入的文件过大)。" +
		"请让它把大文件分块写入,或用 /provider 换用输出上限更大的模型。")
	errEmptyResponseLoop = errors.New("模型连续多次返回空响应,可能是供应商限流或不稳定(免费 / 代理端点常见)。" +
		"请稍后重试,或用 /provider 换用更稳定的模型。")
)

// completionGate 在"这轮没有工具调用"时决定是否还要继续:
//   - 返回非空 = 应继续,内容是注入给模型的提示(催它接着干);
//   - 返回 "" = 真的结束。
//
// 触发继续:① 上轮被截断(truncated,话没说完);② 还有未完成的 todo。
// 死循环保护:连续催 maxGateNudges 次仍无进展就放行。纯对话/单步任务(没建 todo、未截断)照常一轮结束。
func completionGate(truncated bool, todo []PlanItem, nudges *int) string {
	if *nudges >= maxGateNudges {
		return ""
	}
	if truncated {
		*nudges++
		return "(你上一条回复似乎被长度上限截断,没有输出完。请接着把没做完的部分继续做完——该调用工具就调用,不要停在这里总结。)"
	}
	if pending := countPendingTodos(todo); pending > 0 {
		*nudges++
		return fmt.Sprintf("(待办还有 %d 项未完成,任务尚未结束。请继续执行下一步并调用相应工具,不要提前收尾;若确实卡住无法继续,再说明原因。)", pending)
	}
	return ""
}

// countPendingTodos 统计 todo 里仍待办的项(pending/running);done/failed/blocked 不计入。
func countPendingTodos(todo []PlanItem) int {
	n := 0
	for _, it := range todo {
		if it.Status == PlanStatusPending || it.Status == PlanStatusRunning {
			n++
		}
	}
	return n
}

// clampMaxTokens 把单次输出预算(max_tokens)夹进窗口:input + max_tokens 不得超过模型上限,
// 否则即便输入远没满,API 也会以 400 "maximum context length ... you requested ..." 拒掉
// (input 65% + 默认 384K max_tokens 就能在 1M 窗口下溢出)。
//
//	maxTokens=0(走模型默认)或 ctxWin<=0(窗口未知)→ 不夹。
//	否则 max_tokens = min(配置值, 窗口 − 估算输入 − 5% 边际)。
//
// 5% 边际吸收 token 估算误差(实测 ~2.5%)+ 配置窗口与模型真实上限的零头。
func clampMaxTokens(maxTokens, ctxWin int, convo []ChatMessage) int {
	if maxTokens <= 0 || ctxWin <= 0 {
		return maxTokens
	}
	inputEst := 0
	for i := range convo {
		inputEst += MsgTokens(convo[i])
	}
	avail := ctxWin - inputEst - ctxWin/20
	if avail < maxTokens {
		if avail < 1 {
			return 1 // 输入已逼近窗口:夹到最小让请求发得出去(真问题靠压缩解决,不在这)
		}
		return avail
	}
	return maxTokens
}

// isRetryableStatus 判断 HTTP 状态码是否值得重试:429 限流、5xx 服务端错误、408/409/425 等瞬时类。
// 400/401/403/404/422 是请求本身的问题(尤其 400 常是上下文超长),重试无意义 → 直接返回错误。
func isRetryableStatus(code int) bool {
	switch code {
	case 408, 409, 425, 429, 500, 502, 503, 504:
		return true
	}
	return false
}

// retryBackoff 计算第 attempt 次重试(从 0 起)的等待时长:
// 429 优先按 Retry-After 响应头(秒数,夹到 60s);否则指数退避 1→2→4…封顶 30s,叠 +0~20% 抖动防同步重试雪崩。
func retryBackoff(attempt int, retryAfter string) time.Duration {
	if retryAfter != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && secs > 0 {
			return time.Duration(min(secs, 60)) * time.Second // 夹一下:防个别服务器给超大值
		}
	}
	d := min(time.Second<<uint(attempt), 30*time.Second)        // 1,2,4,8,16,32… 封顶 30s
	return d + time.Duration(rand.Int63n(int64(d)/5+1))         // +0~20% 抖动
}

// sleepCtx 等待 d;期间 ctx 取消(ESC/退出)则提前返回 false,让重试循环立刻中止。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// --- 流式请求的超时兜底(issue #181)---
//
// 此前主流式路径上一个超时都没有:ctx 来自 context.WithCancel(无 deadline)、用的是
// http.DefaultClient(Timeout 为 0)、SSE 读取是裸 scanner.Scan() 阻塞。端点接了连接却不吐
// 数据时会无限等下去,UI 上就是 spinner 一直转、用户完全不知道卡在哪(OpenRouter 实测 20 分钟)。
// 注意 maxNoProgressRounds 兜不住这种情况:它数的是「轮次」,而流卡死时根本进不到下一轮。
//
// 刻意不做「总时长超时」:推理模型思考十几分钟是合法的,按总时长砍会误杀长任务。判据改用
// 「空闲」—— 连接活着时 SSE 上一定有东西在流(推理模型持续吐 reasoning_content;纯思考期
// 网关也会发 keep-alive 注释行)。连续这么久一个字节都收不到 = 连接已死。这个判据与「模型想
// 多久」无关,所以能写死、不必做成配置项(issue #181 原提议是加一组超时环境变量)。

// streamIdleTimeout:SSE 上连续这么久没收到任何一行(含 keep-alive 注释行)即判定卡死。
// 远宽于任何网关的 keep-alive 间隔,又远小于用户能忍的等待。
// 是 var 而非 const 仅为让测试能调小后跑真实的超时路径;运行时不改。
var streamIdleTimeout = 120 * time.Second

// streamHeaderTimeout:请求发出到响应头返回的上限。流式请求的响应头是立刻回的
// (模型在响应头之后才开始吐 token),所以可以给得比较紧,且这一层的失败发生在流开始前,
// 能直接复用下面的重试循环。
//
// 只用于流式:非流式请求(CallOnce / CallWithTools)的响应头要等整个生成完成才返回,
// 套上这个超时会把大摘要直接掐死 —— 那条路已由 compactionTimeout 兜住,别动。
const streamHeaderTimeout = 60 * time.Second

// errStreamIdle 是空闲看门狗触发时的哨兵错误(实际返回的是包了具体时长的 wrap,
// 用 errors.Is 判定)。必须能和 context.Canceled 区分开:后者是用户按 ESC、上层静默收尾,
// 前者要明确告诉用户卡在哪(issue #181 的核心诉求)。
var errStreamIdle = errors.New("流式响应空闲超时")

// streamHTTPClient 专供流式请求:在 DefaultTransport 基础上只加响应头超时,
// 连接池 / 代理 / TLS 等默认行为原样保留。不设 Client.Timeout —— 那是「整个请求(含读完 body)」
// 的上限,对流式等于给回复长度设死线,长回复会被拦腰砍断;「活着但不动」由空闲看门狗负责。
var streamHTTPClient = &http.Client{
	Transport: func() http.RoundTripper {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.ResponseHeaderTimeout = streamHeaderTimeout
		return tr
	}(),
}

// streamCtxErr 把 ctx 取消翻译成对上层有意义的错误:空闲看门狗触发 → errStreamIdle
// (可读、会展示给用户);否则是用户 ESC / 退出 → context.Canceled(上层静默收尾)。
func streamCtxErr(ctx context.Context) error {
	if cause := context.Cause(ctx); errors.Is(cause, errStreamIdle) {
		return cause
	}
	return ctx.Err()
}

// maxIdleRetries:空闲超时后的最大重试次数。刻意远小于 maxAPIRetries(10)——
// 每次要先空等满 streamIdleTimeout(120s)才能判定,复用 10 次就是 20 分钟起步,
// 正好复刻 issue #181 抱怨的那种「转到天荒地老」。2 次 = 最坏约 6 分钟,且全程
// 状态行有「重试 N/2 · 响应超时」可看,而不是一个什么都不说的 spinner。
const maxIdleRetries = 2

// streamOnce 在 streamAttempt 之上加一层「空闲超时重试」。
//
// 只在「空闲超时 且 这次尝试零输出」时重来:此刻没有任何 token 吐给 UI、assistant 也没进历史,
// 重试是干净的(与 maxAPIRetries 只重试「进流式之前」是同一条原则)。一旦已经吐过字,
// 重试会让内容重复出现在对话区,所以直接把错误连同已吐内容返回,由上层收尾。
//
// 每次尝试内部各自派生可取消 ctx(见 streamAttempt),所以被看门狗 cancel 掉的 ctx
// 不会污染下一次重试;父 ctx(用户 ESC)始终原样透传。
func streamOnce(
	ctx context.Context,
	apiKey, baseURL, modelID string,
	convo []ChatMessage,
	maxTokens int,
	toolSpecs []tools.OpenAIToolSpec,
	reasoningEffort string,
	thinking string,
	ch chan<- tea.Msg,
) (string, string, []ToolCall, string, *UsageInfo, error) {
	for attempt := 0; ; attempt++ {
		content, reasoning, toolCalls, finishReason, usage, err := streamAttempt(
			ctx, apiKey, baseURL, modelID, convo, maxTokens, toolSpecs, reasoningEffort, thinking, ch,
		)
		emitted := content != "" || reasoning != "" || len(toolCalls) > 0
		if !errors.Is(err, errStreamIdle) || emitted || attempt >= maxIdleRetries {
			return content, reasoning, toolCalls, finishReason, usage, err
		}
		// 零输出的空闲超时 → 干净重试。复用同一套退避 + 状态行提示。
		d := retryBackoff(attempt, "")
		ch <- RetryNoticeMsg{Attempt: attempt + 1, Max: maxIdleRetries, Delay: d, Reason: "响应超时"}
		if !sleepCtx(ctx, d) { // 退避期间用户 ESC
			return content, reasoning, toolCalls, finishReason, usage, ctx.Err()
		}
	}
}

func streamAttempt(
	ctx context.Context,
	apiKey, baseURL, modelID string,
	convo []ChatMessage,
	maxTokens int,
	toolSpecs []tools.OpenAIToolSpec,
	reasoningEffort string,
	thinking string,
	ch chan<- tea.Msg,
) (string, string, []ToolCall, string, *UsageInfo, error) {

	body, err := json.Marshal(chatRequest{
		Model:     modelID,
		MaxTokens: maxTokens,
		Stream:    true,
		StreamOptions: &streamOptions{
			IncludeUsage: true,
		},
		// 发送前消毒:剔除孤儿 tool 消息 / 剥掉无响应的 tool_calls,避免 API 400(见 issue #94),
		// 并自愈已被写进历史的坏配对(下次请求即恢复)。正常对话是 no-op。
		Messages: sanitizeToolPairs(convo),
		Tools:    toolSpecs,
		// thinking 和 reasoning_effort 是两个独立顶层字段。各自 omitempty,
		// 用户设了就发、没设就不发,白名单内的值才透传(防 yaml 笔误)。
		Thinking:        buildThinkingOption(thinking),
		ReasoningEffort: validateReasoningEffort(reasoningEffort),
	})
	if err != nil {
		return "", "", nil, "", nil, err
	}
	// 包一层可取消 ctx:空闲看门狗要能掐断底层网络读,所以请求必须挂在它上面(见下方 scanner 循环)。
	// 用 WithCancelCause 而非 WithCancel —— 看门狗取消时带上 errStreamIdle,才能和用户 ESC 区分开。
	ctx, cancelStream := context.WithCancelCause(ctx)
	defer cancelStream(nil)

	// 进流式之前的失败(网络错 / 429 / 5xx)在这里重试:此刻一个 token 都还没吐给 UI、
	// assistant 消息也没进历史,重试是干净的。进流式后的中途断(scanner.Err)不在此列。
	// streamHeaderTimeout 触发的响应头超时也落在这里,会被当作「网络错误」自动重试。
	var resp *http.Response
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return "", "", nil, "", nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)

		var doErr error
		resp, doErr = streamHTTPClient.Do(req)
		if doErr == nil && resp.StatusCode == 200 {
			break // 成功,进入下面的流式读取
		}

		// 失败分类:决定能否重试。
		var (
			retryable  bool
			failErr    error  // 不可重试 / 重试耗尽时返回给上层
			reason     string // 展示给用户的简短原因
			retryAfter string // 仅非 200 分支可能有
		)
		if doErr != nil {
			if ctx.Err() != nil { // ESC/退出导致的取消,不当作可重试错误
				return "", "", nil, "", nil, streamCtxErr(ctx)
			}
			retryable, failErr, reason = true, doErr, "网络错误"
		} else {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			retryAfter = resp.Header.Get("Retry-After")
			failErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
			retryable = isRetryableStatus(resp.StatusCode)
			reason = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		if !retryable || attempt >= maxAPIRetries {
			return "", "", nil, "", nil, failErr
		}
		d := retryBackoff(attempt, retryAfter)
		ch <- RetryNoticeMsg{Attempt: attempt + 1, Max: maxAPIRetries, Delay: d, Reason: reason}
		if !sleepCtx(ctx, d) { // 退避期间被 ESC/退出打断
			return "", "", nil, "", nil, streamCtxErr(ctx)
		}
	}
	defer resp.Body.Close()

	var (
		contentBuilder   strings.Builder
		reasoningBuilder strings.Builder
		inReasoning      bool
		toolBuf          = map[int]*ToolCall{}
		lastUsage        *UsageInfo // stream_options.include_usage 会在最后 chunk 返回 usage
		finishReason     string     // 最后一个非空 finish_reason("stop"/"length"/"tool_calls"…),供主循环判断截断
	)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// 空闲看门狗:超时即 cancel,底层读被掐断 → scanner.Scan() 立刻返回 false。
	// 每读到一行就续命(见循环内 Reset)。
	idle := time.AfterFunc(streamIdleTimeout, func() {
		cancelStream(fmt.Errorf("%w:%s 内未收到任何数据,端点无响应", errStreamIdle, streamIdleTimeout))
	})
	defer idle.Stop()

	for scanner.Scan() {
		// 续命必须在 data: 过滤之前 —— keep-alive 注释行(如 OpenRouter 的
		// ": OPENROUTER PROCESSING")虽然没有负载,却正是「连接还活着」的证据。
		// 只在 data: 行上续命的话,模型纯思考期会被误判成卡死。
		idle.Reset(streamIdleTimeout)

		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var chunk sseChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		// stream_options.include_usage: 最后 chunk 有 usage、choices 为空
		if chunk.Usage != nil {
			chunk.Usage.normalize() // 统一各供应商的缓存命中口径(mimo 等走 prompt_tokens_details)
			lastUsage = chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		if fr := chunk.Choices[0].FinishReason; fr != nil && *fr != "" {
			finishReason = *fr
		}
		delta := chunk.Choices[0].Delta

		if delta.ReasoningContent != "" {
			// reasoning 走单独消息类型,TUI 只用它驱动 spinner,不写入对话区
			inReasoning = true
			reasoningBuilder.WriteString(delta.ReasoningContent)
			ch <- ReasoningTokenMsg(delta.ReasoningContent)
		}
		if delta.Content != "" {
			inReasoning = false
			contentBuilder.WriteString(delta.Content)
			ch <- TokenMsg(delta.Content)
		}
		_ = inReasoning // 仅用于 reasoning/content 切换语义,保留变量便于将来加 boundary 处理
		for _, tc := range delta.ToolCalls {
			cur, ok := toolBuf[tc.Index]
			if !ok {
				cur = &ToolCall{Index: tc.Index, Type: "function"}
				toolBuf[tc.Index] = cur
			}
			if tc.ID != "" {
				cur.ID = tc.ID
			}
			if tc.Type != "" {
				cur.Type = tc.Type
			}
			if tc.Function.Name != "" {
				cur.Function.Name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				cur.Function.Arguments += tc.Function.Arguments
			}
		}
	}
	idle.Stop()
	// 看门狗已触发 → 无论 scanner 报的是 "context canceled" 还是干净 EOF,都按空闲超时收尾。
	// 必须抢在下面的 scanner.Err() 之前:看门狗的取消在那里表现为 context.Canceled,
	// 而上层(StartStream)对 context.Canceled 是静默 return 的,会把这次卡死悄悄吞掉。
	if cause := context.Cause(ctx); errors.Is(cause, errStreamIdle) {
		return contentBuilder.String(), reasoningBuilder.String(), nil, finishReason, lastUsage, cause
	}
	if err := scanner.Err(); err != nil {
		return contentBuilder.String(), reasoningBuilder.String(), nil, finishReason, lastUsage, err
	}

	// 按 index 升序拼装最终 tool_calls。
	// 注意:toolBuf 的 key 不保证从 0 开始、也不保证连续——DeepSeek 官方 index 从 0 起,
	// 但部分第三方/自建 base_url 池子从 1 起(见 issue #59)。若按 0..len-1 遍历会漏掉
	// 非零起始或跳号的 key,导致工具调用被整个丢弃、会话被误判为结束而提前中断。
	idxs := make([]int, 0, len(toolBuf))
	for idx := range toolBuf {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)
	toolCalls := make([]ToolCall, 0, len(idxs))
	for _, idx := range idxs {
		tc := *toolBuf[idx]
		if tc.ID == "" {
			// 供应商流式整段未给 id(部分第三方/自建 base_url 池子)→ 合成稳定 id。
			// assistant 的 tool_call 与随后的 tool 结果都用它,保证 API 侧能配对(见 issue #94)。
			tc.ID = fmt.Sprintf("call_%d", idx)
		}
		toolCalls = append(toolCalls, tc)
	}
	return contentBuilder.String(), reasoningBuilder.String(), toolCalls, finishReason, lastUsage, nil
}

// ListenToStream 把单条事件转给 bubbletea。
func ListenToStream(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// === 工具白名单 / 执行 ===

// buildToolSpecs 组装本轮工具列表。当前所有模式 / 角色拿到的工具表一致(模式与角色限制都靠
// system prompt + executeTool 兜底,不在这里裁剪),这样前缀缓存稳定。
func buildToolSpecs(mode AgentMode) []tools.OpenAIToolSpec {
	var out []tools.OpenAIToolSpec
	for _, t := range tools.Tools {
		if !allowedInMode(t, mode) {
			continue
		}
		out = append(out, t.ToOpenAISpec())
	}
	// 动态注入的 MCP 工具:对所有角色可见(子 agent 也能用)。放在内置工具之后,
	// 保持内置工具的前缀稳定(MCP 工具变动不影响内置部分的 KV cache)。
	for _, t := range tools.MCPTools() {
		out = append(out, t.ToOpenAISpec())
	}
	return out
}

func allowedInMode(_ tools.Tool, _ AgentMode) bool {
	// tools 数组不再按模式裁剪:所有模式下暴露全部工具,保持 prefix cache 稳定。
	// 模式限制通过 system prompt + 切换时注入的模式通知消息传达,LLM 自行遵守。
	// executeTool 里仍保留硬拦截作为兜底。
	return true
}

// isReviewable 判断工具在 review 模式下是否需要人工审核。
func isReviewable(name string) bool {
	return name == "Write" || name == "Update" || name == "Bash"
}

// blockedInPlan 判断工具在 plan 模式下是否被禁止执行(只读规划,禁一切写/副作用操作)。
// plan 模式不裁剪工具表(保持 prefix cache 稳定),全靠 system prompt 让 LLM 自觉;
// 这里是执行层的硬兜底:模型不听话直接调 Write/Update/Bash 时(issue #108),拦下来。
func blockedInPlan(name string) bool {
	return name == "Write" || name == "Update" || name == "Bash"
}

// fileToolNames 是用于维护 lastFile(模型当前正在编辑的文件)的工具,给 Update 漏 path 时兜底。
// 只取真正"在读写编辑目标文件"的:Read（打开待编辑文件)/ Write / Update。刻意排除:
//   - Grep:path 常是目录或"查别的文件",会把 lastFile 污染成非编辑目标
//   - OCR:path 是图片,而图片永远不会是 Update 的目标
//   - List/Tree:path 是目录
var fileToolNames = map[string]bool{"Read": true, "Write": true, "Update": true}

// executeTool 执行单个工具调用。lastFile(可空)跟踪"最近操作的文件路径":
// 部分模型把 Update 的 path 排在参数最后、偶尔整段漏掉(issue #81),导致第一次 Update 因
// "path 参数为空"失败、再重试才成。此时用 lastFile 兜底回填 path —— 只对 Update 生效
// (它带 old_string,补错文件会因匹配不到而安全失败;Write 是创建/覆盖,绝不猜路径)。
func executeTool(tc ToolCall, mode AgentMode, lastFile *string) tools.ToolResult {
	t := tools.Find(tc.Function.Name)
	if t == nil {
		return tools.ToolResult{
			Output:  fmt.Sprintf("未注册的工具: %s", tc.Function.Name),
			Success: false,
		}
	}
	if !allowedInMode(*t, mode) {
		return tools.ToolResult{
			Output:  fmt.Sprintf("工具 %s 在当前模式 (%s) 不可用", t.Name, mode),
			Success: false,
		}
	}
	// plan 模式硬拦:只读规划,禁止写/副作用操作(issue #108)。
	if mode == AgentMode_Plan && blockedInPlan(t.Name) {
		return tools.ToolResult{
			Output:  fmt.Sprintf("当前是 plan 模式,禁止 %s(只读规划,不修改文件 / 不执行命令)。请先用 /auto 切换到 auto 模式再执行该操作。", t.Name),
			Success: false,
		}
	}
	args, err := tools.ParseArgs(tc.Function.Arguments)
	if err != nil {
		return tools.ToolResult{
			Output:  fmt.Sprintf("参数解析失败: %v / raw=%s", err, tc.Function.Arguments),
			Success: false,
		}
	}
	// 最近文件兜底(issue #81):Update 的 path 为空时,用 lastFile 回填;否则记录本次的 path。
	if lastFile != nil {
		p, _ := args["path"].(string)
		switch {
		case strings.TrimSpace(p) == "" && t.Name == "Update" && *lastFile != "":
			args["path"] = *lastFile
		case strings.TrimSpace(p) != "" && fileToolNames[t.Name]:
			*lastFile = strings.TrimSpace(p)
		}
	}
	// 纵深防御:Executor 为 nil 的工具(SwitchModel / CreatePlan 等)预期在主/子 agent
	// 工具循环里被拦截,不应该走到这里。一旦走到,直接调 nil 会段错误整个进程崩。
	// 退而返回失败给 LLM,让它自纠或交给上层重试,而不是 panic。
	if t.Executor == nil {
		return tools.ToolResult{
			Output:  fmt.Sprintf("工具 %s 不能直接执行(应在 agent 循环内被拦截);请用别的工具完成此步骤", t.Name),
			Success: false,
		}
	}
	// 唯一收口:所有真正执行的工具(Read/Bash/Grep/WebFetch/MCP 等,主 agent / 子 agent / Explore 都走这里)
	// 的返回值在进会话历史前统一限幅,防止单条超大结果撑爆上下文(issue #135)。
	res := runExecutorGuarded(t, args)
	res.Output = clampToolOutput(t.Name, res.Output)
	return res
}

// fsToolTimeout 列出"纯本地、无自带超时"的工具:它们理应秒级返回,一旦底层
// stat/read 卡在病态挂载点(典型是 WSL /mnt/c 的 9p 在杀软扫描/文件被 Windows
// 进程占用时 stall),或目标是非普通文件(FIFO/设备,os.ReadFile 永等写端),
// 会无限阻塞、拖死整个工具循环(实测 Read 卡 50 分钟)。给它们套看门狗超时兜底。
//
// 刻意排除(各有自己的时长管控,套小超时反而会误杀合法长跑):
//   - Bash:自带 timeout 参数(可设几百秒)
//   - Fetch / Search:网络请求自带超时
//   - Workflow / Explore:派子 agent,合法长跑数分钟
//   - MCP 工具(mcp__ 前缀):各自服务端管控
var fsToolTimeout = map[string]time.Duration{
	"Read":      60 * time.Second,
	"Write":     60 * time.Second,
	"Update":    60 * time.Second,
	"Glob":      60 * time.Second,
	"Grep":      60 * time.Second,
	"List":      60 * time.Second,
	"Tree":      60 * time.Second,
	"CodeGraph": 120 * time.Second,
	"OCR":       120 * time.Second,
}

// runExecutorGuarded 执行 executor;对本地文件类工具套看门狗超时,防止病态挂载点
// 把整个工具循环无限卡死。超时后给 LLM 返回失败让它自纠,而不是干等。
//
// executor 签名固定为 map[string]any→ToolResult、拿不到 context,无法真正取消那个
// 已陷在内核 read() 里的 goroutine;但 done 为带缓冲 channel,系统调用一旦最终返回
// goroutine 就能写入并退出,不会泄漏(只有真正永不返回的病态 fd 会留一个孤儿 goroutine,
// 这是无 context 取消能力下可接受的代价——关键是工具循环已经解开了)。
func runExecutorGuarded(t *tools.Tool, args map[string]any) tools.ToolResult {
	d, guarded := fsToolTimeout[t.Name]
	if !guarded {
		return t.Executor(args)
	}
	done := make(chan tools.ToolResult, 1)
	go func() { done <- t.Executor(args) }()
	select {
	case res := <-done:
		return res
	case <-time.After(d):
		return tools.ToolResult{
			Output: fmt.Sprintf("工具 %s 执行超时(%s)。目标可能位于卡死的挂载点"+
				"(如 WSL /mnt/c 的 9p),或是非普通文件(管道/设备)。请换个路径或确认挂载可用。", t.Name, d),
			Success: false,
		}
	}
}
