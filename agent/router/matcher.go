package router

import (
	"sync"

	"deepx/agent"
)

const complexTaskSimThreshold = 0.6

type matcherGroup struct {
	Patterns []string
	Vectors  []map[string]float64
	Decision RouteDecision
}

type SemanticMatcher struct {
	groups   []matcherGroup
	embedder agent.Embedder
	once     sync.Once
}

func NewSemanticMatcher(emb agent.Embedder) *SemanticMatcher {
	m := &SemanticMatcher{embedder: emb}
	m.initGroups()
	return m
}

func (m *SemanticMatcher) initGroups() {
	m.groups = []matcherGroup{
		{
			// 简单任务 → flash (无 thinking)
			Patterns: LoadSimplePatterns(),
			Decision: RouteDecision{Role: "flash", Source: "semantic_simple"},
		},
		{
			// 中等复杂度 → flash + thinking: enabled
			Patterns: LoadMediumPatterns(),
			Decision: RouteDecision{Role: "flash", Thinking: "enabled", Source: "semantic_medium"},
		},
		{
			// 复杂任务 → pro + reasoning_effort=medium, 关闭 thinking(两者冗余)
			Patterns: LoadComplexPatterns(),
			Decision: RouteDecision{Role: "pro", ReasoningEffort: "medium", Thinking: "disabled", Source: "semantic_complex"},
		},
		{
			// 极深推理 → pro + reasoning_effort=high, 关闭 thinking(两者冗余)
			Patterns: LoadDeepPatterns(),
			Decision: RouteDecision{Role: "pro", ReasoningEffort: "high", Thinking: "disabled", Source: "semantic_deep"},
		},
	}
}

func (m *SemanticMatcher) HasEmbedder() bool { return m.embedder != nil }

// BestSimilarity 返回输入与所有语义元句的最高相似度。
func (m *SemanticMatcher) BestSimilarity(text string) float64 {
	if m.embedder == nil {
		return 0
	}
	m.once.Do(func() {
		for i := range m.groups {
			m.groups[i].Vectors = precomputeVectors(m.embedder, m.groups[i].Patterns)
		}
	})
	userVec := m.embedder.Embed(text)
	if len(userVec) == 0 {
		return 0
	}
	best := 0.0
	for i := range m.groups {
		for _, pVec := range m.groups[i].Vectors {
			if len(pVec) == 0 {
				continue
			}
			sim := agent.CosineSimilarity(userVec, pVec)
			if sim > best {
				best = sim
			}
		}
	}
	return best
}

func (m *SemanticMatcher) Match(text string) *RouteDecision {
	if m.embedder == nil {
		return nil
	}
	m.once.Do(func() {
		for i := range m.groups {
			m.groups[i].Vectors = precomputeVectors(m.embedder, m.groups[i].Patterns)
		}
	})
	userVec := m.embedder.Embed(text)
	if len(userVec) == 0 {
		return nil
	}
	for i := range m.groups {
		for _, pVec := range m.groups[i].Vectors {
			if len(pVec) == 0 {
				continue
			}
			if agent.CosineSimilarity(userVec, pVec) >= complexTaskSimThreshold {
				return &m.groups[i].Decision
			}
		}
	}
	return nil
}

func precomputeVectors(emb agent.Embedder, patterns []string) []map[string]float64 {
	vecs := make([]map[string]float64, len(patterns))
	for i, p := range patterns {
		vecs[i] = emb.Embed(p)
	}
	return vecs
}
