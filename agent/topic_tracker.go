package agent

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

// === 本地主题追踪 (Phase 1) ===
//
// TopicTracker 用纯本地算法(零 LLM 调用)追踪对话主题的演化。
// 每轮 user 消息调用 TrackMessage(),自动分配或创建主题。
// 产出 TopicGraph 供 Phase 2 策略性压缩使用。
//
// 算法:
//   - 分词: 拉丁文本按空白/标点切词; CJK 文本按字符二元组(bigram)
//   - 关键词提取: TF-IDF, 每个主题保留 top 5 关键词
//   - 主题匹配: 新消息关键词向量与已有主题做余弦相似度
//   - 阈值: 相似度 < 0.15 则创建新主题

// Topic 表示一个对话主题。
type Topic struct {
	ID       int               // 唯一标识
	Keywords []string          // top 5 关键词
	Vector   map[string]float64 // TF-IDF 向量
	CreateAt int               // 首次出现的消息索引(在 history 中)
	LastAt   int               // 最后一次出现的消息索引
	Files    map[string]bool   // 该主题下涉及的文件路径
}

// TopicGraph 是主题追踪的完整状态, 可在 session 中序列化。
type TopicGraph struct {
	Topics    []Topic
	MsgTopics []int          // msgIdx → topicIdx (在 history 中的索引)
	DocFreq   map[string]int // 文档频率(用于 TF-IDF 回退)
	TotalDocs int
	NextID    int

	segmenter Segmenter // 分词器, 不序列化
	embedder  Embedder  // 嵌入器(TF-IDF/ONNX), 不序列化

	LastModelRole string // 上一轮使用的模型("flash"或"pro"), 不序列化
}

// NewTopicGraph 创建空的 topic graph。
// seg 为分词器, nil 时不启用主题追踪。
// emb 为嵌入器, nil 时使用默认 TF-IDF。
func NewTopicGraph(seg Segmenter, emb Embedder) *TopicGraph {
	tg := &TopicGraph{
		DocFreq:  make(map[string]int),
		embedder: emb,
	}
	if emb == nil {
		tg.embedder = newTFIDFEmbedder()
	}
	if seg != nil {
		tg.segmenter = seg
	}
	return tg
}

// === 分词器集成 ===

// Segment 使用 TopicGraph 绑定的分词器对文本分词。
// 分词器必须已配置; 仅在 segmenter 启用时 TopicGraph 才会被创建。
func (tg *TopicGraph) Segment(text string) []string {
	if tg.segmenter != nil {
		return tg.segmenter.Segment(text)
	}
	return nil
}

// === TF-IDF ===

// extractTFIDF 计算 token 列表的 TF-IDF 向量。
func (tg *TopicGraph) extractTFIDF(tokens []string) map[string]float64 {
	if len(tokens) == 0 {
		return nil
	}

	tf := make(map[string]int)
	for _, t := range tokens {
		tf[t]++
	}

	vec := make(map[string]float64, len(tf))
	for term, count := range tf {
		tfVal := float64(count) / float64(len(tokens))
		df := tg.DocFreq[term] + 1 // +1 平滑
		idf := math.Log(float64(tg.TotalDocs+1) / float64(df))
		vec[term] = tfVal * idf
	}
	return vec
}

// updateDocFreq 用新出现的 token 更新全局文档频率。
func (tg *TopicGraph) updateDocFreq(tokens []string) {
	seen := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		if seen[t] {
			continue
		}
		seen[t] = true
		tg.DocFreq[t]++
	}
	tg.TotalDocs++
}

// === 相似度 ===

