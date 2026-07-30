package agent

import (
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// === 词典分词器 (Forward Maximum Matching) ===
//
// 纯 Go 实现, 零外部依赖。使用词典文件进行正向最大匹配分词。
// 词典文件首次使用时从配置的 URL 下载, 缓存到本地。
//
// 词典格式: 每行一个词, 可选频率
//   词
//   词 频率
//
// 频率越高, 匹配优先级越高(同长度时)。

// dictSegmenter 使用词典进行正向最大匹配。
type dictSegmenter struct {
	mu     sync.RWMutex
	words  map[string]wordEntry // 词 → 频率+词性
	maxLen int                  // 词典中最长词的字符数
	ready  bool
}

// wordEntry 词典条目: 频率 + 词性标签(POS tag)。
type wordEntry struct {
	freq int
	pos  string
}

// DefaultDictURL 是默认词典下载地址(MIT 许可的 jieba 词典)。
// 使用 jsdelivr CDN(国内可用, 无需翻墙)。
const DefaultDictURL = "https://cdn.jsdelivr.net/gh/fxsjy/jieba@master/jieba/dict.txt"

// dictFileName 词典文件名。
const dictFileName = "segmenter_dict.txt.gz"

// newDictSegmenter 创建词典分词器。首次调用会检查缓存目录,
// 若词典文件不存在则从 DefaultDictURL 下载。
func newDictSegmenter(cacheDir string) (*dictSegmenter, error) {
	d := &dictSegmenter{
		words: make(map[string]wordEntry),
	}

	dictPath := filepath.Join(cacheDir, dictFileName)
	if _, err := os.Stat(dictPath); os.IsNotExist(err) {
		// 词典不存在, 尝试下载
		if err := d.downloadDefaultDict(cacheDir); err != nil {
			return nil, fmt.Errorf("下载词典失败: %w\n可手动下载后放入 %s", err, dictPath)
		}
	}

	if err := d.loadDict(dictPath); err != nil {
		return nil, fmt.Errorf("加载词典失败: %w", err)
	}
	return d, nil
}

// downloadDefaultDict 从默认 URL 下载并压缩词典。
func (d *dictSegmenter) downloadDefaultDict(cacheDir string) error {
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return err
	}

	// 从 CDN 下载(MIT 许可的 jieba 词典)。
	url := DefaultDictURL
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("无法下载词典 %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载词典返回 %s", resp.Status)
	}

	// 读取并压缩保存
	tmpPath := filepath.Join(cacheDir, dictFileName+".tmp")
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	// 限制下载大小: 词典不超过 32MB
	if _, err := io.Copy(gw, io.LimitReader(resp.Body, 32<<20)); err != nil {
		os.Remove(tmpPath)
		return err
	}
	gw.Close()
	f.Close()

	os.Rename(tmpPath, filepath.Join(cacheDir, dictFileName))
	return nil
}

// loadDict 从 gzip 压缩的词典文件加载词表。
func (d *dictSegmenter) loadDict(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gr.Close()

	scanner := bufio.NewScanner(gr)
	maxLen := 0
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
			word := parts[0]
		freq := 1
		pos := ""
		if len(parts) >= 2 {
			fmt.Sscanf(parts[1], "%d", &freq)
		}
		if len(parts) >= 3 {
			pos = parts[2]
		}
		if freq <= 0 {
			freq = 1
		}
		runes := []rune(word)
		if len(runes) > maxLen {
			maxLen = len(runes)
		}
		d.words[word] = wordEntry{freq: freq, pos: pos}
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	if len(d.words) == 0 {
		return errors.New("词典为空")
	}
	d.maxLen = maxLen
	d.ready = true
	return nil
}

