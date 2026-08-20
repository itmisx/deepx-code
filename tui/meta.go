package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"deepx/tools"
)

// meta 是 ~/.deepx/meta.json 的全局元数据,集中保存语言选择 + 升级检查缓存。
// 之前分散在 ~/.deepx/lang(纯文本)和 ~/.deepx/upgrade_check.json(JSON)两个文件,
// 现在收口到一个文件,新增全局配置项直接往这里加字段即可。
type meta struct {
	Lang             string    `json:"lang,omitempty"`              // "zh" / "en"
	UpgradeCheckedAt time.Time `json:"upgrade_checked_at,omitzero"` // 上次打 GitHub API 的时间
	LatestVersion    string    `json:"latest_version,omitempty"`    // 最近一次拿到的 latest tag(去 v 前缀)
	UpgradeURL       string    `json:"upgrade_url,omitempty"`       // release 页 URL

	// ModelCaps 缓存每个 "模型@base_url" 探测出的能力。启动时按 key 查:命中即用、不重探;
	// 未命中才发一次最小探测(agent.ProbeVision),结果写回这里,下次启动直接命中。
	// 能力按维度拆开(本期只做 vision,预留 video / audio),不用笼统的 multimodal。
	ModelCaps map[string]modelCaps `json:"model_caps,omitempty"`

	// ModelPin 记忆最近一次 /model 选择("auto"/"flash"/"pro"),给「从没锁过模型」的
	// 会话当起手默认。issue #43 要的是"切完下次启动还生效",光存会话级 state.json 做不到:
	// /new 开的新会话没存过 pin,起手又回 auto,pro 的 token 照样被自动路由跑掉。
	// 两层叠加,会话自己的选择优先 —— 切回旧会话仍是它当时锁的那个,不受这里影响。
	ModelPin string `json:"model_pin,omitempty"`

	// HideStatus 记忆右侧状态栏的显隐选择(Ctrl+B / /status 切换),重启保持。
	HideStatus bool `json:"hide_status,omitempty"`

	// ShowThinking 记忆是否把模型思考(reasoning_content)暗显进对话流(/thinking 切换)。默认关。
	ShowThinking bool `json:"show_thinking,omitempty"`

	// MousePassthrough 记忆鼠标穿透模式(F2 切换):开启后关掉鼠标捕获,由终端原生处理
	// 文字选择与右键粘贴。需要它的用户(WSL2 / 习惯终端原生选择)是一直需要,故重启保持。
	MousePassthrough bool `json:"mouse_passthrough,omitempty"`

	// Sandbox 记忆沙箱模式("native"/"docker",/sandbox 切换)。空 = native(默认)。
	Sandbox string `json:"sandbox,omitempty"`
	// SandboxDockerImage 记忆 docker 沙箱用的镜像(/sandbox docker <image>)。空 = ubuntu:24.04。
	SandboxDockerImage string `json:"sandbox_docker_image,omitempty"`

	// 本地 web dashboard 配置(全局)。从前在 model.yaml,现统一收口到这里。
	WebDisabled bool   `json:"web_disabled,omitempty"` // 默认开启,设 true 才关闭(零值=开,符合 omitempty)
	WebHost     string `json:"web_host,omitempty"`     // 绑定地址,空=127.0.0.1 仅本机;0.0.0.0=局域网可访问
	WebPort     int    `json:"web_port,omitempty"`     // 0=随机端口
}

// webEnabled / webHost / webPort 从 meta.json 解析 web dashboard 配置,带默认值。
// 仅走配置文件(~/.deepx/meta.json),不再读环境变量。
func webEnabled() bool { return !metaGet().WebDisabled }

func webHost() string {
	if h := strings.TrimSpace(metaGet().WebHost); h != "" {
		return h
	}
	return "127.0.0.1" // 默认仅本机;手机/平板访问需显式设 0.0.0.0(见 issue #55)
}

func webPort() int { return metaGet().WebPort }

// modelPinDefault 返回全局记忆的 /model 选择,给「从没锁过模型」的会话当起手默认。
// 值域与 /model 一致(auto/flash/pro);空或非法一律回落 "auto",即自动语义路由。
// 容忍大小写与空白 —— 这个字段虽然由 /model 自动写,但用户手改 meta.json 也是合法用法。
func modelPinDefault() string {
	switch strings.ToLower(strings.TrimSpace(metaGet().ModelPin)) {
	case tools.RoleFlash:
		return tools.RoleFlash
	case tools.RolePro:
		return tools.RolePro
	}
	return "auto"
}

// modelCaps 是单个模型探测出的能力位,按维度独立存。
type modelCaps struct {
	Vision bool `json:"vision"`
}

var (
	metaMu     sync.Mutex
	metaLoaded bool
	metaState  meta
)

func metaPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".deepx", "meta.json")
}

// metaGet 返回当前 meta 的值拷贝。首次调用从磁盘加载(文件不存在时尝试从老的
// ~/.deepx/lang 迁移语言选择),之后走内存。多线程安全。
func metaGet() meta {
	metaMu.Lock()
	defer metaMu.Unlock()
	ensureMetaLoadedLocked()
	return metaState
}

// metaUpdate 在锁内 load-modify-write:fn 修改 *meta 后立即落盘。
// 这样 lang 写入不会覆盖 upgrade 字段,反之亦然。
func metaUpdate(fn func(*meta)) {
	metaMu.Lock()
	defer metaMu.Unlock()
	ensureMetaLoadedLocked()
	fn(&metaState)
	saveMetaLocked(metaState)
}

func ensureMetaLoadedLocked() {
	if metaLoaded {
		return
	}
	metaState = loadMetaLocked()
	metaLoaded = true
}

func loadMetaLocked() meta {
	p := metaPath()
	if p == "" {
		return meta{}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		// meta.json 还不存在 → 尝试迁移老的 lang 文件,upgrade 缓存丢了无所谓(会重拉)。
		return meta{Lang: migrateLegacyLang()}
	}
	var m meta
	if err := json.Unmarshal(data, &m); err != nil {
		return meta{}
	}
	return m
}

func saveMetaLocked(m meta) {
	p := metaPath()
	if p == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(p, data, 0o644)
}

// migrateLegacyLang 读老的 ~/.deepx/lang(纯文本 "zh"/"en")迁移语言选择。
// 读不到 / 内容不识别返回 "" —— 上层会回退默认中文。
func migrateLegacyLang() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".deepx", "lang"))
	if err != nil {
		return ""
	}
	switch strings.TrimSpace(string(data)) {
	case "en":
		return "en"
	case "zh":
		return "zh"
	}
	return ""
}
