// Package router 实现语义辅助的模型路由。
//
// 路由的唯一判据是**用户消息与样板句的语义相似度**(见 patterns.go);关键词表已删除。
// 语义只能把 flash 抬成 pro,不能把 pro 压回 flash。模型没下载 / 没加载时不做入口路由,
// 起手一律 flash,由模型自己 SwitchModel 兜底 —— 这是这个包所有设计的底线。
package router

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Tokenizer 是纯 Go 的 SentencePiece Unigram 分词器,读 HuggingFace 的 tokenizer.json。
//
// multilingual-e5-small 基于 XLM-RoBERTa,用的是 Unigram(25 万词表)而非 BERT 那套 WordPiece,
// 两者算法完全不同:WordPiece 是贪心最长匹配查 vocab.txt,Unigram 要按 piece 的对数概率做
// Viterbi 求最优切分。所以不能复用任何 WordPiece 实现。
//
// 归一化这里用 NFKC 近似 XLM-R 的 Precompiled charsmap —— 后者是一张预编译的字符映射表,
// 完整实现代价大而收益极小(差异只在少数生僻字形),实测对中英文路由判断无影响。
type Tokenizer struct {
	vocab        map[string]int // piece → token ID
	scores       []float64      // token ID → score (log probability)
	vocabLen     int            // vocab 大小
	unkID        int            // <unk> token ID
	bosID        int            // <s> token ID (begin of sentence)
	eosID        int            // </s> token ID (end of sentence)
	maxPiece     int            // vocab 中最长 piece 的字节长度
	multiSpaceRe *regexp.Regexp // 多空格合并正则
}

// tokenizerJSON 是 HuggingFace tokenizer.json 的反序列化结构（只解析需要的字段）
type tokenizerJSON struct {
	Model struct {
		Type  string  `json:"type"`
		UNKId int     `json:"unk_id"`
		Vocab [][]any `json:"vocab"` // [[piece_string, score_float], ...]
	} `json:"model"`
	AddedTokens []struct {
		ID      int    `json:"id"`
		Content string `json:"content"`
		Special bool   `json:"special"`
	} `json:"added_tokens"`
}

// NewTokenizer 从 tokenizer.json 的原始字节创建 Tokenizer
func NewTokenizer(jsonData []byte) (*Tokenizer, error) {
	var tj tokenizerJSON
	if err := json.Unmarshal(jsonData, &tj); err != nil {
		return nil, fmt.Errorf("解析 tokenizer.json 失败: %w", err)
	}

	if tj.Model.Type != "Unigram" {
		return nil, fmt.Errorf("不支持的 tokenizer 类型: %s，仅支持 Unigram", tj.Model.Type)
	}

	vocabLen := len(tj.Model.Vocab)
	t := &Tokenizer{
		vocab:        make(map[string]int, vocabLen),
		scores:       make([]float64, vocabLen),
		vocabLen:     vocabLen,
		unkID:        tj.Model.UNKId,
		multiSpaceRe: regexp.MustCompile(` {2,}`),
	}

	// 构建 vocab 映射和 score 表
	for i, entry := range tj.Model.Vocab {
		if len(entry) < 2 {
			continue
		}
		piece, ok := entry[0].(string)
		if !ok {
			continue
		}
		score, ok := entry[1].(float64)
		if !ok {
			continue
		}
		t.vocab[piece] = i
		t.scores[i] = score
		if byteLen := len(piece); byteLen > t.maxPiece {
			t.maxPiece = byteLen
		}
	}

	// 从 added_tokens 中获取 BOS/EOS ID
	for _, at := range tj.AddedTokens {
		switch at.Content {
		case "<s>":
			t.bosID = at.ID
		case "</s>":
			t.eosID = at.ID
		}
	}

	return t, nil
}

// Encode 将文本分词为 token ID 序列和 attention mask
// 包含 pre-tokenization（Metaspace）、Unigram Viterbi 分词、post-processing（BOS/EOS）
func (t *Tokenizer) Encode(text string) (inputIDs []int64, attentionMask []int64) {
	// 1. Normalize: NFKC + 多空格合并
	text = norm.NFKC.String(text)
	text = t.multiSpaceRe.ReplaceAllString(text, " ")

	// 2. Pre-tokenize (Metaspace): 添加前缀空格，空格替换为 ▁
	text = " " + text
	text = strings.ReplaceAll(text, " ", "▁")

	// 3. Unigram Viterbi 分词
	tokens := t.viterbi(text)

	// 4. Post-process: 添加 BOS/EOS
	ids := make([]int64, 0, len(tokens)+2)
	ids = append(ids, int64(t.bosID))
	for _, tok := range tokens {
		ids = append(ids, int64(tok))
	}
	ids = append(ids, int64(t.eosID))

	// 5. Attention mask: 全 1
	mask := make([]int64, len(ids))
	for i := range mask {
		mask[i] = 1
	}

	return ids, mask
}

// viterbi 使用 Viterbi 算法找到最优 Unigram 分词路径
// 返回 token ID 序列（不含 BOS/EOS）
func (t *Tokenizer) viterbi(text string) []int {
	textBytes := []byte(text)
	n := len(textBytes)
	if n == 0 {
		return nil
	}

	// best[i] = 到达位置 i 的最优路径信息
	type node struct {
		score   float64
		start   int // 该 token 在 textBytes 中的起始位置
		tokenID int
	}

	best := make([]node, n+1)
	for i := range best {
		best[i].score = math.Inf(-1)
	}
	best[0].score = 0

	unkScore := float64(-100) // <unk> 的惩罚分数

	for i := 0; i < n; {
		if best[i].score == math.Inf(-1) {
			// 无法到达此位置，跳到下一个 UTF-8 字符边界
			_, size := utf8.DecodeRune(textBytes[i:])
			i += size
			continue
		}

		// 尝试所有从位置 i 开始的可能 piece
		maxEnd := min(i+t.maxPiece, n)

		matched := false
		for end := i + 1; end <= maxEnd; end++ {
			piece := string(textBytes[i:end])
			id, ok := t.vocab[piece]
			if !ok {
				continue
			}
			matched = true
			candidateScore := best[i].score + t.scores[id]
			if candidateScore > best[end].score {
				best[end] = node{score: candidateScore, start: i, tokenID: id}
			}
		}

		// 如果没有任何 vocab piece 匹配，回退到 <unk>（消耗一个 UTF-8 字符）
		if !matched {
			_, size := utf8.DecodeRune(textBytes[i:])
			end := i + size
			candidateScore := best[i].score + unkScore
			if candidateScore > best[end].score {
				best[end] = node{score: candidateScore, start: i, tokenID: t.unkID}
			}
		}

		// 前进到下一个 UTF-8 字符边界
		_, size := utf8.DecodeRune(textBytes[i:])
		i += size
	}

	// 回溯提取 token 序列
	if best[n].score == math.Inf(-1) {
		return []int{t.unkID}
	}

	var tokens []int
	pos := n
	for pos > 0 {
		tokens = append(tokens, best[pos].tokenID)
		pos = best[pos].start
	}

	// 反转（回溯得到的是逆序）
	for i, j := 0, len(tokens)-1; i < j; i, j = i+1, j-1 {
		tokens[i], tokens[j] = tokens[j], tokens[i]
	}

	return tokens
}
