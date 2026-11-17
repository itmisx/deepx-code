package config

import (
	"os"
	"strings"
	"testing"
)

// migrateEntry 的核心语义:官方域名上的过期 id 换新、顺带纠正当年预设写的窗口;
// 用户手调过的窗口和第三方中转的配置一律不碰。
func TestMigrateEntry(t *testing.T) {
	cases := []struct {
		name       string
		in         ModelEntry
		wantModel  string
		wantCtx    int
		wantChange bool
	}{
		{
			name:      "kimi 死 id 换新并把 256K 窗口补到 1M",
			in:        ModelEntry{BaseURL: "https://api.moonshot.cn/v1", Model: "kimi-k2.5", ContextWindow: 262_144},
			wantModel: "kimi-k3", wantCtx: 1_048_576, wantChange: true,
		},
		{
			name:      "用户手调过的窗口不动,只换 id",
			in:        ModelEntry{BaseURL: "https://api.moonshot.cn/v1", Model: "kimi-k2.6", ContextWindow: 131_072},
			wantModel: "kimi-k3.1-pro", wantCtx: 131_072, wantChange: true,
		},
		{
			name:      "kimi 的另一个官方域名同样迁移",
			in:        ModelEntry{BaseURL: "https://api.moonshot.ai/v1", Model: "kimi-k2.5"},
			wantModel: "kimi-k3", wantChange: true,
		},
		{
			name:      "mimo v2.5 下线 id",
			in:        ModelEntry{BaseURL: "https://api.xiaomimimo.com/v1", Model: "mimo-v2.5-pro"},
			wantModel: "mimo-v2.6-pro", wantChange: true,
		},
		{
			name:      "deepseek 旧 flash 名",
			in:        ModelEntry{BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-flash"},
			wantModel: "deepseek-flash", wantChange: true,
		},
		{
			name:      "qwen 工作空间专属域名(子域)也算官方",
			in:        ModelEntry{BaseURL: "https://llm-x.cn-beijing.maas.aliyuncs.com/compatible-mode/v1", Model: "qwen3.7-max"},
			wantModel: "qwen4-max", wantChange: true,
		},
		{
			name:      "没写 scheme 的 base_url 仍能识别 host",
			in:        ModelEntry{BaseURL: "api.deepseek.com", Model: "deepseek-v4-flash"},
			wantModel: "deepseek-flash", wantChange: true,
		},
		{
			name:      "第三方中转:老名字可能还活着,不碰",
			in:        ModelEntry{BaseURL: "https://relay.example.com/v1", Model: "kimi-k2.5"},
			wantModel: "kimi-k2.5", wantChange: false,
		},
		{
			name:      "现役 id 不动",
			in:        ModelEntry{BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-pro"},
			wantModel: "deepseek-v4-pro", wantChange: false,
		},
		{
			name:      "空 model 不动",
			in:        ModelEntry{BaseURL: "https://api.deepseek.com"},
			wantModel: "",
		},
	}
	for _, c := range cases {
		e := c.in
		if got := migrateEntry(&e); got != c.wantChange {
			t.Errorf("%s: migrateEntry = %v, want %v", c.name, got, c.wantChange)
		}
		if e.Model != c.wantModel {
			t.Errorf("%s: model = %q, want %q", c.name, e.Model, c.wantModel)
		}
		if c.wantCtx != 0 && e.ContextWindow != c.wantCtx {
			t.Errorf("%s: context_window = %d, want %d", c.name, e.ContextWindow, c.wantCtx)
		}
	}
}

// migrateConfig 必须走到两个 role —— 早期写法 a() || b() 会短路漏掉 pro。
func TestMigrateConfigBothRoles(t *testing.T) {
	c := &Config{
		Flash: ModelEntry{BaseURL: "https://api.xiaomimimo.com/v1", Model: "mimo-v2.5"},
		Pro:   ModelEntry{BaseURL: "https://api.xiaomimimo.com/v1", Model: "mimo-v2.5-pro"},
	}
	if !migrateConfig(c) {
		t.Fatal("migrateConfig = false, want true")
	}
	if c.Flash.Model != "mimo-v2.6-flash" || c.Pro.Model != "mimo-v2.6-pro" {
		t.Errorf("models = %q/%q, want mimo-v2.6-flash/mimo-v2.6-pro", c.Flash.Model, c.Pro.Model)
	}
}

// Migrate 把 model.yaml 落盘迁移;且不把 Load 推断出来的 max_tokens 顺手固化进文件。
func TestMigrateModelFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	stale := &Config{
		Flash: ModelEntry{BaseURL: "https://api.moonshot.cn/v1", Model: "kimi-k2.5", APIKey: "sk-k", ContextWindow: 262_144},
		Pro:   ModelEntry{BaseURL: "https://api.moonshot.cn/v1", Model: "kimi-k2.6", APIKey: "sk-k", ContextWindow: 262_144},
	}
	if err := Save(stale); err != nil {
		t.Fatal(err)
	}
	Migrate()

	p, _ := Path()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, dead := range []string{"kimi-k2.5", "kimi-k2.6", "262144"} {
		if strings.Contains(got, dead) {
			t.Errorf("model.yaml 仍含已下线值 %q:\n%s", dead, got)
		}
	}
	for _, want := range []string{"kimi-k3", "kimi-k3.1-pro", "sk-k"} {
		if !strings.Contains(got, want) {
			t.Errorf("model.yaml 缺 %q:\n%s", want, got)
		}
	}
	// max_tokens 原本没设(kimi 走模型默认),迁移不该把 Load 的推断值写进文件。
	if strings.Contains(got, "max_tokens: 131072") {
		t.Errorf("迁移把推断的 max_tokens 固化进了文件:\n%s", got)
	}
}

// 没有过期 id 时,Migrate 不该重写文件(字节不变)。
func TestMigrateNoopKeepsFileBytes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	if err := Save(DefaultFor("deepseek", "sk-ds")); err != nil {
		t.Fatal(err)
	}
	p, _ := Path()
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	Migrate()
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("无过期 id 却重写了 model.yaml:\n%s\n---\n%s", before, after)
	}
}

