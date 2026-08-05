//go:build stepfunlive

// 提交前真实端到端验证(手动跑,消耗真实 API 额度):
//   go test ./agent/ -tags stepfunlive -run TestLive -v
//
// 读 ~/.deepx/model.yaml 的真实 stepfun provider,验证 B+C 改动涉及的真实路径:
//   ① 真实压缩成功路径完好 —— 我改了压缩失败的分流,必须确认没顺手破坏正常压缩(RunCompression
//      用 stepfun 真能生成摘要、不报错);
//   ② reclaim 回收后,回收过的 history 发给 stepfun 能正常返回、不撞 400(issue #201 的病根)。
package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"deepx/config"
)

func liveEntry(t *testing.T) ModelEntry {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("读 ~/.deepx/model.yaml 失败: %v", err)
	}
	if cfg.Flash.APIKey == "" || cfg.Flash.BaseURL == "" {
		t.Skip("flash 未配置 api_key/base_url,跳过 live 测试")
	}
	e := ModelEntry(cfg.Flash)
	if e.MaxTokens == 0 || e.MaxTokens > 2000 {
		e.MaxTokens = 1024 // 省额度,别让它生成太多
	}
	t.Logf("provider=%s model=%s (真实 window=%d)", e.BaseURL, e.Model, e.ContextWindow)
	return e
}

// ① 真实压缩成功路径:多轮大历史 → RunCompression 调 stepfun 生成摘要 → 应成功、摘要非空。
// 这验证 C 的分流改动没破坏正常(成功)压缩路径。
func TestLive_RunCompressionSucceeds(t *testing.T) {
	entry := liveEntry(t)
	const ctxWin = 2_000 // 缩到匹配真实短对话(≈540 token):keepTarget=400,有可压前缀。窗口大小不影响摘要质量

	// 6 个 user 轮,真实、多样的开发对话内容(而非重复垃圾),让模型有实质可总结。
	steps := []struct{ user, cmd, out, asst string }{
		{"帮我看看登录接口为什么返回 500", "grep -rn 'func Login' auth/", "auth/handler.go:42: func Login(w, r)\nauth/service.go:88: func (s *Svc) Login(ctx, cred)", "找到两处 Login,handler 调用 service.Login,我看下 service 实现"},
		{"继续", "sed -n '88,120p' auth/service.go", "func (s *Svc) Login(ctx, cred) {\n  u := s.db.FindUser(cred.Name)\n  if u.Pass == hash(cred.Pass) { ... }", "问题在 FindUser 返回 nil 时没判空,u.Pass 会 panic → 500。我修一下"},
		{"好", "cat auth/service.go | grep -n FindUser", "88: u := s.db.FindUser(cred.Name)\n201: func (r *Repo) FindUser(name)", "FindUser 在用户不存在时返回零值而非 error,调用方没检查。加一个 nil 判断"},
		{"顺便看下有没有测试", "ls auth/*_test.go", "auth/handler_test.go\nauth/service_test.go", "有 service_test.go,但没覆盖用户不存在的场景。我补一个用例"},
		{"补完跑一下", "go test ./auth/ -run TestLogin", "--- FAIL: TestLogin_UserNotFound\n  panic: nil pointer", "复现了 panic,确认就是这个 bug。修复后重跑"},
		{"修好了吗", "go test ./auth/", "ok  auth  0.3s", "修复完成,所有测试通过。根因是 FindUser 未处理用户不存在、调用方未判空"},
	}
	hist := []ChatMessage{}
	for r, s := range steps {
		hist = append(hist, ChatMessage{Role: "user", Content: s.user})
		id := fmt.Sprintf("t%d", r)
		hist = append(hist,
			asstCall(id, "Bash", fmt.Sprintf(`{"command":%q}`, s.cmd)),
			toolMsg(id, "Bash", s.out),
			ChatMessage{Role: "assistant", Content: s.asst})
	}
	t.Logf("历史 ≈ %d tokens", EstimateHistoryTokens(hist))

	summary, cutIdx, turns, err := RunCompression("", "", hist, entry, ctxWin, "")
	if err != nil {
		t.Fatalf("❌ 真实压缩失败(正常路径不该失败): %v", err)
	}
	if strings.TrimSpace(summary) == "" {
		t.Fatal("❌ 摘要为空")
	}
	if cutIdx <= 0 || turns <= 0 {
		t.Errorf("❌ 切点/轮数异常: cutIdx=%d turns=%d", cutIdx, turns)
	}
	t.Logf("✅ 压缩成功:压掉 %d 轮,切点 %d,摘要 %d 字:\n%s",
		turns, cutIdx, len([]rune(summary)), truncRunes(summary, 200))
}

// ② reclaim 兜底后不 400:构造超窗口的碎片+大输出历史 → ReclaimToolOutputs 回收 →
// 回收后的 history 发给 stepfun,应正常返回、不撞 400(context length)。
func TestLive_ReclaimThenSendNo400(t *testing.T) {
	entry := liveEntry(t)
	ctxWin := entry.ContextWindow
	if ctxWin <= 0 {
		ctxWin = 256_000
	}

	// 造一个 ~120% 窗口的历史:一个长任务轮 + 几十条大工具输出。
	hist := []ChatMessage{{Role: "user", Content: "跑全量单元测试,把失败的都修好"}}
	body := strings.Repeat("测试输出:PASS pkg/foo 0.3s;覆盖率 line 128 命中;", 120)
	n := ctxWin * 120 / 100 / EstTokens(body)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("c%d", i)
		hist = append(hist,
			asstCall(id, "Bash", fmt.Sprintf(`{"command":"go test ./pkg%d/"}`, i)),
			toolMsg(id, "Bash", body))
	}
	before := EstimateHistoryTokens(hist)
	t.Logf("回收前历史 ≈ %d tokens(%.0f%% 窗口)", before, float64(before)*100/float64(ctxWin))

	count, freed := ReclaimToolOutputs(hist, ctxWin)
	after := EstimateHistoryTokens(hist)
	t.Logf("回收 %d 条,净省 %d tok → 回收后 ≈ %d tokens(%.0f%% 窗口)",
		count, freed, after, float64(after)*100/float64(ctxWin))
	if count == 0 {
		t.Fatal("❌ 该形态应能回收(不是纯碎片)")
	}

	// 回收后的 history 追加一句,发给真实 stepfun:关键是不 400。
	hist = append(hist, ChatMessage{Role: "user", Content: "只回复「收到」两个字,不要调用工具。"})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	reply, err := CallOnce(ctx, entry.APIKey, entry.BaseURL, entry.Model, hist, 64)
	if err != nil {
		t.Fatalf("❌ 回收后请求出错(若是 400 context length 就是 #201 复现): %v", err)
	}
	t.Logf("✅ 回收后请求成功,模型回复: %q", strings.TrimSpace(reply))
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
