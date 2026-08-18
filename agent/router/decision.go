package router

// RouteDecision 是路由决策结果，包含模型选择、推理深度和决策依据。
type RouteDecision struct {
	Role            string // "flash" / "pro"
	ReasoningEffort string // "" / "medium" / "high" (仅 pro 生效)
	Thinking        string // "" / "enabled" / "disabled" (仅 flash 生效)
	Level           int    // 路由级别 0-3 (simple/medium/complex/deep)
	Source          string // 决策来源: "semantic_simple" / "semantic_medium" / "semantic_complex" / "context" / "length" / "fallback"
	Trace           string // 人类可读的决策依据
}
