package router

import (
	"sync"

	"deepx/agent"
)

// complexTaskSimThreshold 语义匹配命中阈值。
// 实测 text2vec-base-chinese 上: 正确命中的相似度 0.59~0.78, 无关输入 ≤0.44。
// 0.65 落在分隔带内偏保守: 能区分无关误判, 同时漏掉少量 0.59~0.64 的边缘命中(宁可漏判到 flash, 不误判升级)。
const complexTaskSimThreshold = 0.65

type matcherGroup struct {
	Name     string
	Patterns []string
	Vectors  []map[string]float64
	Decision RouteDecision
}

// MatchResult 语义匹配结果, 含命中的元句与相似度(供测试模式展示)。
type MatchResult struct {
	Decision   RouteDecision
	MatchedAt  string  // 命中的语义元句文本
	Similarity float64 // 命中相似度
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
			Name:     "simple",
			Patterns: LoadSimplePatterns(),
			Decision: RouteDecision{Role: "flash", Level: 0, Source: "semantic_simple"},
		},
		{
			Name:     "medium",
			Patterns: LoadMediumPatterns(),
			Decision: RouteDecision{Role: "flash", Level: 1, Thinking: "enabled", Source: "semantic_medium"},
		},
		{
			Name:     "complex",
			Patterns: LoadComplexPatterns(),
			Decision: RouteDecision{Role: "pro", Level: 2, ReasoningEffort: "medium", Thinking: "disabled", Source: "semantic_complex"},
		},
		{
			Name:     "deep",
			Patterns: LoadDeepPatterns(),
			Decision: RouteDecision{Role: "pro", Level: 3, ReasoningEffort: "high", Thinking: "disabled", Source: "semantic_deep"},
		},
	}
}

func (m *SemanticMatcher) HasEmbedder() bool { return m.embedder != nil }

// BestSimilarity 返回输入与所有语义元句的最高相似度。
func (m *SemanticMatcher) BestSimilarity(text string) float64 {
	if res := m.Match(text); res != nil {
		return res.Similarity
	}
	return 0
}

// Match 返回与输入相似度最高的语义组决策。
// 遍历所有组所有元句, 取相似度最高且 ≥ 阈值者; 未命中返回 nil。
func (m *SemanticMatcher) Match(text string) *MatchResult {
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
	var best *MatchResult
	for i := range m.groups {
		for j, pVec := range m.groups[i].Vectors {
			if len(pVec) == 0 {
				continue
			}
			sim := agent.CosineSimilarity(userVec, pVec)
			if sim >= complexTaskSimThreshold && (best == nil || sim > best.Similarity) {
				best = &MatchResult{
					Decision:   m.groups[i].Decision,
					MatchedAt:  m.groups[i].Patterns[j],
					Similarity: sim,
				}
			}
		}
	}
	return best
}

func precomputeVectors(emb agent.Embedder, patterns []string) []map[string]float64 {
	vecs := make([]map[string]float64, len(patterns))
	for i, p := range patterns {
		vecs[i] = emb.Embed(p)
	}
	return vecs
}
