package router

import (
	"deepx/agent"
)

// Router 是模型路由的入口，组合语义匹配和盲区检测。
// 与主题追踪解耦：只依赖嵌入器做语义匹配，不依赖 TopicGraph。
type Router struct {
	matcher   *SemanticMatcher
	detector  *BlindSpotDetector
	emb       agent.Embedder
	lastMatch *MatchResult
}

func NewRouter(emb agent.Embedder) *Router {
	return &Router{
		matcher:  NewSemanticMatcher(emb),
		detector: NewBlindSpotDetector(emb),
		emb:      emb,
	}
}

func (r *Router) Decide(text string) RouteDecision {
	if r.matcher != nil && r.matcher.HasEmbedder() {
		if res := r.matcher.Match(text); res != nil {
			// 记录匹配到的元句, 供测试模式展示
			r.lastMatch = res
			return res.Decision
		}
	}
	// 未命中语义匹配时清除 lastMatch, 避免残留旧匹配结果误导测试模式输出。
	r.lastMatch = nil
	if len([]rune(text)) > 500 {
		return RouteDecision{Role: "pro", Level: 2, Source: "length", Trace: "超长消息"}
	}
	return RouteDecision{Role: "flash", Level: 0, Source: "fallback", Trace: "默认"}
}

// LastMatch 返回最近一次语义匹配的详细结果(含元句/相似度), 供测试模式输出。
func (r *Router) LastMatch() *MatchResult {
	return r.lastMatch
}

// RecordMiss 记录语义匹配遗漏(由 SwitchModel 升级触发)。
func (r *Router) RecordMiss(input string, _ map[string]float64, _ float64) {
	if r.detector == nil {
		return
	}
	// 获取语义匹配的最大相似度
	bestSim := 0.0
	if r.matcher != nil {
		bestSim = r.matcher.BestSimilarity(input)
	}
	// 用嵌入器计算输入向量
	var vec map[string]float64
	if r.emb != nil {
		vec = r.emb.Embed(input)
	}
	r.detector.Record(input, vec, bestSim, "complex")
}

// RecordDowngrade 记录一次语义降级(模型认为当前匹配的元句定级过高)。
// 同一元句多次被降级 → 提示用户降低该元句的路由级别。
func (r *Router) RecordDowngrade() {
	if r.detector == nil {
		return
	}
	// 当前匹配到的元句(最近一次语义匹配结果)。
	if r.lastMatch == nil {
		return
	}
	r.detector.RecordDowngrade(r.lastMatch.MatchedAt)
}

// ReviewHint 返回盲区审查提示。
func (r *Router) ReviewHint() string {
	if r.detector == nil {
		return ""
	}
	return r.detector.ReviewHint()
}

// SetEmbedder 替换嵌入器(用于 ONNX 延迟加载后就绪时切换)。
func (r *Router) SetEmbedder(emb agent.Embedder) {
	r.emb = emb
	if r.matcher != nil {
		r.matcher = NewSemanticMatcher(emb)
	}
	if r.detector != nil {
		r.detector = NewBlindSpotDetector(emb)
	}
}

// BestSimilarity 返回输入与最佳语义元句的相似度。
func (r *Router) BestSimilarity(text string) float64 {
	if r.matcher == nil {
		return 0
	}
	return r.matcher.BestSimilarity(text)
}

// EmbedderName 返回当前实际使用的嵌入器名(供 UI 显示)。
// 启动时为 tfidf, ONNX 后台加载完成后经 SetEmbedder 切换为 onnx(...)。
func (r *Router) EmbedderName() string {
	if r.emb == nil {
		return "无"
	}
	return r.emb.Name()
}