// provider.yaml 的存档也要迁移,否则 /provider 一切换就把死 id 写回 model.yaml。
func TestMigrateProviderFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	if err := SaveProvider("kimi", &Config{
		Flash: ModelEntry{BaseURL: "https://api.moonshot.cn/v1", Model: "kimi-k2.5", APIKey: "sk-k", ContextWindow: 262_144},
		Pro:   ModelEntry{BaseURL: "https://api.moonshot.cn/v1", Model: "kimi-k2.6", APIKey: "sk-k", ContextWindow: 262_144},
	}); err != nil {
		t.Fatal(err)
	}
	// 用户自己填的中转配置:不该被动。
	if err := SaveProvider("custom", &Config{
		Flash: ModelEntry{BaseURL: "https://relay.example.com/v1", Model: "kimi-k2.5", APIKey: "sk-x"},
		Pro:   ModelEntry{BaseURL: "https://relay.example.com/v1", Model: "kimi-k2.6", APIKey: "sk-x"},
	}); err != nil {
		t.Fatal(err)
	}
	Migrate()

	raw, err := loadProvidersRaw() // 读原始文件,确认是真落盘了而不是内存里换的
	if err != nil {
		t.Fatal(err)
	}
	if got := raw["kimi"]; got.Flash.Model != "kimi-k3" || got.Pro.Model != "kimi-k3.1-pro" ||
		got.Flash.ContextWindow != 1_048_576 {
		t.Errorf("kimi 存档未迁移: %+v", got)
	}
	if got := raw["custom"]; got.Flash.Model != "kimi-k2.5" || got.Pro.Model != "kimi-k2.6" {
		t.Errorf("中转存档被改了: %+v", got)
	}
}

// Load / LoadProviders 即使文件没落盘(HOME 只读等)也要在内存里给出现役 id。
func TestLoadMigratesInMemory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	stale := &Config{
		Flash: ModelEntry{BaseURL: "https://api.xiaomimimo.com/v1", Model: "mimo-v2.5", APIKey: "sk-m"},
		Pro:   ModelEntry{BaseURL: "https://api.xiaomimimo.com/v1", Model: "mimo-v2.5-pro", APIKey: "sk-m"},
	}
	if err := Save(stale); err != nil {
		t.Fatal(err)
	}
	if err := SaveProvider("mimo", stale); err != nil {
		t.Fatal(err)
	}

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Flash.Model != "mimo-v2.6-flash" || c.Pro.Model != "mimo-v2.6-pro" {
		t.Errorf("Load 未迁移: %q/%q", c.Flash.Model, c.Pro.Model)
	}

	pc, ok, err := LoadProvider("mimo")
	if err != nil || !ok {
		t.Fatalf("LoadProvider: ok=%v err=%v", ok, err)
	}
	if pc.Flash.Model != "mimo-v2.6-flash" || pc.Pro.Model != "mimo-v2.6-pro" {
		t.Errorf("LoadProvider 未迁移: %q/%q", pc.Flash.Model, pc.Pro.Model)
	}
}
