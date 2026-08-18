package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// RunCompression 因轮数不足被拒时,返回可识别的哨兵错误(agent 据此决定永久关而非冷却重试)。
// 轮数按 user|assistant 计(见 isTurnBoundary),所以"不足"= 总共不多于 keepRecentTurns 条非 tool 消息。
func TestRunCompression_TooFewTurnsSentinel(t *testing.T) {
	hist := []ChatMessage{
		{Role: "user", Content: "一"},
		{Role: "assistant", Content: "回一"},
	} // 2 轮(user + assistant),不多于要保留的 keepRecentTurns
	_, _, _, err := RunCompression("", "", hist, ModelEntry{ContextWindow: 100000}, 100000, "")
	if !errors.Is(err, ErrCompactTooFewTurns) {
		t.Fatalf("2 轮应返回 ErrCompactTooFewTurns 哨兵, got %v", err)
	}
}

// 一个 user 消息 + 几十轮工具调用:轮数按 user|assistant 计后不再被"轮数不足"拒
// —— 这正是 issue #201 里长任务会话压不动的形态(旧规则只数 user,这里 totalUsers=1 直接秒拒)。
func TestRunCompression_SingleUserLongTurnNotRejected(t *testing.T) {
	hist := []ChatMessage{{Role: "user", Content: "跑测试并修好所有失败"}}
	body := strings.Repeat("覆盖率数据 line 42 hit; ", 200)
	for i := range 6 {
		id := fmt.Sprintf("s%d", i)
		hist = append(hist, asstCall(id, "Bash", `{"command":"go test"}`), toolMsg(id, "Bash", body))
	}
	// BaseURL 为空 → 摘要请求在本地就失败;这里只关心它已越过轮数 / 切点守卫。
	_, _, _, err := RunCompression("sys", "[]", hist, ModelEntry{ContextWindow: 20000}, 20000, "")
	if errors.Is(err, ErrCompactTooFewTurns) {
		t.Fatal("单个 user 长任务轮不应再被判成轮数不足")
	}
	if err != nil && strings.Contains(err.Error(), "无需压缩") {
		t.Fatalf("不该被切点守卫拒绝:%v", err)
	}
}

// C-分流①:压缩成功但历史仍超阈值(再压也切不动)→ 永久关,只尝试一次,不每圈刷屏。
func TestInLoopCompact_StillOverThresholdPermanentOff(t *testing.T) {
	const ctxWin = 20000
	url := compactSummaryServer(t) // 压缩请求返正常摘要,其余返工具调用 + 高 prompt_tokens

	cfg := ModelConfig{Flash: ModelEntry{BaseURL: url, Model: "m", APIKey: "k", ContextWindow: ctxWin, MaxTokens: 256}}
	attempts, est := runLoopCountingCompactAttempts2(t, cfg, bloatedHistory())

	if est > 0 && est < CompactTriggerTokens(ctxWin) {
		t.Skipf("前提不成立:压缩后估算 %d tok 已低于触发线 %d,本例测不到永久关",
			est, CompactTriggerTokens(ctxWin))
	}
	if attempts != 1 {
		t.Fatalf("压完仍超阈值应永久关、只尝试 1 次压缩,实际 %d 次(冷却重试会 >1,刷屏)", attempts)
	}
}

// C-分流②:瞬时失败(压缩请求返错)→ 冷却 compactRetryCooldown 圈后重试,尝试 >1 次。
func TestInLoopCompact_TransientRetriesAfterCooldown(t *testing.T) {
	const ctxWin = 20000
	url := compactTestServer(t, true) // 对压缩请求返 400,制造瞬时失败

	cfg := ModelConfig{Flash: ModelEntry{BaseURL: url, Model: "m", APIKey: "k", ContextWindow: ctxWin, MaxTokens: 256}}
	// 4 个 user 轮:压缩不会被 totalUsers<=2 拒,能真发压缩请求 → 撞 400 → 瞬时失败 → 冷却重试。
	hist := []ChatMessage{}
	body := strings.Repeat("覆盖率数据 line 42 hit; ", 200)
	for r := 0; r < 4; r++ {
		hist = append(hist, ChatMessage{Role: "user", Content: fmt.Sprintf("轮%d", r)})
		id := fmt.Sprintf("t%d", r)
		hist = append(hist, asstCall(id, "Bash", `{"command":"go test"}`), toolMsg(id, "Bash", body))
	}

	attempts := runLoopCountingCompactAttempts(t, cfg, hist)
	if attempts < 2 {
		t.Fatalf("瞬时失败应冷却后重试(≥2 次),实际 %d 次 —— 说明一次失败就整轮放弃了", attempts)
	}
	t.Logf("压缩尝试 %d 次(冷却=%d)", attempts, compactRetryCooldown)
}

