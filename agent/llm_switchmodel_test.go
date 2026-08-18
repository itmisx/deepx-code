package agent

import (
	"testing"
	"deepx/tools"
)

// TestLevelConfig 验证 4 级路由到模型配置的映射。
func TestLevelConfig(t *testing.T) {
	cases := []struct {
		level          int
		wantRole       string
		wantEffort     string
		wantThinking   string
	}{
		{0, tools.RoleFlash, "", ""},
		{1, tools.RoleFlash, "", "enabled"},
		{2, tools.RolePro, "medium", "disabled"},
		{3, tools.RolePro, "high", "disabled"},
	}
	for _, c := range cases {
		role, effort, thinking := levelConfig(c.level)
		if role != c.wantRole || effort != c.wantEffort || thinking != c.wantThinking {
			t.Errorf("levelConfig(%d) = (%s,%s,%s), want (%s,%s,%s)",
				c.level, role, effort, thinking, c.wantRole, c.wantEffort, c.wantThinking)
		}
	}
}

// TestSwitchModelCrossLevel 验证升级/降级的跨级判断。
func TestSwitchModelCrossLevel(t *testing.T) {
	// diff = target - current
	// 升级需 diff >= 2, 降级需 diff <= -1
	cases := []struct {
		current, target int
		shouldSwitch    bool
		dir             string
	}{
		{0, 2, true, "upgrade"},   // 跨 2 级升级 ✓
		{0, 3, true, "upgrade"},   // 跨 3 级升级 ✓
		{1, 3, true, "upgrade"},   // 跨 2 级升级 ✓
		{1, 2, false, ""},         // 跨 1 级升级 ✗ (需跨 2)
		{0, 1, false, ""},         // 跨 1 级升级 ✗
		{3, 1, true, "downgrade"}, // 跨 2 级降级 ✓
		{3, 2, true, "downgrade"}, // 跨 1 级降级 ✓
		{2, 1, true, "downgrade"}, // 跨 1 级降级 ✓
		{2, 2, false, ""},         // 同级 ✗
		{1, 0, true, "downgrade"}, // 跨 1 级降级 ✓
	}
	for _, c := range cases {
		diff := c.target - c.current
		shouldSwitch := diff >= 2 || diff <= -1
		if shouldSwitch != c.shouldSwitch {
			t.Errorf("current=%d target=%d diff=%d: shouldSwitch=%v, want %v",
				c.current, c.target, diff, shouldSwitch, c.shouldSwitch)
		}
	}
}
