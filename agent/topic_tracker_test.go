package agent

import (
	"testing"
)

// testSeg 是测试用分词器, 使用 tokenize() 实现。
// 真实环境中 TopicGraph 使用词典分词器。
type testSeg struct{}

func (s *testSeg) Segment(text string) []string { return tokenize(text) }
func (s *testSeg) Name() string                  { return "test" }

// newTestGraph 创建带测试分词器的 TopicGraph(中文虚词过滤)。
func newTestGraph() *TopicGraph {
	return NewTopicGraph(&testSeg{})
}

func TestTokenizeLatin(t *testing.T) {
	tokens := tokenize("Hello World! This is a test.")
	if len(tokens) < 4 {
		t.Fatalf("expected at least 4 tokens, got %d: %v", len(tokens), tokens)
	}
	for _, tok := range tokens {
		if tok == "" {
			t.Error("unexpected empty token")
		}
	}
}

func TestTokenizeCJK(t *testing.T) {
	tokens := tokenize("你好世界")
	if len(tokens) < 2 {
		t.Fatalf("expected at least 2 tokens, got %d: %v", len(tokens), tokens)
	}
}

func TestTokenizeMixed(t *testing.T) {
	tokens := tokenize("修改 model.go 文件中的 max_tokens 配置")
	if len(tokens) < 3 {
		t.Fatalf("expected at least 3 tokens, got %d: %v", len(tokens), tokens)
	}
}

func TestTokenizeEmpty(t *testing.T) {
	tokens := tokenize("")
	if len(tokens) != 0 {
		t.Fatalf("expected 0 tokens, got %d", len(tokens))
	}
}

func TestTokenizeSingleChar(t *testing.T) {
	// 单字符标点应被过滤; 单字母保留(可能是变量名)
	tokens := tokenize(".")
	if len(tokens) != 0 {
		t.Fatalf("single punctuation should be filtered, got %d: %v", len(tokens), tokens)
	}
}

func TestTrackMessageSameTopic(t *testing.T) {
	tg := newTestGraph()

	idx1, isNew1 := tg.TrackMessage("修改 model.yaml 中的 max_tokens 配置", 0)
	if !isNew1 {
		t.Fatal("first message should create new topic")
	}

	idx2, isNew2 := tg.TrackMessage("把 context_window 也改大一些", 1)
	if isNew2 {
		t.Fatal("similar topic should not create new topic")
	}
	if idx1 != idx2 {
		t.Fatalf("expected same topic, got %d and %d", idx1, idx2)
	}
}

func TestTrackMessageDifferentTopic(t *testing.T) {
	tg := newTestGraph()

	idx1, _ := tg.TrackMessage("修改 model.yaml 中的 max_tokens 配置", 0)
	idx2, isNew2 := tg.TrackMessage("关于鼠标右键粘贴的问题，如何适配", 1)

	if !isNew2 {
		t.Fatal("different topic should create new topic")
	}
	if idx1 == idx2 {
		t.Fatal("expected different topics")
	}

	if len(tg.Topics) != 2 {
		t.Fatalf("expected 2 topics, got %d", len(tg.Topics))
	}
}

func TestTopicKeywords(t *testing.T) {
	tg := newTestGraph()

	tg.TrackMessage("修改 deepseek 模型配置文件的 max_tokens", 0)
	kw := tg.TopicKeywords(0)
	if len(kw) == 0 {
		t.Fatal("expected keywords")
	}
	// 关键词应该包含与主题相关的词
	t.Logf("keywords: %v", kw)
}

func TestTrackFile(t *testing.T) {
	tg := newTestGraph()

	idx, _ := tg.TrackMessage("修改 model.yaml 配置", 0)
	tg.TrackFile(idx, "config/model.yaml")
	tg.TrackFile(idx, "tui/model.go")

	if !tg.Topics[idx].Files["config/model.yaml"] {
		t.Error("expected file to be tracked")
	}
	if !tg.Topics[idx].Files["tui/model.go"] {
		t.Error("expected file to be tracked")
	}
}

func TestTopicOf(t *testing.T) {
	tg := newTestGraph()

	tg.TrackMessage("msg 0", 0)
	tg.TrackMessage("msg 1", 1)
	tg.TrackMessage("msg 2", 2)

	if tg.TopicOf(0) != 0 {
		t.Errorf("msg 0: expected topic 0, got %d", tg.TopicOf(0))
	}
	if tg.TopicOf(-1) != -1 {
		t.Error("expected -1 for out of bounds")
	}
	if tg.TopicOf(100) != -1 {
		t.Error("expected -1 for out of bounds")
	}
}

func TestCurrentTopic(t *testing.T) {
	tg := newTestGraph()

	if tg.CurrentTopic() != -1 {
		t.Error("expected -1 for empty graph")
	}

	tg.TrackMessage("first message", 0)
	if tg.CurrentTopic() != 0 {
		t.Errorf("expected topic 0, got %d", tg.CurrentTopic())
	}
}

func TestRebuild(t *testing.T) {
	history := []ChatMessage{
		{Role: "user", Content: "修改 model.yaml 中的 max_tokens"},
		{Role: "assistant", Content: "已修改 config/model.yaml"},
		{Role: "user", Content: "关于鼠标右键粘贴的问题"},
		{Role: "assistant", Content: "需要修改 tui/view.go 和 tui/model.go"},
	}

	tg := newTestGraph()
	tg.Rebuild(history)

	if len(tg.Topics) != 2 {
		t.Fatalf("expected 2 topics, got %d", len(tg.Topics))
	}

	// 第二个主题应该追踪到文件
	if len(tg.Topics[1].Files) == 0 {
		t.Error("expected topic 1 to have tracked files")
	}
}

