package config

// 过期 model id 的静默迁移。
//
// 厂商会下线旧 model id(mimo-v2.5 系列 2026-10-21、kimi-k2.5 系列更早),老用户的
// ~/.deepx/model.yaml 里还留着当年向导写进去的名字 —— 升级二进制不会动这份文件,结果是
// 每次请求都换来一个 404。这里在启动时把这些名字换成同族现役 id:
//
//   - Load / LoadProviders 在内存里换(即使文件写不动,比如 HOME 只读,也能正常跑);
//   - Migrate 把换好的结果落盘(model.yaml + provider.yaml),这样 /provider 的存档
//     不会把死 id 再塞回来。
//
// 全程不打扰用户:没有提示、没有确认,出错一律吞掉 —— 迁移失败最多退回"和没迁一样",
// 而内存那一层已经保证本次运行是对的。

import (
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// legacyModel 一条迁移规则:From(已下线 id)→ To(同族现役 id)。
type legacyModel struct {
	// Hosts 限定只有 base_url 指着这些官方域名(含子域)时才迁移。
	// 指向第三方中转的配置不碰 —— 中转可能仍然认老名字,那是用户自己填的,改它只会打架
	// (而且下次启动还会再改一次)。
	Hosts []string
	From  string // 小写、已 trim 的旧 model id
	To    string
	// StaleCtx / Ctx:当年预设写进 yaml 的 context_window → 新值。
	// 只有 yaml 里的值恰好等于 StaleCtx(说明是向导写的,不是用户手调的)才改。
	// Ctx 为 0 表示这一族的窗口没变,不用动。
	StaleCtx int
	Ctx      int
}

// legacyModels 覆盖本项目历史预设写过、而厂商已下线/改名的全部 model id
// (deepseek 自 0.1.0、mimo 自 8b2e0fd、kimi/qwen 自 57918b3,见 git log -- config/config.go)。
var legacyModels = []legacyModel{
	// deepseek-v4-flash 仍可用(官方把旧名路由到 Flash),但吃不到 V4.1 新版,一并换掉。
	{Hosts: []string{"deepseek.com"}, From: "deepseek-v4-flash", To: "deepseek-flash"},
	// mimo v2.5 系列 2026-10-21 下线,旧名直接失效。
	{Hosts: []string{"xiaomimimo.com"}, From: "mimo-v2.5", To: "mimo-v2.6-flash"},
	{Hosts: []string{"xiaomimimo.com"}, From: "mimo-v2.5-pro", To: "mimo-v2.6-pro"},
	// kimi-k2.5 已下线(404);k2.6 在"2026 年底停服"名单里。两者窗口 256K → k3 系列 1M。
	{Hosts: []string{"moonshot.cn", "moonshot.ai"}, From: "kimi-k2.5", To: "kimi-k3", StaleCtx: 262_144, Ctx: 1_048_576},
	{Hosts: []string{"moonshot.cn", "moonshot.ai"}, From: "kimi-k2.6", To: "kimi-k3.1-pro", StaleCtx: 262_144, Ctx: 1_048_576},
	// qwen3.7-max 已不在现役列表;plus 档并入 qwen3.8-flash。两者都是 1M,窗口不用动。
	{Hosts: []string{"aliyuncs.com"}, From: "qwen3.7-plus", To: "qwen3.8-flash"},
	{Hosts: []string{"aliyuncs.com"}, From: "qwen3.7-max", To: "qwen4-max"},
}

// baseURLHost 从 base_url 取主机名。base_url 允许不写 scheme(host/path 形式),
// 那种写法 url.Parse 会把整串当 Path,所以再手动切一次。
func baseURLHost(baseURL string) string {
	s := strings.ToLower(strings.TrimSpace(baseURL))
	if s == "" {
		return ""
	}
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		return u.Hostname()
	}
	h := strings.SplitN(strings.TrimPrefix(s, "//"), "/", 2)[0]
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i] // 去掉端口
	}
	return h
}

// hostMatches 报告 base_url 的主机名是否命中任一域名后缀(本域或其子域)。
func hostMatches(baseURL string, suffixes []string) bool {
	h := baseURLHost(baseURL)
	if h == "" {
		return false
	}
	for _, s := range suffixes {
		if h == s || strings.HasSuffix(h, "."+s) {
			return true
		}
	}
	return false
}

// migrateEntry 把一个 role 的过期 model id 换成同族现役 id,返回是否改过。
func migrateEntry(e *ModelEntry) bool {
	m := strings.ToLower(strings.TrimSpace(e.Model))
	if m == "" {
		return false
	}
	for _, lm := range legacyModels {
		if m != lm.From || !hostMatches(e.BaseURL, lm.Hosts) {
			continue
		}
		e.Model = lm.To
		if lm.Ctx > 0 && e.ContextWindow == lm.StaleCtx {
			e.ContextWindow = lm.Ctx
		}
		return true
	}
	return false
}

// migrateConfig 迁移一份配置的 flash + pro,返回是否改过。
// 两个 role 都要走到,所以不能写成 migrateEntry(flash) || migrateEntry(pro)(会短路)。
func migrateConfig(c *Config) bool {
	flash := migrateEntry(&c.Flash)
	pro := migrateEntry(&c.Pro)
	return flash || pro
}

// Migrate 把 model.yaml 和 provider.yaml 里的过期 model id 就地换成现役 id 并落盘。
// 静默、best-effort:没有过期 id 就一个字节都不写,任何错误都吞掉(内存层已兜住本次运行)。
// 在读配置前调一次即可(见 main.loadOrEmptyConfig)。
func Migrate() {
	migrateModelFile()
	migrateProviderFile()
}

// migrateModelFile 迁移 ~/.deepx/model.yaml。
// 刻意绕开 Load() 读原始 yaml —— Load 会给缺失的 context_window / max_tokens 填推断值,
// 那些值不该被这次迁移顺手固化进用户的文件里。
func migrateModelFile() {
	p, err := Path()
	if err != nil {
		return
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return
	}
	if !migrateConfig(&c) {
		return
	}
	_ = Save(&c)
}

// migrateProviderFile 迁移 ~/.deepx/provider.yaml 里每个供应商存档。
// 不迁的话 /provider 一切换就把死 id 从存档写回 model.yaml。
func migrateProviderFile() {
	ps, err := loadProvidersRaw()
	if err != nil || len(ps) == 0 {
		return
	}
	changed := false
	for name, c := range ps {
		if migrateConfig(&c) {
			ps[name] = c
			changed = true
		}
	}
	if !changed {
		return
	}
	_ = saveProviders(ps)
}
