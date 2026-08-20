package router

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"deepx/config"
)

// === 语义路由 ===
//
// 判据只有一条:用户消息与某条样板句(complexPatterns 或用户自定义)足够相似 → pro。
//
// 关键词表已删除(理由见 patterns.go)。于是路由完全依赖语义模型,而模型是异步下载的 ——
// 没下好 / 下载失败时**不做入口路由**,起手一律 flash。这不是裸奔:模型手里有 SwitchModel
// 工具(见 tools/tools.go),执行中发现任务比预期复杂可以自己升到 pro。所以最坏情况是
// "第一轮用 flash 起手,发现难了再升",而不是"复杂任务被硬塞给 flash 做完"。
//
// **语义只能把 flash 抬成 pro,不能把 pro 压回 flash。** 这条是刻意的:
//   - 压回去要切模型,而上下文缓存按模型分,切一次就得全价重新 prefill 整段历史 ——
//     长会话下比继续用 pro 的缓存价还贵(见 agent/llm.go 的 Model 字段注释);
//   - 语义判断本身有误差,只让它做加法,最坏情况是"多花了一轮 pro",
//     而不是"该用 pro 的任务被降级、答得不好"。

// simThreshold 是判定"像复杂任务"的余弦相似度下限。
//
// 必须随模型标定,不能照搬别处的经验值:multilingual-e5-small 是对比学习训练的、未做中心化,
// 实测任意两句的相似度都挤在 0.84~1.00,拿 0.6 之类的通用阈值会让所有消息都判 pro。
//
// 0.91 是在 36 条真实开发语料(中文 24 + 英文 12)上扫出来的(TestThresholdSweep):
//
//	阈值   0.94  0.93  0.92  0.91  0.90  0.89  0.88  0.87
//	错例     7     6     2     0     0     1     2     3
//
// **这个 0 是假的。** 样板句是拿这批语料调出来的,阈值又是在同一批上选的,
// 过拟合是结构性的,不是运气不好 —— 真实分布上的错误率必然高于 0。
// 有意义的是曲线形状:0.90~0.91 是一段平地而不是一个尖点,两侧衰减也平缓。
//
// 这个"平地"是负样本(simplePatterns)带来的:只有单一阈值时,最优点是 0.91 一个尖峰
// (0.90 就掉到 4 错),因为求知问句能顶到 0.912、真任务最低 0.916,阈值卡在 0.004 的缝里。
// 加上"离正样本比离负样本更近"这条相对判据后,那批顶到 0.9 以上的问句改由负样本拦下,
// 阈值不必再去切那条缝,于是变得不敏感 —— 这比阈值本身取多少重要得多。
//
// 换语料、换模型版本仍可能失准 —— 这也是为什么语义只做加法(见上):
// 判宽了最多多花一轮 pro,判窄了就退化成"起手 flash + 模型自己 SwitchModel",
// 两个方向的最坏结果都可接受。
// 真要调准,靠 /router-add、/router-delete 让用户按自己的用法增删语句,而不是拧这个数。
const simThreshold = 0.91

// 用户自定义的两组样板句(经 SetUserPatterns 注入)。
// nil / 空 = 该组未配置,用内置表。两组各存各的:改了一组不该影响另一组。
var (
	userProPatterns   atomic.Pointer[[]string]
	userFlashPatterns atomic.Pointer[[]string]
)

// SetUserPatterns 注入用户自定义的两组样板句全量表。启动时从 ~/.deepx/router.yaml 读入,
// /router-add-*、/router-delete-* 改动后重新注入。某组传 nil / 空 = 该组恢复内置默认。
//
// 改动会让对应那组已缓存的向量表失效,下次判定时按新表重算(见 patternVectors)。
func SetUserPatterns(pro, flash []string) {
	store := func(dst *atomic.Pointer[[]string], ps []string) {
		if len(ps) == 0 {
			dst.Store(nil)
			return
		}
		cp := append([]string(nil), ps...) // 存副本,免得调用方后续改动影响路由
		dst.Store(&cp)
	}
	store(&userProPatterns, pro)
	store(&userFlashPatterns, flash)
}

