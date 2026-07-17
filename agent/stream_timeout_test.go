package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// sseServer 起一个假的 /chat/completions 流式端点,handler 决定怎么吐。
func sseServer(t *testing.T, handler func(w http.ResponseWriter, flush func(), done <-chan struct{})) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("ResponseWriter 不支持 Flush")
			return
		}
		f.Flush() // 先把响应头吐出去,模拟真实流式端点
		// done 在客户端断开时关闭,让"挂住不吐"的 handler 能立刻收摊,
		// 否则 httptest.Close 要一直等它 sleep 完,测试白白变慢。
		handler(w, f.Flush, r.Context().Done())
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// hang 模拟"响应头回了,然后再也不吐数据"的端点,直到客户端断开。
func hang(_ http.ResponseWriter, _ func(), done <-chan struct{}) {
	<-done
}

func drain(ch chan tea.Msg) {
	go func() {
		for range ch {
		}
	}()
}

// 把空闲阈值调小,跑真实的超时路径。
func withIdleTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := streamIdleTimeout
	streamIdleTimeout = d
	t.Cleanup(func() { streamIdleTimeout = old })
}

func callStream(ctx context.Context, baseURL string, ch chan tea.Msg) error {
	_, _, _, _, _, err := streamOnce(
		ctx, "k", baseURL, "m",
		[]ChatMessage{{Role: "user", Content: "hi"}},
		100, nil, "", "", ch,
	)
	return err
}

// issue #181 的核心场景:端点回了响应头,然后再也不吐数据。
// 修复前 scanner.Scan() 会永远阻塞(spinner 转到天荒地老);
// 修复后应在空闲阈值后判定卡死,重试 maxIdleRetries 次仍不行才报 errStreamIdle。
func TestStreamIdleTimeoutFires(t *testing.T) {
	withIdleTimeout(t, 200*time.Millisecond)

	var hits int32
	url := sseServer(t, func(w http.ResponseWriter, flush func(), done <-chan struct{}) {
		atomic.AddInt32(&hits, 1)
		<-done // 头已发,之后一个字节都不给
	})

	ch := make(chan tea.Msg, 64)
	var notices int32
	go func() {
		for msg := range ch {
			if _, ok := msg.(RetryNoticeMsg); ok {
				atomic.AddInt32(&notices, 1)
			}
		}
	}()

	err := callStream(context.Background(), url, ch)

	t.Logf("err=%v  端点被请求 %d 次,发出 %d 条重试提示", err, hits, notices)
	if err == nil {
		t.Fatal("❌ 端点不吐数据却没报错 —— 说明仍会无限等待")
	}
	if !errors.Is(err, errStreamIdle) {
		t.Errorf("❌ 应报 errStreamIdle,实际 %v", err)
	}
	// 关键:不能是 context.Canceled,否则 StartStream 会静默 return、把卡死悄悄吞掉。
	if errors.Is(err, context.Canceled) {
		t.Errorf("❌ 空闲超时被当成用户取消(context.Canceled)→ 上层会静默吞掉,用户仍然看不到任何提示")
	}
	// 首次 + maxIdleRetries 次重试
	if want := int32(1 + maxIdleRetries); hits != want {
		t.Errorf("❌ 应尝试 %d 次(首次+%d 次重试),实际 %d 次", want, maxIdleRetries, hits)
	}
	if notices != int32(maxIdleRetries) {
		t.Errorf("❌ 每次重试都该发 RetryNoticeMsg 让状态行有提示,期望 %d 条,实际 %d 条",
			maxIdleRetries, notices)
	}
}

// 空闲超时后重试能自愈:第一次挂住,第二次正常吐 —— 用户不该看到任何错误。
func TestStreamIdleRetryRecovers(t *testing.T) {
	withIdleTimeout(t, 200*time.Millisecond)

	var hits int32
	url := sseServer(t, func(w http.ResponseWriter, flush func(), done <-chan struct{}) {
		if atomic.AddInt32(&hits, 1) == 1 {
			<-done // 第一次挂住
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"recovered\"}}]}\n\n")
		flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flush()
	})

	ch := make(chan tea.Msg, 64)
	drain(ch)

	content, _, _, _, _, err := streamOnce(
		context.Background(), "k", url, "m",
		[]ChatMessage{{Role: "user", Content: "hi"}},
		100, nil, "", "", ch,
	)
	t.Logf("content=%q err=%v 端点被请求 %d 次", content, err, hits)
	if err != nil {
		t.Errorf("❌ 第二次尝试已正常返回,不该报错: %v", err)
	}
	if content != "recovered" {
		t.Errorf("❌ 重试后应拿到完整内容,实际 %q", content)
	}
	if hits != 2 {
		t.Errorf("❌ 应恰好请求 2 次(首次挂住 + 重试成功),实际 %d 次", hits)
	}
}

