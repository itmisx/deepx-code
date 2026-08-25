package tui

import (
	"strings"
	"testing"

	"deepx/config"
)

// fillCustomFields 造一个只填了自定义表单的 model:按 setupCustomFieldDefs 的顺序塞值(缺的留空)。
func fillCustomFields(vals map[int]string) *model {
	m := &model{setupCustomFields: newSetupCustomFields()}
	for i, v := range vals {
		m.setupCustomFields[i].SetValue(v)
	}
	return m
}

// TestCustomFieldIndexesMatchDefs 守住字段下标常量与 setupCustomFieldDefs 的对应关系 ——
// 往表里插字段而忘了改常量,会让 api_key 被当成 base_url 存下去,这里直接把它挡住。
func TestCustomFieldIndexesMatchDefs(t *testing.T) {
	want := []struct {
		idx   int
		group string
		label string
	}{
		{fCustomName, "Provider", "name"},
		{fCustomFlashBaseURL, "Flash", "base_url"},
		{fCustomFlashModel, "Flash", "model"},
		{fCustomFlashAPIKey, "Flash", "api_key"},
		{fCustomFlashMaxTokens, "Flash", "max_tokens"},
		{fCustomFlashCtxWindow, "Flash", "context_window"},
		{fCustomProBaseURL, "Pro", "base_url"},
		{fCustomProModel, "Pro", "model"},
		{fCustomProAPIKey, "Pro", "api_key"},
		{fCustomProMaxTokens, "Pro", "max_tokens"},
		{fCustomProCtxWindow, "Pro", "context_window"},
	}
	if len(want) != len(setupCustomFieldDefs) {
		t.Fatalf("字段数对不上: 常量 %d 个, defs %d 个", len(want), len(setupCustomFieldDefs))
	}
	for _, w := range want {
		def := setupCustomFieldDefs[w.idx]
		if def.group != w.group || def.label != w.label {
			t.Fatalf("下标 %d 应是 %s/%s, got %s/%s", w.idx, w.group, w.label, def.group, def.label)
		}
	}
}

// TestBuildCustomConfigName 覆盖供应商名的三条路径:留空回退 custom、正常起名、非法/保留名报错。
func TestBuildCustomConfigName(t *testing.T) {
	base := map[int]string{
		fCustomFlashBaseURL: "https://openrouter.ai/api/v1",
		fCustomFlashModel:   "ox-alpha",
		fCustomFlashAPIKey:  "sk-or",
	}
	with := func(name string) map[int]string {
		v := map[int]string{fCustomName: name}
		for k, s := range base {
			v[k] = s
		}
		return v
	}

	// 留空 → custom(老行为)。
	cfg, name, errMsg := fillCustomFields(with("")).buildCustomConfig()
	if errMsg != "" || name != "custom" {
		t.Fatalf("留空应存为 custom, name=%q err=%q", name, errMsg)
	}
	// pro 留空继承 flash —— 顺带确认下标没串位。
	if cfg.Pro.BaseURL != "https://openrouter.ai/api/v1" || cfg.Pro.Model != "ox-alpha" || cfg.Pro.APIKey != "sk-or" {
		t.Fatalf("pro 应继承 flash, got %+v", cfg.Pro)
	}

	// 起名 + 规范化(大小写/空白)。
	_, name, errMsg = fillCustomFields(with("  OpenRouter ")).buildCustomConfig()
	if errMsg != "" || name != "openrouter" {
		t.Fatalf("应规范化为 openrouter, name=%q err=%q", name, errMsg)
	}

	// 非法字符。
	if _, _, errMsg = fillCustomFields(with("my provider")).buildCustomConfig(); errMsg == "" {
		t.Fatal("含空格的名字应被拒")
	}

	// 预设名是保留字,错误信息里要带上是哪个名字。
	_, _, errMsg = fillCustomFields(with("deepseek")).buildCustomConfig()
	if errMsg == "" || !strings.Contains(errMsg, "deepseek") {
		t.Fatalf("预设名应被拒且提示名字, got %q", errMsg)
	}

	// flash 缺 api_key 仍按原规则报错(名字合法不该放行不完整的 flash)。
	if _, _, errMsg = fillCustomFields(map[int]string{
		fCustomName:         "openrouter",
		fCustomFlashBaseURL: "https://openrouter.ai/api/v1",
		fCustomFlashModel:   "ox-alpha",
	}).buildCustomConfig(); errMsg == "" {
		t.Fatal("flash 缺 api_key 应报错")
	}
}

