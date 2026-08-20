package router

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	ort "github.com/getcharzp/onnxruntime_purego"

	"deepx/ocr"
)

// embeddingDim 是 multilingual-e5-small 的输出维度。
const embeddingDim = 384

// maxTokens 是单次编码的 token 上限。路由只看用户这一句话,截断到 128 足够 ——
// 超长消息本来就会被长度规则直接判 pro,不依赖语义。
const maxTokens = 128

// embedder 持有 ONNX 会话与分词器。加载成功后原子发布,路由侧读到非 nil 才启用语义。
type embedder struct {
	mu   sync.Mutex // ONNX 会话非并发安全,串行化调用(路由是每轮一次,不是热点)
	sess *ort.Session
	tok  *Tokenizer
}

var active atomic.Pointer[embedder]

// loadEmbedder 加载模型 + 分词器并发布。失败返回 err,调用方据此置 StatusFailed。
func loadEmbedder(dir string) error {
	raw, err := os.ReadFile(filepath.Join(dir, tokenizerFile))
	if err != nil {
		return fmt.Errorf("读取分词器失败: %w", err)
	}
	tok, err := NewTokenizer(raw)
	if err != nil {
		return fmt.Errorf("解析分词器失败: %w", err)
	}

	ortDir, err := ortCacheDir()
	if err != nil {
		return err
	}
	engine, err := ort.NewEngine(filepath.Join(ortDir, ocr.ORTLibName()))
	if err != nil {
		return fmt.Errorf("初始化 onnxruntime 失败: %w", err)
	}
	opts, err := engine.NewSessionOptions()
	if err != nil {
		return err
	}
	defer opts.Destroy()
	// 单线程即可:一次编码就一句话,开多线程反而抢 CPU(主流程还在等 LLM 响应)。
	_ = opts.SetIntraOpNumThreads(1)
	_ = opts.SetCpuMemArena(true)

	sess, err := engine.NewSession(filepath.Join(dir, modelFile), opts)
	if err != nil {
		return fmt.Errorf("加载模型失败: %w", err)
	}
	active.Store(&embedder{sess: sess, tok: tok})
	return nil
}

// Ready 表示语义嵌入器已可用。
func Ready() bool { return active.Load() != nil }

// embed 把文本编码成归一化后的句向量。任何异常都返回 nil ——
// 调用方据此判为"不像复杂任务",而不是让路由失败。
func embed(text string) []float32 {
	e := active.Load()
	if e == nil || text == "" {
		return nil
	}
	ids, mask := e.tok.Encode(text)
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > maxTokens {
		ids, mask = ids[:maxTokens], mask[:maxTokens]
	}
	seqLen := int64(len(ids))

	e.mu.Lock()
	defer e.mu.Unlock()

	inputIDs, err := ort.NewTensor([]int64{1, seqLen}, ids)
	if err != nil {
		return nil
	}
	defer inputIDs.Destroy()
	attn, err := ort.NewTensor([]int64{1, seqLen}, mask)
	if err != nil {
		return nil
	}
	defer attn.Destroy()

	// token_type_ids 全零。XLM-R 系模型只有单段输入、用不到 segment 区分,但计算图里
	// token_type_embeddings 那个 Gather 节点仍然会读它 —— 不传就是
	// "Missing Input: token_type_ids",推理直接失败(而且 Run 只返回一个笼统的 error,
	// 真正的原因只出现在 onnxruntime 打到 stderr 的日志里,极难查)。
	types := make([]int64, len(ids))
	typeTensor, err := ort.NewTensor([]int64{1, seqLen}, types)
	if err != nil {
		return nil
	}
	defer typeTensor.Destroy()

	outputs, err := e.sess.Run(map[string]*ort.Value{
		"input_ids":      inputIDs,
		"attention_mask": attn,
		"token_type_ids": typeTensor,
	})
	if err != nil || len(outputs) == 0 {
		return nil
	}
	var out *ort.Value
	for _, v := range outputs {
		out = v
		break
	}
	defer out.Destroy()

	hidden, err := ort.GetTensorData[float32](out)
	if err != nil || len(hidden) < int(seqLen)*embeddingDim {
		return nil
	}
	return normalize(meanPool(hidden, mask, int(seqLen)))
}

// meanPool 按 attention_mask 对 token 向量加权平均得到句向量。
// e5 / bge 这类模型的官方用法都是 mean pooling —— 直接取 [CLS] 或整段平铺都是错的:
// 前者这些模型没为它训练过,后者会让向量维度随句长变化、彼此无法比较。
func meanPool(hidden []float32, mask []int64, seqLen int) []float32 {
	out := make([]float32, embeddingDim)
	n := float32(0)
	for t := range seqLen {
		if mask[t] == 0 {
			continue
		}
		n++
		base := t * embeddingDim
		for d := range embeddingDim {
			out[d] += hidden[base+d]
		}
	}
	if n == 0 {
		return out
	}
	for d := range out {
		out[d] /= n
	}
	return out
}

// normalize 归一化到单位长度,这样余弦相似度退化成点积。
func normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	norm := float32(math.Sqrt(sum))
	if norm == 0 {
		return v
	}
	for i := range v {
		v[i] /= norm
	}
	return v
}

// cosine 计算两个已归一化向量的相似度。
func cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot
}
