package tui

import (
	"testing"

	"deepx/agent"
	"deepx/tools"
)

// TestDefaultModel 验证 meta.json 的 default_model 归一化:空/非法回退 auto,
// flash/pro 原样返回。语义与 /model 一致。
func TestDefaultModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // 写临时 ~/.deepx/meta.json,不碰真实配置
	cases := []struct {
		in   string
		want string
	}{
		{"", "auto"},
		{"auto", "auto"},
		{"flash", "flash"},
		{"pro", "pro"},
		{"bogus", "auto"},
		{"PRO", "auto"}, // 大小写敏感,非法回退 auto
	}
	for _, c := range cases {
		metaUpdate(func(m *meta) { m.DefaultModel = c.in })
		if got := defaultModel(); got != c.want {
			t.Errorf("defaultModel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRestoreModelPinDefaultMeta 验证 restoreModelPin 对空 pin 的回退逻辑:
// 用 meta.json 的 default_model 兜底起手模型,但显式保存的 /model 锁定优先。
func TestRestoreModelPinDefaultMeta(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := agent.ModelConfig{
		Flash: agent.ModelEntry{Model: "flash-x"},
		Pro:   agent.ModelEntry{Model: "pro-x"},
	}
	newModel := func() *model { return &model{models: cfg} }

	// 空 pin + default_model=flash → 起手 flash
	metaUpdate(func(m *meta) { m.DefaultModel = "flash" })
	m := newModel()
	m.restoreModelPin("")
	if m.modelPin != tools.RoleFlash {
		t.Errorf("空 pin + default flash: modelPin = %q, want %q", m.modelPin, tools.RoleFlash)
	}

	// 空 pin + default_model=pro → 起手 pro
	metaUpdate(func(m *meta) { m.DefaultModel = "pro" })
	m = newModel()
	m.restoreModelPin("")
	if m.modelPin != tools.RolePro {
		t.Errorf("空 pin + default pro: modelPin = %q, want %q", m.modelPin, tools.RolePro)
	}

	// 空 pin + default_model 为空 → 回退 auto(现有行为)
	metaUpdate(func(m *meta) { m.DefaultModel = "" })
	m = newModel()
	m.restoreModelPin("")
	if m.modelPin != "auto" {
		t.Errorf("空 pin + 无默认: modelPin = %q, want auto", m.modelPin)
	}

	// 显式 /model 锁定优先于 default_model
	metaUpdate(func(m *meta) { m.DefaultModel = "pro" })
	m = newModel()
	m.restoreModelPin("auto")
	if m.modelPin != "auto" {
		t.Errorf("显式 auto: modelPin = %q, want auto", m.modelPin)
	}
	m.restoreModelPin(tools.RoleFlash)
	if m.modelPin != tools.RoleFlash {
		t.Errorf("显式 flash: modelPin = %q, want %q", m.modelPin, tools.RoleFlash)
	}

	// default_model=flash 但 flash 未配置 → 回退 auto(与现有锁定逻辑一致)
	metaUpdate(func(m *meta) { m.DefaultModel = "flash" })
	m2 := &model{models: agent.ModelConfig{Pro: agent.ModelEntry{Model: "pro-x"}}}
	m2.restoreModelPin("")
	if m2.modelPin != "auto" {
		t.Errorf("default flash 未配置: modelPin = %q, want auto", m2.modelPin)
	}
}
