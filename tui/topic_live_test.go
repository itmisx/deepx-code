//go:build topiclive

// 真实端到端验证(手动跑,消耗真实 API 额度):
//
//	go test ./tui/ -tags topiclive -run TestLiveTopic -v
//
// 用 ~/.deepx/model.yaml 的真实 provider,发**真实的** system prompt(agent.BuildSystemPrompt,
// 就是 deepx 每轮发的那份),按真实的 SSE 分块喂进 topicFilter。
//
// 对照两种放置方式,看模型到底听哪一种:
//
//	A 只写在 system prompt 里(埋在中间,离输出远)
//	B system prompt + 每条 user 消息尾部再提醒一次(同 renderWorkingMode 的做法)
package tui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"deepx/agent"
	"deepx/config"
)

type liveMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// topicUserSuffix 是候选方案 B:追加到 user 消息尾部的提醒,措辞对齐 workingModePrompt。
const topicUserSuffix = `[会话主题] 本轮回复的最后一行必须输出:<topic shift="no">一句话概括当前会话在做的事</topic>。` +
	`与上一轮 <topic> 是同一件事(含子问题、追问、返工)→ shift="no";用户明确转去做另一件不相干的事 → shift="yes" 并写新主题。` +
	`不要解释这一行,也不要因为它改变你的正常回答。`

// liveStream 发一次真实的流式请求,把每个 chunk 喂给 f,返回可见文本 / 原文 / finish_reason。
func liveStream(t *testing.T, e config.ModelEntry, msgs []liveMsg, f *topicFilter) (visible, raw, finish string) {
	t.Helper()
	maxTok := e.MaxTokens
	if maxTok <= 0 {
		maxTok = 8192
	}
	body, _ := json.Marshal(map[string]any{
		"model": e.Model, "messages": msgs, "stream": true, "max_tokens": maxTok,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST",
		strings.TrimRight(e.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.APIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("HTTP %d", resp.StatusCode)
	}

	var vis, rawB strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimPrefix(sc.Text(), "data: ")
		if line == "" || line == "[DONE]" || !strings.HasPrefix(line, "{") {
			continue
		}
		var ev struct {
			Choices []struct {
				Delta        struct{ Content string } `json:"delta"`
				FinishReason string                   `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil || len(ev.Choices) == 0 {
			continue
		}
		if r := ev.Choices[0].FinishReason; r != "" {
			finish = r // stop = 说完了;length = 撞 max_tokens 被截断
		}
		if c := ev.Choices[0].Delta.Content; c != "" {
			rawB.WriteString(c)
			vis.WriteString(f.feed(c)) // ← 真实 chunk 边界喂进过滤器
		}
	}
	vis.WriteString(f.flush())
	return vis.String(), rawB.String(), finish
}

func TestLiveTopic(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("读 ~/.deepx/model.yaml 失败: %v", err)
	}
	if cfg.Flash.APIKey == "" || cfg.Flash.BaseURL == "" {
		t.Skip("未配置 api_key/base_url")
	}
	e := cfg.Flash
	t.Logf("provider=%s model=%s", e.BaseURL, e.Model)

	// 三轮:同话题追问 ×2,然后硬切一个完全不相干的话题。
	// 刻意不选需要工具的问题 —— 这个测试没带 tools 定义,问编码任务模型会去吐 tool_call 就没正文了。
	turns := []struct {
		text      string
		wantShift bool
	}{
		{"golang和php的区别", false},
		{"那并发模型上具体差在哪", false},
		{"算了不聊这个了,给我讲讲红烧肉怎么做", true},
	}

	for _, mode := range []struct {
		name   string
		suffix bool
	}{
		{"A 只写在 system prompt", false},
		{"B system + 每条 user 尾部", true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			msgs := []liveMsg{{Role: "system", Content: agent.BuildSystemPrompt("/tmp/ws", "", "", false)}}
			hit, shiftOK := 0, 0
			for i, turn := range turns {
				text := turn.text
				if mode.suffix {
					text += "\n\n" + topicUserSuffix
				}
				msgs = append(msgs, liveMsg{Role: "user", Content: text})

				var f topicFilter
				vis, raw, fin := liveStream(t, e, msgs, &f)
				msgs = append(msgs, liveMsg{Role: "assistant", Content: raw}) // 历史存原文

				status := "✅"
				if f.topic == "" {
					status = "❌ 没吐标签"
				} else {
					hit++
					if f.shift == turn.wantShift {
						shiftOK++
					} else {
						status = "⚠ shift 判错"
					}
				}
				t.Logf("  轮%d %s finish=%s %d字 主题=%q shift=%v(期望 %v)",
					i+1, status, fin, len([]rune(raw)), f.topic, f.shift, turn.wantShift)
				if strings.Contains(vis, "<topic") {
					t.Errorf("  轮%d 标签漏进显示文本", i+1)
				}
			}
			t.Logf("  → 出标签 %d/%d,shift 判对 %d/%d", hit, len(turns), shiftOK, len(turns))
			if hit < len(turns) {
				t.Errorf("方案「%s」没做到每轮都出标签(%d/%d)", mode.name, hit, len(turns))
			}
			if shiftOK < len(turns) {
				t.Errorf("方案「%s」shift 判断不全对(%d/%d)", mode.name, shiftOK, len(turns))
			}
		})
	}
}
