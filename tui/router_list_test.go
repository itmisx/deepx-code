package tui

import (
	"fmt"
	"strings"
	"testing"

	"deepx/agent"
	"deepx/agent/router"

	"github.com/charmbracelet/x/ansi"
)

// renderRouterList 跑一次 /router-list-<key>,把落进 chatLog 的原文取出来。
func renderRouterList(t *testing.T, key string) string {
	t.Helper()
	m := model{chatContent: newChatLog(1 << 20)}
	m.handleRouterListCommand(key)
	return chatRaw(m)
}

func chatRaw(m model) string {
	var sb strings.Builder
	for _, seg := range m.chatContent.segments {
		sb.WriteString(seg.raw)
		sb.WriteString("\n")
	}
	return sb.String()
}

// 两组分开查看:各自只列自己那组,不能把另一组混进来。
// 混着列的话,用户看到的序号跟 /router-delete-<组> 的入参对不上,会删错。
func TestRouterList_ShowsOnlyItsOwnGroup(t *testing.T) {
	pro, flash := router.ActiveProPatterns(), router.ActiveFlashPatterns()

	outPro := renderRouterList(t, "pro")
	for _, p := range pro {
		if !strings.Contains(outPro, p) {
			t.Errorf("/router-list-pro 缺了本组的 %q", p)
			break
		}
	}
	for _, p := range flash {
		if strings.Contains(outPro, p) {
			t.Errorf("/router-list-pro 混进了 flash 组的 %q —— 序号会与 /router-delete-pro 对不上", p)
			break
		}
	}

	outFlash := renderRouterList(t, "flash")
	for _, p := range flash {
		if !strings.Contains(outFlash, p) {
			t.Errorf("/router-list-flash 缺了本组的 %q", p)
			break
		}
	}
	for _, p := range pro {
		if strings.Contains(outFlash, p) {
			t.Errorf("/router-list-flash 混进了 pro 组的 %q", p)
			break
		}
	}
}

// 每组各自从 1 编号。
func TestRouterList_NumbersFromOne(t *testing.T) {
	for _, tc := range []struct {
		key string
		ps  []string
	}{
		{"pro", router.ActiveProPatterns()},
		{"flash", router.ActiveFlashPatterns()},
	} {
		out := renderRouterList(t, tc.key)
		for i, p := range tc.ps {
			if want := fmt.Sprintf("%2d. %s", i+1, p); !strings.Contains(out, want) {
				t.Errorf("%s 组第 %d 条编号不对,期望行 %q", tc.key, i+1, want)
				break
			}
		}
	}
}

// 渲染后每条样板句必须独占一行。
//
// 断言必须落在**渲染结果**上,不是源文本:CommonMark 只允许从 1 开始的有序列表打断
// 上一个段落,曾经因为标题和列表之间少一个空行,整组被 glamour 糊成一坨,
// 而只查源文本的测试全绿。
func TestRouterList_RendersOnePatternPerLine(t *testing.T) {
	for _, key := range []string{"pro", "flash"} {
		m := model{chatContent: newChatLog(1 << 20)}
		m.handleRouterListCommand(key)
		out := ansi.Strip(m.renderMarkdown(chatRaw(m), 200))
		if out == "" {
			t.Fatalf("%s 组渲染结果为空", key)
		}
		all := append(router.ActiveProPatterns(), router.ActiveFlashPatterns()...)
		// 判据:任意一行都不该同时出现两条样板句。对折行免疫,而"糊成一坨"必被抓住。
		for line := range strings.SplitSeq(out, "\n") {
			var hit []string
			for _, p := range all {
				if strings.Contains(line, p) {
					hit = append(hit, p)
				}
			}
			if len(hit) > 1 {
				t.Fatalf("%s 组渲染后 %d 条挤在同一行:\n  行: %s\n  命中: %q",
					key, len(hit), strings.TrimSpace(line), hit[:min(3, len(hit))])
			}
		}
	}
}

// 删除回执要回显组名 + 序号 + 原句。
// 两组序号都从 1 排,用户敲错组时只有把删掉的原文摆出来才看得出不对。
func TestRouterDelete_ReceiptIdentifiesGroupAndItem(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Cleanup(func() { router.SetUserPatterns(nil, nil) })

	pro := router.ActiveProPatterns()
	m := model{chatContent: newChatLog(1 << 20)}
	m.handleRouterDeleteCommand("pro", "/router-delete-pro 3")

	got := chatRaw(m)
	for _, want := range []string{"升级 pro", "第 3 条", pro[2]} {
		if !strings.Contains(got, want) {
			t.Errorf("删除回执缺少 %q,用户无法判断删对没有。实际: %q", want, got)
		}
	}
	// 只该动 pro 组
	if len(router.ActiveFlashPatterns()) != len(router.DefaultFlashPatterns()) {
		t.Error("删 pro 组却动到了 flash 组")
	}
}

