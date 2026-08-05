package agent

import (
	"strings"
	"sync"
)

// RouteByKeyword 是入口路由的确定性版本 — 纯本地、零延迟,替代之前的 LLM classifier。
//
// 决策顺序:
//  1. 用户消息小写化后,任意命中 complexKeywords 中的关键词 → "pro"
//  2. 否则,按消息长度(rune 数)兜底:
//     - < 100 → "flash"  (短消息默认走快模型)
//     - > 500 → "pro"    (长消息一般有深度)
//     -        → "flash" (中间长度默认 flash 省钱)
//
// 关键词覆盖英文 + 简繁中文 + 日韩,这样不同语言用户的"复杂任务直觉"都能被路由捕获。
func RouteByKeyword(userMsg string) string {
	lower := strings.ToLower(userMsg)
	for _, kw := range complexKeywords {
		// 关键词本身已经是小写或 CJK(CJK 无大小写概念),
		// 用 Contains 即可正确匹配;不做边界检测以保持简单。
		if strings.Contains(lower, kw) {
			// 命中复杂关键词只是"必要条件":若整句是求知 / 问答句式
			// (什么是 X / how to X / X 是什么 等),属问答而非任务,降回 flash 省钱。
			if isLearningQuery(lower) {
				return "flash"
			}
			return "pro"
		}
	}

	runeCount := len([]rune(userMsg))
	if runeCount < 100 {
		return "flash"
	}
	if runeCount > 500 {
		return "pro"
	}
	return "flash"
}

// isLearningQuery 判断消息是否为"求知 / 问答"句式(而非任务指令)。
// 仅用锚定在句首 / 句尾的前缀、后缀匹配明确的问法,避免把任务误降级;入参为已小写化的消息。
// 取舍:"how to / 怎么 / 如何"这类略有歧义的也算问答,偏向降级 flash 省钱 ——
// 真需要 pro 时用户可手动指定,或模型在执行中自行 SwitchModel 升级。
func isLearningQuery(lower string) bool {
	s := strings.TrimRight(strings.TrimSpace(lower), " \t　?？!！。.~")
	for _, p := range learningPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	for _, suf := range learningSuffixes {
		if strings.HasSuffix(s, suf) {
			return true
		}
	}
	return false
}

// learningPrefixes:出现在句首即视为问答。多语言覆盖,英文为小写。
var learningPrefixes = []string{
	// 中文
	"什么是", "为什么", "为何", "怎么", "如何", "解释", "介绍", "啥是",
	// 英文
	"what is", "what are", "what's", "what does", "what do",
	"how to", "how do", "how does", "how can", "why ",
	"explain", "tell me about", "can you explain",
	// 日文 / 韩文
	"なぜ", "왜 ",
}

// learningSuffixes:出现在句尾即视为问答(中日韩疑问后置)。
var learningSuffixes = []string{
	"是什么", "是啥", "怎么用", // 中文
	"とは", "って何", "説明して", // 日文
	"란", "이란", "설명해", // 韩文
}

// complexKeywords 触发 pro 路由的关键词列表。
// 维护原则:
//  1. 整体偏中性,避免把日常查询误判(比如别加"看一下")
//  2. 多语言覆盖 — 国际用户可能用本地语言描述同样的复杂任务
//  3. 优先包含"动词+范围"组合(refactor / 重构 / 分析整个),不是单一动词
//
// 顺序按地区分组,便于维护。
var complexKeywords = []string{
	// === English ===
	"refactor",
	"architecture",
	"design",
	"debug",
	"security",
	"review",
	"audit",
	"migrate",
	"optimize",
	"rewrite",
	"implement",
	"analyze",
	"investigate",
	"root cause",
	"multi-file",
	"end-to-end",
	"cross-file",

	// === 简体中文 ===
	"重构",
	"架构",
	"设计",
	"调试",
	"安全",
	"审查",
	"审计",
	"迁移",
	"优化",
	"重写",
	"实现",
	"分析",
	"规划",
	"排查",
	"根因",
	"整个",
	"跨多个",
	"跨文件",
	"方案",
	"调研",

	// === 繁体中文 ===
	"重構",
	"架構",
	"設計",
	"調試",
	"審查",
	"審計",
	"遷移",
	"優化",
	"重寫",
	"實現",
	"規劃",
	"排查",
	"整個",
	"跨多個",
	"跨檔案",
	"方案",
	"調研",

	// === 日本語 ===
	"リファクタリング",
	"リファクタ",
	"アーキテクチャ",
	"設計",
	"デバッグ",
	"セキュリティ",
	"レビュー",
	"監査",
	"マイグレーション",
	"移行",
	"最適化",
	"書き直し",
	"リライト",
	"実装",
	"解析",
	"計画",
	"調査",
	"根本原因",
	"複数ファイル",
	"エンドツーエンド",

	// === 한국어 ===
	"리팩토링",
	"리팩터링",
	"아키텍처",
	"구조",
	"설계",
	"디자인",
	"디버깅",
	"디버그",
	"보안",
	"리뷰",
	"검토",
	"감사",
	"마이그레이션",
	"이전",
	"최적화",
	"재작성",
	"구현",
	"분석",
	"계획",
	"조사",
	"근본 원인",
}

