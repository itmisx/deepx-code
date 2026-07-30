package tui

import (
	"fmt"
	"strings"

	"deepx/agent"
	"deepx/web"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// /sessions 历史对话列表 + /new 新建对话。
// 底层多对话能力在 session 包(零迁移:默认对话=rootDir,新对话进 conversations/)。

// openSessionListModal 弹出历史对话列表,默认光标落在当前对话上。
func (m *model) openSessionListModal() {
	if m.session == nil {
		m.appendChat("System", "会话存储不可用")
		return
	}
	m.sessionConvs = m.session.ListConversations()
	m.sessionListIdx = 0
	for i, c := range m.sessionConvs {
		if c.Active {
			m.sessionListIdx = i
			break
		}
	}
	m.showSessionList = true
	m.sessionListDelete = false
	m.input.Blur()
}

// submitSessionSwitch 切到列表里选中的对话。流式中拒绝;已是当前对话则不动。
func (m *model) submitSessionSwitch() {
	defer func() { m.showSessionList = false; m.input.Focus() }()
	if m.streaming {
		m.appendChat("System", T("session.streaming"))
		return
	}
	if m.sessionListIdx < 0 || m.sessionListIdx >= len(m.sessionConvs) {
		return
	}
	target := m.sessionConvs[m.sessionListIdx]
	if target.Active {
		return
	}
	if err := m.session.SwitchConversation(target.ID); err != nil {
		m.appendChat("System", "切换失败:"+err.Error())
		return
	}
	m.loadCurrentConversation()
	title := target.Title
	if title == "" {
		title = T("session.untitled")
	}
	m.appendChat("System", fmt.Sprintf(T("session.switched"), title))
}

// startNewConversation 新建一条空对话并切过去(/new)。流式中拒绝。
func (m *model) startNewConversation() {
	if m.streaming {
		m.appendChat("System", T("session.streaming"))
		return
	}
	if m.session == nil {
		return
	}
	if _, err := m.session.NewConversation(); err != nil {
		m.appendChat("System", "新建对话失败:"+err.Error())
		return
	}
	// 新对话视作一次"启动":刷新 AGENTS.md 快照,让新会话用上最新偏好。
	agent.RefreshPreferences(m.workspace)
	m.loadCurrentConversation()
	m.appendChat("System", T("session.new"))
}

// loadCurrentConversation 把 session 当前对话(convDir 指向的)载入内存并重画对话区。
// 复位 history/summary/plan,再按 history.gob 重建。镜像启动加载逻辑(含老 gob 的 system/摘要兼容)。
func (m *model) loadCurrentConversation() {
	m.history = nil
	m.summary = ""
	m.plan = nil
	m.planKind = ""
	m.pendingUserText = ""
	// 切会话:影子档位归零;代数 +1 让另一会话在飞的影子结果 gen 失配被丢弃(不串台存错会话)。
	m.shadowDonePct = 0
	m.compactGen++
	m.chatContent.Reset()
	if m.session != nil {
		m.workingMode = agent.NormalizeWorkingMode(m.session.LoadWorkingMode()) // 切会话同步工作模式
		m.restoreModelPin(m.session.LoadModelPin())                             // 切会话同步 /model 锁定(issue #43)
		m.summary = m.session.LoadSummary()
		var gobHistory []agent.ChatMessage
		if err := m.session.LoadGob("history.gob", &gobHistory); err == nil && len(gobHistory) > 0 {
			if gobHistory[0].Role == "system" {
				gobHistory = gobHistory[1:]
			}
			if len(gobHistory) > 0 && gobHistory[0].Role == "assistant" &&
				strings.HasPrefix(gobHistory[0].Content, "## 会话摘要") {
				if m.summary == "" {
					m.summary = strings.TrimPrefix(gobHistory[0].Content, "## 会话摘要\n")
				}
				gobHistory = gobHistory[1:]
			}
			m.history = gobHistory
			rebuildChatFromHistory(m.chatContent, gobHistory)
			// 切会话: 重建主题追踪图, 避免旧会话的 TF-IDF 文档频率污染新会话。
			if m.topicGraph != nil {
				m.topicGraph.Rebuild(gobHistory)
				m.lastFocusID = -1
			}
		} else {
			// 新会话无历史: 重置主题追踪图。
			if m.topicGraph != nil {
				m.topicGraph.Rebuild(nil)
				m.lastFocusID = -1
			}
		}
	}
	m.refreshViewport()
	m.chatViewport.GotoBottom()
	// 镜像给 web:载入新会话的消息 + 同步控制态(工作模式按会话恢复,故一并广播)。
	// 终端 /new、/sessions 与 web 按钮都经此函数,故 web 镜像两边都能跟上。
	m.broadcastSessionLoaded()
	m.broadcastControlState()
}

// maybeSetConvTitle 在对话还没标题时,用当前这条用户输入当标题(截断)。
// 返回是否真的设置了标题(供调用方决定要不要广播会话列表给 web)。
func (m *model) maybeSetConvTitle(userText string) bool {
	if m.session == nil {
		return false
	}
	if strings.TrimSpace(m.session.ConvTitle()) != "" {
		return false
	}
	t := strings.TrimSpace(userText)
	if t == "" {
		return false
	}
	m.session.SetConvTitle(truncTitle(t, 40))
	return true
}

// toggleStatusPanel 显隐右侧状态栏并记忆到 meta。chat 宽度随之变化,需重设 viewport 宽度、
// 重排内容并清掉选区(选区坐标按旧宽算的)。
func (m *model) toggleStatusPanel() {
	m.hideStatusPanel = !m.hideStatusPanel
	metaUpdate(func(mm *meta) { mm.HideStatus = m.hideStatusPanel })
	leftW, vpH := m.layout()
	m.chatViewport.SetWidth(leftW)
	m.chatViewport.SetHeight(vpH)
	m.input.SetWidth(leftW - inputGutterWidth) // 输入区跟着左列宽变化(状态栏显隐改变 leftW)
	m.selecting = false
	m.refreshViewport()
}

// toggleThinking 显隐"模型思考(reasoning_content)暗显"并记忆到 meta。
// 只影响之后回合的思考渲染,不改已有段;给一条系统提示让用户知道当前状态。
func (m *model) toggleThinking() {
	m.showThinking = !m.showThinking
	metaUpdate(func(mm *meta) { mm.ShowThinking = m.showThinking })
	m.broadcast(web.Event{Kind: "show_thinking", Text: onOff(m.showThinking)}) // 同步给 web
	if m.showThinking {
		m.chatContent.Open(kindSystem, T("thinking.on"))
	} else {
		m.chatContent.Open(kindSystem, T("thinking.off"))
	}
	m.refreshViewport()
}

// firstUserText 取历史里第一条用户消息的显示文本(用作对话标题)。没有则空串。
func firstUserText(history []agent.ChatMessage) string {
	for _, msg := range history {
		if msg.Role == "user" {
			return chatDisplayText(msg)
		}
	}
	return ""
}

func truncTitle(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func (m model) sessionListModalBlock() string {
	const modalW = 56
	innerW := modalW - 6 // 减边框/内边距余量
	if maxW := m.width - 10; innerW > maxW {
		innerW = maxW
	}
	if innerW < 20 {
		innerW = 20
	}

	titleKey := "session.modal.title"
	if m.sessionListDelete {
		titleKey = "session.modal.title_delete"
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(highlightColor).Render(T(titleKey))
	// "当前对话"标记的固定槽位:所有行都留这么宽,非当前行填空格 → 日期右边界对齐。
	tagStr := " ·" + T("session.current")
	tagW := ansi.StringWidth(tagStr)
	const dateW = 11 // "01-02 15:04"

	var rows []string
	if len(m.sessionConvs) == 0 {
		rows = append(rows, lipgloss.NewStyle().Foreground(dimColor).Render(T("session.modal.empty")))
	}
	for i, c := range m.sessionConvs {
		label := c.Title
		if label == "" {
			label = T("session.untitled")
		}
		marker := "  "
		if i == m.sessionListIdx {
			marker = "▸ "
		}
		date := c.LastSeenAt.Format("01-02 15:04")
		tagSlot := strings.Repeat(" ", tagW)
		if c.Active {
			tagSlot = tagStr
		}
		// 右侧固定区 = 日期(定宽)+ 标记槽(定宽);标题按显示宽度截断填左侧,整行 pad 到 innerW。
		fixedRight := dateW + tagW
		avail := innerW - ansi.StringWidth(marker) - fixedRight - 1
		titleSeg := truncWidth(label, avail)
		left := marker + titleSeg
		pad := innerW - ansi.StringWidth(left) - fixedRight
		if pad < 1 {
			pad = 1
		}
		line := ansi.Cut(left+strings.Repeat(" ", pad)+date+tagSlot, 0, innerW)

		if i == m.sessionListIdx {
			rows = append(rows, lipgloss.NewStyle().Bold(true).
				Foreground(lipgloss.Color("15")).Background(lipgloss.Color("238")).Render(line))
		} else {
			rows = append(rows, lipgloss.NewStyle().Foreground(softFgColor).Render(line))
		}
	}
	footerKey := "session.modal.footer"
	if m.sessionListDelete {
		footerKey = "session.modal.footer_delete"
	}
	footer := lipgloss.NewStyle().Foreground(dimColor).Render(T(footerKey))
	parts := append([]string{title, ""}, rows...)
	parts = append(parts, "", footer)
	return wrapModal(lipgloss.JoinVertical(lipgloss.Left, parts...), modalW, m.width)
}

// truncWidth 按显示宽度(CJK 算 2 列)把 s 截到 w 列;超长则截到 w-1 列再接 "…"。
func truncWidth(s string, w int) string {
	if w < 1 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Cut(s, 0, w-1) + "…"
}
