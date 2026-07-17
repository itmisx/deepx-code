package tui

import (
	"context"
	"time"

	"deepx/agent"

	tea "charm.land/bubbletea/v2"
)

// 账户余额查询的接入层(配合右栏「模型厂商」段展示):
//
// 仅 DeepSeek / Kimi 提供可凭模型 Key 调用的余额接口(见 agent.ProbeBalance);其它供应商
// 回 Supported=false → 右栏显示 "-"。每次启动、每次 /config 改配置、每次 /provider 切换、
// 每轮回答结束都重探一次,结果经 balanceMsg 回灌当前会话(异步,拿不到不阻塞 UI)。
//
// 除此之外还有一次「延迟重探」(issue #182):供应商的计费是滞后结算的,答完立刻查拿到的
// 往往还是扣费前的数,右栏显示的余额于是比网页大,给人「怎么这么省钱」的错觉。所以每轮结束
// 除了即时探一次(让用户马上看到个大概),还挂一次 balanceSettleDelay 之后的重探来校准到真实值。

// balanceSettleDelay 是「等供应商计费结算完」再重探余额的延迟(issue #182)。
//
// 注意这个值是拍的:目前没有 DeepSeek/Kimi 计费实际延迟多久的实测数据,5 分钟来自 issue
// 提议者的体感。真要调准得实打一轮再按 30s 采样看多久数字才变。偏大比偏小安全 —— 早了拿到的
// 还是旧值(等于白探),晚了只是校准慢一点,期间显示的即时值本来也够用。
const balanceSettleDelay = 5 * time.Minute

// balanceMsg 是一次余额查询的回执。
type balanceMsg struct {
	display   string // 可渲染金额串(supported 时有效)
	supported bool
}

// balanceSettleTickMsg 是延迟重探的闹钟。gen 用于防抖:连续对话时每轮都会重新挂钟,
// 回来时若与当前 balanceGen 不符,说明期间又答了新的一轮、已有更晚的钟在排队 → 丢弃本次,
// 不然连聊 20 轮就会攒 20 个待触发的钟、白打 20 次接口(issue #182)。
type balanceSettleTickMsg struct {
	gen int
}

// balanceSettleCmd 挂一个 balanceSettleDelay 之后的重探闹钟,带上触发时的代数供回来比对。
func balanceSettleCmd(gen int) tea.Cmd {
	return tea.Tick(balanceSettleDelay, func(time.Time) tea.Msg {
		return balanceSettleTickMsg{gen: gen}
	})
}

// balanceProbeCmd 用当前 flash(无 key 则退 pro)的配置查余额。无 key/base_url 直接返回 nil 不发命令。
func balanceProbeCmd(models agent.ModelConfig) tea.Cmd {
	entry := models.Flash
	if entry.APIKey == "" {
		entry = models.Pro
	}
	if entry.APIKey == "" || entry.BaseURL == "" {
		return nil
	}
	return func() tea.Msg {
		res, err := agent.ProbeBalance(context.Background(), entry)
		if err != nil {
			return nil // 瞬时错误 → 不更新,保留上次值
		}
		return balanceMsg{display: res.Display, supported: res.Supported}
	}
}

// applyBalance 把回执落到当前会话:支持则存金额串,不支持存 "-"。
func (m *model) applyBalance(msg balanceMsg) {
	if msg.supported {
		m.balance = msg.display
		return
	}
	m.balance = "-"
}