// cosineSimilarity 计算两个向量的余弦相似度。
func cosineSimilarity(a, b map[string]float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for k, va := range a {
		normA += va * va
		if vb, ok := b[k]; ok {
			dot += va * vb
		}
	}
	for _, vb := range b {
		normB += vb * vb
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// === 主题管理 ===

// newTopicThreshold: 余弦相似度低于此值则创建新主题。
// 0.15 适用于 TF-IDF 稀疏向量; ONNX 稠密向量需使用 adjustedNewTopicThreshold。
const newTopicThreshold = 0.15

// adjustedNewTopicThreshold 返回适配当前嵌入器的新主题阈值。
// TF-IDF: 0.15(稀疏向量需要较低阈值区分)
// ONNX: 0.4(稠密向量相似度普遍偏高)
func (tg *TopicGraph) adjustedNewTopicThreshold() float64 {
	if tg.embedder != nil && strings.HasPrefix(tg.embedder.Name(), "onnx") {
		return 0.4
	}
	return newTopicThreshold
}

// topKeywordCount 是每个主题保留的关键词数。
const topKeywordCount = 5

// TrackMessage 处理一条 user 消息, 返回归属的主题索引和是否新建了主题。
// msgIdx 是消息在 history 中的位置, 用于 CreateAt/LastAt 追踪。
func (tg *TopicGraph) TrackMessage(content string, msgIdx int) (topicIdx int, isNew bool) {
	tokens := tg.Segment(content)
	if len(tokens) == 0 {
		// 空消息: 归入最近主题
		if len(tg.Topics) > 0 {
			last := len(tg.Topics) - 1
			tg.Topics[last].LastAt = msgIdx
			tg.MsgTopics = append(tg.MsgTopics, last)
			return last, false
		}
		return 0, false
	}

	vec := tg.embedder.Embed(content)
	if len(vec) == 0 {
		vec = tg.extractTFIDF(tokens) // 回退: embedder 未就绪时用 TF-IDF
	}

	// 查找最相似的主题
	bestTopic := -1
	bestScore := 0.0
	for i := range tg.Topics {
		score := cosineSimilarity(vec, tg.Topics[i].Vector)
		if score > bestScore {
			bestScore = score
			bestTopic = i
		}
	}

	if bestTopic == -1 || bestScore < tg.adjustedNewTopicThreshold() {
		// 创建新主题
		topic := Topic{
			ID:       tg.NextID,
			Keywords: topKeywords(vec, topKeywordCount),
			Vector:   vec,
			CreateAt: msgIdx,
			LastAt:   msgIdx,
			Files:    make(map[string]bool),
		}
		tg.NextID++
		tg.Topics = append(tg.Topics, topic)
		idx := len(tg.Topics) - 1
		tg.MsgTopics = append(tg.MsgTopics, idx)
		return idx, true
	}

	// 合并到已有主题
	tg.Topics[bestTopic].LastAt = msgIdx
	tg.Topics[bestTopic].Vector = mergeVectors(tg.Topics[bestTopic].Vector, vec, 0.3)
	tg.Topics[bestTopic].Keywords = topKeywords(tg.Topics[bestTopic].Vector, topKeywordCount)
	tg.MsgTopics = append(tg.MsgTopics, bestTopic)
	return bestTopic, false
}

// TrackFile 记录某个主题下涉及的文件路径。
func (tg *TopicGraph) TrackFile(topicIdx int, path string) {
	if topicIdx < 0 || topicIdx >= len(tg.Topics) {
		return
	}
	tg.Topics[topicIdx].Files[path] = true
}

// TopicOf 返回消息索引对应的主题索引, -1 表示未追踪。
func (tg *TopicGraph) TopicOf(msgIdx int) int {
	if msgIdx < 0 || msgIdx >= len(tg.MsgTopics) {
		return -1
	}
	return tg.MsgTopics[msgIdx]
}

// CurrentTopic 返回最近一次消息所属的主题索引。
func (tg *TopicGraph) CurrentTopic() int {
	if len(tg.MsgTopics) == 0 {
		return -1
	}
	return tg.MsgTopics[len(tg.MsgTopics)-1]
}

// TopicKeywords 返回指定主题的关键词列表。
func (tg *TopicGraph) TopicKeywords(topicIdx int) []string {
	if topicIdx < 0 || topicIdx >= len(tg.Topics) {
		return nil
	}
	return tg.Topics[topicIdx].Keywords
}

// === 辅助函数 ===

// topKeywords 从 TF-IDF 向量中取 top N 关键词。
func topKeywords(vec map[string]float64, n int) []string {
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
			return pairs[i].v > pairs[j].v // 高分优先
		}
		return len([]rune(pairs[i].k)) > len([]rune(pairs[j].k)) // 等分时, 长词优先
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

// mergeVectors 将 src 向量按权重 rate 合并到 dst。
// rate=0.3 表示新消息占 30% 权重, 旧主题向量占 70%。
func mergeVectors(dst, src map[string]float64, rate float64) map[string]float64 {
	if dst == nil {
		dst = make(map[string]float64)
	}
	for k, v := range dst {
		dst[k] = v * (1 - rate)
	}
	for k, v := range src {
		dst[k] += v * rate
	}
	return dst
}

// Rebuild 从 history 重建完整的 TopicGraph。
// 用于会话恢复时从 gob 加载的历史重建主题追踪状态。
func (tg *TopicGraph) Rebuild(history []ChatMessage) {
	seg := tg.segmenter
	emb := tg.embedder
	*tg = *NewTopicGraph(nil, emb)
	tg.segmenter = seg

	for i, msg := range history {
		if msg.Role != "user" {
			tg.MsgTopics = append(tg.MsgTopics, -1) // 非 user 消息占位
			continue
		}
		tg.TrackMessage(msg.Content, i)
		// 从 assistant 回复中提取文件引用
		if i+1 < len(history) && history[i+1].Role == "assistant" {
			topicIdx := tg.CurrentTopic()
			for _, path := range extractFileRefs(history[i+1].Content) {
				tg.TrackFile(topicIdx, path)
			}
		}
	}
}

// extractFileRefs 从 assistant 内容中提取文件路径引用。
func extractFileRefs(content string) []string {
	var refs []string
	// 匹配常见文件引用模式: `/path/to/file.go`, `file.go`, `tui/model.go`
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// 简单启发式: 包含常见代码文件扩展名的路径
		for _, ext := range []string{".go", ".py", ".js", ".ts", ".rs", ".java", ".rb", ".yaml", ".json", ".md", ".html", ".css"} {
			if idx := strings.Index(line, ext); idx >= 0 {
				// 向前搜索路径开始
				start := idx
				for start > 0 && (unicode.IsLetter(rune(line[start-1])) || unicode.IsDigit(rune(line[start-1])) ||
					line[start-1] == '/' || line[start-1] == '.' || line[start-1] == '_' || line[start-1] == '-') {
					start--
				}
				ref := strings.TrimSpace(line[start : idx+len(ext)])
				if strings.Contains(ref, ".") && !strings.HasPrefix(ref, "http") {
					refs = append(refs, ref)
					break
				}
			}
		}
	}
	return refs
}

// === 话题切换检测 ===

// TopicSwitched 判断当前消息是否偏离了会话的整体上下文。
// 通过计算当前话题向量与会话重心向量(全部话题的加权平均)的余弦相似度,
// 而非仅与主导话题比对, 更准确地反映会话的整体语义方向。
// 会话总消息数 ≥ minMsgs 且当前话题 ≥ 2 条消息,
// 且当前话题与会话重心语义不相关(余弦相似度 < extensionSim)时,
// 认为发生了有意义的话题切换(而非主题扩展)。
// 返回 (是否切换, 会话重心关键词, 新话题关键词)。
func (tg *TopicGraph) TopicSwitched(minMsgs int) (switched bool, oldKW, newKW []string) {
	if len(tg.Topics) < 2 {
		return false, nil, nil
	}
	cur := tg.CurrentTopic()
	if cur < 0 {
		return false, nil, nil
	}
	// 当前话题至少要有 2 条消息才认为是"新方向", 而非临时插话
	curMsgs := tg.Topics[cur].LastAt - tg.Topics[cur].CreateAt + 1
	if curMsgs < 2 {
		return false, nil, nil
	}
	// 会话总消息数不足 → 上下文还不够形成"会话重心"
	totalMsgs := 0
	for _, t := range tg.Topics {
		totalMsgs += t.LastAt - t.CreateAt + 1
	}
	if totalMsgs < minMsgs {
		return false, nil, nil
	}
	// 计算会话重心向量(全部话题的加权平均, 权重 = 消息数)
	centroid := tg.sessionCentroid()
	// 当前话题与重心向量的语义相似度
	sim := cosineSimilarity(tg.Topics[cur].Vector, centroid)
	if sim >= topicExtensionSim {
		return false, nil, nil // 主题扩展, 不提示
	}
	// 提取重心方向的关键词: 用消息数最多的主导话题
	dominant := -1
	dominantMsgs := 0
	for i := range tg.Topics {
		if i == cur {
			continue
		}
		msgs := tg.Topics[i].LastAt - tg.Topics[i].CreateAt + 1
		if msgs > dominantMsgs {
			dominantMsgs = msgs
			dominant = i
		}
	}
	if dominant < 0 {
		return false, nil, nil
	}
	return true, tg.Topics[dominant].Keywords, tg.Topics[cur].Keywords
}

// sessionCentroid 计算会话重心向量: 全部话题的加权平均 TF-IDF 向量。
// 权重 = 消息数, 话题消息越多, 对该向量的贡献越大。
func (tg *TopicGraph) sessionCentroid() map[string]float64 {
	centroid := make(map[string]float64)
	totalMsgs := 0
	for _, t := range tg.Topics {
		msgs := t.LastAt - t.CreateAt + 1
		totalMsgs += msgs
		weight := float64(msgs)
		for k, v := range t.Vector {
			centroid[k] += v * weight
		}
	}
	if totalMsgs > 0 {
		for k := range centroid {
			centroid[k] /= float64(totalMsgs)
		}
	}
	return centroid
}

// topicExtensionSim 是"主题扩展"的相似度阈值。
// 新话题与主导话题的余弦相似度 ≥ 此值时, 视为同一会话下的主题扩展, 不提示创建新会话。
// 设为 0.05, 介于"完全无关(0.0)"和"相似话题(0.10+)"之间。
const topicExtensionSim = 0.05

// RelevanceTo 返回当前消息与 msgIdx 处消息的主题相关性(0~1)。
// 值越大表示越相关, <0.15 表示不同话题。
func (tg *TopicGraph) RelevanceTo(msgIdx int) float64 {
	cur := tg.CurrentTopic()
	if cur < 0 || msgIdx < 0 || msgIdx >= len(tg.MsgTopics) {
		return 0
	}
	target := tg.MsgTopics[msgIdx]
	if target < 0 || target == cur {
		if target == cur {
			return 1.0 // 同话题
		}
		return 0
	}
	return cosineSimilarity(tg.Topics[cur].Vector, tg.Topics[target].Vector)
}

// SessionFocus 返回当前会话侧重点的摘要描述。
// 基于当前话题的关键词、消息数和涉及文件生成。
func (tg *TopicGraph) SessionFocus() string {
	cur := tg.CurrentTopic()
	if cur < 0 || cur >= len(tg.Topics) {
		return ""
	}
	t := tg.Topics[cur]
	// ONNX 语义向量: 无关键词可提取, 返回空
	if tg.embedder != nil && strings.HasPrefix(tg.embedder.Name(), "onnx") {
		return ""
	}
	kw := t.Keywords
	if len(kw) == 0 {
		return ""
	}
	label := strings.Join(kw, " ")
	msgs := t.LastAt - t.CreateAt + 1
	if msgs > 1 {
		label += fmt.Sprintf(" (%d轮)", msgs)
	}
	if len(t.Files) > 0 {
		files := make([]string, 0, len(t.Files))
		for f := range t.Files {
			files = append(files, f)
		}
		sort.Strings(files)
		if len(files) > 3 {
			label += fmt.Sprintf(" +%d文件", len(files))
		} else {
			label += " " + strings.Join(files, " ")
		}
	}
	return label
}

// FocusChanged 判断会话侧重点是否发生有意义的变化。
// 当当前话题消息数达到 minMsgs, 且与上次焦点不同时返回 true。
func (tg *TopicGraph) FocusChanged(minMsgs int, lastFocusID *int) (changed bool, focus string) {
	cur := tg.CurrentTopic()
	if cur < 0 {
		return false, ""
	}
	msgs := tg.Topics[cur].LastAt - tg.Topics[cur].CreateAt + 1
	if msgs < minMsgs {
		return false, ""
	}
	if *lastFocusID == cur {
		return false, ""
	}
	*lastFocusID = cur
	return true, tg.SessionFocus()
}

// EmbedderName 返回嵌入器名称, 用于 UI 判断。
func (tg *TopicGraph) EmbedderName() string {
	if tg.embedder != nil {
		return tg.embedder.Name()
	}
	return "tfidf"
}

// === 发送前偏离检测 ===

// DriftDetectThreshold 是"发送前偏离检测"的相似度阈值。
// 用户输入与会话重心的相似度低于此值时, 提示用户确认是否发送。
// 设为 0.02, 比 topicExtensionSim(0.05) 更严格, 只拦截明显偏离的输入。
const DriftDetectThreshold = 0.02

// FocusEstablished 判断会话是否已形成稳定的关注点(≥ 3 条消息)。
func (tg *TopicGraph) FocusEstablished() bool {
	total := 0
	for _, t := range tg.Topics {
		total += t.LastAt - t.CreateAt + 1
	}
	return total >= 3
}

// SimilarityToSession 返回文本与会话整体上下文的语义相似度(0~1)。
// 使用 cosineSimilarity 比较文本向量与会话重心向量。
func (tg *TopicGraph) SimilarityToSession(text string) float64 {
	vec := tg.embedder.Embed(text)
	if len(vec) == 0 {
		vec = tg.extractTFIDF(tg.Segment(text))
	}
	if len(vec) == 0 {
		return 0
	}
	centroid := tg.sessionCentroid()
	if len(centroid) == 0 {
		return 1.0 // 无重心 → 允许任意输入
	}
	return cosineSimilarity(vec, centroid)
}