// runLoopCountingCompactAttempts 跑 StartStream,转够圈数后取消,返回压缩尝试次数(CompactingMsg 计数)。
func runLoopCountingCompactAttempts(t *testing.T, cfg ModelConfig, hist []ChatMessage) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, ch := StartStream(ctx, cfg, hist, AgentMode_Auto, t.TempDir(), "", "", "flash", WorkingModeDefault, "", "", false, 0)

	attempts, rounds := 0, 0
	for msg := range ch {
		switch msg.(type) {
		case CompactingMsg:
			attempts++
		case ToolCallStartMsg:
			if rounds++; rounds >= compactRetryCooldown+4 {
				cancel()
			}
		}
	}
	return attempts
}

// runLoopCountingCompactAttempts2 同上,额外返回压缩成功时上报的估算量(用于判断"压完是否仍超线")。
func runLoopCountingCompactAttempts2(t *testing.T, cfg ModelConfig, hist []ChatMessage) (attempts, est int) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, ch := StartStream(ctx, cfg, hist, AgentMode_Auto, t.TempDir(), "", "", "flash", WorkingModeDefault, "", "", false, 0)

	rounds := 0
	for msg := range ch {
		switch m := msg.(type) {
		case CompactingMsg:
			attempts++
		case CompactedMsg:
			est = m.EstPromptTokens
		case ToolCallStartMsg:
			if rounds++; rounds >= compactRetryCooldown+4 {
				cancel()
			}
		}
	}
	return attempts, est
}

// compactSummaryServer:对「压缩请求」(body 含 checkpoint 指令)返回一份正常的非流式摘要;
// 其余请求返回流式 Bash 工具调用 + 高 prompt_tokens(驱动循环、过触发线)。
func compactSummaryServer(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "checkpoint") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"## 任务目标\n跑测试\n\n## 下一步\n继续\n最后模式: auto"}}]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("ResponseWriter 不支持 Flush")
			return
		}
		f.Flush()
		fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"Bash","arguments":"{\"command\":\"echo hi\"}"}}]}}]}`+"\n\n")
		f.Flush()
		fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":18000,"completion_tokens":10}}`+"\n\n")
		f.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		f.Flush()
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// bloatedHistory 造「过触发线的大历史」:一个 user 任务 + 若干轮大工具输出,可压。
func bloatedHistory() []ChatMessage {
	hist := []ChatMessage{{Role: "user", Content: "轮一"}}
	body := strings.Repeat("覆盖率数据 line 42 hit; ", 200)
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("h%d", i)
		hist = append(hist, asstCall(id, "Bash", `{"command":"go test"}`), toolMsg(id, "Bash", body))
	}
	return append(hist, ChatMessage{Role: "user", Content: "轮二"})
}

// compactTestServer 每次请求回一个 Bash 工具调用 + 高 prompt_tokens 的 usage(驱动循环、过触发线)。
// failCompaction=true 时,对「压缩请求」(body 含 checkpoint 指令)返 400,制造瞬时压缩失败。
func compactTestServer(t *testing.T, failCompaction bool) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if failCompaction && strings.Contains(string(body), "checkpoint") {
			w.WriteHeader(http.StatusBadRequest) // 400 不重试,直接让 RunCompression 失败
			_, _ = io.WriteString(w, `{"error":{"message":"boom"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("ResponseWriter 不支持 Flush")
			return
		}
		f.Flush()
		fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"Bash","arguments":"{\"command\":\"echo hi\"}"}}]}}]}`+"\n\n")
		f.Flush()
		fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":18000,"completion_tokens":10}}`+"\n\n")
		f.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		f.Flush()
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}
