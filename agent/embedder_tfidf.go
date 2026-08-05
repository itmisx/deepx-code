package agent

import (
	"math"
	"sort"
)

// tfidfEmbedder 使用 TF-IDF 将文本映射为稀疏向量。
// 纯本地运行, 零外部依赖, 零 API 调用。
type tfidfEmbedder struct {
	docFreq   map[string]int // 文档频率(用于 IDF)
	totalDocs int
}

func newTFIDFEmbedder() *tfidfEmbedder {
	return &tfidfEmbedder{
		docFreq: make(map[string]int),
	}
}

func (e *tfidfEmbedder) Name() string { return "tfidf" }

// Embed 计算文本的 TF-IDF 向量。
// 分词工作由 Segmenter 完成, 嵌入器只负责向量化。
func (e *tfidfEmbedder) Embed(text string) map[string]float64 {
	tokens := tokenize(text)
	if len(tokens) == 0 {
		return nil
	}
	vec := e.extractTFIDF(tokens)
	e.updateDocFreq(tokens)
	return vec
}

func (e *tfidfEmbedder) extractTFIDF(tokens []string) map[string]float64 {
	tf := make(map[string]int)
	for _, t := range tokens {
		tf[t]++
	}
	vec := make(map[string]float64, len(tf))
	for term, count := range tf {
		tfVal := float64(count) / float64(len(tokens))
		df := e.docFreq[term] + 1
		idf := math.Log(float64(e.totalDocs+1) / float64(df))
		vec[term] = tfVal * idf
	}
	return vec
}

func (e *tfidfEmbedder) updateDocFreq(tokens []string) {
	seen := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		if seen[t] {
			continue
		}
		seen[t] = true
		e.docFreq[t]++
	}
	e.totalDocs++
}

// TopKeywords 从 TF-IDF 向量中取 top N 关键词。
func (e *tfidfEmbedder) TopKeywords(vec map[string]float64, n int) []string {
	type kv struct {
		k string
		v float64
	}
	pairs := make([]kv, 0, len(vec))
	for k, v := range vec {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].v != pairs[j].v {
			return pairs[i].v > pairs[j].v
		}
		return len([]rune(pairs[i].k)) > len([]rune(pairs[j].k))
	})
	if n > len(pairs) {
		n = len(pairs)
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = pairs[i].k
	}
	return out
}