// 已经吐过字之后再卡死,绝不能重试 —— 重试会把已显示在对话区的内容再吐一遍。
// 此时应带着已吐内容直接报错收尾。
func TestStreamIdleNoRetryAfterOutput(t *testing.T) {
	withIdleTimeout(t, 200*time.Millisecond)

	var hits int32
	url := sseServer(t, func(w http.ResponseWriter, flush func(), done <-chan struct{}) {
		atomic.AddInt32(&hits, 1)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"半句话\"}}]}\n\n")
		flush()
		<-done // 吐了一半就卡死
	})

	ch := make(chan tea.Msg, 64)
	drain(ch)

	content, _, _, _, _, err := streamOnce(
		context.Background(), "k", url, "m",
		[]ChatMessage{{Role: "user", Content: "hi"}},
		100, nil, "", "", ch,
	)
	t.Logf("content=%q err=%v 端点被请求 %d 次", content, err, hits)
	if !errors.Is(err, errStreamIdle) {
		t.Errorf("❌ 应报 errStreamIdle,实际 %v", err)
	}
	if hits != 1 {
		t.Errorf("❌ 已吐字后不该重试(会重复吐字),期望请求 1 次,实际 %d 次", hits)
	}
	if content != "半句话" {
		t.Errorf("❌ 已吐的内容应随错误一起返回给上层,实际 %q", content)
	}
}

// 防误杀:模型纯思考期端点只发 keep-alive 注释行(OpenRouter 的 ": OPENROUTER PROCESSING")。
// 这些行没有负载,但证明连接活着 —— 必须用来续命,否则推理模型会被误判卡死。
func TestStreamKeepAliveCommentsResetIdle(t *testing.T) {
	withIdleTimeout(t, 300*time.Millisecond)

	url := sseServer(t, func(w http.ResponseWriter, flush func(), done <-chan struct{}) {
		// 只发注释行,持续时间远超空闲阈值(300ms):1s。
		for i := 0; i < 10; i++ {
			fmt.Fprint(w, ": OPENROUTER PROCESSING\n\n")
			flush()
			time.Sleep(100 * time.Millisecond)
		}
		// 思考结束,真正吐内容。
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flush()
	})

	ch := make(chan tea.Msg, 64)
	drain(ch)

	err := callStream(context.Background(), url, ch)
	t.Logf("err=%v", err)
	if errors.Is(err, errStreamIdle) {
		t.Errorf("❌ keep-alive 注释行没能续命 → 纯思考期被误判卡死。"+
			"续命的 idle.Reset 必须在 `data: ` 过滤之前。err=%v", err)
	}
	if err != nil {
		t.Errorf("❌ 意外错误: %v", err)
	}
}

// 正常吐 token 期间不应误触发:每个 chunk 都续命。
func TestStreamSlowButAliveNotKilled(t *testing.T) {
	withIdleTimeout(t, 300*time.Millisecond)

	url := sseServer(t, func(w http.ResponseWriter, flush func(), done <-chan struct{}) {
		// 每 100ms 一个 chunk,总时长 1s —— 远超空闲阈值,但从不空闲。
		for i := 0; i < 10; i++ {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"t%d\"}}]}\n\n", i)
			flush()
			time.Sleep(100 * time.Millisecond)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flush()
	})

	ch := make(chan tea.Msg, 64)
	drain(ch)

	content, _, _, _, _, err := streamOnce(
		context.Background(), "k", url, "m",
		[]ChatMessage{{Role: "user", Content: "hi"}},
		100, nil, "", "", ch,
	)
	t.Logf("content=%q err=%v", content, err)
	if err != nil {
		t.Errorf("❌ 持续吐 token 的慢流被误杀: %v", err)
	}
	if content != "t0t1t2t3t4t5t6t7t8t9" {
		t.Errorf("❌ 内容不完整: %q", content)
	}
}

// 用户按 ESC 取消:必须仍报 context.Canceled,不能被误报成空闲超时
// (否则上层会弹一个"端点无响应"的错误,而其实是用户自己中断的)。
func TestStreamUserCancelNotReportedAsIdle(t *testing.T) {
	withIdleTimeout(t, 10*time.Second) // 调大,确保不是看门狗触发的

	url := sseServer(t, hang)

	ch := make(chan tea.Msg, 64)
	drain(ch)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel() // 模拟用户 ESC
	}()

	err := callStream(ctx, url, ch)
	t.Logf("err=%v", err)
	if errors.Is(err, errStreamIdle) {
		t.Errorf("❌ 用户主动取消被误报成空闲超时: %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("❌ 用户取消应报 context.Canceled(上层据此静默收尾),实际 %v", err)
	}
}
