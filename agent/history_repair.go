package agent

import "strings"

// 历史里"内容为空"的消息会让整轮对话直接失败:出站序列化时 content 字段被整个省掉,
// 服务端回 `Param Incorrect: "content" is not set`(HTTP 400)。
//
// 空消息本不该被写进历史,但历史是长期落盘的 gob:进程被杀、磁盘写一半、旧版本的 bug、
// 手工改过的文件,都可能留下这种残缺记录。而一旦留下,**这个会话就再也发不出请求了** ——
// 每轮都会带上那条坏消息,用户只看到一个没头没脑的 400,除了删掉整个会话没有别的出路。
//
// 所以恢复会话时主动查一遍并就地修好,而不是指望写入端永不出错。

// 修复用的占位文本。写成人能看懂的说明,而不是空格之类的"技术性填充":
// 模型会读到它,用户在 /sessions 里也会看到,含糊的填充只会让人以为自己记错了。
const (
	lostUserContent = "(此前这条消息的内容已丢失)"
	lostToolContent = "(工具无输出)"
)

// HistoryRepair 记录一次修复都动了什么,供 UI 如实告知用户。
type HistoryRepair struct {
	Filled  int // 补了内容的消息数
	Dropped int // 丢弃的消息数
}

func (r HistoryRepair) Any() bool { return r.Filled > 0 || r.Dropped > 0 }

// RepairHistory 检查并修复从磁盘恢复的历史,返回修好的副本与改动统计。
//
// 逐类处理(判据都是"序列化出去会不会丢 content"):
//
//	system 空          → 丢弃。system prompt 每轮由 BuildSystemPrompt 现建,存下来的空壳纯属垃圾。
//	user 空            → 填占位,**不丢弃**。丢掉可能让历史以 assistant 开头,那是另一种 400;
//	                     填占位则结构不变,模型还能看出这里出过问题。
//	user 空 + 有图      → 原样保留。图片渲染那一层会把内容补上(见 image_render.go)。
//	tool 空            → 填占位,**绝不能丢弃**:丢了就成了孤儿 tool_call,
//	                     sanitizeToolPairs 会连带把 assistant 的 tool_calls 剥掉,一路塌下去。
//	assistant 空        → 不动。带 tool_calls 时省略 content 是 OpenAI 的标准形状;
//	                     不带时序列化会补空串,两种都不会触发 400。
//
// 入参不被修改;没有任何需要修的地方时直接返回原切片,不做无谓拷贝。
func RepairHistory(msgs []ChatMessage) ([]ChatMessage, HistoryRepair) {
	var r HistoryRepair
	need := false
	for _, m := range msgs {
		if repairKind(m) != repairNone {
			need = true
			break
		}
	}
	if !need {
		return msgs, r
	}

	out := make([]ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		switch repairKind(m) {
		case repairDrop:
			r.Dropped++
		case repairFillUser:
			m.Content = lostUserContent
			r.Filled++
			out = append(out, m)
		case repairFillTool:
			m.Content = lostToolContent
			r.Filled++
			out = append(out, m)
		default:
			out = append(out, m)
		}
	}
	return out, r
}

type repairAction int

const (
	repairNone repairAction = iota
	repairDrop
	repairFillUser
	repairFillTool
)

func repairKind(m ChatMessage) repairAction {
	// ContentParts 自带内容,序列化走数组分支,不会丢 content。
	if len(m.ContentParts) > 0 || strings.TrimSpace(m.Content) != "" {
		return repairNone
	}
	switch m.Role {
	case "system":
		return repairDrop
	case "user":
		if len(m.ImagePaths) > 0 {
			return repairNone // 图片渲染层会补内容
		}
		return repairFillUser
	case "tool":
		return repairFillTool
	}
	return repairNone
}
