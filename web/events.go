// Package web 提供 deepx 的本地 web dashboard:把终端 TUI 正在跑的同一个会话
// 通过 SSE 实时镜像到浏览器,并把浏览器的输入 / review 确认注入回 TUI。
//
// 设计要点:
//   - agent loop 完全不动 —— 它本来就把事件发到 channel,TUI 的 Update() 每条消息过一次,
//     那里顺手 Broadcast 给本包的 Hub。
//   - 本包不 import tui,避免循环依赖;输入回注通过 Server 上的回调(由 tui/run.go 注入)。
package web

import (
	"deepx/agent"

	tea "charm.land/bubbletea/v2"
)

// Event 是发往浏览器的统一事件(SSE data 即它的 JSON)。
// 用扁平结构 + Kind 区分,前端按 kind 取对应字段,省去 JS 端的 union 解包。
type Event struct {
	Kind string `json:"kind"`

	// 文本类:token / reasoning_token / user_message / error
	Text string `json:"text,omitempty"`

	// tool_call / tool_result
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Args    string `json:"args,omitempty"`
	Output  string `json:"output,omitempty"`
	Success *bool  `json:"success,omitempty"`

	// model_switch
	Role    string `json:"role,omitempty"`
	ModelID string `json:"modelId,omitempty"`
	Reason  string `json:"reason,omitempty"`

	// models(全量模型名同步:/provider 切供应商后更新 flash/pro 展示名)
	Flash string `json:"flash,omitempty"`
	Pro   string `json:"pro,omitempty"`

	// plan(整份)/ plan_status(单节点)
	Plan     []PlanNode `json:"plan,omitempty"`
	PlanKind string     `json:"planKind,omitempty"` // "todo"(计划)| "createplan"(步骤)
	Status   string     `json:"status,omitempty"`
	Summary  string     `json:"summary,omitempty"`

	// usage
	Usage *Usage `json:"usage,omitempty"`

	// review_request / review_resolved
	Approve *bool `json:"approve,omitempty"`

	// ask_request(LLM 发起的选择题弹窗);ask_resolved 时为空
	Questions []agent.AskQuestion `json:"questions,omitempty"`

	// sessions(会话列表)/ session_loaded(切换会话后载入的消息)
	Sessions []SessionInfo `json:"sessions,omitempty"`
	Messages []Message     `json:"messages,omitempty"`

	// queued(流式/压缩中排队待发送的消息原文,FIFO);空 = 队列已清空
	Queued []string `json:"queued,omitempty"`
}

// PlanNode 是发往前端的 plan DAG 节点。
type PlanNode struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Model     string   `json:"model"`
	Status    string   `json:"status"`
	Summary   string   `json:"summary,omitempty"`
	DependsOn []string `json:"dependsOn,omitempty"`
}

// Usage 单次 API 用量(给右栏统计)。
type Usage struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
	CacheHit         int `json:"cacheHit"`
	CacheMiss        int `json:"cacheMiss"`
}

// ToWebEvent 把一个 agent tea.Msg 映射成 web Event。
// 第二个返回值 false 表示该消息与 web 无关(不广播)。
// user_message / review_request / review_resolved 由 tui 侧直接构造 Event,不经过这里。
// TokenMsg 也不在这里映射:助手正文由 tui 的 TokenMsg 分支显式广播(和写入对话区同一处)。
func ToWebEvent(msg tea.Msg) (Event, bool) {
	switch m := msg.(type) {
	case agent.ReasoningTokenMsg:
		return Event{Kind: "reasoning_token", Text: string(m)}, true
	case agent.ToolCallStartMsg:
		return Event{Kind: "tool_call", Name: m.Name, Args: m.Args}, true
	case agent.ToolCallResultMsg:
		ok := m.Success
		return Event{Kind: "tool_result", Name: m.Name, Output: m.Output, Success: &ok}, true
	case agent.ModelSwitchMsg:
		return Event{Kind: "model_switch", Role: m.Role, ModelID: m.ModelID, Reason: m.Reason}, true
	case agent.PlanCreatedMsg:
		return Event{Kind: "plan", Plan: toPlanNodes(m.Plans), PlanKind: m.Kind}, true
	case agent.TaskStatusMsg:
		return Event{Kind: "plan_status", ID: m.ID, Status: string(m.Status), Summary: m.Summary}, true
	case agent.UsageMsg:
		return Event{Kind: "usage", Usage: &Usage{
			PromptTokens:     m.Usage.PromptTokens,
			CompletionTokens: m.Usage.CompletionTokens,
			CacheHit:         m.Usage.PromptCacheHitTokens,
			CacheMiss:        m.Usage.PromptCacheMissTokens,
		}}, true
	case agent.StreamDoneMsg:
		return Event{Kind: "done"}, true
	case agent.StreamErrMsg:
		if m.Err == nil {
			return Event{Kind: "done"}, true
		}
		return Event{Kind: "error", Text: m.Err.Error()}, true
	}
	return Event{}, false
}

func toPlanNodes(items []agent.PlanItem) []PlanNode {
	out := make([]PlanNode, 0, len(items))
	for _, it := range items {
		st := string(it.Status)
		if st == "" {
			st = string(agent.PlanStatusPending)
		}
		out = append(out, PlanNode{
			ID:        it.ID,
			Title:     it.Title,
			Model:     it.Model,
			Status:    st,
			DependsOn: it.DependsOn,
		})
	}
	return out
}
