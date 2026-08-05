package agent

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	ort "github.com/getcharzp/onnxruntime_purego"
)

// onnxEmbedder 使用 ONNX Sentence Embeddings 模型生成语义向量。
// 默认模型: bge-small-zh-v1.5 (384维, 轻量中文语义)。
// 可通过 ~/.deepx/segmenter.yaml 中的 onnx_model_url / onnx_vocab_url 自定义。
type onnxEmbedder struct {
	mu        sync.Mutex
	session   *ort.Session
	vocab     map[string]int32 // WordPiece 词汇表
	ready     bool
	modelName string // 模型名称(用于 Name() 返回)
}

// onnxModelInfo 预注册的 ONNX 模型信息。
type onnxModelInfo struct {
	ModelURL string // ONNX 模型下载地址
	VocabURL string // 词汇表下载地址
}

// onnxModelRegistry 预注册的 ONNX 模型。
// 默认模型 "bge-small-zh-v1.5" 可自动下载, 其他模型需手动下载。
var onnxModelRegistry = map[string]onnxModelInfo{
	"bge-small-zh-v1.5": {
		ModelURL: "https://hf-mirror.com/onnx-community/bge-small-zh-v1.5-ONNX/resolve/main/onnx/model.onnx",
		VocabURL: "https://hf-mirror.com/BAAI/bge-small-zh-v1.5/resolve/main/vocab.txt",
	},
	"text2vec-base-chinese": {
		ModelURL: "https://hf-mirror.com/shibing624/text2vec-base-chinese/resolve/main/onnx/model.onnx",
		VocabURL: "https://hf-mirror.com/shibing624/text2vec-base-chinese/resolve/main/vocab.txt",
	},
}

// DefaultONNXModel 默认 ONNX 模型名称。
const DefaultONNXModel = "bge-small-zh-v1.5"

const onnxModelFile = "embedder_model.onnx"
const onnxVocabFile = "embedder_vocab.txt"

// newONNXEmbedder 创建 ONNX 嵌入器。
// modelName 为模型名, 空时使用默认模型。
// 默认模型未下载时自动下载, 其他模型未下载时返回错误(提示手动下载)。
func newONNXEmbedder(cacheDir, modelName string) (*onnxEmbedder, error) {
	if modelName == "" {
		modelName = DefaultONNXModel
	}
	info, ok := onnxModelRegistry[modelName]
	if !ok {
		// 未注册的模型: 必须手动下载, 检查文件是否存在
		modelPath := filepath.Join(cacheDir, onnxModelFile)
		vocabPath := filepath.Join(cacheDir, onnxVocabFile)
		if _, err := os.Stat(modelPath); os.IsNotExist(err) {
			return nil, fmt.Errorf("模型 %q 未注册, 且 ONNX 模型文件未找到: %s\n请手动下载模型和词汇表到此目录", modelName, cacheDir)
		}
		if _, err := os.Stat(vocabPath); os.IsNotExist(err) {
			return nil, fmt.Errorf("模型 %q 未注册, 且词汇表文件未找到: %s\n请手动下载", modelName, vocabPath)
		}
		return initONNXEmbedder(cacheDir, modelPath, vocabPath, modelName)
	}

	modelPath := filepath.Join(cacheDir, onnxModelFile)
	vocabPath := filepath.Join(cacheDir, onnxVocabFile)

	// 默认模型: 自动下载
	if modelName == DefaultONNXModel {
		// 检查模型文件
		if _, err := os.Stat(modelPath); os.IsNotExist(err) {
			if err := os.MkdirAll(cacheDir, 0700); err != nil {
				return nil, fmt.Errorf("创建缓存目录失败: %w", err)
			}
			if err := downloadFileHTTP(info.ModelURL, modelPath); err != nil {
				return nil, fmt.Errorf("下载 ONNX 模型失败: %w", err)
			}
		}
		// 检查词汇表
		if _, err := os.Stat(vocabPath); os.IsNotExist(err) {
			if err := os.MkdirAll(cacheDir, 0700); err != nil {
				return nil, fmt.Errorf("创建缓存目录失败: %w", err)
			}
			if err := downloadFileHTTP(info.VocabURL, vocabPath); err != nil {
				return nil, fmt.Errorf("下载词汇表失败: %w", err)
			}
		}
		return initONNXEmbedder(cacheDir, modelPath, vocabPath, modelName)
	}

	// 非默认模型: 必须手动下载
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("ONNX 模型文件未找到: %s\n请手动下载模型: %s", modelPath, info.ModelURL)
	}
	if _, err := os.Stat(vocabPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("词汇表文件未找到: %s\n请手动下载: %s", vocabPath, info.VocabURL)
	}
	return initONNXEmbedder(cacheDir, modelPath, vocabPath, modelName)
}

