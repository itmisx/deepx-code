package agent

import "strings"

// === 分词器 ===
//
// 分词器默认不启用。在 ~/.deepx/segmenter.yaml 中设置 language: zh 启用中文词典分词。
// 启用后首次使用自动下载词典文件到 ~/.deepx/segmenter/。
// 未启用时, TopicGraph 不会被创建, 无主题追踪。

// Segmenter 是分词器接口。每种语言一个实现, 纯本地运行, 零 LLM 调用。
type Segmenter interface {
	Segment(text string) []string
	Name() string
}

// NewSegmenter 按语言类型创建分词器实例。
//  cacheDir 是词典文件缓存目录。
//  t 为空时返回 nil, 表示不启用分词器。
func NewSegmenter(t string, cacheDir string) (Segmenter, error) {
	switch t {
	case "zh":
		return newDictSegmenter(cacheDir)
	default:
		return nil, nil
	}
}

// tokenize 是通用内置分词, 仅用于测试。
// 生产代码中 TopicGraph 通过 Segmenter 接口使用词典分词器。
func tokenize(text string) []string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return nil
	}

	var tokens []string
	var buf strings.Builder

	flush := func() {
		if buf.Len() > 0 {
			w := buf.String()
			if len(w) > 1 || isSignificant(w) {
				tokens = append(tokens, w)
			}
			buf.Reset()
		}
	}

	var cjkBuf []rune
	flushCJK := func() {
		if len(cjkBuf) > 0 {
			for i := 0; i+1 < len(cjkBuf); i += 2 {
				tokens = append(tokens, string(cjkBuf[i:i+2]))
			}
			if len(cjkBuf)%2 != 0 {
				tokens = append(tokens, string(cjkBuf[len(cjkBuf)-1]))
			}
			cjkBuf = cjkBuf[:0]
		}
	}

	runes := []rune(text)
	for _, r := range runes {
		if isCJK(r) {
			flush()
			cjkBuf = append(cjkBuf, r)
		} else if isLetterOrDigit(r) {
			flushCJK()
			buf.WriteRune(r)
		} else {
			flushCJK()
			flush()
		}
	}
	flushCJK()
	flush()
	return tokens
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x3040 && r <= 0x309F) ||
		(r >= 0x30A0 && r <= 0x30FF) ||
		(r >= 0xAC00 && r <= 0xD7AF)
}

func isLetterOrDigit(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func isSignificant(s string) bool {
	if len(s) != 1 {
		return true
	}
	r := rune(s[0])
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}
