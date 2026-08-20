package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"deepx/agent"
	"deepx/agent/router"
	"deepx/config"

	tea "charm.land/bubbletea/v2"
)

// === 路由样板句管理 ===
//
// 入口路由靠"消息离哪组样板句更近"决定起手用 flash 还是 pro,两组对等:
//
//	pro 组    像这些 → 起手用 pro   /router-add-pro   /router-delete-pro   /router-list-pro
//	flash 组  像这些 → 维持 flash   /router-add-flash /router-delete-flash /router-list-flash
//
// 分开维护是必要的:两组修的是不同方向的错。漏判(该 pro 却走了 flash)往 pro 组加句子,
// 误判(琐事被抬成 pro)往 flash 组加句子 —— 混在一个命令里,用户得先想明白
// "我这条到底算正例还是反例",而那正是他最容易搞反的地方。
//
// 不做"降级表"(pro→flash 的运行时切换):切模型会让上下文缓存整段失效
// (见 agent/llm.go 的 Model 字段注释),长会话下比继续用 pro 还贵。
// 两组样板句只影响**起手**选哪个模型,不在会话中途改主意。

// routerGroup 把两组的差异收拢成数据,六个命令共用一套逻辑 ——
// 否则同样的增删查要各写两遍,改一处漏一处。
type routerGroup struct {
	key     string // "pro" / "flash"
	title   string // 列表标题
	effect  string // 命中后的效果,用在回执里
	active  func() []string
	builtin func() []string
}

var routerGroups = map[string]routerGroup{
	"pro": {
		key: "pro", title: "升级 pro", effect: "起手用 pro",
		active: router.ActiveProPatterns, builtin: router.DefaultProPatterns,
	},
	"flash": {
		key: "flash", title: "维持 flash", effect: "维持 flash",
		active: router.ActiveFlashPatterns, builtin: router.DefaultFlashPatterns,
	},
}

// currentPatterns / builtinPatterns 取两组的当前值与内置值,交给 config 落盘。
func currentPatterns() config.RouterPatterns {
	return config.RouterPatterns{Pro: router.ActiveProPatterns(), Flash: router.ActiveFlashPatterns()}
}

func builtinPatterns() config.RouterPatterns {
	return config.RouterPatterns{Pro: router.DefaultProPatterns(), Flash: router.DefaultFlashPatterns()}
}

// loadRouterPatterns 启动时准备好 ~/.deepx/router.yaml 并注入 router。
//
// 没有文件就先把两组内置表整份写出来 —— 用户手上得有一份能直接编辑的起点,
// 而不是先摸清 YAML 结构、或先跑一次 /router-add-pro 才把文件催生出来。
// 某一组没改过的话,内置表升级时那一组会自动同步(靠指纹,见 config 包注释)。
//
// 之后的每次路由都会按需重载这个文件(见 router.reloadUserPatterns),
// 所以用编辑器改完不需要重启。
//
// 解析失败不静默:用户改坏了 yaml 却以为生效了,比直接报错更难查。
func (m *model) loadRouterPatterns() {
	res, err := config.EnsureRouterFile(builtinPatterns())
	if err != nil {
		m.appendChat("System", fmt.Sprintf("**router.yaml 准备失败**,本次用内置默认:%v", err))
		return
	}
	// 只在真覆盖了用户文件时说一声。首次生成不吭声 —— 那是每个新用户都会经历的
	// 静默初始化,报一句只会变成噪音。
	var synced []string
	if res.RefreshedPro {
		synced = append(synced, "升级 pro")
	}
	if res.RefreshedFlash {
		synced = append(synced, "维持 flash")
	}
	if len(synced) > 0 {
		m.appendChat("System", fmt.Sprintf("路由样板句已同步到新版内置默认:%s(检测到你未修改过这些组)",
			strings.Join(synced, "、")))
	}

	cur, err := config.LoadRouterPatterns()
	if err != nil {
		m.appendChat("System", fmt.Sprintf("**router.yaml 读取失败**,本次用内置默认:%v", err))
		return
	}
	router.SetUserPatterns(cur.Pro, cur.Flash)
}

// startSemanticRouter 在后台补齐语义模型并在就绪后接上。**不阻塞启动**。
//
// 模型就绪前(以及下载失败时)不做入口路由,起手一律 flash —— 兜底是模型自己手里的
// SwitchModel 工具,执行中发现任务比预期复杂可以自行升到 pro(见 agent.RouteEntry)。
func (m *model) startSemanticRouter() tea.Cmd {
	return func() tea.Msg {
		done := make(chan routerAssetMsg, 1)
		router.EnsureAssetsAsync(func(s router.Status, errMsg string) {
			if s == router.StatusReady {
				agent.SetSemanticAssist(router.LooksComplex)
			}
			done <- routerAssetMsg{status: s, err: errMsg}
		})
		return <-done
	}
}

