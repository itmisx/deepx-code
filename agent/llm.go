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
	"net/http"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type AgentMode string

const (
	AgentMode_Plan   AgentMode = "plan"
	AgentMode_Auto   AgentMode = "auto"
	AgentMode_Review AgentMode = "review"

	// 主 agent 单轮对话内的工具调用上限。
	// 100 轮给复杂多步任务留足空间(典型场景:CreatePlan 之后还要做修改 + 测试 + 修复循环)。
	// 触顶通常意味着 LLM 在死循环,会返回错误中断。
	mainAgentMaxRounds = 100
)

// ModelEntry 单个 role 的完整连接配置 — base_url / model id / api_key 三件套。
// 设计目标:flash 和 pro 可以指向不同 provider(比如 flash 用本地 vllm,pro 用 DeepSeek 云端)。
type ModelEntry struct {
	BaseURL       string
	Model         string
	APIKey        string
	ContextWindow int // 上下文窗口大小(tokens)
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
		ReasoningContent string     `json:"reasoning_content,omitempty"`
		ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
		ToolCallID       string     `json:"tool_call_id,omitempty"`
		Name             string     `json:"name,omitempty"`
	}
	w := wire{
		Role:             m.Role,
		ReasoningContent: m.ReasoningContent,
		ToolCalls:        m.ToolCalls,
		ToolCallID:       m.ToolCallID,
		Name:             m.Name,
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
	Model         string                 `json:"model"`
	MaxTokens     int                    `json:"max_tokens"`
	Stream        bool                   `json:"stream"`
	StreamOptions *streamOptions         `json:"stream_options,omitempty"`
	Messages      []ChatMessage          `json:"messages"`
	Tools         []tools.OpenAIToolSpec `json:"tools,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// UsageInfo 单次 API 调用的 token 用量,含缓存命中信息。
type UsageInfo struct {
	PromptTokens          int `json:"prompt_tokens"`
	CompletionTokens      int `json:"completion_tokens"`
	TotalTokens           int `json:"total_tokens"`
	PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
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

# 技能skill使用
- 在实现功能、修复 bug、重构或 review 代码之前，读取karpathy-guidelines，并遵照其规则执行。

# 任务规划
- 简单/单步任务:直接做,不要过度规划。
- 多步顺序任务(≥3 步且有先后,如从零搭应用 / 跨多文件改动 / 调试修复链路):动手前先用 Todo 列出全部步骤,之后每开始或完成一步就重发整张 todos 更新状态,让用户看到进度。你自己逐步执行,不派子 agent。
- 真正可并行、彼此独立的扇出任务才用 CreatePlan 拆 DAG(会派并发子 agent 各自跑);搭一个连贯的应用别用 CreatePlan。

# Shell 安全
- 不主动执行破坏性命令(rm -rf / drop / force push 等)
- 优先可逆操作,destructive 操作先确认

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
	if summary != "" {
		base += "\n\n# 会话摘要(此前对话的压缩,延续上下文)\n" + summary
	}
	return base
}

// forceRole 非空时,强制使用该角色(跳过 RouteByKeyword 关键词路由)。
// 用于 TUI 层的"pro 保持 N 轮"机制。
func StartStream(
	ctx context.Context,
	models ModelConfig,
	history []ChatMessage,
	maxTokens int,
	mode AgentMode,
	workspace string,
	skillCatalog string, // 见下方 system prompt 注入逻辑;空串表示当前没有 skill
	summary string, // 会话压缩摘要,垫在 system prompt 末尾;空串表示尚未压缩
	forceRole string, // 非空时强制指定起手角色,跳过 RouteByKeyword
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

		// 入口路由:纯本地关键词 + 长度判定,零延迟,无 LLM 调用。
		// 命中复杂关键词 / 消息 > 500 字 → pro;否则 flash。
		// 如果 forceRole 非空,则跳过 RouteByKeyword,强制使用指定角色(pro 保持机制)。
		// 本轮锁定该模型,主循环不再切换 — plan 节点可独立指定 model 字段,
		// 由 sub-agent 按节点要求路由,跟"起手模型"解耦。
		if latestUserTask != "" && models.Pro.Model != "" {
			choice := ""
			if forceRole != "" {
				choice = forceRole
			} else {
				choice = RouteByKeyword(latestUserTask)
			}
			if choice == "pro" {
				role = tools.RolePro
				currentEntry = models.Pro
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

		// 100 轮上限给复杂多步任务留足空间(read → analyze → edit → test → fix 这种循环)。
		// 触顶通常说明 LLM 在死循环或反复试错,需要返回错误让用户介入。
		for round := 0; round < mainAgentMaxRounds; round++ {
			// 检查 context 是否取消(ESC/退出),提前退出不卡后台
			if ctx.Err() != nil {
				return
			}
			// 不再主动 strip reasoning_content:本轮不切换模型,thinking 模型仍按需回传,
			// 非 thinking 模型对 history 里的字段视而不见。若个别模型报错,
			// streamOnce 仍有 errReasoningRequired retry 兜底。
			assistantContent, reasoning, toolCalls, usage, err := streamOnce(
				ctx,
				currentEntry.APIKey, currentEntry.BaseURL, currentEntry.Model,
				convo, maxTokens, toolSpecs, ch,
			)
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
			}

			// 把本轮 assistant 回复写入历史(含 reasoning_content,thinking 模型下轮需要)
			convo = append(convo, ChatMessage{
				Role:             "assistant",
				Content:          assistantContent,
				ReasoningContent: reasoning,
				ToolCalls:        toolCalls,
			})

			if len(toolCalls) == 0 {
				ch <- HistoryUpdateMsg{History: convo}
				ch <- StreamDoneMsg{}
				return
			}

			// 执行每个工具调用,把结果加进 convo。
			// 这些工具被 deepx 拦截 (不走 Executor):
			//   - CreatePlan         → 解析后产 PlanCreatedMsg,触发 DAG 调度(派并发子 agent)
			//   - Todo               → 解析后产 PlanCreatedMsg 刷新可见清单,主 agent 自己执行,不派子 agent
			//   - UpdatePlanStatus   → 解析后产 TaskStatusMsg,UI 更新单项状态
			//   - SwitchModel        → 改本轮 currentEntry / role,通过 ModelSwitchMsg 通知 UI
			// 拦截后仍要给 LLM 一个 fake tool result,让 OpenAI 工具循环能正常推进。
			for _, tc := range toolCalls {
				// review 模式:对 Write/Update/Bash 发起审核
				var reviewCh chan bool
				if mode == AgentMode_Review && isReviewable(tc.Function.Name) {
					reviewCh = make(chan bool, 1)
				}
				ch <- ToolCallStartMsg{Name: tc.Function.Name, Args: tc.Function.Arguments, ReviewCh: reviewCh}
				if reviewCh != nil && !<-reviewCh {
					ch <- ToolCallResultMsg{Name: tc.Function.Name, Output: "操作已被用户拒绝 (review 模式)", Success: false}
					convo = append(convo, ChatMessage{
						Role:       "tool",
						ToolCallID: tc.ID,
						Name:       tc.Function.Name,
						Content:    "操作已被用户拒绝 (review 模式)",
					})
					continue
				}

				var result tools.ToolResult
				switch tc.Function.Name {
				case "CreatePlan":
					plans, perr := parseCreatePlanArgs(tc.Function.Arguments)
					if perr != nil {
						result = tools.ToolResult{Output: perr.Error(), Success: false}
					} else {
						// 1. 通知 UI 渲染 plan 树
						ch <- PlanCreatedMsg{Plans: plans}
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
								MaxTokens:    maxTokens,
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
						ch <- PlanCreatedMsg{Plans: items}
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
					if role == tools.RolePro {
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
				default:
					result = executeTool(tc, mode)
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
					Content:    result.Output,
				})
			}
			ch <- HistoryUpdateMsg{History: convo}

			// C: 两阶段级联 — 如果 flash 首轮就调用了 ≥2 个不同"主动"工具(写文件/执行命令等),
			// 说明任务确实复杂,自动升级到 pro 处理后续推理。
			if role == tools.RoleFlash && round == 0 && models.Pro.Model != "" {
				if cnt := distinctActiveToolCount(toolCalls); cnt >= 2 {
					role = tools.RolePro
					currentEntry = models.Pro
					ch <- ModelSwitchMsg{Role: role, ModelID: currentEntry.Model, Reason: "首轮使用了多个工具,自动升级到 pro"}
				}
			}
		}

		ch <- StreamErrMsg{fmt.Errorf("超过工具调用轮数上限")}
	}()

	return ListenToStream(ch), ch
}

// streamOnce 发起一次 chat/completions 请求,返回 (content, reasoning_content, tool_calls, usage, error)。
func streamOnce(
	ctx context.Context,
	apiKey, baseURL, modelID string,
	convo []ChatMessage,
	maxTokens int,
	toolSpecs []tools.OpenAIToolSpec,
	ch chan<- tea.Msg,
) (string, string, []ToolCall, *UsageInfo, error) {

	body, err := json.Marshal(chatRequest{
		Model:     modelID,
		MaxTokens: maxTokens,
		Stream:    true,
		StreamOptions: &streamOptions{
			IncludeUsage: true,
		},
		Messages: convo,
		Tools:    toolSpecs,
	})
	if err != nil {
		return "", "", nil, nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", "", nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return "", "", nil, nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}

	var (
		contentBuilder   strings.Builder
		reasoningBuilder strings.Builder
		inReasoning      bool
		toolBuf          = map[int]*ToolCall{}
		lastUsage        *UsageInfo // stream_options.include_usage 会在最后 chunk 返回 usage
	)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
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
			lastUsage = chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			continue
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
	if err := scanner.Err(); err != nil {
		return contentBuilder.String(), reasoningBuilder.String(), nil, lastUsage, err
	}

	// 按 index 升序拼装最终 tool_calls
	var toolCalls []ToolCall
	for i := 0; i < len(toolBuf); i++ {
		if tc, ok := toolBuf[i]; ok {
			toolCalls = append(toolCalls, *tc)
		}
	}
	return contentBuilder.String(), reasoningBuilder.String(), toolCalls, lastUsage, nil
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

func executeTool(tc ToolCall, mode AgentMode) tools.ToolResult {
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
	args, err := tools.ParseArgs(tc.Function.Arguments)
	if err != nil {
		return tools.ToolResult{
			Output:  fmt.Sprintf("参数解析失败: %v / raw=%s", err, tc.Function.Arguments),
			Success: false,
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
	return t.Executor(args)
}

// readOnlyTools 是不计入"主动工具"的信号集合。
// 如果 flash 首轮只调用了这些工具,说明只是在做信息收集,不必升 pro。
var readOnlyTools = map[string]bool{
	"Read":     true,
	"Grep":     true,
	"List":     true,
	"Glob":     true,
	"Tree":     true,
	"Search":   true,
	"Fetch":    true,
	"CodeGraph": true,
	"Memory":   true,
	"OCR":      true,
	"LoadSkill": true,
}

// distinctActiveToolCount 统计 toolCalls 中不同的"主动"工具数(排除了 readOnlyTools)。
// 用于两阶段级联:如果 flash 首轮调用了 ≥2 个不同主动工具,说明任务复杂,应升 pro。
func distinctActiveToolCount(toolCalls []ToolCall) int {
	seen := make(map[string]bool)
	for _, tc := range toolCalls {
		if !readOnlyTools[tc.Function.Name] {
			seen[tc.Function.Name] = true
		}
	}
	return len(seen)
}
