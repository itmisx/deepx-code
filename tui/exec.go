package tui

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"deepx/agent"
	"deepx/skill"
	"deepx/tools"
)

// RunExec 执行一次性非交互任务(`deepx exec "<任务>"`):把 prompt 发给 agent,**只把模型最终
// 结果打到 stdout**,跑完即退出,不启动 TUI。供脚本 / 管道 / CI / cron 使用。
//
//   - 固定 auto 模式(全工具,不弹人工审批——非交互下无法确认,这是有意为之)。
//   - 工具调用进度 / 思考过程一律不显示,保证 stdout 干净(`> file` 拿到的就是结果)。
//   - 出错通过返回值上报,调用方据此给非零退出码。
func RunExec(cfg agent.ModelConfig, prompt string) error {
	wd, _ := os.Getwd()
	home, _ := os.UserHomeDir()

	// skill 发现 + codegraph 绑定,与 TUI 启动时保持一致,保证 exec 下工具能力对齐。
	loader := skill.New(
		[]string{filepath.Join(wd, ".deepx", "skills")},
		[]string{
			filepath.Join(home, ".agents", "skills"),
			filepath.Join(home, ".claude", "skills"),
			filepath.Join(home, ".deepx", "skills"),
		},
	)
	tools.SetSkillLoader(loader)
	tools.SetCodeGraphRoot(wd)
	// 单次 Write 的 content 上限同样按窗口注入(见 agent.WriteContentLimitFor),
	// 否则 exec 下会一直吃 tools 的保守兜底值,与 TUI 行为不一致。
	tools.SetWriteContentLimit(agent.WriteContentLimitFor(cfg))
	skillCatalog := buildSkillCatalog(loader)

	// Ctrl+C → 取消 ctx;StartStream 在轮次间 / HTTP 层检测到取消会平滑收尾并 close channel。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		cancel()
	}()

	history := []agent.ChatMessage{{Role: "user", Content: prompt}}

	// exec 的"启动"点:读一次 AGENTS.md 冻结进缓存,让偏好对一次性执行也生效。
	agent.RefreshPreferences(wd)

	// 固定 auto 模式;forceRole 传 "auto" → 走本地语义路由(零 token 决定起手模型)。
	// summary 空(一次性,无压缩)。
	_, ch := agent.StartStream(ctx, cfg, history, agent.AgentMode_Auto, wd, skillCatalog, "", "auto", agent.WorkingModeDefault)

	var streamErr error
	for msg := range ch {
		switch m := msg.(type) {
		case agent.TokenMsg:
			fmt.Print(string(m)) // 只输出模型正式回复 → stdout;工具调用 / 思考一律不打印
		case agent.StreamErrMsg:
			streamErr = m.Err
		}
	}
	fmt.Println() // 给 stdout 末尾补个换行,终端更整洁(重定向到文件也无害)
	return streamErr
}
