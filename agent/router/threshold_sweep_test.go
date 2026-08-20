package router

import (
	"fmt"
	"testing"

	"deepx/agent"
)

// TestThresholdSweep 扫 simThreshold,把整条曲线打出来。
//
// 这不是断言性测试(不校验具体数值),而是**改样板句 / 换模型后重新标定阈值的工具**:
// 跑一遍就能看到当前阈值是不是还压在最优点上,以及各阈值下具体错在哪几条。
// 走的是完整 RouteEntry 管线(含长度规则与正负样本比对),不是裸相似度。
func TestThresholdSweep(t *testing.T) {
	requireModel(t)
	SetUserPatterns(nil, nil)

	all := allCorpus()

	// 相似度与阈值无关,先算一次,后面各阈值复用。正负两边都要 ——
	// 判定是"过门槛 且 离正样本更近",少算一边就扫出个假的最优值。
	pos := make(map[string]float64, len(all))
	neg := make(map[string]float64, len(all))
	for _, c := range all {
		pos[c.msg], neg[c.msg] = BestSimilarity(c.msg), BestSimpleSimilarity(c.msg)
	}

	// cur 由闭包读取,扫的时候改它即可 —— 复刻 LooksComplex 的两条判据,
	// 只把阈值换成扫描变量。走的仍是 agent 那边的真实判定顺序。
	cur := simThreshold
	agent.SetSemanticAssist(func(msg string) bool {
		return pos[msg] >= cur && pos[msg] > neg[msg]
	})
	t.Cleanup(func() { agent.SetSemanticAssist(nil) })

	fmt.Printf("\n=== 阈值扫描(完整管线,共 %d 条:中文 %d + 英文 %d)===\n",
		len(all), len(e2eCorpus), len(e2eCorpusEN))
	fmt.Println("阈值  | 中文错 | 英文错 | 合计错 | 中文准确率 | 英文准确率")
	fmt.Println("------|--------|--------|--------|------------|----------")

	type res struct {
		th               float64
		zhBad, enBad     int
		zhTot, enTot     int
		zhWrong, enWrong []string
	}
	var results []res

	for th := 1.00; th >= 0.849; th -= 0.01 {
		cur = th
		r := res{th: th}
		for _, c := range all {
			got := agent.RouteEntry(c.msg)
			bad := got != c.want
			line := fmt.Sprintf("%s(want %s got %s, 正 %.3f / 负 %.3f)", c.msg, c.want, got, pos[c.msg], neg[c.msg])
			if c.lang == "zh" {
				r.zhTot++
				if bad {
					r.zhBad++
					r.zhWrong = append(r.zhWrong, line)
				}
			} else {
				r.enTot++
				if bad {
					r.enBad++
					r.enWrong = append(r.enWrong, line)
				}
			}
		}
		results = append(results, r)
		mark := ""
		if th > simThreshold-0.005 && th < simThreshold+0.005 {
			mark = "  ← 当前"
		}
		fmt.Printf("%.2f  |   %2d   |   %2d   |   %2d   |    %3.0f%%    |   %3.0f%%%s\n",
			th, r.zhBad, r.enBad, r.zhBad+r.enBad,
			100*float64(r.zhTot-r.zhBad)/float64(r.zhTot),
			100*float64(r.enTot-r.enBad)/float64(r.enTot), mark)
	}

	// 找合计错误最少的阈值。并列时取先出现的 —— 循环从高往低扫,于是取到的是较高的那个,
	// 更保守 = 语义少插手 = 过拟合风险小(见 simThreshold 注释)。
	best := results[0]
	for _, r := range results {
		if r.zhBad+r.enBad < best.zhBad+best.enBad {
			best = r
		}
	}
	fmt.Printf("\n扫描最优 %.2f(当前设定 %.2f):中文错 %d、英文错 %d\n",
		best.th, simThreshold, best.zhBad, best.enBad)
	for _, w := range best.zhWrong {
		fmt.Println("  zh ✗", w)
	}
	for _, w := range best.enWrong {
		fmt.Println("  en ✗", w)
	}

	// 只在扫描最优与当前设定明显偏离时提醒 —— 阈值本就对语料过拟合,
	// 差一格不值得改,差得多才说明样板句或模型变了。
	if diff := best.th - simThreshold; diff > 0.015 || diff < -0.015 {
		t.Logf("⚠ 扫描最优阈值 %.2f 与当前 %.2f 偏离较大,考虑重新标定 simThreshold", best.th, simThreshold)
	}
}