// routerAssetMsg 是语义模型资产就绪 / 失败的通知。
type routerAssetMsg struct {
	status router.Status
	err    string
}

// routerArg 从 "/router-add-pro 把模块拆开" 这类原文里取出参数,保留大小写。
func routerArg(input string) string {
	s := strings.TrimSpace(input)
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return strings.TrimSpace(s[i:])
	}
	return ""
}

// applyRouterPatterns 落盘 + 重新注入,并给用户一条回执。
func (m *model) applyRouterPatterns(cur config.RouterPatterns, note string) {
	if err := config.SaveRouterPatterns(cur, builtinPatterns()); err != nil {
		m.appendChat("System", fmt.Sprintf("保存失败:%v", err))
		m.refreshViewport()
		return
	}
	router.SetUserPatterns(cur.Pro, cur.Flash)
	m.appendChat("System", note)
	m.refreshViewport()
}

// withGroup 把某一组换成 next,另一组原样带过 —— 落盘存的是全量两组。
func withGroup(key string, next []string) config.RouterPatterns {
	cur := currentPatterns()
	if key == "pro" {
		cur.Pro = next
	} else {
		cur.Flash = next
	}
	return cur
}

// routerStatusLine 一行说清入口路由此刻有没有在工作。
// 未就绪要讲明白"现在不做自动路由",否则用户只会觉得复杂任务老是用 flash 起手。
//
// **不用 emoji 做前缀。** 终端对 emoji 宽度的判断与 ansi.StringWidth 常常不一致
// (✅ U+2705、⚠️ U+26A0+FE0F 都是重灾区,后者 East Asian Width 本身就是 Ambiguous),
// 库按 2 格补白、终端只画 1 格,那一行就短一格,右栏分割线在这一行往左塌。
// 同样的坑 formatToolCallLine 早就踩过并写在注释里了(见 tool_display.go)。
func routerStatusLine() string {
	if agent.SemanticRoutingEnabled() {
		return "**语义模型已就绪**"
	}
	st, errMsg := router.CurrentStatus()
	line := fmt.Sprintf("**语义模型%s** —— 当前不做自动路由(起手一律 flash)", st)
	if errMsg != "" {
		line += "\n" + errMsg
	}
	return line
}

// handleRouterListCommand 处理 /router-list-pro、/router-list-flash。
//
// 列表 + 规则 + 状态,不写设计理由 —— 那些写在 agent/router 的注释里给改代码的人看,
// 不该占用户的屏幕。
func (m *model) handleRouterListCommand(key string) {
	g := routerGroups[key]
	ps := g.active()
	// 判"改过没有"要比内容,不能只看文件在不在 —— 文件是启动时自动生成的,
	// 光看存在性会把每个人都显示成已自定义。
	proCustom, flashCustom := config.RouterPatternsCustomized(builtinPatterns())
	src := "内置默认"
	if (key == "pro" && proCustom) || (key == "flash" && flashCustom) {
		src = "已自定义"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "**%s** · %s(%d 条)\n\n", g.title, src, len(ps))
	// **标题与列表之间必须留空行**:CommonMark 规定只有从 1 开始的有序列表才能"打断"
	// 上一个段落。这里恰好从 1 开始,但留空行更稳,也和另一组的写法统一。
	for i, p := range ps {
		fmt.Fprintf(&sb, "%2d. %s\n", i+1, p)
	}

	other := oppositeKey(key)

	// 两组显示**同一套**规则,不按本组视角改写。
	// 因为算法只有一条:比谁更近,flash 是兜底。从 flash 组视角写成
	// "与本组相似度 ≥ 阈值 → 维持 flash" 是错的 —— 匹配不上任何一组的消息也是 flash,
	// 并不需要先命中 flash 组。措辞和实现对不上,用户照着排错只会更迷糊。
	sb.WriteString("\n**规则**(与实际实现一致)\n\n")
	sb.WriteString("- 消息 > 500 字 → pro(这条不依赖模型,离线也生效)\n")
	fmt.Fprintf(&sb, "- 与「升级 pro」组的最高相似度 ≥ %.2f **且**高于与「维持 flash」组的 → pro\n",
		router.Threshold())
	sb.WriteString("- 否则 → flash(兜底,不需要命中「维持 flash」组)\n\n")
	fmt.Fprintf(&sb, "所以「维持 flash」组的作用是**把已过门槛、但其实是问概念 / 小修小补的消息拉回来**,"+
		"不是让消息命中它才留在 flash。\n")

	sb.WriteString("\n**命令**\n\n")
	fmt.Fprintf(&sb, "- `/router-add-%s <整句>` 添加\n", key)
	fmt.Fprintf(&sb, "- `/router-delete-%s <序号>` 删除\n", key)
	fmt.Fprintf(&sb, "- `/router-list-%s` 看另一组\n", other)
	sb.WriteString("- 也可直接编辑 `~/.deepx/router.yaml`,下一条消息即生效\n")

	sb.WriteString("\n")
	sb.WriteString(routerStatusLine())

	m.appendChat("System", sb.String())
	m.refreshViewport()
}

