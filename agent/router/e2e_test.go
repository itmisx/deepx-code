package router

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"deepx/agent"
	"deepx/ocr"
)

// === 端到端验证 ===
//
// 单测只能证明各零件自己没错;这两个测试要回答的是产品问题:
//   1. 语义补判到底有没有用(能不能捞回关键词漏判的,又不制造新的误判)
//   2. 下载真的不阻塞吗,没下完时走的是不是内置关键词,下完之后会不会自动切
//
// 需要真实模型(约 118 MB)。未就绪时 skip,不让 CI 因为网络挂掉。

// 真实开发场景语料,标注"人认为该走哪个模型"。中英各一组 —— 样板句现在中英都有,
// 两边都得测(英文曾经靠 refactor / migrate 这类关键词托底,关键词删掉后全靠英文样板句)。
var e2eCorpus = []struct{ msg, want string }{
	// 原先关键词表漏判的(语义应该捞回来)
	{"把用户模块拆成独立的服务", "pro"},
	{"这段代码为什么会内存泄漏", "pro"},
	{"帮我梳理一下这几个包之间的调用关系", "pro"},
	{"把 MySQL 换成 PostgreSQL", "pro"},
	{"整理下这个函数,太长了", "pro"},
	{"帮我看看这个并发问题", "pro"},
	{"给这个模块补上完整的测试", "pro"},
	{"这几个文件的职责有点乱,理一理", "pro"},
	{"线上偶发 502,帮我定位", "pro"},
	{"把这些重复逻辑抽出来", "pro"},
	// 原先关键词表误判的(子串匹配的锅:优化 / 分析 / 实现 / 方案 / review / 设计 / 安全)
	{"优化一下这行代码的写法", "flash"},
	{"分析一下这个变量为什么是 nil", "flash"},
	{"实现一个 hello world", "flash"},
	{"这个方案不错", "flash"},
	{"review 下这个变量名合不合适", "flash"},
	{"设计稿在哪个目录", "flash"},
	{"这个安全吗", "flash"},
	// 原先关键词表本来就对的(删掉关键词后得靠语义顶上)
	{"重构整个认证模块,拆成三层", "pro"},
	{"帮我 review 一下这个 PR 的安全性", "pro"},
	{"排查一下这个性能瓶颈的根因", "pro"},
	{"改个错别字", "flash"},
	{"这行加个日志", "flash"},
	{"什么是依赖注入", "flash"},
	{"如何配置 nginx", "flash"},
}

// e2eCorpusEN 英文语料。关键词表还在时,英文靠 refactor / migrate / root cause 这几个词
// 勉强托底;删掉之后完全依赖英文样板句,所以这组是本次改动风险最高的地方。
var e2eCorpusEN = []struct{ msg, want string }{
	{"split the user module into a standalone service", "pro"},
	{"why does this code leak memory", "pro"},
	{"map out the call graph between these packages", "pro"},
	{"migrate the database from MySQL to PostgreSQL", "pro"},
	{"these files have muddled responsibilities, sort them out", "pro"},
	{"trace the root cause of this intermittent 502", "pro"},
	{"extract these duplicated blocks into one helper", "pro"},
	{"fix a typo", "flash"},
	{"add a log line here", "flash"},
	{"what is dependency injection", "flash"},
	{"rename this variable", "flash"},
	{"is this thread safe", "flash"},
}

// allCorpus 中英合并,带语言标签。
func allCorpus() []struct{ msg, want, lang string } {
	var out []struct{ msg, want, lang string }
	for _, c := range e2eCorpus {
		out = append(out, struct{ msg, want, lang string }{c.msg, c.want, "zh"})
	}
	for _, c := range e2eCorpusEN {
		out = append(out, struct{ msg, want, lang string }{c.msg, c.want, "en"})
	}
	return out
}

