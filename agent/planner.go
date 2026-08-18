package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// PlanStatus 是 plan/task 在生命周期里的状态。UI 用 statusIcon 渲染成符号。
type PlanStatus string

const (
	PlanStatusPending PlanStatus = "pending" // 排队等执行
	PlanStatusRunning PlanStatus = "running" // 正在跑
	PlanStatusDone    PlanStatus = "done"    // 已完成
	PlanStatusFailed  PlanStatus = "failed"  // 执行失败
	PlanStatusBlocked PlanStatus = "blocked" // 依赖失败,跳过
)

// PlanItem 是 CreatePlan 产出的一个规划节点(顶层 DAG 节点)。
type PlanItem struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Model     string   `json:"model"`      // "flash" | "pro"
	DependsOn []string `json:"depends_on"` // 依赖的其他 plan ID

	// 运行时字段 — LLM 看不到,deepx 内部状态机驱动
	Status  PlanStatus `json:"-"`
	Summary string     `json:"-"`
	Phase   string     `json:"-"` // workflow 用:所属阶段名,渲染时据此分组(CreatePlan 留空 → 平铺)

	// workflow 用:子 agent 计时(Go 侧计时,不入 journal、不影响 resume)。
	StartedAt time.Time     `json:"-"` // 开跑时刻(start 时记)
	Elapsed   time.Duration `json:"-"` // 完成耗时(finish 时 = now-StartedAt);>0 才显示
}

// === TUI 事件 ===

// PlanCreatedMsg 通知 TUI: LLM 刚产出一份规划。
// Kind 区分来源:"todo"(Todo 工具,顺序清单)/ "createplan"(CreatePlan,并发 DAG)——
// 右栏据此分别在「计划/Plan」和「步骤/Step」两段显示。
// TUI 应初始化 plan 状态,所有 item 初始 Status=Pending。
type PlanCreatedMsg struct {
	Plans []PlanItem
	Kind  string
	// Phases 是 workflow 声明的全部阶段名(meta.phases,声明顺序)。非空时 UI 在运行前就把所有阶段
	// 当成清单展示出来,逐步点亮;各步骤(agent)动态产生,运行到才出现在所属阶段下。CreatePlan/Todo 为空。
	Phases []string
}

// TaskStatusMsg 通知 TUI: 某个 plan 节点的状态变了。
type TaskStatusMsg struct {
	ID      string
	Status  PlanStatus
	Summary string // 可选,完成/失败时写一段简短说明
}

// === 解析 ===

// parseCreatePlanArgs 把 LLM 调用 CreatePlan 时传来的原始 JSON arguments
// 解码成 []PlanItem。任何字段缺失会用零值,不报错 (Phase 2 优先跑通)。
func parseCreatePlanArgs(rawArgs string) ([]PlanItem, error) {
	var wrapper struct {
		Plans []PlanItem `json:"plans"`
	}
	if rawArgs == "" || rawArgs == "null" {
		return nil, fmt.Errorf("CreatePlan: 空参数")
	}
	if err := json.Unmarshal([]byte(rawArgs), &wrapper); err != nil {
		return nil, fmt.Errorf("CreatePlan: 参数解析失败: %w", err)
	}
	if len(wrapper.Plans) == 0 {
		return nil, fmt.Errorf("CreatePlan: plans 数组为空")
	}
	for i := range wrapper.Plans {
		wrapper.Plans[i].Status = PlanStatusPending
	}
	return wrapper.Plans, nil
}

