package agent

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// reclaimPlainSSE 一个只回一句文本、不带工具调用的假端点(收到即结束本轮)。
func reclaimPlainSSE(t *testing.T, text string) string {
	return sseServer(t, func(w http.ResponseWriter, flush func(), _ <-chan struct{}) {
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", text)
		flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flush()
	})
}

// reclaimBloatedHistory 造"1 个 user 轮 + 一堆大工具输出" —— reclaim 的目标形态(#201)。
func reclaimBloatedHistory(rounds, perTool int) []ChatMessage {
	h := []ChatMessage{{Role: "user", Content: "跑单元测试并生成覆盖率报告"}}
	body := strings.Repeat("覆盖率数据 line 42 hit; ", perTool)
	for i := range rounds {
		id := fmt.Sprintf("c%d", i)
		h = append(h,
			asstCall(id, "Bash", fmt.Sprintf(`{"command":"go test ./pkg%d/"}`, i)),
			toolMsg(id, "Bash", body),
		)
	}
	return h
}

// 回收落地后 agent 必须发 ContextReclaimedMsg(带条数、净回收 token、回收后估算),
// 否则 TUI 无从刷新状态栏 / 留痕迹 —— reclaim 又会回到"默默干活、零反馈"(issue #201)。
func TestStartStream_EmitsContextReclaimed(t *testing.T) {
	const ctxWin = 20000
	hist := reclaimBloatedHistory(30, 120) // 够大,过 70% 触发线,且 reclaim 有得回收

	// 本地先跑一遍拿期望值(agent 内部走同一个函数)。
	local := append([]ChatMessage(nil), hist...)
	wantCount, wantFreed := reclaimToolOutputs(local, ctxWin)
	if wantCount == 0 {
		t.Fatal("测试数据没能触发回收,调大 reclaimBloatedHistory")
	}

	url := reclaimPlainSSE(t, "好的")
	cfg := ModelConfig{Flash: ModelEntry{
		BaseURL: url, Model: "m", APIKey: "k",
		ContextWindow: ctxWin, MaxTokens: 256,
	}}
	_, ch := StartStream(context.Background(), cfg, hist,
		AgentMode_Auto, t.TempDir(), "", "", "flash", WorkingModeDefault, "", "", false, 0)

	var got []ContextReclaimedMsg
	for msg := range ch {
		if m, ok := msg.(ContextReclaimedMsg); ok {
			got = append(got, m)
		}
		if e, ok := msg.(StreamErrMsg); ok {
			t.Fatalf("假端点不该报错: %v", e.Err)
		}
	}

	if len(got) == 0 {
		t.Fatal("历史已过阈值,回收应发生并发 ContextReclaimedMsg")
	}
	if got[0].Count != wantCount || got[0].Tokens != wantFreed {
		t.Errorf("回收消息 = {Count:%d Tokens:%d}, want {Count:%d Tokens:%d}",
			got[0].Count, got[0].Tokens, wantCount, wantFreed)
	}
	// 回收后估算应为正、且明显小于回收前的整段历史(说明真减了负)。
	if got[0].EstPromptTokens <= 0 {
		t.Errorf("回收后估算应为正, got %d", got[0].EstPromptTokens)
	}
	if before := EstimateHistoryTokens(hist); got[0].EstPromptTokens >= before {
		t.Errorf("回收后估算 %d 应小于回收前历史 %d", got[0].EstPromptTokens, before)
	}
}

// 上下文没到阈值:不回收,也不该发 ContextReclaimedMsg。
func TestStartStream_NoReclaimWhenSmall(t *testing.T) {
	url := reclaimPlainSSE(t, "好的")
	cfg := ModelConfig{Flash: ModelEntry{
		BaseURL: url, Model: "m", APIKey: "k",
		ContextWindow: 200000, MaxTokens: 256,
	}}
	_, ch := StartStream(context.Background(), cfg,
		[]ChatMessage{{Role: "user", Content: "你好"}},
		AgentMode_Auto, t.TempDir(), "", "", "flash", WorkingModeDefault, "", "", false, 0)

	for msg := range ch {
		if m, ok := msg.(ContextReclaimedMsg); ok {
			t.Errorf("小上下文不该触发回收: %+v", m)
		}
	}
}
