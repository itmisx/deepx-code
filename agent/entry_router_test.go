package agent

import (
	"strings"
	"testing"
)

func withAssist(t *testing.T, fn func(string) bool) {
	t.Helper()
	prev := semanticAssist.Load()
	SetSemanticAssist(fn)
	t.Cleanup(func() { semanticAssist.Store(prev) })
}

// 语义未注入(模型没下载 / 下载失败)时,入口路由只剩长度兜底,其余一律 flash。
// 这是"下载失败就不做自动路由"的直接体现:不猜、不用关键词硬凑,交给模型自己 SwitchModel。
func TestRouteEntry_NoSemantic(t *testing.T) {
	withAssist(t, nil)

	cases := map[string]string{
		"重构整个认证模块":                 "flash", // 以前关键词会判 pro,现在不猜
		"refactor the auth module": "flash",
		"改个错别字":                    "flash",
		"什么是依赖注入":                  "flash",
		"":                         "flash",
		"我有一个" + strings.Repeat("内容", 300): "pro", // 长度规则不依赖模型
	}
	for msg, want := range cases {
		if got := RouteEntry(msg); got != want {
			t.Errorf("%q: want %s got %s", msg, want, got)
		}
	}
}

// 语义就绪后,判定完全由它决定(求知问句与长度规则除外)。
func TestRouteEntry_SemanticDecides(t *testing.T) {
	withAssist(t, func(string) bool { return true })
	if got := RouteEntry("把用户模块拆成独立的服务"); got != "pro" {
		t.Errorf("语义判为复杂时应 pro, got %s", got)
	}
	withAssist(t, func(string) bool { return false })
	if got := RouteEntry("把用户模块拆成独立的服务"); got != "flash" {
		t.Errorf("语义判为不复杂时应 flash, got %s", got)
	}
}

// 求知问句不再有专门规则,完全交给语义:它们匹配不上任何一条任务样板句,自然回落 flash。
// 这里用"语义说什么都像"来证明**确实没有别的规则在挡** —— 挡不住是预期,
// 真实判定由 agent/router 的相似度负责(见 TestNoLearningQueryRule 在 router 包的对照)。
func TestRouteEntry_NoLearningQueryRule(t *testing.T) {
	withAssist(t, func(string) bool { return true })

	for _, q := range []string{
		"什么是依赖注入", "如何配置 nginx", "依赖注入是什么",
		"what is dependency injection", "explain the architecture here",
		"why does this code leak memory", "这段代码为什么会内存泄漏",
	} {
		if got := RouteEntry(q); got != "pro" {
			t.Errorf("已无求知问句规则,%q 应完全由语义决定, got %s", q, got)
		}
	}
}

// 超长消息走长度规则直接 pro,不必问语义 —— 省一次推理。
func TestRouteEntry_LongMessageSkipsSemantic(t *testing.T) {
	called := false
	withAssist(t, func(string) bool { called = true; return false })

	if got := RouteEntry(strings.Repeat("啊", 600)); got != "pro" {
		t.Fatalf("超长消息应判 pro, got %s", got)
	}
	if called {
		t.Error("长度规则已判 pro,不该再调用语义")
	}
}

// SemanticRoutingEnabled 要如实反映开关状态 —— UI 靠它告诉用户"当前有没有在自动路由"。
func TestSemanticRoutingEnabled(t *testing.T) {
	withAssist(t, nil)
	if SemanticRoutingEnabled() {
		t.Error("未注入时应为 false")
	}
	withAssist(t, func(string) bool { return false })
	if !SemanticRoutingEnabled() {
		t.Error("已注入时应为 true")
	}
}
