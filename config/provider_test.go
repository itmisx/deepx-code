package config

import (
	"strings"
	"testing"
)

// TestProviderRoundTrip 验证 provider.yaml 的存档/读取/列名闭环:
// SaveProvider upsert、LoadProvider 取回原值、ProviderNames 按预设顺序排列。
func TestProviderRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())        // ProviderPath 走 os.UserHomeDir() → $HOME
	t.Setenv("USERPROFILE", t.TempDir()) // Windows 兜底,避免在该平台读到真实 home

	// 初始为空。
	if names, err := ProviderNames(); err != nil || len(names) != 0 {
		t.Fatalf("空 provider.yaml 应返回空列表, got %v err=%v", names, err)
	}

	ds := &Config{
		Flash: ModelEntry{BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-flash", APIKey: "sk-ds"},
		Pro:   ModelEntry{BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-pro", APIKey: "sk-ds"},
	}
	cu := &Config{
		Flash: ModelEntry{BaseURL: "https://x.example/v1", Model: "x-small", APIKey: "sk-x", MaxTokens: 4096},
		Pro:   ModelEntry{BaseURL: "https://x.example/v1", Model: "x-large", APIKey: "sk-x", MaxTokens: 8192},
	}
	if err := SaveProvider("deepseek", ds); err != nil {
		t.Fatal(err)
	}
	if err := SaveProvider("custom", cu); err != nil {
		t.Fatal(err)
	}

	// ProviderNames:预设顺序优先(deepseek 在 custom 前)。
	names, err := ProviderNames()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "deepseek" || names[1] != "custom" {
		t.Fatalf("期望 [deepseek custom], got %v", names)
	}

	// LoadProvider 取回原值。
	got, ok, err := LoadProvider("custom")
	if err != nil || !ok {
		t.Fatalf("custom 应存在, ok=%v err=%v", ok, err)
	}
	if got.Flash.Model != "x-small" || got.Pro.Model != "x-large" || got.Pro.MaxTokens != 8192 {
		t.Fatalf("custom 配置读回不一致: %+v", got)
	}

	// 不存在的供应商。
	if _, ok, _ := LoadProvider("nope"); ok {
		t.Fatal("不存在的供应商应返回 ok=false")
	}

	// upsert 覆盖同名,且不影响其它供应商。
	ds2 := &Config{Flash: ModelEntry{Model: "deepseek-v5-flash"}, Pro: ModelEntry{Model: "deepseek-v5-pro"}}
	if err := SaveProvider("deepseek", ds2); err != nil {
		t.Fatal(err)
	}
	got2, _, _ := LoadProvider("deepseek")
	if got2.Flash.Model != "deepseek-v5-flash" {
		t.Fatalf("deepseek 应被覆盖为 v5, got %q", got2.Flash.Model)
	}
	if cuStill, ok, _ := LoadProvider("custom"); !ok || cuStill.Flash.Model != "x-small" {
		t.Fatal("覆盖 deepseek 不应影响 custom")
	}
}

// TestProviderNameRules 覆盖自定义供应商名的规范化与校验规则:
// 空 → custom;大小写/空白被规范化;非法字符被挡;预设名是保留字。
func TestProviderNameRules(t *testing.T) {
	if got := NormalizeProviderName("  "); got != ProviderCustom {
		t.Fatalf("空名应回退 %q, got %q", ProviderCustom, got)
	}
	if got := NormalizeProviderName("  OpenRouter "); got != "openrouter" {
		t.Fatalf("规范化应去空白+转小写, got %q", got)
	}

	for _, ok := range []string{"openrouter", "my-relay", "gw_2", "a", "v1.5"} {
		if !ValidProviderName(ok) {
			t.Fatalf("%q 应合法", ok)
		}
	}
	for _, bad := range []string{"", "-lead", ".dot", "has space", "UPPER", strings.Repeat("a", 33)} {
		if ValidProviderName(bad) {
			t.Fatalf("%q 应非法", bad)
		}
	}

	for _, p := range []string{"deepseek", "mimo", "kimi", "qwen"} {
		if !IsPresetProvider(p) {
			t.Fatalf("%q 应是预设供应商名", p)
		}
	}
	// custom 是"没起名字"的默认落点,不算保留字;未知名字也不算。
	if IsPresetProvider(ProviderCustom) || IsPresetProvider("openrouter") {
		t.Fatal("custom / 未知名字都不应被当成预设供应商名")
	}
}

// TestMultipleNamedCustomProviders 验证本次改动的核心诉求:多份自定义配置按各自的名字
// 各占一个槽,互不覆盖,且都能被 ProviderNames 列出、被 LoadProvider 取回。
func TestMultipleNamedCustomProviders(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	or := &Config{Flash: ModelEntry{BaseURL: "https://openrouter.ai/api/v1", Model: "ox-alpha", APIKey: "sk-or"}}
	relay := &Config{Flash: ModelEntry{BaseURL: "https://relay.local/v1", Model: "gpt-4o-mini", APIKey: "sk-relay"}}
	if err := SaveProvider("openrouter", or); err != nil {
		t.Fatal(err)
	}
	if err := SaveProvider("my-relay", relay); err != nil {
		t.Fatal(err)
	}

	got, ok, err := LoadProvider("openrouter")
	if err != nil || !ok || got.Flash.Model != "ox-alpha" {
		t.Fatalf("openrouter 应原样取回, ok=%v err=%v got=%+v", ok, err, got)
	}
	if got, ok, _ := LoadProvider("my-relay"); !ok || got.Flash.Model != "gpt-4o-mini" {
		t.Fatal("存第二个自定义供应商不应覆盖第一个")
	}

	// 两个自定义名都不在 ProviderOptions 里 → 按字母序排在预设之后。
	names, err := ProviderNames()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "my-relay" || names[1] != "openrouter" {
		t.Fatalf("期望 [my-relay openrouter], got %v", names)
	}
}

// TestProviderNamesOrder 固定 /provider 列表的排序:预设(按 ProviderOptions 顺序)→
// 具名自定义(字母序)→ custom 垫底。
func TestProviderNamesOrder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	// 故意打乱写入顺序:custom 最先写、预设夹在中间,验证排序不依赖写入次序。
	for _, n := range []string{"custom", "openrouter", "qwen", "deepseek", "my-relay", "mimo"} {
		if err := SaveProvider(n, &Config{Flash: ModelEntry{Model: n}}); err != nil {
			t.Fatal(err)
		}
	}

	names, err := ProviderNames()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"deepseek", "mimo", "qwen", "my-relay", "openrouter", "custom"}
	if len(names) != len(want) {
		t.Fatalf("期望 %v, got %v", want, names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("期望 %v, got %v", want, names)
		}
	}
}