// initONNXEmbedder 加载模型和词汇表, 创建 ONNX 推理会话。
func initONNXEmbedder(cacheDir, modelPath, vocabPath, modelName string) (*onnxEmbedder, error) {
	// 验证词汇表文件
	str, err := os.Stat(vocabPath)
	if err != nil {
		return nil, fmt.Errorf("词汇表文件不可读: %w", err)
	}
	if str.Size() < 100 {
		return nil, fmt.Errorf("词汇表文件无效(%d 字节), 请手动下载: %s", str.Size(), vocabPath)
	}

	// 加载词汇表
	e := &onnxEmbedder{modelName: modelName}
	if err := e.loadVocab(vocabPath); err != nil {
		return nil, fmt.Errorf("加载词汇表失败: %w", err)
	}

	// 获取共享 ONNX Runtime 引擎(使用 OCR 的缓存目录, 复用已有的 ONNX Runtime 共享库)
	ortDir := filepath.Join(filepath.Dir(cacheDir), "ocr")
	engine, err := GetORTEngine(ortDir)
	if err != nil {
		return nil, fmt.Errorf("ONNX Runtime 不可用: %w", err)
	}

	// 创建推理会话(单线程即可, 向量化很快)
	session, err := engine.NewSession(modelPath, 1)
	if err != nil {
		return nil, fmt.Errorf("创建 ONNX 会话失败: %w", err)
	}
	e.session = session
	e.ready = true
	return e, nil
}

// loadVocab 加载 WordPiece 词汇表。
func (e *onnxEmbedder) loadVocab(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	e.vocab = make(map[string]int32, len(lines))
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		e.vocab[line] = int32(i)
	}
	return nil
}

