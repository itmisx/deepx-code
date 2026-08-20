package router

import (
	"encoding/json"
	"testing"
)

// 造一份最小的 Unigram tokenizer.json,不依赖 17MB 的真实文件。
func miniTokenizer(t *testing.T) *Tokenizer {
	t.Helper()
	// Metaspace 会把空格换成 ▁,所以词表里得有带 ▁ 的 piece。
	// score 越大越优先(是对数概率,通常为负,这里用相对大小表达偏好)。
	vocab := [][]any{
		{"<unk>", 0.0},
		{"▁重构", -1.0},
		{"▁重", -5.0},
		{"构", -5.0},
		{"代码", -1.0},
		{"代", -6.0},
		{"码", -6.0},
		{"▁hello", -1.0},
		{"▁", -3.0},
	}
	doc := map[string]any{
		"model": map[string]any{
			"type": "Unigram", "unk_id": 0, "vocab": vocab,
		},
		"added_tokens": []any{
			map[string]any{"id": 100, "content": "<s>", "special": true},
			map[string]any{"id": 101, "content": "</s>", "special": true},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := NewTokenizer(raw)
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	return tok
}

func TestTokenizer_RejectsNonUnigram(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"model": map[string]any{"type": "WordPiece", "vocab": [][]any{}},
	})
	if _, err := NewTokenizer(raw); err == nil {
		t.Error("WordPiece 词表应被拒绝 —— 算法不同,硬用会得到无意义的切分")
	}
}

// Viterbi 要选**整体最优**路径,而不是从左到右贪心。
// 「重构」整词(-1)优于拆成「重」+「构」(-10),必须选前者。
func TestTokenizer_ViterbiPrefersWholeWord(t *testing.T) {
	tok := miniTokenizer(t)
	ids, mask := tok.Encode("重构")

	if len(ids) != len(mask) {
		t.Fatalf("ids 与 mask 长度应一致: %d vs %d", len(ids), len(mask))
	}
	// BOS + 1 个 piece + EOS
	if len(ids) != 3 {
		t.Errorf("「重构」应切成单个 piece(共 3 个 token 含 BOS/EOS), got %d: %v", len(ids), ids)
	}
	if ids[0] != 100 || ids[len(ids)-1] != 101 {
		t.Errorf("首尾应为 BOS/EOS, got %v", ids)
	}
	for _, m := range mask {
		if m != 1 {
			t.Errorf("attention mask 应全 1, got %v", mask)
		}
	}
}

// 词表里没有的字符必须回退 <unk>,不能丢字也不能 panic。
func TestTokenizer_UnknownFallsBackToUnk(t *testing.T) {
	tok := miniTokenizer(t)
	ids, _ := tok.Encode("鿿")
	if len(ids) < 3 {
		t.Fatalf("应有 BOS + 至少一个 token + EOS, got %v", ids)
	}
	found := false
	for _, id := range ids[1 : len(ids)-1] {
		if id == 0 { // unk_id
			found = true
		}
	}
	if !found {
		t.Errorf("未登录字符应回退 <unk>, got %v", ids)
	}
}

func TestTokenizer_EmptyInput(t *testing.T) {
	tok := miniTokenizer(t)
	ids, mask := tok.Encode("")
	if len(ids) != len(mask) {
		t.Errorf("空输入下 ids/mask 长度仍应一致: %d vs %d", len(ids), len(mask))
	}
	// 不 panic 即可;具体是否只剩 BOS/EOS 不作约束
}

func TestTokenizer_Deterministic(t *testing.T) {
	tok := miniTokenizer(t)
	a, _ := tok.Encode("重构代码")
	for range 5 {
		b, _ := tok.Encode("重构代码")
		if len(a) != len(b) {
			t.Fatalf("同一输入切分结果不稳定: %v vs %v", a, b)
		}
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("同一输入切分结果不稳定: %v vs %v", a, b)
			}
		}
	}
}
