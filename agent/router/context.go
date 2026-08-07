package router

import (
	"deepx/agent"
)

// ContextAnalyzer 基于会话上下文分析路由决策。
// 当前仅用于跟踪上轮模型选择，不做路由干预。
type ContextAnalyzer struct {
	tg *agent.TopicGraph
}

// NewContextAnalyzer 创建上下文分析器。
func NewContextAnalyzer(tg *agent.TopicGraph) *ContextAnalyzer {
	return &ContextAnalyzer{tg: tg}
}

// RecordLastModel 记录本轮模型选择，供 TopicGraph 跟踪。
func (c *ContextAnalyzer) RecordLastModel(role string) {
	if c.tg != nil {
		c.tg.LastModelRole = role
	}
}