// TestProviderNamesNoCustom 没存过 custom 时,列表里不该凭空多出一项。
func TestProviderNamesNoCustom(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	if err := SaveProvider("openrouter", &Config{Flash: ModelEntry{Model: "ox-alpha"}}); err != nil {
		t.Fatal(err)
	}
	names, err := ProviderNames()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "openrouter" {
		t.Fatalf("期望 [openrouter], got %v", names)
	}
}

// TestDeleteProvider 验证存档删除:删掉的名字消失、其余不受影响、删不存在的名字是幂等成功。
func TestDeleteProvider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	for _, n := range []string{"openrouter", "my-relay"} {
		if err := SaveProvider(n, &Config{Flash: ModelEntry{Model: n}}); err != nil {
			t.Fatal(err)
		}
	}
	// 改名场景:存新名 + 删旧名。
	if err := DeleteProvider("openrouter"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := LoadProvider("openrouter"); ok {
		t.Fatal("删除后不应还能读到")
	}
	if _, ok, _ := LoadProvider("my-relay"); !ok {
		t.Fatal("删一个不应影响另一个")
	}
	// 幂等:删不存在的名字不报错。
	if err := DeleteProvider("openrouter"); err != nil {
		t.Fatalf("删不存在的名字应幂等成功, got %v", err)
	}
	// 文件本身没坏,仍能正常列名。
	names, err := ProviderNames()
	if err != nil || len(names) != 1 || names[0] != "my-relay" {
		t.Fatalf("期望 [my-relay], got %v err=%v", names, err)
	}
}