// savedCfg 造一份可辨识的自定义供应商配置。
func savedCfg(tag string) *config.Config {
	return &config.Config{
		Flash: config.ModelEntry{
			BaseURL: "https://" + tag + ".example/v1", Model: tag + "-small", APIKey: "sk-" + tag,
			MaxTokens: 4096, ContextWindow: 65536,
		},
		Pro: config.ModelEntry{
			BaseURL: "https://" + tag + ".example/v1", Model: tag + "-large", APIKey: "sk-" + tag,
			// MaxTokens 故意留 0(未设,走模型默认)—— 预填时该字段应留空,而不是填个凭空的数字。
			ContextWindow: 131072,
		},
	}
}

// TestRefreshSetupProviders 验证 /config 第一步的候选列表:预设段在前(含「其它(自定义)」),
// 已保存的自定义段跟在后面,setupSavedStart 正好指向分界。
func TestRefreshSetupProviders(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	for _, n := range []string{"deepseek", "openrouter", "my-relay", "custom"} {
		if err := config.SaveProvider(n, savedCfg(n)); err != nil {
			t.Fatal(err)
		}
	}

	m := &model{}
	m.refreshSetupProviders()

	if m.setupSavedStart != len(config.ProviderOptions) {
		t.Fatalf("分界应在预设段之后, got %d", m.setupSavedStart)
	}
	// 预设段原样,deepseek 虽已存档也不该在已保存段里重复出现。
	for i, p := range config.ProviderOptions {
		if m.setupProviders[i] != p {
			t.Fatalf("预设段第 %d 项应是 %q, got %q", i, p, m.setupProviders[i])
		}
	}
	saved := m.setupProviders[m.setupSavedStart:]
	want := []string{"my-relay", "openrouter", "custom"} // 字母序 + custom 垫底
	if len(saved) != len(want) {
		t.Fatalf("已保存段期望 %v, got %v", want, saved)
	}
	for i := range want {
		if saved[i] != want[i] {
			t.Fatalf("已保存段期望 %v, got %v", want, saved)
		}
	}
}

// TestSetupFormRouting 验证第二步走哪套表单、算不算「二次修改」:
// 预设 → api_key 单框;「其它(自定义)」→ 空表单新建;已保存段 → 预填表单。
func TestSetupFormRouting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	if err := config.SaveProvider("openrouter", savedCfg("openrouter")); err != nil {
		t.Fatal(err)
	}

	m := &model{}
	m.refreshSetupProviders()

	idxOf := func(name string) int {
		for i, p := range m.setupProviders {
			if p == name {
				return i
			}
		}
		t.Fatalf("列表里找不到 %q: %v", name, m.setupProviders)
		return -1
	}

	// 预设:单 api_key 框,不算二次修改。
	m.setupProviderIdx = idxOf("deepseek")
	if m.setupIsCustomForm() || m.setupEditingSaved() {
		t.Fatal("预设供应商应走 api_key 单框、且不算二次修改")
	}
	// 「其它(自定义)」:多字段表单,但是新建。
	m.setupProviderIdx = idxOf(config.ProviderCustom)
	if !m.setupIsCustomForm() || m.setupEditingSaved() {
		t.Fatal("「其它(自定义)」应走表单且算新建")
	}
	// 已保存的自定义:多字段表单 + 二次修改。
	m.setupProviderIdx = idxOf("openrouter")
	if !m.setupIsCustomForm() || !m.setupEditingSaved() {
		t.Fatal("已保存的自定义应走表单且算二次修改")
	}
}

