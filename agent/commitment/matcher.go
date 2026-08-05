// matcher.go 工具执行结果 → 匹配承诺 → 推进完成。
// 只相信工具执行结果,不信任模型"说完成"。匹配策略:精确 / 目录前缀 / 空目标任意。
package commitment

import "strings"

// TypeFromTool 工具名 → ActionType。
func TypeFromTool(tool string) ActionType {
	switch tool {
	case "Write":
		return ActionWriteFile
	case "Update":
		return ActionEditFile
	case "Bash":
		return ActionRunCommand
	case "Grep", "Search":
		return ActionSearch
	case "Read":
		return ActionReadFile
	default:
		return ActionUnknown
	}
}

// Matches 判断承诺是否命中某个工具目标 path:
//   - 承诺无目标(空) → 任意匹配;
//   - 精确等于 → 匹配;
//   - 目录前缀(path 以 target 开头且后随 / 或 \) → 匹配(如 target="config/" 命中 "config/a.yaml")。
func Matches(c *Commitment, path string) bool {
	if len(c.Targets) == 0 {
		return true
	}
	for _, t := range c.Targets {
		if t == path {
			return true
		}
		if strings.HasPrefix(path, t) && len(path) > len(t) {
			sep := path[len(t)]
			if sep == '/' || sep == '\\' {
				return true
			}
		}
	}
	return false
}

// Verify 工具执行成功后调用:找到类型匹配且未完成的承诺,命中目标则推进一个完成数。
// 返回是否推进了某个承诺(供诊断/测试)。
func (s *Store) Verify(tool, path string) bool {
	typ := TypeFromTool(tool)
	advanced := false
	for _, c := range s.items {
		if c.Type != typ || !c.Pending() {
			continue
		}
		if Matches(c, path) {
			c.markProgress()
			advanced = true
		}
	}
	// 验证类承诺由命令执行推进(Bash 常是验证载体,如 python check.py):
	// 工具为 Bash 时,同时匹配 RunCommand 与 Verify 型承诺。
	if tool == "Bash" {
		for _, c := range s.items {
			if c.Type == ActionVerify && c.Pending() {
				c.markProgress()
				advanced = true
			}
		}
	}
	return advanced
}

// Fail 工具执行失败时调用:把类型匹配的未完成承诺置 Failed(不再等待)。
func (s *Store) Fail(tool string) {
	typ := TypeFromTool(tool)
	for _, c := range s.items {
		if c.Type == typ {
			c.fail()
		}
	}
}

// AbandonPending 完成度门禁放行(不再追究)时调用:把所有未完成承诺置 Abandoned。
func (s *Store) AbandonPending() {
	for _, c := range s.items {
		c.abandon()
	}
}

// CancelPending 用户主动取消时调用(当前无触发路径,语义保留)。
func (s *Store) CancelPending() {
	for _, c := range s.items {
		c.cancel()
	}
}