// parseTodoArgs 把主 agent 调用 Todo 工具的全量快照解成 []PlanItem。
// 每次调用都是完整清单(非增量),状态嵌在各项里;ID 自动按序号生成(纯展示用,不参与调度)。
func parseTodoArgs(rawArgs string) ([]PlanItem, error) {
	var w struct {
		Todos []struct {
			Content string `json:"content"`
			Title   string `json:"title"` // 容错:模型偶尔用 title 代替 content
			Status  string `json:"status"`
		} `json:"todos"`
	}
	if rawArgs == "" || rawArgs == "null" {
		return nil, fmt.Errorf("Todo: 空参数")
	}
	if err := json.Unmarshal([]byte(rawArgs), &w); err != nil {
		return nil, fmt.Errorf("Todo: 参数解析失败: %w", err)
	}
	if len(w.Todos) == 0 {
		return nil, fmt.Errorf("Todo: todos 数组为空")
	}
	items := make([]PlanItem, len(w.Todos))
	for i, t := range w.Todos {
		title := t.Content
		if title == "" {
			title = t.Title
		}
		items[i] = PlanItem{
			ID:     fmt.Sprintf("todo%d", i+1),
			Title:  title,
			Status: normalizeTodoStatus(t.Status),
		}
	}
	return items, nil
}

// normalizeTodoStatus 把 Todo 工具的状态词(TodoWrite 习惯用 pending/in_progress/completed)
// 映射到 deepx 内部的 PlanStatus,同时容忍 running/done 等同义写法。
func normalizeTodoStatus(s string) PlanStatus {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "in_progress", "in-progress", "running", "active", "doing":
		return PlanStatusRunning
	case "completed", "complete", "done", "finished":
		return PlanStatusDone
	case "failed", "error":
		return PlanStatusFailed
	case "blocked", "skipped", "cancelled", "canceled":
		return PlanStatusBlocked
	default:
		return PlanStatusPending
	}
}

// parseUpdatePlanStatusArgs 把 UpdatePlanStatus 的参数解出来。
func parseUpdatePlanStatusArgs(rawArgs string) (id string, status PlanStatus, summary string, err error) {
	var p struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Summary string `json:"summary"`
	}
	if rawArgs == "" || rawArgs == "null" {
		err = fmt.Errorf("UpdatePlanStatus: 空参数")
		return
	}
	if err = json.Unmarshal([]byte(rawArgs), &p); err != nil {
		err = fmt.Errorf("UpdatePlanStatus: 解析失败: %w", err)
		return
	}
	if p.ID == "" {
		err = fmt.Errorf("UpdatePlanStatus: id 必填")
		return
	}
	switch p.Status {
	case "running":
		status = PlanStatusRunning
	case "done":
		status = PlanStatusDone
	case "failed":
		status = PlanStatusFailed
	case "blocked":
		status = PlanStatusBlocked
	case "pending":
		status = PlanStatusPending
	default:
		err = fmt.Errorf("UpdatePlanStatus: 未知 status %q (允许: pending/running/done/failed/blocked)", p.Status)
		return
	}
	return p.ID, status, p.Summary, nil
}

// parseSwitchModelReason 从 SwitchModel 工具调用 args 里抠 reason 字段。
// 解析失败 / 字段缺失 → 返回空串(允许 LLM 不写 reason,只是 UI 提示更含糊)。
func parseSwitchModelReason(rawArgs string) string {
	var p struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal([]byte(rawArgs), &p)
	return p.Reason
}

// switchModelArgs SwitchModel 工具的结构化参数。
type switchModelArgs struct {
	TargetLevel int    `json:"target_level"` // 目标路由级别 0-3
	ReasonType  string `json:"reason_type"`  // 升级: "context" / "complexity"; 降级省略
	Reason      string `json:"reason"`       // 一句话理由
}

// parseSwitchModelArgs 解析 SwitchModel 工具参数。
func parseSwitchModelArgs(rawArgs string) switchModelArgs {
	var p switchModelArgs
	_ = json.Unmarshal([]byte(rawArgs), &p)
	return p
}

// parseExploreArgs 取 Explore 工具参数:task(要探索的问题)+ thoroughness(深度,可空)。
func parseExploreArgs(rawArgs string) (task, thoroughness string) {
	var p struct {
		Task         string `json:"task"`
		Thoroughness string `json:"thoroughness"`
	}
	_ = json.Unmarshal([]byte(rawArgs), &p)
	return p.Task, p.Thoroughness
}
