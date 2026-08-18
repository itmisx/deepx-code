package router

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"deepx/agent"
)

// BlindSpot 记录语义匹配遗漏的输入模式。
type BlindSpot struct {
	Centroid map[string]float64 // 遗漏输入的语义重心
	Count    int                // 累积次数
	Samples  []string           // 最多 3 条示例
	MaxSim   float64            // 与最佳元句的相似度
	Target   string             // 应路由到的等级 (simple/medium/complex/deep)
}

// BlindSpotDetector 语义盲区检测器。
type BlindSpotDetector struct {
	mu       sync.Mutex
	spots    []*BlindSpot
	embedder agent.Embedder
	// downgradeCounts 统计"被降级的语义元句"次数。
	// 降级说明该元句定级过高(代表的任务实际比路由级别简单), 多次触发应提示用户降级该元句。
	downgradeCounts map[string]int
}

// NewBlindSpotDetector 创建盲区检测器。
func NewBlindSpotDetector(emb agent.Embedder) *BlindSpotDetector {
	return &BlindSpotDetector{
		embedder:        emb,
		downgradeCounts: make(map[string]int),
	}
}

// RecordDowngrade 记录一次语义降级(模型认为匹配到的元句定级过高)。
// pattern 是当前匹配到的语义元句文本。
func (d *BlindSpotDetector) RecordDowngrade(pattern string) {
	if pattern == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.downgradeCounts[pattern]++
}

// Record 记录一次语义匹配遗漏。
// bestSim 是与最佳语义元句的相似度, target 是应路由到的等级。
func (d *BlindSpotDetector) Record(input string, vec map[string]float64, bestSim float64, target string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(vec) == 0 {
		return
	}

	// 查找同类盲区(相似度 > 0.7)
	for _, s := range d.spots {
		if agent.CosineSimilarity(vec, s.Centroid) > 0.7 {
			s.Count++
			if len(s.Samples) < 3 {
				s.Samples = append(s.Samples, input)
			}
			s.MaxSim = math.Max(s.MaxSim, bestSim)
			// 合并到重心
			for k, v := range s.Centroid {
				s.Centroid[k] = v * 0.9
			}
			for k, v := range vec {
				s.Centroid[k] += v * 0.1
			}
			return
		}
	}

	// 创建新盲区
	d.spots = append(d.spots, &BlindSpot{
		Centroid: vec,
		Count:    1,
		Samples:  []string{input},
		MaxSim:   bestSim,
		Target:   target,
	})
}

// ReviewHint 返回审查提示文本。
func (d *BlindSpotDetector) ReviewHint() string {
	d.mu.Lock()
	defer d.mu.Unlock()

	var b strings.Builder

	// 降级提示: 同一元句多次被降级 → 建议降级该元句。
	const downgradeThreshold = 3
	var downgraded []struct {
		pattern string
		count   int
	}
	for p, c := range d.downgradeCounts {
		if c >= downgradeThreshold {
			downgraded = append(downgraded, struct {
				pattern string
				count   int
			}{p, c})
		}
	}
	if len(downgraded) > 0 {
		sort.Slice(downgraded, func(i, j int) bool { return downgraded[i].count > downgraded[j].count })
		b.WriteString("\n⚠️ 语义元句定级过高(多次被降级):\n")
		for _, dg := range downgraded {
			p := dg.pattern
			if len([]rune(p)) > 40 {
				p = string([]rune(p)[:40]) + "…"
			}
			b.WriteString(fmt.Sprintf("  • \"%s\" 被降级 %d 次, 建议降低该元句对应的路由级别\n", p, dg.count))
		}
	}

	// 升级盲区: 语义匹配遗漏(≥5 次)。
	spots := d.ReviewSpotsLocked()
	if len(spots) == 0 {
		if b.Len() == 0 {
			return ""
		}
		return b.String()
	}
	b.WriteString("\n⚠️ 语义匹配盲区:\n")
	for _, s := range spots {
		b.WriteString(fmt.Sprintf("  • %d 次漏判, 最佳相似度 %.0f%%, 应路由到 %s\n", s.Count, s.MaxSim*100, s.Target))
		for _, ex := range s.Samples {
			if len([]rune(ex)) > 40 {
				ex = string([]rune(ex)[:40]) + "…"
			}
			b.WriteString(fmt.Sprintf("    - \"%s\"\n", ex))
		}
	}
	return b.String()
}

// ReviewSpotsLocked 返回需要审查的盲区(≥ 5 次)。调用方需持有锁。
func (d *BlindSpotDetector) ReviewSpotsLocked() []*BlindSpot {
	var out []*BlindSpot
	for _, s := range d.spots {
		if s.Count >= 5 {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Count > out[j].Count
	})
	return out
}