// TestPrefillSavedProvider 验证二次修改的闭环:选中已存的自定义 → Enter 预填 →
// 不改任何字段直接构造,得到的配置与存档一致(名字也还是原名,即覆盖同一个槽)。
func TestPrefillSavedProvider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	orig := savedCfg("openrouter")
	if err := config.SaveProvider("openrouter", orig); err != nil {
		t.Fatal(err)
	}

	m := &model{}
	m.refreshSetupProviders()
	for i, p := range m.setupProviders {
		if p == "openrouter" {
			m.setupProviderIdx = i
		}
	}
	m.enterSetupStep2()

	if m.setupStep != 1 || len(m.setupCustomFields) != len(setupCustomFieldDefs) {
		t.Fatalf("应进入第二步并铺好表单, step=%d fields=%d", m.setupStep, len(m.setupCustomFields))
	}
	// Pro.MaxTokens 存档里是 0(未设)→ 预填应留空,不该凭空写个数字。
	if got := m.setupCustomFields[fCustomProMaxTokens].Value(); got != "" {
		t.Fatalf("未设的 max_tokens 应预填为空, got %q", got)
	}

	cfg, name, errMsg := m.buildCustomConfig()
	if errMsg != "" {
		t.Fatalf("预填后不改动应能直接保存, err=%q", errMsg)
	}
	if name != "openrouter" {
		t.Fatalf("名字应预填为原名(覆盖同槽), got %q", name)
	}
	if cfg.Flash != orig.Flash {
		t.Fatalf("flash 应原样回填\nwant %+v\ngot  %+v", orig.Flash, cfg.Flash)
	}
	// pro 的 max_tokens 留空 → 落到通用默认(而不是继承 flash 的 4096)。
	if cfg.Pro.MaxTokens != config.CustomDefaultMaxTokens {
		t.Fatalf("留空的 max_tokens 应取通用默认 %d, got %d", config.CustomDefaultMaxTokens, cfg.Pro.MaxTokens)
	}
	if cfg.Pro.BaseURL != orig.Pro.BaseURL || cfg.Pro.Model != orig.Pro.Model || cfg.Pro.ContextWindow != orig.Pro.ContextWindow {
		t.Fatalf("pro 其余字段应原样回填\nwant %+v\ngot  %+v", orig.Pro, cfg.Pro)
	}
}

// TestNewCustomFormNotPrefilled 「其它(自定义)」是新建入口:哪怕 custom 槽里已有存档,
// 表单也必须是空的(预填后用户想改还得先删,反而麻烦 —— 要改就走已保存段)。
func TestNewCustomFormNotPrefilled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	if err := config.SaveProvider(config.ProviderCustom, savedCfg("old")); err != nil {
		t.Fatal(err)
	}

	m := &model{}
	m.refreshSetupProviders()
	for i, p := range m.setupProviders {
		if p == config.ProviderCustom && i < m.setupSavedStart {
			m.setupProviderIdx = i
		}
	}
	m.enterSetupStep2()
	for i, f := range m.setupCustomFields {
		if f.Value() != "" {
			t.Fatalf("新建表单第 %d 个字段应为空, got %q", i, f.Value())
		}
	}
}

// setupWithSaved 造一个开着 /config 第一步、光标停在 name 上的 model。
func setupWithSaved(t *testing.T, names []string, cursorOn string) *model {
	t.Helper()
	for _, n := range names {
		if err := config.SaveProvider(n, savedCfg(n)); err != nil {
			t.Fatal(err)
		}
	}
	m := &model{chatContent: newChatLog(1 << 20)} // 删除成功会 appendChat,得有 chatLog
	m.refreshSetupProviders()
	for i, p := range m.setupProviders {
		if p == cursorOn && i >= m.setupSavedStart {
			m.setupProviderIdx = i
		}
	}
	return m
}

