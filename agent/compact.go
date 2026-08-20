package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"deepx/tools"

	"github.com/tiktoken-go/tokenizer"
)

// === 会话压缩 ===
//
// 本文件封装与 TUI 无关的压缩纯逻辑:token 估算 + RunCompression。
// bubbletea 胶水(触发时机、tea.Cmd 包装、消息处理、读 model/session)仍在 tui 包,调用这里的导出函数。

// ErrCompactTooFewTurns:压缩因「对话轮数不足」被拒(轮数按 isTurnBoundary 计,见下)。
// 这个检查在发 LLM 请求前就本地秒拒,agent 据此区别对待:轮数不足永久关本轮压缩(而非冷却重试),
// 避免每圈闪「压缩中…/失败」刷屏(issue #201)。只有瞬时失败(超时/网络)才值得冷却重试。
var ErrCompactTooFewTurns = errors.New("对话轮数不足,无需压缩")

// keepRecentTurns 是压缩时至少保留的最近对话轮数下限(与 compactKeepPct 预算取较大者)。
// 2 轮能覆盖"最近指代",同时不过度保留。
const keepRecentTurns = 2

// compactKeepPct 是压缩后保留的尾部上下文占上下文窗口的百分比(切点由它反推)。
// 15%(原 20%):留得更少 = 压完离触发线更远、少压几次,长任务会话尤其受益。
const compactKeepPct = 15

// CompactKeepTokens 返回压缩后应保留的尾部上下文 token 数(= 窗口 × compactKeepPct%)。
// 导出供 tui 使用:"值不值得压"的判断必须与这里的保留口径一致,别再各处各写一份百分比。
func CompactKeepTokens(ctxWin int) int { return ctxWin * compactKeepPct / 100 }

// 单次 Write content 上限的推导参数(见 WriteContentLimitBytes)。
const (
	// writeLimitBytesPerTok:token → 字节的保守折算比。实测 64KB 中文 ≈ 15.1K token
	// (≈4.3 字节/token),纯 ASCII 代码 ≈ 8 字节/token。取中文这一头,宁可估紧不估松。
	writeLimitBytesPerTok = 4
	// writeLimitFloor:下限。极小窗口也得能写点有用的东西,否则模型连一个正常源文件都落不了地。
	writeLimitFloor = 8 * 1024
	// writeLimitCap:上限。超大窗口下也不放行病态写入 —— 它仍要在每轮请求里重传,
	// 直到被压缩带走,且这期间无法回收。
	writeLimitCap = 64 * 1024
	// writeLimitDefault:窗口未知时的保守默认。
	writeLimitDefault = 16 * 1024
)

// WriteContentLimitBytes 返回单次 Write 的 content 字节上限,随上下文窗口自适应。
// 由 tui 启动 / 换模型时算出来注入 tools(见 tools.SetWriteContentLimit)。
//
// 为什么必须有这个上限:工具参数一旦进了 assistant.tool_calls,就只能等压缩把它切走 ——
// 而压缩强制保留最近 keepRecentTurns(2)轮,reclaim 又只处理 role=tool 的输出、够不着
// tool_call 的参数。也就是说大写入刚发生的那两轮里,它既压不掉也回收不了。小窗口模型单条
// 大写入就能把上下文顶爆,压缩根本等不到触发。所以只能在源头挡,不能指望事后收拾。
//
// 取值:单次写入不超过「压缩保留预算」的一半 —— 保证它落进尾部保护区时,压缩仍压得动。
func WriteContentLimitBytes(ctxWin int) int {
	if ctxWin <= 0 {
		return writeLimitDefault
	}
	n := CompactKeepTokens(ctxWin) / 2 * writeLimitBytesPerTok
	return min(max(n, writeLimitFloor), writeLimitCap)
}

// WriteContentLimitFor 按一份模型配置算出该注入的 Write 上限,取 flash / pro 中**较小**的窗口。
// 上限是安全阀,得按最脆弱的那个模型定:同一会话里 flash 和 pro 会来回切(入口路由 +
// SwitchModel 升级),而写入发生在哪一轮事先不知道。两个窗口可以配得不一样(model.yaml 里
// 每个条目各有 context_window),按大的算会让跑在小窗口模型上的那轮顶爆。
// 都没配(都是 0)时回落保守默认。
func WriteContentLimitFor(models ModelConfig) int {
	ctxWin := 0
	for _, w := range []int{models.Flash.ContextWindow, models.Pro.ContextWindow} {
		if w > 0 && (ctxWin == 0 || w < ctxWin) {
			ctxWin = w
		}
	}
	return WriteContentLimitBytes(ctxWin)
}

