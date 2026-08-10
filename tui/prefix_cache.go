package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"deepx/agent"
	"deepx/mcp"
	"deepx/tools"

	tea "charm.land/bubbletea/v2"
)

// cacheWarmWindow:重启压缩只在距上次请求这么久之内才有意义 —— 超过此窗口 DeepSeek 的上下文
// 缓存大概率已失效,复刻旧前缀命中不了,"缓存友好压缩"退化成冷摘要调用 + 阻塞首屏,得不偿失。
// 注:此值是经验假设,非 DeepSeek 官方 TTL —— 官方未承诺确切缓存寿命,高负载下还可能提前驱逐。
// 取 1h 偏保守,宁可漏压(代价小:冷首请求而已),不愿白跑一次冷摘要。
const cacheWarmWindow = 1 * time.Hour

// restartCompactKeepFactor:启动检测到前缀变化时,仅当"历史 token"(estimateHistoryTokens)≥
// 保留目标(agent.CompactKeepTokens)的这个倍数才压缩。口径与"保留"统一 —— 都只算历史、
// 不含 system/tools/summary 底座(底座压不掉,不该计入"值不值得压"的判断)。
//
// 取 2:历史要 ≥ 2× 保留目标才压,确保压完至少去掉一半,而非"刚过线压一点点 ——
// 省下的空间还抵不过一次摘要调用 + 信息有损"。低于此量,冷首请求本来就便宜,直接扛着、
// 让缓存在新前缀上自然回暖即可。
const restartCompactKeepFactor = 2