// Segment 对文本进行分词。
func (d *dictSegmenter) Segment(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	d.mu.RLock()
	ready := d.ready
	d.mu.RUnlock()
	if !ready {
		return nil
	}

	var tokens []string
	runes := []rune(text)
	i := 0
	for i < len(runes) {
		if isCJK(runes[i]) {
			// CJK 部分: 正向最大匹配
			tok, consumed := d.matchLongest(runes, i)
			// POS 过滤: 跳过连词/介词/助词/代词/叹词/拟声词等虚词
			if !isFunctionPOS(d.posOf(tok)) {
				tokens = append(tokens, tok)
			}
			i += consumed
		} else if isLetterOrDigit(runes[i]) {
			// 拉丁/数字: 连续读入
			var buf strings.Builder
			for i < len(runes) && isLetterOrDigit(runes[i]) {
				buf.WriteRune(runes[i])
				i++
			}
			tokens = append(tokens, buf.String())
		} else {
			i++
		}
	}
	// 后处理: 合并连续的单 CJK 字符为二元组(补偿词典未收录的复合词, 如"会话"→"会话")
	tokens = mergeSingleCJK(tokens)
	return tokens
}

// mergeSingleCJK 合并 tokens 中连续的单 CJK 字符为二元组。
// 词典分词中若某复合词(如"会话")未收录但单字存在, FMM 会输出["会","话"],
// 此函数将其合并为["会话"]。
func mergeSingleCJK(tokens []string) []string {
	if len(tokens) < 2 {
		return tokens
	}
	out := make([]string, 0, len(tokens))
	i := 0
	for i < len(tokens) {
		// 检查当前 token 是否为单 CJK 字符
		r := []rune(tokens[i])
		if len(r) == 1 && isCJK(r[0]) && i+1 < len(tokens) {
			r2 := []rune(tokens[i+1])
			if len(r2) == 1 && isCJK(r2[0]) {
				// 合并为一对
				out = append(out, string(r[0])+string(r2[0]))
				i += 2
				continue
			}
		}
		out = append(out, tokens[i])
		i++
	}
	return out
}

// matchLongest 从 runes[pos] 开始, 在词典中查找最长匹配词。
func (d *dictSegmenter) matchLongest(runes []rune, pos int) (string, int) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	maxLen := d.maxLen
	if maxLen <= 0 {
		maxLen = 4
	}
	remaining := len(runes) - pos
	if maxLen > remaining {
		maxLen = remaining
	}

	// 从最长开始尝试匹配
	bestWord := ""
	bestFreq := 0
	for length := maxLen; length >= 1; length-- {
		if pos+length > len(runes) {
			continue
		}
		candidate := string(runes[pos : pos+length])
		if entry, ok := d.words[candidate]; ok {
			bestLen := len([]rune(bestWord))
			if length > bestLen || (length == bestLen && entry.freq > bestFreq) {
				bestWord = candidate
				bestFreq = entry.freq
			}
		}
	}

	if bestWord != "" {
		return bestWord, len([]rune(bestWord))
	}
	// 未匹配: 返回单字符
	return string(runes[pos]), 1
}

// Name 返回分词器名称。
func (d *dictSegmenter) Name() string {
	d.mu.RLock()
	cnt := len(d.words)
	d.mu.RUnlock()
	return fmt.Sprintf("dict(%d词)", cnt)
}

// posOf 返回词的词性标签, 未找到返回空。
func (d *dictSegmenter) posOf(word string) string {
	if entry, ok := d.words[word]; ok {
		return entry.pos
	}
	return ""
}

// isFunctionPOS 判断词性标签是否为虚词/功能词, 不应作为关键词。
// 基于 jieba 词性标注体系:
//   c=连词, p=介词, u=助词, r=代词, e=叹词, o=拟声词, f=方位词,
//   w=标点, x=非语素, y=语气词, h=前缀, k=后缀, q=量词, g=语素
func isFunctionPOS(pos string) bool {
	if pos == "" {
		return false
	}
	switch pos[0] {
	case 'c', 'p', 'u', 'r', 'e', 'o', 'f', 'w', 'x', 'y', 'h', 'k', 'q', 'g':
		return true
	}
	return false
}