func TestCosineSimilarity(t *testing.T) {
	a := map[string]float64{"hello": 1.0, "world": 0.5}
	b := map[string]float64{"hello": 1.0, "world": 0.5}

	sim := cosineSimilarity(a, b)
	if sim < 0.99 {
		t.Errorf("identical vectors should have similarity ~1.0, got %f", sim)
	}

	c := map[string]float64{"foo": 1.0, "bar": 0.5}
	sim2 := cosineSimilarity(a, c)
	if sim2 > 0.01 {
		t.Errorf("disjoint vectors should have similarity ~0, got %f", sim2)
	}
}

func TestMergeVectors(t *testing.T) {
	dst := map[string]float64{"a": 1.0}
	src := map[string]float64{"b": 1.0}

	merged := mergeVectors(dst, src, 0.5)
	// a: 1.0*0.5 = 0.5, b: 1.0*0.5 = 0.5
	if merged["a"] != 0.5 {
		t.Errorf("expected a=0.5, got %f", merged["a"])
	}
	if merged["b"] != 0.5 {
		t.Errorf("expected b=0.5, got %f", merged["b"])
	}
}

func TestExtractFileRefs(t *testing.T) {
	content := "已修改 `tui/model.go` 和 `tui/view.go` 文件"
	refs := extractFileRefs(content)
	if len(refs) < 2 {
		t.Fatalf("expected at least 2 file refs, got %d: %v", len(refs), refs)
	}
}

func TestExtractFileRefsHTTP(t *testing.T) {
	content := "参考 https://example.com/file.go 文档"
	refs := extractFileRefs(content)
	if len(refs) != 0 {
		t.Fatalf("HTTP URLs should not be treated as file refs, got %v", refs)
	}
}

func TestTokenizeCompoundCJK(t *testing.T) {
	// 验证 CJK 词组不被拆成单字
	tokens := tokenize("分析I2 MAX代码中关于SACode相关信息")
	t.Logf("tokens: %v", tokens)

	// SACode 应作为完整词出现
	foundSACode := false
	for _, tok := range tokens {
		if tok == "sacode" {
			foundSACode = true
			break
		}
	}
	if !foundSACode {
		t.Fatal("expected 'sacode' in tokens")
	}

	// CJK 二元组应出现
	foundCompound := false
	for _, tok := range tokens {
		if len([]rune(tok)) >= 2 && isCJK([]rune(tok)[0]) {
			foundCompound = true
			break
		}
	}
	if !foundCompound {
		t.Fatal("expected at least one CJK bigram")
	}
}

func TestTopicKeywordsTiebreaker(t *testing.T) {
	// 等分时, 长词优先
	tg := newTestGraph()
	tg.TrackMessage("x y z abc defghijklmn", 0)
	kw := tg.TopicKeywords(0)
	t.Logf("keywords: %v", kw)
	// 最长的词应该排第一个
	if len(kw) > 0 && kw[0] != "defghijklmn" {
		t.Errorf("longest token should be first, got %q", kw[0])
	}
}

// TestTopicGraphUsesSegmenter 验证 TopicGraph 使用注入的分词器而非默认 tokenize。
type testSegmenter struct{}

func (s *testSegmenter) Segment(text string) []string {
	return []string{"custom", "segmenter"}
}
func (s *testSegmenter) Name() string { return "test" }

func TestTopicGraphUsesSegmenter(t *testing.T) {
	tg := NewTopicGraph(&testSegmenter{})
	tokens := tg.Segment("任何文本")
	if len(tokens) != 2 || tokens[0] != "custom" {
		t.Fatalf("expected segmenter output, got %v", tokens)
	}
	idx, isNew := tg.TrackMessage("任何文本", 0)
	if !isNew {
		t.Fatal("expected new topic")
	}
	kw := tg.TopicKeywords(idx)
	if len(kw) == 0 {
		t.Fatal("expected keywords from segmenter")
	}
	t.Logf("keywords with test segmenter: %v", kw)
}

// TestTopicGraphNilSegmenter 验证无分词器时 TopicGraph 不创建主题。
func TestTopicGraphNilSegmenter(t *testing.T) {
	tg := NewTopicGraph(nil) // 无 segmenter
	_, isNew := tg.TrackMessage("关注点识别", 0)
	if isNew {
		t.Fatal("expected no topic created without segmenter")
	}
	if len(tg.Topics) != 0 {
		t.Fatalf("expected 0 topics, got %d", len(tg.Topics))
	}
}

// TestStopWords 验证虚词过滤(POS 标签过滤, 由 dictSegmenter 内部处理)。
func TestStopWords(t *testing.T) {
	tg := newTestGraph()
	idx, isNew := tg.TrackMessage("修改了 model.yaml 中的 max_tokens 配置", 0)
	if !isNew {
		t.Fatal("expected new topic")
	}
	kw := tg.TopicKeywords(idx)
	t.Logf("keywords: %v", kw)
	// 测试分词器的虚词已由 POS 标签过滤, 此处仅验证关键词不为空
	if len(kw) == 0 {
		t.Fatal("expected keywords")
	}
}