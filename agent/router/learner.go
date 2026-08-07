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
}

// NewBlindSpotDetector 创建盲区检测器。
func NewBlindSpotDetector(emb agent.Embedder) *BlindSpotDetector {
	return &BlindSpotDetector{embedder: emb}
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

// ReviewSpots 返回需要审查的盲区(≥ 5 次)。
func (d *BlindSpotDetector) ReviewSpots() []*BlindSpot {
	d.mu.Lock()
	defer d.mu.Unlock()
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

// ReviewHint 返回审查提示文本。
func (d *BlindSpotDetector) ReviewHint() string {
	spots := d.ReviewSpots()
	if len(spots) == 0 {
		return ""
	}
	var b strings.Builder
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
