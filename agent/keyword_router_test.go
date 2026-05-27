package agent

import "testing"

func TestRouteByKeyword(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want string
	}{
		// 关键词命中(短消息也升级)
		{"en-refactor", "refactor the auth module", "pro"},
		{"en-debug-short", "debug this", "pro"},
		{"zh-refactor", "帮我重构这个", "pro"},
		{"zh-trad", "幫我重構這個", "pro"},
		{"ja-debug", "デバッグして", "pro"},
		{"ja-refactor", "リファクタリングしてください", "pro"},
		{"ko-refactor", "리팩토링 해주세요", "pro"},
		{"ko-debug", "디버깅 도와줘", "pro"},
		{"en-implement", "implement user login", "pro"},
		{"en-arch", "design the architecture", "pro"},
		{"en-migrate", "migrate database schema", "pro"},
		{"en-optimize", "optimize query performance", "pro"},

		// 假阳性过滤:求知类问题即使包含关键词也降级
		{"zh-learn-refactor", "什么是重构", "flash"},
		{"zh-learn-arch", "什么是微服务架构", "flash"},
		{"zh-learn-debug", "什么是调试模式", "flash"},
		{"zh-learn-design", "解释一下设计模式", "flash"},
		{"zh-learn-migrate", "介绍一下数据库迁移", "flash"},
		{"zh-learn-compound", "帮我看看什么是重构", "flash"},
		{"en-learn-refactor", "what is refactoring", "flash"},
		{"en-learn-debug", "how to debug memory leak", "flash"},
		{"en-learn-design", "explain design patterns", "flash"},
		{"en-learn-arch", "tell me about microservice architecture", "flash"},
		{"ja-learn", "リファクタリングとは", "flash"},
		{"ko-learn", "리팩토링이란 무엇인가", "flash"},

		// 假阳性但仍是复杂命令 → 保持 pro
		{"zh-still-command", "帮我实现一个用户登录功能", "pro"},
		{"zh-still-debug", "帮我调试这个内存泄漏问题", "pro"},

		// 长度阈值
		{"short-no-keyword", "你好", "flash"},
		{"short-en", "hi", "flash"},
		{"short-question", "main.go 第 50 行写的什么", "flash"},

		// 中等长度无关键词 → flash
		{"medium-no-keyword-zh", "我想问问你这个 main.go 里的 loadEnvFile 函数读取顺序是怎么样的,优先级是怎么定的", "flash"},

		// 长消息 > 500 → pro (300 个"内容" = 600 字)
		{"long-no-keyword", "我有一个" + repeat("内容", 300), "pro"},

		// 大小写不敏感
		{"uppercase-en", "REFACTOR THE CODE", "pro"},
		{"mixed-case", "Debug this issue", "pro"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RouteByKeyword(c.msg)
			if got != c.want {
				t.Errorf("RouteByKeyword(%q) = %q, want %q", c.msg, got, c.want)
			}
		})
	}
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
