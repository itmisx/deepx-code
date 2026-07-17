package tui

import (
	"strings"
	"testing"

	"deepx/agent"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
)

// 带 key 的 model:balanceProbeCmd 只在有 key + base_url 时才返回非 nil,
// 否则测不出「探了」和「没探」的区别。
func balanceTestModel() model {
	m := model{}
	m.input = textarea.New()
	m.chatContent = newChatLog(1 << 20)
	m.chatViewport = viewport.New()
	m.currentReply = &strings.Builder{}
	m.models = agent.ModelConfig{
		Flash: agent.ModelEntry{BaseURL: "https://example.invalid/v1", APIKey: "k", Model: "m"},
	}
	return m
}

// 前置:确认这个 model 下探测命令确实是非 nil 的,否则下面的断言全是假阳性。
func TestBalanceProbeCmdNonNilWithKey(t *testing.T) {
	m := balanceTestModel()
	if balanceProbeCmd(m.models) == nil {
		t.Fatal("前置失败:有 key 时 balanceProbeCmd 不该返回 nil,后续用例将无法区分探/不探")
	}
	m.models.Flash.APIKey = ""
	if balanceProbeCmd(m.models) != nil {
		t.Fatal("前置失败:无 key 时 balanceProbeCmd 应返回 nil")
	}
}

// 代数相符 → 延迟重探真的会去查。
func TestBalanceSettleTickFiresWhenGenMatches(t *testing.T) {
	m := balanceTestModel()
	m.balanceGen = 3

	_, cmd := m.Update(balanceSettleTickMsg{gen: 3})
	if cmd == nil {
		t.Error("❌ 代数相符时应发出余额探测命令,实际 nil(延迟重探没生效)")
	}
}

// 防抖核心:代数过期 → 丢弃,不去查。
// 否则连聊 N 轮就会攒 N 个待触发的钟,到点一起打接口。
func TestBalanceSettleTickDroppedWhenStale(t *testing.T) {
	m := balanceTestModel()
	m.balanceGen = 5 // 期间又答了几轮,已有更晚的钟在排队

	_, cmd := m.Update(balanceSettleTickMsg{gen: 2}) // 旧钟到点
	if cmd != nil {
		t.Error("❌ 过期的钟仍触发了探测 → 连续对话会攒一堆钟、白打接口(防抖失效)")
	}
}

// 连续 N 轮只应留下最后一口钟:模拟每轮 balanceGen++ 后,
// 只有最新代数的 tick 会真的探测,之前的全部丢弃。
func TestBalanceSettleDebounceAcrossTurns(t *testing.T) {
	m := balanceTestModel()

	const turns = 20
	var gens []int
	for i := 0; i < turns; i++ {
		m.balanceGen++ // 对应 StreamDoneMsg 里的自增
		gens = append(gens, m.balanceGen)
	}

	fired := 0
	for _, g := range gens { // 所有轮次挂的钟都到点
		if _, cmd := m.Update(balanceSettleTickMsg{gen: g}); cmd != nil {
			fired++
		}
	}
	t.Logf("连聊 %d 轮 → 挂了 %d 口钟,实际触发探测 %d 次", turns, len(gens), fired)
	if fired != 1 {
		t.Errorf("❌ 连聊 %d 轮应只有最后一口钟触发探测,实际触发 %d 次", turns, fired)
	}
}

// 每轮回答结束(StreamDoneMsg)应推进代数,否则防抖无从谈起。
// 这里只锁「代数会变」这个契约,不跑整条 StreamDone 路径(依赖太重)。
func TestBalanceGenAdvancesPerTurn(t *testing.T) {
	m := balanceTestModel()
	before := m.balanceGen
	m.balanceGen++ // StreamDoneMsg 中的自增
	if m.balanceGen == before {
		t.Error("❌ 每轮结束必须推进 balanceGen,否则旧钟无法被识别为过期")
	}
	// 新钟应被接受,紧邻的旧钟应被丢弃
	if _, cmd := m.Update(balanceSettleTickMsg{gen: before}); cmd != nil {
		t.Error("❌ 上一轮的钟应已过期")
	}
	if _, cmd := m.Update(balanceSettleTickMsg{gen: m.balanceGen}); cmd == nil {
		t.Error("❌ 当前轮的钟应触发探测")
	}
}
