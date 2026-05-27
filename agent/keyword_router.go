package agent

import "strings"

// RouteByKeyword 是入口路由的确定性版本 — 纯本地、零延迟,替代之前的 LLM classifier。
//
// 决策顺序:
//  1. 用户消息小写化后,任意命中 complexKeywords 中的关键词 → "pro"
//     (但求知类问题如"什么是重构"会被 learningPatterns 拦截,降级到长度兜底)
//  2. 否则,按消息长度(rune 数)兜底:
//     - < 100 → "flash"  (短消息默认走快模型)
//     - > 500 → "pro"    (长消息一般有深度)
//     -        → "flash" (中间长度默认 flash 省钱)
//
// 关键词覆盖英文 + 简繁中文 + 日韩,这样不同语言用户的"复杂任务直觉"都能被路由捕获。
func RouteByKeyword(userMsg string) string {
	lower := strings.ToLower(userMsg)
	for _, kw := range complexKeywords {
		if strings.Contains(lower, kw) {
			// 命中关键词后再检查是否是求知类问题,避免"什么是重构"误升 pro
			if isLearningQuery(lower) {
				break // 穿透到长度兜底
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

// isLearningQuery 检测消息是否是"求知类"问题(询问概念/知识而非执行任务)。
// 这类消息即使命中复杂关键词也不需要 pro 模型。
// 检测方式:
//   - 中/英文:检查消息前缀(用户通常以疑问词开头)
//   - 日/韩文:日韩的疑问助词在词尾(XXとは / XX이란),用 Contains 查任意位置
func isLearningQuery(lower string) bool {
	for _, p := range learningPatterns {
		// 日/韩文模式:任意位置匹配(助词在词尾)
		if isJAKOPattern(p) {
			if strings.Contains(lower, p) {
				return true
			}
			continue
		}
		// 中/英文模式:前缀匹配(疑问词在句首)
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// isJAKOPattern 判断是否为日/韩文学习模式(这些语言疑问助词在词尾,需 Contains 匹配)。
func isJAKOPattern(p string) bool {
	switch p {
	case "とは", "って何", "どうやって", "説明して",
		"란 ", "이란 ", "뭐야 ", "설명해":
		return true
	}
	return false
}

// learningPatterns 是求知类问题的前缀模式。
// 匹配前缀而非 Contains,因为用户通常以疑问词开头询问知识性问题。
// 顺序:短词在后避免被长词误挡(如"怎么"不在列表里,只有"怎么用""怎么理解")。
var learningPatterns = []string{
	// === 简体中文 ===
	"什么是",
	"什么是",
	"怎么用",
	"怎么理解",
	"怎么配置",
	"如何理解",
	"如何配置",
	"解释一下",
	"介绍一下",
	"帮我看看什么是",
	"帮我解释一下",
	"讲讲",
	"说说",

	// === 繁体中文 ===
	"什麼是",
	"什麼是",
	"怎麼用",
	"怎麼理解",
	"如何理解",
	"解釋一下",
	"介紹一下",
	"幫我看看什麼是",
	"幫我解釋一下",
	"講講",
	"說說",

	// === English ===
	"what is ",
	"what's ",
	"how to ",
	"how do i ",
	"how does ",
	"explain ",
	"tell me about ",
	"define ",
	"what does ",

	// === 日本語 ===
	"とは",
	"って何",
	"どうやって",
	"説明して",

	// === 한국어 ===
	"란 ",
	"이란 ",
	"뭐야 ",
	"설명해",
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