// CutHistory 按切点截断历史,返回要保留的尾部(新切片,不共享底层数组、不改入参)。
//
// 切点按 user|assistant 取(见 isTurnBoundary),长任务轮里往往落在 assistant 上,于是保留段以
// assistant 打头、整段可能一条 user 消息都没有。这时把**被压掉那部分里最近一条 user 消息**复制
// 一份放到最前面,一举两得:
//   - 模型能看到用户任务的原文,而不是只剩摘要里的转述;
//   - 开头恒为 system → user → assistant。vLLM 按模型自带 chat template 渲染,Mistral / Llama-2 系
//     模板写着 raise_exception("Conversation roles must alternate ..."),system 后直接跟 assistant 会被拒。
//
// 从**被压掉的部分**取,所以它在时间线上必早于保留段的全部内容,插到最前面顺序正确,
// 也不会与保留段里已有的 user 消息重复。复制时丢掉图片:图会被重新渲染成 base64、很占 token,
// 而摘要里已有相关描述。保留段本就以 user 开头时原样返回,不做任何事。
//
// 所有应用切点的地方都该走这里,别自己 history[cutIdx:] —— 否则各处行为不一致。
func CutHistory(history []ChatMessage, cutIdx int) []ChatMessage {
	cutIdx = min(max(cutIdx, 0), len(history))
	kept := history[cutIdx:]
	out := make([]ChatMessage, 0, len(kept)+1)
	if len(kept) > 0 && kept[0].Role != "user" {
		for i := cutIdx - 1; i >= 0; i-- {
			// 取最近一条**有正文**的 user 消息:纯图片消息剥掉图后正文为空,
			// 发出去会是一条没有 content 字段的 user 消息,部分后端直接拒。
			if history[i].Role != "user" || strings.TrimSpace(history[i].Content) == "" {
				continue
			}
			task := history[i]
			task.ImagePaths = nil // 图不复制(见上)
			task.ContentParts = nil
			out = append(out, task)
			break
		}
	}
	return append(out, kept...)
}

// isTurnBoundary 判断这条消息能否当作"一轮对话"的边界,也即能否作为压缩切点。
//
// user 和 assistant 都算,tool 不算。两个原因:
//   - "一个 user 消息 + 几十轮工具调用"的长任务轮里一条 user 都没有(issue #201 的典型形态),
//     只认 user 就找不到切点,压缩要么退到最前面(等于不压)、要么把整个长轮全保住;
//   - tool 消息必须紧跟发起调用的 assistant,切在它上面会留下孤儿 tool → API 400
//     (见 sanitizeToolPairs);而切在 assistant 上,它的 tool 结果自然跟着一起保留,配对不坏。
func isTurnBoundary(m ChatMessage) bool { return m.Role == "user" || m.Role == "assistant" }

// compactionTimeout 返回摘要 LLM 调用的硬超时,随上下文窗口放宽。
// 没有它,卡住的请求会让压缩锁永远占住、把所有压缩堵死;只为兜住"永不返回",超时即失败、下轮重试。
//
// 为什么不能一律 10 分钟(issue #232):压缩要做的两件事都随窗口线性增长 ——
// prefill 整段待压历史(512K 窗口在 70% 触发线上就是约 29 万 token),外加生成 summaryMax 的摘要。
// 非流式(Stream: false)得等整个响应回来,512K 下光生成就要好几分钟,10 分钟必然不够 ——
// 表现为大窗口用户一到 70% 就 "context deadline exceeded",小窗口用户毫无感觉。
// 给得宽松些无妨:它只是"永不返回"的兜底,正常压缩远早于此返回。
func compactionTimeout(ctxWin int) time.Duration {
	if ctxWin <= 0 {
		return baseCompactionTimeout
	}
	d := baseCompactionTimeout + time.Duration(ctxWin/(64<<10))*perChunkCompactionTimeout
	return min(d, maxCompactionTimeout)
}