// === 语义路由 ===

// complexTaskPatterns 是描述"需要 pro 模型推理"的语义原子。
// 每个原子是对一类复杂任务的完整语义描述，不是关键词——语义模型通过
// 余弦相似度匹配的是"整体语义"，而非词表命中。
// 设计原则: 宽泛到能覆盖同类变体，精确到能区分简单任务。
var complexTaskPatterns = []string{
	// 重构: 跨文件代码修改，需要理解架构和依赖
	"大规模代码重构需要理解整个系统的架构和模块间的依赖关系",
	// 分析: 深度分析系统设计，需要推理因果关系
	"分析复杂系统设计文档架构决策调用链和依赖图并推理因果",
	// 调试: 排查性能瓶颈、内存泄漏、并发问题等根因分析
	"排查性能瓶颈内存泄漏死锁并发问题并分析根因修复方案",
	// 设计: 设计新的模块接口、API 规范、抽象层和架构
	"设计新的模块接口API规范抽象层数据库表结构和技术选型方案",
	// 审查: 审查代码质量、安全性和正确性，需要深度理解
	"审查代码质量和安全性查找潜在漏洞边界条件和逻辑错误",
	// 策略: 研究技术方案、实现策略和执行计划
	"研究技术方案实现策略制定执行计划权衡利弊做出架构决策",
}

// patternVecs 缓存复杂任务模式的语义向量。
var patternVecs map[string]map[string]float64
var patternVecsOnce sync.Once

// initPatternVecs 预计算复杂任务模式的语义向量。
func initPatternVecs(embedder Embedder) {
	patternVecsOnce.Do(func() {
		if embedder == nil {
			return
		}
		patternVecs = make(map[string]map[string]float64, len(complexTaskPatterns))
		for _, p := range complexTaskPatterns {
			patternVecs[p] = embedder.Embed(p)
		}
	})
}

// RouteBySemantic 通过语义匹配判断是否需要 pro 模型。
// 当用户输入与任意"复杂任务"模式的语义相似度 ≥ complexTaskSimThreshold 时返回 "pro"。
// embedder 为 nil 时返回空串(调用方决定回退)。
const complexTaskSimThreshold = 0.6

func RouteBySemantic(userMsg string, embedder Embedder) string {
	if embedder == nil {
		return ""
	}
	initPatternVecs(embedder)
	userVec := embedder.Embed(userMsg)
	if len(userVec) == 0 {
		return ""
	}
	for _, p := range complexTaskPatterns {
		pVec := patternVecs[p]
		if len(pVec) == 0 {
			continue
		}
		sim := cosineSimilarity(userVec, pVec)
		if sim >= complexTaskSimThreshold {
			return "pro"
		}
	}
	return ""
}

// === 上下文感知路由 ===

// RouteWithContext 上下文感知路由，决定使用 flash 还是 pro 模型。
// 当 ONNX 嵌入器可用时完全使用语义匹配，否则使用关键词回退。
// 参数:
//   userMsg  - 用户当前输入
//   tg       - 主题追踪图(nil 时退化为纯关键词路由)
// 返回 "flash" 或 "pro"。
func RouteWithContext(userMsg string, tg *TopicGraph) string {
	// ONNX 模式下: 语义匹配优先(完全替代关键词)
	if tg != nil && tg.embedder != nil && strings.HasPrefix(tg.embedder.Name(), "onnx") {
		// 1. 语义匹配: 与复杂任务模式相似度
		if r := RouteBySemantic(userMsg, tg.embedder); r == "pro" {
			return r
		}
		// 2. 上下文感知: 短消息延续上轮模型
		if tg.FocusEstablished() {
			sim := tg.SimilarityToSession(userMsg)
			if len([]rune(userMsg)) < 100 && sim > 0.3 && tg.LastModelRole != "" {
				return tg.LastModelRole
			}
			if sim < 0.02 {
				return "flash"
			}
		}
		// 3. 长度启发: 超长消息需要 pro
		if len([]rune(userMsg)) > 500 {
			return "pro"
		}
		return "flash"
	}

	// TF-IDF 模式: 关键词 + 上下文
	role := RouteByKeyword(userMsg)
	if role == "pro" {
		return role
	}
	if tg != nil && tg.FocusEstablished() {
		sim := tg.SimilarityToSession(userMsg)
		if len([]rune(userMsg)) < 100 && sim > 0.3 && tg.LastModelRole != "" {
			return tg.LastModelRole
		}
		if sim < 0.02 {
			return "flash"
		}
	}
	return role
}
