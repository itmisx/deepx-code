// detector.go 从模型输出中发现"未来动作"承诺(规则阶段,中英双语)。
// 输入 = assistant reasoning + content(thinking 模型常在思考里声明动作,不能只看 final)。
package commitment

import (
	"regexp"
	"strconv"
	"strings"
)

// commitmentRe:承诺引导词 + 执行动词。中英双语 —— reasoning 模型在技术场景常用
// 英文思考与输出,只匹配中文会漏检(实测 "Now batch 3: Write all 10." 无工具调用)。
var commitmentRe = regexp.MustCompile(`(?i)(将|先|接下来|下一步|准备|马上|待会|继续)(?:要|去|给)?(?:继续)?(写|创建|生成|执行|调用|修改|更新|新建|开始|补充|添加|替换|删除|搜索|查找|读取|验证|校验|核实)|(will|gonna|about to|let me|going to)[\s,;:]+(write|create|generate|execute|call|run|add|update|build|make|start|search|find|read|verify|check|validate)|\b(now|next)[^.\n]{0,50}?\b(write|create|generate|execute|run|call|build|make|search|find|read|verify|check)\b`)

// explanatoryRe:解释性措辞("说明/解释怎么做",而非要执行),命中则不算承诺,防误报
// (如 "I will explain how to write files"、"下面说明文件生成方案")。
var explanatoryRe = regexp.MustCompile(`(?i)(说明|解释|介绍|概述|说明一下|explain|describe|how to|tutorial|overview|walk through)`)

// numRe:提取声明数量(如 "30 files"、"10 个文件"),用于任务级承诺的 Expected。
var numRe = regexp.MustCompile(`(?i)(\d{1,4})\s*(files?|个|份|个文件)`)

// Detect 从模型文本提取"承诺要执行的动作"。文本含解释性措辞或未命中承诺模式 → 返回空。
// 返回的 Commitment 均为 Pending 状态;Expected 取文本声明的数量(提取不到则 1)。
func Detect(text string) []*Commitment {
	if text == "" || explanatoryRe.MatchString(text) || !commitmentRe.MatchString(text) {
		return nil
	}
	typ := ActionWriteFile
	if m := commitmentRe.FindStringSubmatch(text); m != nil {
		if m[1] != "" {
			typ = typeFromZhAction(m[1])
		} else if m[2] != "" {
			typ = typeFromEnAction(m[2])
		}
	}
	expected := 1
	if m := numRe.FindStringSubmatch(text); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			expected = n
		}
	}
	return []*Commitment{{Type: typ, Expected: expected, Status: Pending}}
}

// typeFromZhAction 中文动作词 → ActionType。
func typeFromZhAction(zh string) ActionType {
	switch zh {
	case "写", "创建", "生成", "新建", "补充", "添加":
		return ActionWriteFile
	case "修改", "更新", "替换", "删除":
		return ActionEditFile
	case "执行", "运行":
		return ActionRunCommand
	case "调用":
		return ActionRunCommand
	case "搜索", "查找":
		return ActionSearch
	case "读取":
		return ActionReadFile
	case "验证", "校验", "核实":
		return ActionVerify
	default:
		return ActionWriteFile
	}
}

// typeFromEnAction 英文动作词 → ActionType。
func typeFromEnAction(en string) ActionType {
	switch strings.ToLower(en) {
	case "write", "create", "generate", "build", "make":
		return ActionWriteFile
	case "update", "modify", "add":
		return ActionEditFile
	case "run", "execute", "call", "start":
		return ActionRunCommand
	case "search", "find":
		return ActionSearch
	case "read":
		return ActionReadFile
	case "verify", "check", "validate":
		return ActionVerify
	default:
		return ActionWriteFile
	}
}