// requireModel 加载真实模型;没下好就 skip。
func requireModel(t *testing.T) {
	t.Helper()
	dir, err := AssetDir()
	if err != nil {
		t.Skip(err)
	}
	if !assetsReady(dir) {
		t.Skipf("模型未就绪(%s),跳过端到端验证", dir)
	}
	if !Ready() {
		if err := loadEmbedder(dir); err != nil {
			t.Fatalf("模型已下载但加载失败: %v", err)
		}
	}
}

// TestE2E_Effectiveness 功能有效性:语义模型就绪前后,中英语料的判对率怎么变。
//
// "before" 是模型没就绪的状态 —— 现在等于**完全不做入口路由**(起手一律 flash),
// 不再是"关键词兜底"。所以这个对比量的是:自动路由相比什么都不判,到底值多少。
func TestE2E_Effectiveness(t *testing.T) {
	requireModel(t)
	SetUserPatterns(nil, nil) // 用内置默认样板句

	corpus := allCorpus()
	type row struct{ msg, want, lang, before, after string }
	var rows []row

	agent.SetSemanticAssist(nil)
	for _, c := range corpus {
		rows = append(rows, row{c.msg, c.want, c.lang, agent.RouteEntry(c.msg), ""})
	}
	agent.SetSemanticAssist(LooksComplex)
	t.Cleanup(func() { agent.SetSemanticAssist(nil) })
	for i, c := range corpus {
		rows[i].after = agent.RouteEntry(c.msg)
	}

	fmt.Println("\n输入                                                     | 期望  | 未启用 | 启用后 | 最相似度")
	fmt.Println("---------------------------------------------------------|-------|--------|--------|--------")
	okBefore, okAfter, rescued, broke := 0, 0, 0, 0
	byLang := map[string][2]int{} // lang → [判对数, 总数]
	for _, r := range rows {
		if r.before == r.want {
			okBefore++
		}
		c := byLang[r.lang]
		c[1]++
		if r.after == r.want {
			okAfter++
			c[0]++
		}
		byLang[r.lang] = c
		if r.before != r.want && r.after == r.want {
			rescued++
		}
		if r.before == r.want && r.after != r.want {
			broke++
		}
		mark := "  "
		if r.before != r.after {
			mark = "→ "
		}
		bad := ""
		if r.after != r.want {
			bad = "  ✗"
		}
		fmt.Printf("%-57s| %-6s| %-7s| %s%-5s | %.3f%s\n",
			r.msg, r.want, r.before, mark, r.after, BestSimilarity(r.msg), bad)
	}
	n := len(rows)
	fmt.Printf("\n  未启用路由: %d/%d = %d%%\n", okBefore, n, okBefore*100/n)
	fmt.Printf("  启用语义后: %d/%d = %d%%   (中文 %d/%d · 英文 %d/%d)\n",
		okAfter, n, okAfter*100/n,
		byLang["zh"][0], byLang["zh"][1], byLang["en"][0], byLang["en"][1])
	fmt.Printf("  捞回:      %d 条    判坏: %d 条\n", rescued, broke)

	// 底线:语义只做加法。"判坏"只可能是把该 flash 的抬成了 pro(未启用时全是 flash),
	// 那是真金白银的成本,不能容忍太多。
	if broke > rescued/3 {
		t.Errorf("语义把 %d 条原本判对的搞错了(只捞回 %d 条)—— 误升级太多,阈值或样板句有问题", broke, rescued)
	}
	if okAfter <= okBefore {
		t.Errorf("启用语义后判对率没提升:%d → %d", okBefore, okAfter)
	}
	// 英文单独设底线:关键词删掉后英文全靠英文样板句,退化了必须能被发现。
	if byLang["en"][1] > 0 && byLang["en"][0]*100/byLang["en"][1] < 70 {
		t.Errorf("英文判对率 %d/%d 低于 70%% —— 英文样板句覆盖不足",
			byLang["en"][0], byLang["en"][1])
	}
}

