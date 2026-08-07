package router

import (
	"deepx/agent"
)

// Router 是模型路由的入口，组合语义匹配和盲区检测。
type Router struct {
	matcher  *SemanticMatcher
	detector *BlindSpotDetector
	tg       *agent.TopicGraph
}

func NewRouter(tg *agent.TopicGraph) *Router {
	if tg == nil {
		return nil
	}
	return &Router{
		matcher:  NewSemanticMatcher(tg.Embedder()),
		detector: NewBlindSpotDetector(tg.Embedder()),
		tg:       tg,
	}
}

func (r *Router) Decide(text string) RouteDecision {
	if r.matcher != nil && r.matcher.HasEmbedder() {
		if d := r.matcher.Match(text); d != nil {
			return *d
		}
	}
	if len([]rune(text)) > 500 {
		return RouteDecision{Role: "pro", Source: "length", Trace: "超长消息"}
	}
	return RouteDecision{Role: "flash", Source: "fallback", Trace: "默认"}
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
	if emb := r.tg.Embedder(); emb != nil {
		vec = emb.Embed(input)
	}
	r.detector.Record(input, vec, bestSim, "complex")
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
