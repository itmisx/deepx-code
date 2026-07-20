//go:build !windows

package tools

import (
	"strings"
	"syscall"
	"testing"
	"time"
)

// processAlive 用 signal 0 探测进程是否还在(unix)。
func processAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// 后台进程全生命周期:启动→读到输出且状态为运行中→KillBash 后进程真的死了。
func TestBackgroundLifecycle(t *testing.T) {
	// 一个常驻进程:每 50ms 打一行,永不退出。
	res := RunCommand(map[string]any{
		"command":           "i=0; while true; do echo line$i; i=$((i+1)); sleep 0.05; done",
		"run_in_background": true,
	})
	if !res.Success {
		t.Fatalf("后台启动失败: %s", res.Output)
	}
	id := extractID(t, res.Output)

	// 给它一点时间产出输出
	time.Sleep(200 * time.Millisecond)

	out := BashOutput(map[string]any{"id": id})
	if !out.Success || !strings.Contains(out.Output, "line0") {
		t.Fatalf("BashOutput 应读到 line0 且运行中,got: %q", out.Output)
	}
	if !strings.Contains(out.Output, "[运行中]") {
		t.Fatalf("状态应为运行中,got: %q", out.Output)
	}

	// 增量语义:再读一次应是上次之后的新输出,不重复 line0
	time.Sleep(150 * time.Millisecond)
	out2 := BashOutput(map[string]any{"id": id})
	if strings.Contains(out2.Output, "line0") {
		t.Fatalf("第二次读不应再含 line0(增量语义),got: %q", out2.Output)
	}

	// 记下 pid,kill 后确认进程组真的没了
	bgMu.Lock()
	p := bgProcs[id]
	bgMu.Unlock()
	if p == nil {
		t.Fatal("注册表里找不到该后台进程")
	}
	pid := p.cmd.Process.Pid

	kill := KillBash(map[string]any{"id": id})
	if !kill.Success {
		t.Fatalf("KillBash 失败: %s", kill.Output)
	}
	// 已从注册表移除
	if r := BashOutput(map[string]any{"id": id}); r.Success {
		t.Fatalf("kill 后该 id 应已不存在,got success: %q", r.Output)
	}
	// 进程确实死了(signal 0 探测)
	time.Sleep(100 * time.Millisecond)
	if processAlive(pid) {
		t.Fatalf("KillBash 后主进程 %d 仍存活", pid)
	}
}

// run_in_background:true 走后台启动并立即返回句柄,不阻塞。
func TestRunCommandBackgroundStart(t *testing.T) {
	res := RunCommand(map[string]any{
		"command":           "echo hi",
		"run_in_background": true,
	})
	if !res.Success {
		t.Fatalf("run_in_background 启动不应被护栏拦下: %q", res.Output)
	}
	KillBash(map[string]any{"id": extractID(t, res.Output)}) // 收尾,别留在注册表
}

// 杀一个已自行退出的后台进程应是干净的成功,而非报 "process already finished"(F2)。
func TestKillBashAfterNaturalExit(t *testing.T) {
	res := RunCommand(map[string]any{
		"command":           "echo hi", // 立即退出
		"run_in_background": true,
	})
	if !res.Success {
		t.Fatalf("启动失败: %s", res.Output)
	}
	id := extractID(t, res.Output)

	// 等它自行退出(done=true)
	deadline := time.Now().Add(2 * time.Second)
	for {
		bgMu.Lock()
		p := bgProcs[id]
		bgMu.Unlock()
		if p == nil {
			t.Fatal("KillBash 前进程不该被移除")
		}
		p.mu.Lock()
		done := p.done
		p.mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("echo 进程迟迟未退出")
		}
		time.Sleep(10 * time.Millisecond)
	}

	kill := KillBash(map[string]any{"id": id})
	if !kill.Success {
		t.Fatalf("杀已退出的后台进程应成功,got: %q", kill.Output)
	}
	if !strings.Contains(kill.Output, "已自行退出") {
		t.Fatalf("应说明进程已自行退出,got: %q", kill.Output)
	}
}

func TestBackgroundUnknownID(t *testing.T) {
	if r := BashOutput(map[string]any{"id": "bash_999999"}); r.Success {
		t.Fatal("未知 id 应返回失败")
	}
	if r := KillBash(map[string]any{"id": ""}); r.Success {
		t.Fatal("空 id 应返回失败")
	}
}