// DefaultProPatterns / DefaultFlashPatterns 返回两组内置表的副本 ——
// 生成 / 同步 router.yaml 时以它们为准,用户第一次增删也以它们为起点。
func DefaultProPatterns() []string   { return append([]string(nil), complexPatterns...) }
func DefaultFlashPatterns() []string { return append([]string(nil), simplePatterns...) }

// ActiveProPatterns / ActiveFlashPatterns 返回当前生效的两组(用户配置优先)。返回副本。
func ActiveProPatterns() []string {
	return append([]string(nil), srcOf(&userProPatterns, complexPatterns)...)
}

func ActiveFlashPatterns() []string {
	return append([]string(nil), srcOf(&userFlashPatterns, simplePatterns)...)
}

var (
	reloadMu   sync.Mutex
	lastStamp  string // 上次读取时 router.yaml 的 mtime+size,用来判断要不要重读
	stampValid bool
)

// reloadUserPatterns 在每次判定前按需重载 ~/.deepx/router.yaml。
//
// 用户可以直接拿编辑器改这个文件(见 config 包注释),改完不该被要求重启 deepx。
// 但也不能每条消息都去解析一遍 YAML,所以先 stat 一下:mtime+size 没变就直接返回。
// 路由是每轮对话一次,不是热路径,一次 stat 的代价可以忽略。
//
// **只影响正样本。** 反例(simplePatterns)内置只读,不在这个文件里,不受重载影响。
//
// 任何一步出错都保持现状(而不是回落内置):用户把 YAML 改坏了的那一刻,
// 沿用上一次读到的好配置,比悄悄换回默认表更接近他的预期 —— 换回去的话,
// 他会看到路由行为莫名其妙全变了,却完全联想不到是自己少打了一个缩进。
func reloadUserPatterns() {
	reloadMu.Lock()
	defer reloadMu.Unlock()

	p, err := config.RouterPath()
	if err != nil {
		return
	}
	stamp := ""
	if fi, err := os.Stat(p); err == nil {
		stamp = fi.ModTime().UTC().Format("20060102150405.000000000") + ":" + strconv.FormatInt(fi.Size(), 10)
	}
	if stampValid && stamp == lastStamp {
		return // 文件没动过
	}

	cur, err := config.LoadRouterPatterns()
	if err != nil {
		return // 解析失败:保持现状,别拿默认表盖掉
	}
	lastStamp, stampValid = stamp, true
	SetUserPatterns(cur.Pro, cur.Flash) // 某组为空 → 该组用内置默认
}

var (
	patMu   sync.Mutex
	posVecs [][]float32 // pro 组向量
	posKey  string      // posVecs 对应的样板句列表指纹
	negVecs [][]float32 // flash 组向量
	negKey  string      // negVecs 对应的指纹 —— 与 pro 组分开,改一组不必重编另一组
)

// patternVectors 返回正负两组样板句的向量表,必要时重算。
//
// 缓存键是样板句列表本身而不是一个 bool:样板句会在运行中变(/router-add、/router-delete),
// 用 sync.Once 那种一次性初始化会让新加的句子直到重启才生效。
//
// 编码失败(模型尚未就绪)时**不写缓存键**,这样下次会重试 —— 否则模型下载完成后,
// 缓存里那份空表会一直命中,语义永远启用不了。
func patternVectors() (pos, neg [][]float32) {
	reloadUserPatterns()

	patMu.Lock()
	defer patMu.Unlock()

	proSrc := srcOf(&userProPatterns, complexPatterns)
	flashSrc := srcOf(&userFlashPatterns, simplePatterns)

	encode := func(ss []string) [][]float32 {
		out := make([][]float32, 0, len(ss))
		for _, s := range ss {
			if v := embed(s); len(v) > 0 {
				out = append(out, v)
			}
		}
		return out
	}

	// 两组各判各的缓存键:只改了 pro 组时,不该把 flash 组那二十来句白编一遍
	// (实测约 50ms),反之亦然。编不出来(模型未就绪)就不写 key,下次重试。
	if k := strings.Join(proSrc, "\x00"); k != posKey || len(posVecs) == 0 {
		if v := encode(proSrc); len(v) > 0 {
			posVecs, posKey = v, k
		}
	}
	if k := strings.Join(flashSrc, "\x00"); k != negKey || len(negVecs) == 0 {
		if v := encode(flashSrc); len(v) > 0 {
			negVecs, negKey = v, k
		}
	}
	return posVecs, negVecs
}