// handleRouterAddCommand 处理 /router-add-pro、/router-add-flash。
func (m *model) handleRouterAddCommand(key, input string) tea.Cmd {
	g := routerGroups[key]
	line := routerArg(input)
	if line == "" {
		m.appendChat("System", fmt.Sprintf("用法:`/router-add-%s <完整的任务陈述>`\n\n"+
			"例:`/router-add-%s %s`\n"+
			"按整句语义匹配,写成句子而非词条。", key, key, routerAddExample(key)))
		m.refreshViewport()
		return nil
	}

	ps := g.active() // 未配置过时以内置表为起点
	if slices.Contains(ps, line) {
		m.appendChat("System", "已在本组中")
		m.refreshViewport()
		return nil
	}
	// 同一句话同时出现在两组里,判定就成了掷硬币(两边相似度都是 1.0,谁先谁后看实现)。
	// 这种自相矛盾的配置必须当场拦掉,不能等它在路由里表现成"时好时坏"。
	if slices.Contains(routerGroups[oppositeKey(key)].active(), line) {
		m.appendChat("System", fmt.Sprintf("这条已经在「%s」组里了 —— 同一句话不能同时属于两组。\n"+
			"先 `/router-delete-%s` 删掉,再加到本组。",
			routerGroups[oppositeKey(key)].title, oppositeKey(key)))
		m.refreshViewport()
		return nil
	}
	ps = append(ps, line)

	note := fmt.Sprintf("已在「%s」组添加第 %d 条:%s", g.title, len(ps), line)
	// 短词条不会报错,但基本不会命中 —— 与其让用户等一场匹配不上的困惑,不如当场说清。
	if len([]rune(line)) < 8 {
		note += "\n注意:太短,按整句语义匹配大概率不会生效,建议写成完整的任务陈述"
	}
	if !agent.SemanticRoutingEnabled() {
		note += "\n" + routerStatusLine()
	}
	m.applyRouterPatterns(withGroup(key, ps), note)
	return nil
}

// handleRouterDeleteCommand 处理 /router-delete-pro、/router-delete-flash。
// 支持按序号删是必须的:样板句是整句,让用户原样重敲一遍才能删掉不现实。
func (m *model) handleRouterDeleteCommand(key, input string) tea.Cmd {
	g := routerGroups[key]
	arg := routerArg(input)
	if arg == "" {
		m.appendChat("System", fmt.Sprintf("用法:`/router-delete-%s <序号>`,序号见 `/router-list-%s`", key, key))
		m.refreshViewport()
		return nil
	}

	ps := g.active()
	idx := -1
	if n, err := strconv.Atoi(arg); err == nil {
		if n < 1 || n > len(ps) {
			m.appendChat("System", fmt.Sprintf("序号 %d 超出范围(1~%d)", n, len(ps)))
			m.refreshViewport()
			return nil
		}
		idx = n - 1
	} else {
		idx = slices.Index(ps, arg)
		if idx < 0 {
			// 敲错组是最容易犯的错:两组序号都从 1 排,原文也长得像。直接指路。
			if slices.Contains(routerGroups[oppositeKey(key)].active(), arg) {
				m.appendChat("System", fmt.Sprintf("这条在「%s」组里,用 `/router-delete-%s`",
					routerGroups[oppositeKey(key)].title, oppositeKey(key)))
			} else {
				m.appendChat("System", fmt.Sprintf("不在本组中,用 `/router-list-%s` 查序号", key))
			}
			m.refreshViewport()
			return nil
		}
	}

	removed := ps[idx]
	out := append(append([]string(nil), ps[:idx]...), ps[idx+1:]...)

	// 回执必须回显组名 + 序号 + 被删原句:两组序号都从 1 排,用户敲错组时
	// 只有把删掉的原文摆出来他才看得出不对。
	note := fmt.Sprintf("已删除「%s」组第 %d 条:%s(剩 %d 条)", g.title, idx+1, removed, len(out))
	if len(out) == 0 {
		// 删空 = 该组恢复内置默认。空表若当真生效,等于这一侧的判据整体消失,
		// 那不会是用户想要的。
		note = fmt.Sprintf("「%s」组已全部删除,恢复内置默认", g.title)
	}
	m.applyRouterPatterns(withGroup(key, out), note)
	return nil
}

func oppositeKey(key string) string {
	if key == "pro" {
		return "flash"
	}
	return "pro"
}

func routerAddExample(key string) string {
	if key == "flash" {
		return "这个配置项默认值是多少"
	}
	return "把这个服务的灰度发布流程梳理清楚"
}