const (
	baseCompactionTimeout     = 10 * time.Minute // 起步(小窗口绰绰有余,保持原有行为)
	perChunkCompactionTimeout = 3 * time.Minute  // 每 64K 窗口追加(主要覆盖 prefill 变长)
	// maxCompactionTimeout:封顶。压缩是**前台阻塞**的(compactingFG,期间挡输入),
	// 真让它挂到一小时用户就废了。摘要长度封顶后"生成慢"这个主因已经消除,剩下只有 prefill,
	// 30 分钟对本地慢模型跑满窗口也够,再长就该判定为病态、失败重试而不是继续等。
	maxCompactionTimeout = 30 * time.Minute
)

// maxSummaryTokens 是摘要生成的绝对上限。
// 摘要是"当前工作状态快照",信息量由会话内容决定,不该随窗口线性膨胀:
// 512K 窗口下 ctxWin*3% = 15728 tok,既拖慢生成(压缩超时的主因之一),
// 又让压缩后的上下文一上来就被摘要占掉一大块。4000 tok 足够装下这套模板的全部字段。
const maxSummaryTokens = 4000

// compressionPrompt 是冷路径(无前缀快照)压缩历史时发给 LLM 的 system prompt。
const compressionPrompt = `你是会话「工作状态 checkpoint」生成器。把对话历史提炼成一份结构化的当前工作状态快照,用于丢弃旧历史后延续上下文。

## 摘要需保留
- 用户的任务目标和明确要求（尽量原文保留）
- 已修改的文件及改动目的
- 发现的错误和修复方案
- 架构设计决策
- 未完成的任务和下一步计划

## 可以丢弃
- 重复的调试尝试
- 工具调用的详细输出
- 已解决且不再相关的中间讨论

如果输入中有 [previous summary],将其与新对话合并为一个连贯摘要。

## 输出格式(严格按以下字段;无内容的字段写「无」)
## 任务目标
<用户想达成什么;明确要求尽量原文>

## 当前进度
<已完成 / 已验证的事项>

## 关键决策与取舍
<做了哪些决定及原因;试过并否决的方案 + 为什么>

## 关键事实与约束
<技术事实、项目约定、不能动的东西、踩过的坑>

## 相关文件
- <path> — <作用 / 改动>

## 未决项
<待用户确认 / 阻塞中的问题;无则写「无」>

## 下一步
<下一个具体动作>

最后模式: plan/auto`

// warmCompressInstruction 是缓存友好压缩时追加在历史末尾的指令(对应 compressionPrompt 的内容,
// 但作为尾部 user 消息而非独立 system,从而不破坏 [system][tools][history] 前缀的命中)。
const warmCompressInstruction = `请把以上完整对话(包括 system 提示词里已有的"当前工作状态"/会话摘要,若有)提炼成一份新的、连贯的结构化「当前工作状态 checkpoint」,用它覆盖旧的。

## 摘要需保留
- 用户的任务目标和明确要求(尽量原文保留)
- 已修改的文件及改动目的
- 发现的错误和修复方案
- 架构设计决策
- 未完成的任务和下一步计划

## 可以丢弃
- 重复的调试尝试
- 工具调用的详细输出
- 已解决且不再相关的中间讨论

## 输出格式(严格按以下字段;无内容的字段写「无」)
## 任务目标
<用户想达成什么;明确要求尽量原文>

## 当前进度
<已完成 / 已验证的事项>

## 关键决策与取舍
<做了哪些决定及原因;试过并否决的方案 + 为什么>

## 关键事实与约束
<技术事实、项目约定、不能动的东西、踩过的坑>

## 相关文件
- <path> — <作用 / 改动>

## 未决项
<待用户确认 / 阻塞中的问题;无则写「无」>

## 下一步
<下一个具体动作>

最后模式: plan/auto`

// === token 估算 ===

// tikCodec 惰性初始化 o200k_base 分词器(OpenAI 现代词表)。DeepSeek 未公开 Go 分词器,但 BPE
// token 密度相近,实测与真实 prompt_tokens 差 ~2.5%,对压缩阈值判断足够;且纯本地、零 API 依赖、
// 内容无关(中文/代码/JSON 都准)。词表编译进二进制,Get 仅首次有开销,故 sync.Once 缓存。
var (
	tikOnce  sync.Once
	tikCodec tokenizer.Codec
)

