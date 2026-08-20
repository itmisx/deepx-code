package router

import (
	"fmt"
	"testing"
)

// TestCrossLingual 记录"为什么样板句要中英各写一套"的实测依据。
//
// multilingual-e5 的训练目标包含把各语言的等价句映射到同一片向量空间,这确实起作用了 ——
// 但起作用的幅度不够我们用:跨语言同义句的相似度只比同语言异义句高一点点,
// 而阈值要从这两堆数里划一刀。所以不能靠跨语言对齐省掉一套句子。
func TestCrossLingual(t *testing.T) {
	requireModel(t)

	sim := func(a, b string) float64 {
		va, vb := embed(a), embed(b)
		if len(va) == 0 || len(vb) == 0 {
			t.Fatalf("编码失败: %q / %q", a, b)
		}
		return cosine(va, vb)
	}

	fmt.Println("\n=== ① 同义不同语(跨语言对齐要拉高的)===")
	same := [][2]string{
		{"把用户模块拆成独立的服务", "split the user module into a standalone service"},
		{"这段代码为什么会内存泄漏", "why does this code leak memory"},
		{"重构整个认证模块", "refactor the entire authentication module"},
		{"改个错别字", "fix a typo"},
		{"如何配置 nginx", "how to configure nginx"},
	}
	var sameVals []float64
	for _, p := range same {
		s := sim(p[0], p[1])
		sameVals = append(sameVals, s)
		fmt.Printf("  %.3f  %-28s ←→ %s\n", s, p[0], p[1])
	}

	fmt.Println("\n=== ② 异义同语(阈值必须把它们排除掉)===")
	diff := [][2]string{
		{"把用户模块拆成独立的服务", "改个错别字"},
		{"重构整个认证模块", "今天天气真好"},
		{"split the user module into a service", "fix a typo"},
		{"refactor the auth module", "the weather is nice today"},
	}
	var diffVals []float64
	for _, p := range diff {
		s := sim(p[0], p[1])
		diffVals = append(diffVals, s)
		fmt.Printf("  %.3f  %-28s ←→ %s\n", s, p[0], p[1])
	}

	avg := func(v []float64) float64 {
		t := 0.0
		for _, x := range v {
			t += x
		}
		return t / float64(len(v))
	}
	fmt.Printf("\n  同义不同语 均值 %.3f   异义同语 均值 %.3f   差距 %+.3f\n",
		avg(sameVals), avg(diffVals), avg(sameVals)-avg(diffVals))
	fmt.Printf("  当前阈值 %.2f —— 跨语言同义句大多够不着它,所以样板句必须中英各写一套。\n", simThreshold)

	if avg(sameVals) <= avg(diffVals) {
		t.Error("同义不同语的相似度没有高过异义同语 —— 跨语言对齐没起作用,前提假设变了")
	}
}

// TestEnglishMatchesEnglishPatterns 守住"英文靠英文样板句"这条:
// 关键词表删除后,英文用户完全依赖英文样板句。谁要是把它们删了或改成只剩中文,这里会红。
func TestEnglishMatchesEnglishPatterns(t *testing.T) {
	requireModel(t)
	SetUserPatterns(nil, nil)

	fmt.Printf("\n=== 英文输入 × 英文样板句(阈值 %.2f)===\n", simThreshold)
	for _, c := range e2eCorpusEN {
		s := BestSimilarity(c.msg)
		got := "flash"
		if LooksComplex(c.msg) {
			got = "pro"
		}
		mark := ""
		if got != c.want {
			mark = "  ✗"
		}
		fmt.Printf("  %.3f  %-58s want %-6s semantic=%s%s\n", s, c.msg, c.want, got, mark)
	}

	// 硬底线:典型英文复杂任务必须能被语义抬起来。
	for _, msg := range []string{
		"split the user module into a standalone service",
		"map out the call graph between these packages",
		"extract these duplicated blocks into one helper",
	} {
		if !LooksComplex(msg) {
			t.Errorf("英文复杂任务 %q 未被语义识别(相似度 %.3f)—— 英文样板句覆盖不足",
				msg, BestSimilarity(msg))
		}
	}
	// 反向底线:英文琐事不能被抬起来。
	for _, msg := range []string{"fix a typo", "rename this variable", "add a log line here"} {
		if LooksComplex(msg) {
			t.Errorf("英文琐事 %q 被误判为复杂任务(相似度 %.3f)", msg, BestSimilarity(msg))
		}
	}
}