// prefixSignature 计算前缀变化检测用的签名:直接对"会进缓存前缀的实际内容"取指纹:
//
//		hash(核心系统提示词文本 + 内置工具 catalog JSON + 排序后的 mcp.json 配置)
//
//	  - 核心系统提示词 = BuildSystemPrompt(workspace, skill, "") —— 含提示词正文 + workspace + skill 目录,
//	    不含会话摘要(摘要变化是压缩的正常结果,不应触发"重启压缩";两侧都不含,天然可比)。
//	  - 内置工具 catalog:遍历 tools.Tools 取 ToOpenAISpec —— 工具增删/改 schema 都会变。
//	  - mcp 配置:取自 mcp.json(server 身份),非实时连上的工具,避免异步上线/连接失败抖动。
//
// 这样 prompt 文本 / 工具 / skill / mcp 任一改动都能检测到,dev(go run)和发布版均生效,
// 不再依赖 version 代理(之前手改提示词、go run 时 version 恒为 "dev",签名不变 → 漏检)。
func (m *model) prefixSignature() string {
	core := agent.BuildSystemPrompt(m.workspace, m.skillCatalog, "")

	specs := make([]tools.OpenAIToolSpec, 0, len(tools.Tools))
	for _, t := range tools.Tools {
		specs = append(specs, t.ToOpenAISpec())
	}
	toolsJSON := agent.MarshalToolSpecs(specs)

	servers, _ := mcp.LoadConfig()
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
	mcpJSON, _ := json.Marshal(servers)

	h := sha256.New()
	h.Write([]byte(core))
	h.Write([]byte{0})
	h.Write([]byte(toolsJSON))
	h.Write([]byte{0})
	h.Write(mcpJSON)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// onPrefixSnapshot 持久化本轮"实际发送"的前缀(供压缩复刻热缓存)+ 当前稳定签名(供重启检测)。
func (m *model) onPrefixSnapshot(msg agent.PrefixSnapshotMsg) {
	if m.session == nil {
		return
	}
	sig := m.prefixSignature()
	m.session.SavePrefixSnapshot(sig, msg.Model, msg.SystemPrompt, msg.ToolSpecsJSON)
}

// compactCtxWin 返回压缩基准窗口 —— 所有"上下文占了多少"的判断都必须用它,包括:
// 压缩触发线、影子压缩档位、以及右栏那个百分比的分母。
//
// 取 Pro 的窗口:压缩阈值是整个会话的全局属性,而 flash/pro 会在会话里来回切,
// 按"当前活跃模型"取会让同一段历史在不同轮显示出不同的占用率。曾经右栏就是这么取的,
// 结果 pro=512K / flash=128K 的配置下,压缩把上下文砍到 17% 后右栏仍显示 71%(issue #232)。
// Pro 未配置时退 65536 —— 与既有各处的兜底值保持一致。
func (m *model) compactCtxWin() int {
	if w := m.models.Pro.ContextWindow; w > 0 {
		return w
	}
	return 65536
}

// lastPromptTokens 返回"下一次请求 prompt 大约多大"的 token 数,用于压缩触发判断。
// 优先用 API 上次返回的真实 prompt_tokens(精确);若没有(后端没返回 usage)则退回本地估算。
func (m *model) lastPromptTokens() int {
	if m.lastUsage != nil && m.lastUsage.PromptTokens > 0 {
		return m.lastUsage.PromptTokens
	}
	return m.estimatePromptTokens()
}

// estimateHistoryTokens / estimatePromptTokens 是 agent 包压缩估算的薄封装,填入本 model 的字段。
// token 估算与 tiktoken 分词器都已下沉到 agent/compact.go。
func (m *model) estimateHistoryTokens() int {
	return agent.EstimateHistoryTokens(m.history)
}

func (m *model) estimatePromptTokens() int {
	return agent.EstimatePromptTokens(m.workspace, m.skillCatalog, m.summary, m.history)
}

// entryForModel 按 model ID 取对应的 ModelEntry:命中 flash 则用 flash,否则退 pro。
// 缓存按模型分,压缩必须用"缓存那段历史的同一模型"才命中(见 DeepSeek 缓存讨论)。
func (m *model) entryForModel(id string) agent.ModelEntry {
	if id != "" && m.models.Flash.Model == id {
		return m.models.Flash
	}
	return m.models.Pro
}

// detectRestartCompaction 在启动加载历史后调用:若签名相对上次会话变了、且历史够大,
// 暂存上次的前缀快照(oldSys/oldTools)并返回 true,表示需要在首请求前跑一次缓存友好压缩。
func (m *model) detectRestartCompaction() bool {
	if m.session == nil || m.models.Pro.Model == "" {
		return false
	}
	persistedSig, oldModel, oldSys, oldTools := m.session.LoadPrefixSnapshot()
	if persistedSig == "" || oldSys == "" {
		return false // 首次运行 / 无快照,没有可失效的缓存
	}
	if m.prefixSignature() == persistedSig {
		return false // prompt / 工具 / mcp 均未变,缓存仍有效,不压
	}
	// 缓存时效:距上次请求超过 cacheWarmWindow,旧前缀缓存已凉,复刻命中不了 —— 重启压缩失去意义。
	if t, ok := m.session.PrefixSnapshotTime(); !ok || time.Since(t) > cacheWarmWindow {
		return false
	}
	ctxWin := m.compactCtxWin()
	// 只看历史 token(与保留口径一致),且要 ≥ 保留目标的 restartCompactKeepFactor 倍才值得压。
	// 走 agent.CompactKeepTokens,与压缩实际保留的口径共用一份定义,避免两边百分比各自漂移。
	keepTarget := agent.CompactKeepTokens(ctxWin)
	if m.estimateHistoryTokens() < keepTarget*restartCompactKeepFactor {
		return false // 历史不够大,压完省不下多少,冷首请求本来就便宜,不值得压
	}
	m.pendingCompactModel = oldModel
	m.pendingCompactSys = oldSys
	m.pendingCompactTools = oldTools
	m.compacting = true // Init 会 fire restartCompactionCmd;占住锁防与首轮 70% 触发并发
	return true
}

// restartCompactionCmd 返回一个在首请求前执行的缓存友好压缩 Cmd(复刻旧前缀命中热缓存)。
func (m *model) restartCompactionCmd() tea.Cmd {
	snapshot := append([]agent.ChatMessage(nil), m.history...)
	oldSys, oldTools := m.pendingCompactSys, m.pendingCompactTools
	entry := m.entryForModel(m.pendingCompactModel) // 用缓存那段历史的同一模型才命中
	ctxWin := m.compactCtxWin()
	return func() tea.Msg {
		summary, cutIdx, compressedTurns, err := agent.RunCompression(oldSys, oldTools, snapshot, entry, ctxWin)
		return compressionResultMsg{
			summary:         summary,
			cutIdx:          cutIdx,
			compressedTurns: compressedTurns,
			err:             err,
		}
	}
}