func tokCodec() tokenizer.Codec {
	tikOnce.Do(func() {
		tikCodec, _ = tokenizer.Get(tokenizer.O200kBase) // 失败留 nil,EstTokens 自动退回 字符/2.5
	})
	return tikCodec
}

// fallbackCharsPerTok:tiktoken 不可用时的兜底"字符/token"比。取 2.5(略保守,估得偏高一点 →
// 宁可早压不晚压),而非 3 —— 实测混合内容真实比例在 2.3~3.2,2.5 居中且不易低估历史。
const fallbackCharsPerTok = 2.5

// EstTokens 估算文本 token 数:优先用 tiktoken(o200k)精确分词;分词器不可用时退回 字符/2.5。
func EstTokens(s string) int {
	if s == "" {
		return 0
	}
	if c := tokCodec(); c != nil {
		if ids, _, err := c.Encode(s); err == nil {
			return len(ids)
		}
	}
	return int(float64(len([]rune(s))) / fallbackCharsPerTok)
}

// MsgTokens 估算单条消息在请求体里占的 token(全字段:Content + ReasoningContent + ContentParts +
// ToolCalls 的 Name/Arguments)—— 漏算 ToolCalls.Arguments(agentic 会话里占比可观)会系统性低估历史。
func MsgTokens(m ChatMessage) int {
	t := EstTokens(m.Content) + EstTokens(m.ReasoningContent)
	for _, p := range m.ContentParts {
		t += EstTokens(p.Text)
	}
	for _, tc := range m.ToolCalls {
		t += EstTokens(tc.Function.Name) + EstTokens(tc.Function.Arguments)
	}
	return t
}

// EstimateHistoryTokens 估算会话历史的 token 数(全字段),不含 system / tools / summary 固定底座 ——
// 与压缩"保留量"口径一致(RunCompression 也只按历史算):历史是唯一可被压缩的部分,底座压不掉,
// 故"值不值得压"只看历史。
func EstimateHistoryTokens(history []ChatMessage) int {
	t := 0
	for _, msg := range history {
		t += MsgTokens(msg)
	}
	return t
}

// EstimatePromptTokens 本地估算整个 prompt 的 token 数 = 系统提示词 + 工具定义 JSON + 历史。
// 仅在 API 没返回 usage 时作兜底(调用方优先用真实 prompt_tokens)。
func EstimatePromptTokens(workspace, skillCatalog, summary string, history []ChatMessage) int {
	t := EstTokens(BuildSystemPrompt(workspace, skillCatalog, summary))

	specs := make([]tools.OpenAIToolSpec, 0, len(tools.Tools))
	for _, tl := range tools.Tools {
		specs = append(specs, tl.ToOpenAISpec())
	}
	for _, tl := range tools.MCPTools() {
		specs = append(specs, tl.ToOpenAISpec())
	}
	t += EstTokens(MarshalToolSpecs(specs))

	return t + EstimateHistoryTokens(history)
}

// === 压缩执行 ===

