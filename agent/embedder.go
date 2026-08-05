package agent

// Embedder 生成文本的语义向量, 用于主题相似度计算。
// 两种实现: TF-IDF (稀疏, 零依赖) 和 ONNX (稠密, 语义级)。
type Embedder interface {
	// Embed 返回文本的语义向量。
	Embed(text string) map[string]float64
	// Name 返回嵌入器名称。
	Name() string
}

// EmbedderType 嵌入器类型。
type EmbedderType string

const (
	EmbedderTFIDF EmbedderType = "tfidf" // 默认: TF-IDF 稀疏向量
	EmbedderONNX  EmbedderType = "onnx"  // ONNX Sentence Embeddings
)

// NewEmbedder 创建嵌入器实例。
//  t 为类型, cacheDir 为模型缓存目录(仅 ONNX 需要)。
func NewEmbedder(t EmbedderType, cacheDir, modelName string) (Embedder, error) {
	switch t {
	case EmbedderONNX:
		if modelName == "" {
			modelName = DefaultONNXModel
		}
		emb, err := newONNXEmbedder(cacheDir, modelName)
		if err != nil {
			return nil, err
		}
		return emb, nil
	default:
		return newTFIDFEmbedder(), nil
	}
}
