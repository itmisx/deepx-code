package agent

import (
	"sync/atomic"
)

// semanticAssist 由 tui 在语义模型就绪后注入(见 agent/router)。
// 放成函数变量而不是直接 import:agent 是底层包,不能反过来依赖 router —— router 要用
// agent 的类型。函数变量把依赖方向掰正,agent 侧对语义实现一无所知。
var semanticAssist atomic.Pointer[func(string) bool]

// SetSemanticAssist 注入语义判定。传 nil 关闭(等于关掉入口路由,起手一律 flash)。
func SetSemanticAssist(fn func(string) bool) {
	if fn == nil {
		semanticAssist.Store(nil)
		return
	}
	semanticAssist.Store(&fn)
}

// SemanticRoutingEnabled 表示语义模型已就绪、入口路由正在工作。
// 未就绪时 RouteEntry 只剩长度兜底,UI 需要据此如实告诉用户"当前没在自动路由"。
func SemanticRoutingEnabled() bool { return semanticAssist.Load() != nil }

// RouteEntry 决定本轮的**起手**模型 —— 纯本地、零延迟,替代之前的 LLM classifier。
//
// 决策顺序:
//  1. 消息 > 500 字 → pro。长消息一般有深度,且这条不依赖模型,离线也能用。
//  2. 语义上像复杂任务 → pro(见 agent/router)。
//  3. 否则 → flash。
//
// **所有字符串匹配规则都已删除。** 原先第一位是一张 95 条的关键词表(子串匹配),
// 第二位是一组求知问句的前缀 / 后缀表;两者都已移除,理由是同一个:
// 匹配字面量没有语义,判错了只能靠继续堆词条去打补丁,而补丁本身又制造新的误判 ——
// 「设计」命中「设计稿在哪个目录」,「why」命中「why does this code leak memory」。
// 求知问句现在也交给语义:"什么是依赖注入"这类问法匹配不上任何一条任务样板句,
// 自然回落 flash,不需要单独一条规则去挡。
//
// 语义模型是异步下载的,没下好 / 下载失败时第 2 条直接跳过,起手一律 flash。
// 兜底是模型自己手里的 SwitchModel 工具:执行中发现任务比预期复杂可以自行升到 pro。
func RouteEntry(userMsg string) string {
	if userMsg == "" {
		return "flash"
	}
	if len([]rune(userMsg)) > 500 {
		return "pro"
	}
	if p := semanticAssist.Load(); p != nil && (*p)(userMsg) {
		return "pro"
	}
	return "flash"
}