// RunCompression 执行一次会话压缩:保留 max(context_window × compactKeepPct%, 最近 keepRecentTurns 轮)
// 的尾部上下文,切点落在 user / assistant 边界(见 isTurnBoundary),因此也能切进长任务轮内部。
// 通常在 goroutine 中调用。history 仅含会话消息(不含 system / 旧摘要消息)。
//
// 缓存友好:传入 lastSystemPrompt(上次实际发送的 system 文本)时,摘要请求构造成
// [lastSystemPrompt] + history[:keepStart] + [尾部压缩指令],并带上 lastToolSpecsJSON 还原的
// 工具集 —— 这串前缀正是上次缓存下来的,几乎全命中,只有尾部指令是 miss。
// lastSystemPrompt 为空(无快照)时退回冷路径:compressionPrompt 当 system + 拍平历史。
func RunCompression(lastSystemPrompt, lastToolSpecsJSON string, history []ChatMessage, entry ModelEntry, ctxWin int) (
	summary string, cutIdx int, compressedTurns int, err error) {

	// 轮数按 isTurnBoundary(user 或 assistant)计:一个 user 消息 + 几十轮工具调用同样是几十轮对话,
	// 只数 user 会把这种长任务会话判成"轮数不足"而永远压不动(issue #201)。
	// 总轮数不多于要保留的轮数 → 全都要留,没有可压前缀。
	turns := 0
	for _, msg := range history {
		if isTurnBoundary(msg) {
			turns++
		}
	}
	if turns <= keepRecentTurns {
		return "", 0, 0, ErrCompactTooFewTurns
	}

	// 保留量 = max(compactKeepPct 预算, 最近 keepRecentTurns 轮),取保留更多者(切点更靠前 = 下标更小)。
	// 两个循环都只在 isTurnBoundary 处取切点 —— 切在 tool 上会留下孤儿 tool、被 API 拒。
	// 关键:budgetStart 初值 = 0 = "默认保留全部"。从尾部累加 token 攒够预算,才把切点后移
	// (留得更少)。整段历史都不够预算 → budgetStart 停在 0 → keepStart=0 → 守卫拒绝,不压。
	keepTarget := CompactKeepTokens(ctxWin)

	budgetStart := 0 // 默认保留全部;攒够预算才后移切点
	cc := 0
	for i := len(history) - 1; i >= 0; i-- {
		// 用 MsgTokens(tiktoken 精确分词)按 token 累加全字段,含 ReasoningContent + ToolCalls 参数。
		cc += MsgTokens(history[i])
		if isTurnBoundary(history[i]) && cc >= keepTarget {
			budgetStart = i
			break
		}
	}
	turnStart := len(history) // 最近 keepRecentTurns 轮;不足则保持 len(不参与取 min)
	uc := 0
	for i := len(history) - 1; i >= 0; i-- {
		if isTurnBoundary(history[i]) {
			uc++
			if uc >= keepRecentTurns {
				turnStart = i
				break
			}
		}
	}
	keepStart := min(budgetStart, turnStart) // 取保留更多者 = 更靠前的切点
	if keepStart <= 0 {
		// 整段都要留:历史不足保留预算,或预算边界就在最前一条 —— 没有可压缩前缀。
		return "", 0, 0, fmt.Errorf("历史不足 %d%% 窗口,无需压缩", compactKeepPct)
	}
	cutIdx = keepStart

	lastMode := "auto"
	compressedUserCount := 0
	for _, msg := range history[:keepStart] {
		if msg.Role == "user" {
			compressedUserCount++
		}
		if msg.Role == "assistant" && strings.Contains(msg.Content, "当前模式: plan") {
			lastMode = "plan"
		}
		if msg.Role == "assistant" && strings.Contains(msg.Content, "当前模式: auto") {
			lastMode = "auto"
		}
	}
	compressedTurns = compressedUserCount

	// 下限 256 tok(太小就失去摘要意义),上限 maxSummaryTokens(摘要不随窗口线性膨胀)。
	summaryMax := min(max(ctxWin*3/100, 256), maxSummaryTokens)

	// 硬超时:卡住的摘要请求不会永久占住压缩锁(否则压缩全堵死)。随窗口放宽,见 compactionTimeout。
	ctx, cancel := context.WithTimeout(context.Background(), compactionTimeout(ctxWin))
	defer cancel()

	if lastSystemPrompt != "" {
		// 缓存友好路径:复刻 [system][tools][history[:keepStart]] + 尾部指令。
		convo := make([]ChatMessage, 0, keepStart+2)
		convo = append(convo, ChatMessage{Role: "system", Content: lastSystemPrompt})
		convo = append(convo, history[:keepStart]...)
		convo = append(convo, ChatMessage{Role: "user", Content: warmCompressInstruction})
		toolSpecs := UnmarshalToolSpecs(lastToolSpecsJSON)
		summary, err = CallWithTools(ctx, entry.APIKey, entry.BaseURL, entry.Model, convo, toolSpecs, summaryMax)
	} else {
		// 冷路径:无快照,拍平历史走独立 system(必 miss,但正确)。
		var inputBuf strings.Builder
		for _, msg := range history[:keepStart] {
			inputBuf.WriteString("[" + msg.Role + "]\n" + msg.Content + "\n\n")
		}
		convo := []ChatMessage{
			{Role: "system", Content: compressionPrompt},
			{Role: "user", Content: inputBuf.String()},
		}
		summary, err = CallOnce(ctx, entry.APIKey, entry.BaseURL, entry.Model, convo, summaryMax)
	}
	if err != nil {
		return "", 0, 0, err
	}
	if !strings.Contains(summary, "最后模式:") {
		summary += "\n最后模式: " + lastMode
	}
	return summary, cutIdx, compressedTurns, nil
}