// 敲错组时要指路,而不是回一句"不在表中"让人以为自己记错了内容。
func TestRouterDelete_PointsToTheOtherGroup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Cleanup(func() { router.SetUserPatterns(nil, nil) })

	flashItem := router.ActiveFlashPatterns()[0]
	m := model{chatContent: newChatLog(1 << 20)}
	m.handleRouterDeleteCommand("pro", "/router-delete-pro "+flashItem)

	got := chatRaw(m)
	if !strings.Contains(got, "维持 flash") || !strings.Contains(got, "/router-delete-flash") {
		t.Errorf("应指出这条在另一组并给出正确命令,实际: %q", got)
	}
	if len(router.ActiveProPatterns()) != len(router.DefaultProPatterns()) {
		t.Error("敲错组不该改动任何一组")
	}
}

// 同一句话不能同时进两组:两边相似度都是 1.0,判定会变成掷硬币。
func TestRouterAdd_RejectsCrossGroupDuplicate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Cleanup(func() { router.SetUserPatterns(nil, nil) })

	flashItem := router.ActiveFlashPatterns()[0]
	m := model{chatContent: newChatLog(1 << 20)}
	m.handleRouterAddCommand("pro", "/router-add-pro "+flashItem)

	got := chatRaw(m)
	if !strings.Contains(got, "不能同时属于两组") {
		t.Errorf("应拒绝跨组重复,实际: %q", got)
	}
	if len(router.ActiveProPatterns()) != len(router.DefaultProPatterns()) {
		t.Error("被拒绝的添加不该落盘")
	}
}

// 路由命令的输出里不能出现 emoji。
//
// 终端对 emoji 宽度的判断与 ansi.StringWidth 常常不一致(✅ U+2705、⚠️ U+26A0+FE0F
// 都是重灾区,后者 East Asian Width 本身就是 Ambiguous):库按 2 格补白、终端只画 1 格,
// 那一行就短一格,**右栏分割线在这一行往左塌**。formatToolCallLine 早就踩过这个坑。
//
// 这里守的是"别再往回加"——受影响的是整屏布局,而不只是这一行的观感。
func TestRouterOutput_NoEmoji(t *testing.T) {
	// 已知会与终端打架的码点。不做通用 emoji 检测:那需要完整 Unicode 表,
	// 而实际踩过坑的就是这一小撮,列清单反而更好维护、也更好解释。
	risky := map[rune]string{
		'✅': "✅ U+2705",
		'⚠': "⚠ U+26A0",
		'⏳': "⏳ U+23F3",
		'ℹ': "ℹ U+2139",
		'❌': "❌ U+274C",
		'❗': "❗ U+2757",
		'️': "变体选择符 U+FE0F",
	}

	var outs []string
	for _, key := range []string{"pro", "flash"} {
		outs = append(outs, renderRouterList(t, key))

		m := model{chatContent: newChatLog(1 << 20)}
		m.handleRouterAddCommand(key, "/router-add-"+key) // 用法提示
		m.handleRouterAddCommand(key, "/router-add-"+key+" 短")
		m.handleRouterDeleteCommand(key, "/router-delete-"+key)         // 用法提示
		m.handleRouterDeleteCommand(key, "/router-delete-"+key+" 9999") // 越界
		outs = append(outs, chatRaw(m))
	}
	// 状态行有两个分支,都要覆盖 —— 只测默认那个的话,"就绪"分支里
	// 加回一个 emoji 也不会被发现(这里就漏过一次)。
	outs = append(outs, routerStatusLine()) // 未就绪
	agent.SetSemanticAssist(func(string) bool { return false })
	outs = append(outs, routerStatusLine()) // 已就绪
	agent.SetSemanticAssist(nil)

	for _, out := range outs {
		for _, r := range out {
			if name, bad := risky[r]; bad {
				t.Errorf("路由输出里出现 %s —— 会把右栏分割线推偏,改用纯文字 / markdown 加粗。\n片段: %.80s",
					name, out)
				break
			}
		}
	}
}
