package router

import (
	"fmt"
	"sort"
	"testing"
)

// TestLearningQueriesStayBelowThreshold 求知问句的专项体检。
//
// 原先有一组前缀 / 后缀表(什么是… / how to… / …是什么)专门把这类消息挡在 pro 之外,
// 已随关键词表一并删除 —— 现在它们能不能维持 flash,完全取决于"问概念"和"派活"
// 在向量空间里离得够不够远。这个测试就是盯着那段余量:样板句一改就可能被顶穿,
// 而顶穿的表现是用户每次问个概念都白花一轮 pro,不容易被察觉。
func TestLearningQueriesStayBelowThreshold(t *testing.T) {
	requireModel(t)
	SetUserPatterns(nil, nil)

	// 覆盖各种问法:概念、原理、用法、对比、多语言。都该判 flash。
	queries := []string{
		// 中文 —— 概念 / 原理
		"什么是依赖注入", "什么是事件溯源", "为什么要用消息队列", "解释一下 CAP 定理",
		"介绍一下 gRPC", "协程和线程有什么区别", "TCP 三次握手是什么",
		// 中文 —— 用法 / 配置
		"如何配置 nginx", "怎么用 docker compose", "git rebase 怎么用",
		// 中文 —— 带"复杂"词汇但仍是问概念(原关键词表最容易在这栽跟头)
		"什么是微服务架构", "为什么要做代码重构", "解释一下什么是性能优化",
		// English
		"what is dependency injection", "what is event sourcing",
		"explain the CAP theorem", "how to configure nginx",
		"what's the difference between a process and a thread",
		"why do people use message queues",
		"what is microservice architecture",
	}

	type row struct {
		msg      string
		pos, neg float64
		pro      bool
	}
	var rows []row
	over := 0
	for _, q := range queries {
		r := row{q, BestSimilarity(q), BestSimpleSimilarity(q), LooksComplex(q)}
		rows = append(rows, r)
		if r.pro {
			over++
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].pos > rows[j].pos })

	fmt.Printf("\n=== 求知问句体检(阈值 %.2f)===\n", simThreshold)
	fmt.Println("任务样板 | 负样本  | 判定  | 消息")
	fmt.Println("---------|---------|-------|------")
	for _, r := range rows {
		verdict := "flash"
		mark := ""
		if r.pro {
			verdict, mark = "pro", "  ❌ 被误抬"
		} else if r.pos >= simThreshold {
			// 过了绝对门槛、靠"离负样本更近"拦下来的 —— 正是负样本存在的意义。
			mark = "  ← 靠负样本拦下"
		}
		fmt.Printf("  %.3f  |  %.3f  | %-5s | %s%s\n", r.pos, r.neg, verdict, r.msg, mark)
	}

	if over > 0 {
		t.Errorf("%d 条求知问句被判成 pro —— 删掉问句过滤规则后,这类消息靠"+
			"「相似度低于阈值」或「离负样本更近」两道防线维持 flash,现在都没拦住。"+
			"要么补负样本(simplePatterns),要么收紧正样本", over)
	}
}