// TestDeleteSavedProviderTwoStep 删除要按两次 d:第一次只亮确认,第二次才落盘。
func TestDeleteSavedProviderTwoStep(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	m := setupWithSaved(t, []string{"openrouter", "my-relay"}, "openrouter")

	// 第一次:只记下待删,存档还在。
	m.setupDeleteSelected()
	if m.setupDeleteConfirm != "openrouter" {
		t.Fatalf("第一次按 d 应记下待删名, got %q", m.setupDeleteConfirm)
	}
	if _, ok, _ := config.LoadProvider("openrouter"); !ok {
		t.Fatal("第一次按 d 不该真删")
	}

	// 第二次:真删,确认状态清空,列表里没了,另一个不受影响。
	m.setupDeleteSelected()
	if m.setupDeleteConfirm != "" {
		t.Fatalf("删完应清空待确认, got %q", m.setupDeleteConfirm)
	}
	if _, ok, _ := config.LoadProvider("openrouter"); ok {
		t.Fatal("第二次按 d 应真删")
	}
	if _, ok, _ := config.LoadProvider("my-relay"); !ok {
		t.Fatal("不该误删别的供应商")
	}
	for _, p := range m.setupProviders {
		if p == "openrouter" {
			t.Fatal("删完列表应立即刷新")
		}
	}
	// 提示语要写清"只删存档、不动 model.yaml",免得用户以为当前配置也没了。
	if log := m.chatContent.String(); !strings.Contains(log, "openrouter") || !strings.Contains(log, "model.yaml") {
		t.Fatalf("删除提示不完整: %q", log)
	}
	// 光标不能越界(删的是最后一项时尤其容易踩)。
	if m.setupProviderIdx < 0 || m.setupProviderIdx >= len(m.setupProviders) {
		t.Fatalf("光标越界: idx=%d len=%d", m.setupProviderIdx, len(m.setupProviders))
	}
}

// TestDeleteOnlyAllowedForSaved 预设项和「其它(自定义)」是固定入口不是存档,按 d 应被挡下,
// 且不留下待确认状态(否则移到别处再按一次 d 就误删了)。
func TestDeleteOnlyAllowedForSaved(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	m := setupWithSaved(t, []string{"openrouter"}, "openrouter")

	for _, target := range []string{"deepseek", config.ProviderCustom} {
		for i, p := range m.setupProviders {
			if p == target && i < m.setupSavedStart {
				m.setupProviderIdx = i
			}
		}
		m.setupDeleteSelected()
		if m.setupErr == "" {
			t.Fatalf("停在 %q 上按 d 应报错", target)
		}
		if m.setupDeleteConfirm != "" {
			t.Fatalf("停在 %q 上按 d 不该留下待确认状态", target)
		}
	}
	if _, ok, _ := config.LoadProvider("openrouter"); !ok {
		t.Fatal("存档不该被动到")
	}
}

// TestDeleteConfirmIsPerName 待确认状态绑定具体名字:在 A 上按了 d,移到 B 再按 d
// 只应变成"待删 B",不能直接把 B 删掉。
func TestDeleteConfirmIsPerName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	m := setupWithSaved(t, []string{"openrouter", "my-relay"}, "openrouter")

	m.setupDeleteSelected() // 待删 openrouter
	for i, p := range m.setupProviders {
		if p == "my-relay" {
			m.setupProviderIdx = i
		}
	}
	m.setupDeleteSelected() // 换了目标 → 只重新记待删,不该删
	if m.setupDeleteConfirm != "my-relay" {
		t.Fatalf("待确认应改成 my-relay, got %q", m.setupDeleteConfirm)
	}
	if _, ok, _ := config.LoadProvider("my-relay"); !ok {
		t.Fatal("换目标后的第一次 d 不该真删")
	}
	if _, ok, _ := config.LoadProvider("openrouter"); !ok {
		t.Fatal("原目标也不该被删")
	}
}
