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

// TodoType 规划项的职责分类:Action=要执行的动作;Verification/Review=验证/检查(不阻塞完成度门禁)。
// 模型不声明该字段,由 parseTodoArgs 按标题启发式分类(见 classifyTodoType)。
type TodoType int

const (
	TodoAction TodoType = iota
	TodoVerification
	TodoReview
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
	// Type 规划项职责分类(完成度门禁用):只有 TodoAction 计入"阻塞性待办"
	// (countBlockingTasks),Verification/Review 不阻塞 agent 结束。
	Type TodoType `json:"-"`
	// Progress 工具执行推进计数(完成度门禁用):只推进 TodoAction 型项,
	// 每次 Write/Update 成功推进一次。Progress>0 表示"模型已在执行",gate 不再催
	// (countBlockingTasks 只统计从未执行过的项,见 completionGate)。
	Progress int `json:"-"`

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
			Type:   classifyTodoType(title),
		}
	}
	return items, nil
}

// classifyTodoType 按标题启发式分类规划项职责:
//   - 含验证/检查/确认/核对/verify/check/review/格式/文件数 等 → TodoVerification 或 TodoReview;
//   - 其余 → TodoAction(执行动作,计入阻塞性待办)。
//
// 目的:非执行型待办(验证/检查/总结)不应阻塞 agent 结束 —— 完成度门禁只被
// TodoAction 阻塞(见 countBlockingTasks),避免"模型写完文件还被'验证文件数'
// 这类 todo 反复打断"。
func classifyTodoType(title string) TodoType {
	low := strings.ToLower(title)
	for _, v := range []string{"验证", "检查", "确认", "核对", "校验", "verify", "check", "review", "格式", "文件数", "统计", "总结"} {
		if strings.Contains(low, v) {
			// 含"格式/检查/审阅"倾向 Review;其余(验证/校验/文件数)归 Verification。
			if strings.Contains(low, "格式") || strings.Contains(low, "review") || strings.Contains(low, "检查") {
				return TodoReview
			}
			return TodoVerification
		}
	}
	return TodoAction
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

// parseExploreArgs 取 Explore 工具参数:task(要探索的问题)+ thoroughness(深度,可空)。
func parseExploreArgs(rawArgs string) (task, thoroughness string) {
	var p struct {
		Task         string `json:"task"`
		Thoroughness string `json:"thoroughness"`
	}
	_ = json.Unmarshal([]byte(rawArgs), &p)
	return p.Task, p.Thoroughness
}