// srcOf 取某一组当前生效的样板句(用户配置优先)。不复制 —— 调用方只读。
func srcOf(p *atomic.Pointer[[]string], fallback []string) []string {
	if v := p.Load(); v != nil {
		return *v
	}
	return fallback
}

// WarmUp 预热样板句向量。在模型加载完成的那个后台 goroutine 里调用,
// 把首次编码的 ~140ms 从"用户的第一条消息"挪到后台 —— 否则会话第一句白等一下。
func WarmUp() {
	if Ready() {
		patternVectors()
	}
}

// maxCosine 返回 v 与一组向量的最高相似度;空表返回 -1。
func maxCosine(v []float32, set [][]float32) float64 {
	best := -1.0
	for _, p := range set {
		if s := cosine(v, p); s > best {
			best = s
		}
	}
	return best
}

// LooksComplex 判断消息在语义上是否像一件复杂任务。
//
// 两个条件都要满足:
//  1. 与任务样板句的相似度 ≥ simThreshold —— 绝对门槛,挡掉"跟哪边都不像"的消息;
//  2. 离任务样板句比离负样本更近 —— 相对判据,挡掉"问概念但用词很像任务"的消息
//     (「为什么要做代码重构」对正样本 0.912 已过门槛,但它离负样本更近)。
//
// 只有条件 1 是不够的:实测求知问句能顶到 0.912,而真任务最低 0.916,
// 中间那 0.004 放不下一个可靠的阈值(见 simThreshold 注释)。
//
// 嵌入器没就绪、编码失败、或不满足条件,一律返回 false —— 调用方据此起手 flash。
func LooksComplex(msg string) bool {
	if !Ready() || msg == "" {
		return false
	}
	pos, neg := patternVectors()
	if len(pos) == 0 {
		return false
	}
	v := embed(msg)
	if len(v) == 0 {
		return false
	}
	bestPos := maxCosine(v, pos)
	if bestPos < simThreshold {
		return false
	}
	// 负样本表为空时(理论上不会,除非被改没了)退化成纯阈值判定,而不是全判 false。
	return len(neg) == 0 || bestPos > maxCosine(v, neg)
}

// BestSimilarity 返回消息与最相近**任务**样板句的相似度,供 /router-test 之类的诊断展示。
// 未就绪返回 -1(而不是 0 —— 0 是合法的相似度,区分不出"没跑"和"完全不像")。
func BestSimilarity(msg string) float64 {
	return bestSim(msg, true)
}

// BestSimpleSimilarity 返回与最相近**负样本**的相似度。诊断时两个数要一起看:
// 只看正样本那个数,解释不了"0.93 为什么还是 flash"。
func BestSimpleSimilarity(msg string) float64 {
	return bestSim(msg, false)
}

func bestSim(msg string, positive bool) float64 {
	if !Ready() || msg == "" {
		return -1
	}
	pos, neg := patternVectors()
	set := pos
	if !positive {
		set = neg
	}
	v := embed(msg)
	if len(set) == 0 || len(v) == 0 {
		return -1
	}
	return maxCosine(v, set)
}

// Threshold 暴露当前判定阈值,供 /router-list 如实展示 ——
// UI 里写死一个数,改了 simThreshold 就会对不上,用户照着排错会被误导。
func Threshold() float64 { return simThreshold }