// TestE2E_NonBlockingAndAutoSwitch 验证三件事:
//
//	① EnsureAssetsAsync 立即返回,不阻塞启动
//	② 就绪之前不做入口路由 —— 起手一律 flash,靠模型自己 SwitchModel 兜底
//	③ 就绪之后自动接上语义判定
func TestE2E_NonBlockingAndAutoSwitch(t *testing.T) {
	dir, err := AssetDir()
	if err != nil {
		t.Skip(err)
	}
	SetUserPatterns(nil, nil)
	agent.SetSemanticAssist(nil)
	t.Cleanup(func() { agent.SetSemanticAssist(nil) })

	const probe = "把用户模块拆成独立的服务"

	// ② 就绪前:路由未启用,判 flash
	if got := agent.RouteEntry(probe); got != "flash" {
		t.Fatalf("未接语义时应判 flash, got %s", got)
	}
	if agent.SemanticRoutingEnabled() {
		t.Fatal("未接语义时 SemanticRoutingEnabled 应为 false")
	}
	fmt.Printf("\n  ① 就绪前 RouteEntry(%q) = flash ✅(路由未启用,由 SwitchModel 兜底)\n", probe)

	// ① 非阻塞:EnsureAssetsAsync 必须立刻返回
	ready := make(chan Status, 1)
	start := time.Now()
	EnsureAssetsAsync(func(s Status, _ string) {
		if s == StatusReady {
			agent.SetSemanticAssist(LooksComplex)
		}
		ready <- s
	})
	elapsed := time.Since(start)
	fmt.Printf("  ② EnsureAssetsAsync 返回耗时 %v(必须 ≈0,不阻塞启动)\n", elapsed.Round(time.Microsecond))
	if elapsed > 100*time.Millisecond {
		t.Errorf("EnsureAssetsAsync 阻塞了 %v —— 启动会被拖住", elapsed)
	}

	// 后台还在跑的期间,路由必须照常工作(而不是等着、或者报错)
	if got := agent.RouteEntry("改个错别字"); got != "flash" {
		t.Errorf("下载期间路由应照常工作, got %s", got)
	}
	fmt.Println("  ③ 下载/加载期间路由照常工作 ✅")

	if !assetsReady(dir) {
		fmt.Println("  ④ 模型未就绪,跳过自动切换验证")
		t.Skip("模型未下载完,无法验证自动切换")
	}

	select {
	case s := <-ready:
		fmt.Printf("  ④ 最终状态: %s\n", s)
		if s != StatusReady {
			_, msg := CurrentStatus()
			t.Fatalf("模型文件齐备却未就绪: %s", msg)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("等待就绪超时")
	}

	// ③ 就绪后:同一条消息应被语义抬成 pro
	got := agent.RouteEntry(probe)
	fmt.Printf("  ⑤ 就绪后 RouteEntry(%q) = %s(相似度 %.3f)\n", probe, got, BestSimilarity(probe))
	if got != "pro" {
		t.Errorf("就绪后语义应把这条抬成 pro, got %s —— 自动切换没生效或阈值不合适", got)
	}
}

// TestE2E_AssetLayout 资产落在程序真正会读的位置,且 ORT 库路径没指错。
// (指错目录是这类功能最常见的翻车点:库明明下好了却报找不到。)
func TestE2E_AssetLayout(t *testing.T) {
	dir, err := AssetDir()
	if err != nil {
		t.Skip(err)
	}
	ortDir, err := ortCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("\n  资产目录: %s\n  ORT 目录: %s\n", dir, ortDir)

	if !assetsReady(dir) {
		t.Skip("模型未就绪")
	}
	for _, f := range []string{modelFile, tokenizerFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s 不在预期位置: %v", f, err)
		}
	}
	// ORT 库必须真的在 ortCacheDir() 指的地方
	if _, err := os.Stat(filepath.Join(ortDir, ocr.ORTLibName())); err != nil {
		t.Errorf("ORT 库不在 ortCacheDir() 指向的目录: %v", err)
	}
}