// Embed 生成文本的语义向量(384维, 归一化)。
func (e *onnxEmbedder) Embed(text string) map[string]float64 {
	if !e.ready || e.session == nil {
		return nil
	}
	if text == "" {
		return nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// 分词: 转换为 token IDs + attention mask
	inputIDs, attentionMask := e.tokenize(text)
	if len(inputIDs) == 0 {
		return nil
	}

	// 构造输入张量
	inputTensor, err := ort.NewTensor([]int64{1, int64(len(inputIDs))}, inputIDs)
	if err != nil {
		return nil
	}
	defer inputTensor.Destroy()

	maskTensor, err := ort.NewTensor([]int64{1, int64(len(attentionMask))}, attentionMask)
	if err != nil {
		return nil
	}
	defer maskTensor.Destroy()

	// token_type_ids: 全零(单段输入, 不需要区分 segment)
	tokenTypes := make([]int64, len(inputIDs))
	typeTensor, err := ort.NewTensor([]int64{1, int64(len(tokenTypes))}, tokenTypes)
	if err != nil {
		return nil
	}
	defer typeTensor.Destroy()

	// ONNX 推理
	outputs, err := e.session.Run(map[string]*ort.Value{
		"input_ids":      inputTensor,
		"attention_mask": maskTensor,
		"token_type_ids": typeTensor,
	})
	if err != nil || len(outputs) == 0 {
		return nil
	}
	// 获取输出(last_hidden_state, shape [1, seq_len, 384])
	var outVal *ort.Value
	for _, v := range outputs {
		outVal = v
		break
	}
	defer outVal.Destroy()

	raw, err := ort.GetTensorData[float32](outVal)
	if err != nil {
		return nil
	}

	// mean pooling: 按 attention_mask 对 token 向量加权平均得到句向量
	seqLen := len(attentionMask)
	dim := len(raw) / seqLen
	vec := make(map[string]float64, dim)
	maskSum := 0.0
	for t := 0; t < seqLen; t++ {
		if attentionMask[t] == 0 {
			continue
		}
		maskSum++
		for d := 0; d < dim; d++ {
			vec[fmt.Sprintf("d%d", d)] += float64(raw[t*dim+d])
		}
	}
	if maskSum > 0 {
		for k := range vec {
			vec[k] /= maskSum
		}
	}
	// 归一化
	var norm float64
	for _, v := range vec {
		norm += v * v
	}
	norm = math.Sqrt(norm)
	if norm > 0 {
		for k := range vec {
			vec[k] /= norm
		}
	}
	return vec
}

func (e *onnxEmbedder) Name() string {
	if e.modelName != "" {
		return "onnx(" + e.modelName + ")"
	}
	return "onnx"
}

// tokenize 将文本转换为 token IDs 和 attention mask。
// 使用 WordPiece 分词算法(与 BERT 兼容)。
func (e *onnxEmbedder) tokenize(text string) ([]int64, []int64) {
	const maxLen = 128
	ids := make([]int64, 0, maxLen+2)
	mask := make([]int64, 0, maxLen+2)

	// [CLS] token (id=101 in BERT vocab)
	ids = append(ids, 101)
	mask = append(mask, 1)

	// 对文本进行分词
	runes := []rune(text)
	i := 0
	for i < len(runes) && len(ids) < maxLen {
		r := runes[i]
		if isCJK(r) {
			// CJK: 每个字符单独查找词汇表
			tok := strings.ToLower(string(r))
			if id, ok := e.vocab[tok]; ok {
				ids = append(ids, int64(id))
			} else {
				ids = append(ids, 100) // [UNK]
			}
			mask = append(mask, 1)
			i++
		} else if isLetterOrDigit(r) {
			// 英文单词: 累积到空格或标点
			var buf strings.Builder
			for i < len(runes) && isLetterOrDigit(runes[i]) {
				buf.WriteRune(runes[i])
				i++
			}
			word := strings.ToLower(buf.String())
			subIDs := e.wordpiece(word)
			ids = append(ids, subIDs...)
			for range subIDs {
				mask = append(mask, 1)
			}
		} else {
			i++
		}
	}

	// [SEP] token (id=102)
	ids = append(ids, 102)
	mask = append(mask, 1)

	return ids, mask
}

// wordpiece 将英文单词切分为子词。
func (e *onnxEmbedder) wordpiece(word string) []int64 {
	if len(word) == 0 {
		return nil
	}
	var ids []int64
	start := 0
	runes := []rune(word)
	for start < len(runes) {
		end := len(runes)
		found := false
		for end > start {
			sub := string(runes[start:end])
			if start > 0 {
				sub = "##" + sub
			}
			if id, ok := e.vocab[sub]; ok {
				ids = append(ids, int64(id))
				start = end
				found = true
				break
			}
			end--
		}
		if !found {
			ids = append(ids, 100) // [UNK]
			start++
		}
	}
	return ids
}

func downloadFileHTTP(url, path string) error {
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Get(url)
	if err != nil {
		return fmt.Errorf("无法下载 %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载返回 %s", resp.Status)
	}
	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, io.LimitReader(resp.Body, 200<<20)); err != nil {
		os.Remove(tmpPath)
		return err
	}
	f.Close()
	return os.Rename(tmpPath, path)
}
