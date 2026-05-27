package tui

import (
	"sync/atomic"
)

// Lang 是支持的语言枚举。当前只有中英两档。
type Lang string

const (
	LangZH Lang = "zh"
	LangEN Lang = "en"
)

// currentLang 是进程当前语言。atomic.Value 允许 /lang 切换时无锁读写,
// View() 渲染线程跟 Update() 修改线程读到的值各自一致。
var currentLang atomic.Value

func init() {
	currentLang.Store(loadLangFromDisk())
}

// CurrentLang 返回当前语言。
func CurrentLang() Lang {
	if v, ok := currentLang.Load().(Lang); ok {
		return v
	}
	return LangZH
}

// SetLang 切换语言并持久化到 ~/.deepx/meta.json。
// 失败(权限/磁盘满)不致命,只是下次启动回退到默认中文。
func SetLang(l Lang) {
	if l != LangEN {
		l = LangZH
	}
	currentLang.Store(l)
	metaUpdate(func(m *meta) { m.Lang = string(l) })
}

// T 按当前语言查表,key 找不到回退 key 本身(方便开发期定位漏译)。
func T(key string) string {
	l := CurrentLang()
	if bundle, ok := translations[key]; ok {
		if v, ok := bundle[l]; ok && v != "" {
			return v
		}
		// 缺当前语言的翻译时,回退中文(默认全集)
		if v, ok := bundle[LangZH]; ok {
			return v
		}
	}
	return key
}

// loadLangFromDisk 从 ~/.deepx/meta.json 读语言。没设置 / 不识别回退中文。
func loadLangFromDisk() Lang {
	if Lang(metaGet().Lang) == LangEN {
		return LangEN
	}
	return LangZH
}

// translations 是所有 UI 字符串的多语言映射。key 用 dotted-namespace 风格,方便分组管理。
// 新增 key 时记得每个语言都填,缺译会回退中文(开发期没问题,生产可见时显得不专业)。
var translations = map[string]map[Lang]string{
	// === Slash 命令描述 ===
	"cmd.plan.desc": {
		LangZH: "切到只读模式",
		LangEN: "Switch to read-only mode",
	},
	"cmd.auto.desc": {
		LangZH: "切回全工具模式",
		LangEN: "Switch back to full-tools mode",
	},
	"cmd.review.desc": {
		LangZH: "切到审核模式",
		LangEN: "Switch to review mode",
	},
	"cmd.mode.desc": {
		LangZH: "显示当前模式",
		LangEN: "Show current mode",
	},
	"cmd.config.desc": {
		LangZH: "重新配置 API key",
		LangEN: "Reconfigure API key",
	},
	"cmd.skills.desc": {
		LangZH: "列出可用 skill",
		LangEN: "List available skills",
	},
	"cmd.mcp-list.desc": {
		LangZH: "列出 MCP server 及状态",
		LangEN: "List MCP servers & status",
	},
	"cmd.mcp-add.desc": {
		LangZH: "添加 MCP server（弹窗）",
		LangEN: "Add an MCP server (popup)",
	},
	"cmd.mcp-delete.desc": {
		LangZH: "删除 MCP server（弹窗）",
		LangEN: "Delete an MCP server (popup)",
	},
	"cmd.lang.desc": {
		LangZH: "切换语言 / Switch language",
		LangEN: "Switch language / 切换语言",
	},
	"cmd.compact.desc": {
		LangZH: "手动压缩会话历史(保留 20%)",
		LangEN: "Manually compact session history (keep 20%)",
	},
	"cmd.flash.desc": {
		LangZH: "切回 flash 模型,取消 pro 保持",
		LangEN: "Reset to flash model, cancel pro keep",
	},
	"cmd.help.desc": {
		LangZH: "帮助",
		LangEN: "Help",
	},

	// === /help 输出 ===
	"help.body": {
		LangZH: "\n**Slash 命令**\n\n" +
			"- `/plan` — 切到只读模式(仅 Read / List / Grep / Glob / Tree / Search / Fetch / Memory)\n" +
			"- `/auto` — 切回全工具模式(默认)\n" +
			"- `/review` — 切到审核模式(Write/Update/Bash 需人工确认)\n" +
			"- `/mode` — 显示当前模式\n" +
			"- `/config` — 重新配置 API key (覆盖 `~/.deepx/model.yaml`)\n" +
			"- `/skills` — 列出可用 skill\n" +
			"- `/mcp-list` `/mcp-add` `/mcp-delete` — 管理 MCP server\n" +
			"- `/lang` — 切换语言 (中/英)\n" +
			"- `/compact` — 手动压缩会话历史(保留尾部 20%)\n" +
			"- `/flash` — 切回 flash 模型(取消 pro 保持)\n" +
			"- `/help` — 帮助\n\n" +
			"**快捷键**\n\n" +
			"- `Enter` — 发送\n" +
			"- `Ctrl+Shift+A` / macOS `Cmd+Shift+A` — 输入框全选\n" +
			"- `Ctrl+V` — 粘贴(含图片)\n" +
			"- `Esc` — 中断当前对话\n" +
			"- `Ctrl+C` — 退出程序",
		LangEN: "\n**Slash commands**\n\n" +
			"- `/plan` — Switch to read-only mode (Read / List / Grep / Glob / Tree / Search / Fetch / Memory only)\n" +
			"- `/auto` — Switch back to full-tools mode (default)\n" +
			"- `/review` — Switch to review mode (Write/Update/Bash require confirmation)\n" +
			"- `/mode` — Show current mode\n" +
			"- `/config` — Reconfigure API key (overwrites `~/.deepx/model.yaml`)\n" +
			"- `/skills` — List available skills\n" +
			"- `/mcp-list` `/mcp-add` `/mcp-delete` — Manage MCP servers\n" +
			"- `/lang` — Switch language (zh/en)\n" +
			"- `/compact` — Manually compact session history (keep last 20%)\n" +
			"- `/flash` — Switch back to flash model (cancel pro keep)\n" +
			"- `/help` — Help\n\n" +
			"**Keybindings**\n\n" +
			"- `Enter` — Send\n" +
			"- `Ctrl+Shift+A` / macOS `Cmd+Shift+A` — Select all in input\n" +
			"- `Ctrl+V` — Paste (including images)\n" +
			"- `Esc` — Interrupt current turn\n" +
			"- `Ctrl+C` — Quit",
	},

	// === 模式提示 ===
	"mode.plan":   {LangZH: "plan", LangEN: "plan"},
	"mode.auto":   {LangZH: "auto", LangEN: "auto"},
	"mode.review": {LangZH: "review", LangEN: "review"},
	"mode.current_prefix": {
		LangZH: "当前模式: ",
		LangEN: "Current mode: ",
	},
	"mode.model_suffix": {
		LangZH: ", 模型: ",
		LangEN: ", model: ",
	},
	"mode.show": {
		LangZH: "当前模式: %s",
		LangEN: "Current mode: %s",
	},
	"mode.unknown_cmd": {
		LangZH: "未知命令: %s (输入 /help 查看)",
		LangEN: "Unknown command: %s (type /help for help)",
	},

	// === Right panel section titles ===
	// 注:section() 会 strings.ToUpper(title) — 对英文是大写化,对中文是 no-op,两边都能用。
	"panel.endpoint":  {LangZH: "端点", LangEN: "Endpoint"},
	"panel.workspace": {LangZH: "工作区", LangEN: "Workspace"},
	"panel.models":    {LangZH: "模型", LangEN: "Models"},
	"panel.status":    {LangZH: "状态", LangEN: "Status"},
	"panel.usage":     {LangZH: "用量", LangEN: "Usage"},
	"panel.commands":  {LangZH: "命令", LangEN: "Commands"},
	"panel.codegraph": {LangZH: "代码图谱", LangEN: "CodeGraph"},
	"panel.plan":      {LangZH: "计划", LangEN: "Plan"},

	// === Right panel labels ===
	"panel.label.flash":   {LangZH: "flash ", LangEN: "flash "},
	"panel.label.pro":     {LangZH: "pro   ", LangEN: "pro   "},
	"panel.label.status":  {LangZH: "status", LangEN: "status"},
	"panel.label.mode":    {LangZH: "mode  ", LangEN: "mode  "},
	"panel.label.prompt":  {LangZH: "prompt", LangEN: "prompt"},
	"panel.label.output":  {LangZH: "output", LangEN: "output"},
	"panel.label.cache":   {LangZH: "cache ", LangEN: "cache "},
	"panel.label.time":    {LangZH: "duration", LangEN: "duration"},
	"panel.label.cgstate": {LangZH: "状态", LangEN: "status"},
	"panel.label.cgcalls": {LangZH: "调用次数", LangEN: "calls "},

	// === Status values ===
	"status.idle":      {LangZH: "idle", LangEN: "idle"},
	"status.thinking":  {LangZH: "thinking", LangEN: "thinking"},
	"status.streaming": {LangZH: "streaming", LangEN: "streaming"},
	"status.tool":      {LangZH: "tool", LangEN: "tool"},
	"status.error":     {LangZH: "error", LangEN: "error"},

	// === 输入框上方活动行 / 完成行 ===
	// footer 状态词单独成键(不复用右栏紧凑的 status.* 英文 token),这样能给中文模式
	// 提供本地化文案。footer.* 的 key 后缀与 m.status 取值一一对应(thinking/streaming/tool)。
	"footer.interrupt": {LangZH: "Esc 中断", LangEN: "Esc to interrupt"},
	"footer.thinking":  {LangZH: "思考中", LangEN: "Thinking"},
	"footer.streaming": {LangZH: "输出中", LangEN: "Responding"},
	"footer.tool":      {LangZH: "调用工具", LangEN: "Running tool"},
	"footer.error":     {LangZH: "出错", LangEN: "Error"},
	"done.done":        {LangZH: "完成", LangEN: "Done"},
	"done.tools":       {LangZH: "次工具调用", LangEN: "tool calls"},

	// === CodeGraph 状态值 ===
	"codegraph.idle":    {LangZH: "—", LangEN: "—"},
	"codegraph.loading": {LangZH: "加载", LangEN: "loading"},
	"codegraph.ready":   {LangZH: "就绪", LangEN: "ready"},
	"codegraph.stale":   {LangZH: "更新", LangEN: "stale"},

	// === Setup modal ===
	"setup.title": {
		LangZH: "🐋 deepx — 配置 API Key",
		LangEN: "🐋 deepx — Configure API Key",
	},
	"setup.hint.first_run": {
		LangZH: "看起来这是首次启动。请粘贴你的 API key。\n" +
			"配置会写入 ~/.deepx/model.yaml(权限 0600),之后启动不再询问。\n" +
			"以后可直接编辑该文件,或在聊天中输入 /config 重开本面板。",
		LangEN: "Looks like first launch. Paste your API key below.\n" +
			"Config saves to ~/.deepx/model.yaml (perms 0600); won't ask again.\n" +
			"Edit that file directly later, or type /config in chat to reopen.",
	},
	"setup.hint.reconfig": {
		LangZH: "修改 API key — 旧值会被覆盖。\n" +
			"提交后立即生效;Esc 取消不保存。\n" +
			"如果你只想换 base_url / model id,直接编辑 ~/.deepx/model.yaml 重启即可。",
		LangEN: "Change API key — the old value will be overwritten.\n" +
			"Saves take effect immediately; Esc to cancel.\n" +
			"To change base_url / model id only, edit ~/.deepx/model.yaml directly and restart.",
	},
	"setup.input_label": {LangZH: "API key:", LangEN: "API key:"},
	"setup.error.empty": {
		LangZH: "API key 不能为空",
		LangEN: "API key cannot be empty",
	},
	"setup.error.save": {
		LangZH: "保存失败: %v",
		LangEN: "Save failed: %v",
	},
	"setup.error.reload": {
		LangZH: "重新加载失败: %v",
		LangEN: "Reload failed: %v",
	},
	"setup.error.required": {
		LangZH: "需先填入 API key 才能继续 (Ctrl+C 退出 deepx)",
		LangEN: "API key required to proceed (Ctrl+C to quit deepx)",
	},
	"setup.footer.first_run": {
		LangZH: "Enter 保存并继续 · Ctrl+C 退出 deepx",
		LangEN: "Enter to save and continue · Ctrl+C to quit deepx",
	},
	"setup.footer.reconfig": {
		LangZH: "Enter 保存 · Esc 取消 · Ctrl+C 退出",
		LangEN: "Enter to save · Esc to cancel · Ctrl+C to quit",
	},
	"setup.saved_to": {
		LangZH: "✓ 已保存配置到 ",
		LangEN: "✓ Config saved to ",
	},

	// === Review modal ===
	"review.title": {
		LangZH: "⏳  Review Required",
		LangEN: "⏳  Review Required",
	},
	"review.desc_prefix": {
		LangZH: "Review ",
		LangEN: "Review ",
	},
	"review.yes": {LangZH: "  [ YES ]", LangEN: "  [ YES ]"},
	"review.no":  {LangZH: "  [ NO  ]", LangEN: "  [ NO  ]"},
	"review.footer": {
		LangZH: "↑/↓ 选择 · Enter 确认 · Esc 拒绝",
		LangEN: "↑/↓ select · Enter confirm · Esc reject",
	},

	// === Lang modal ===
	"lang.title": {
		LangZH: "🌐 选择语言 / Choose Language",
		LangEN: "🌐 Choose Language / 选择语言",
	},
	"lang.option.zh": {LangZH: "中文 (Chinese)", LangEN: "中文 (Chinese)"},
	"lang.option.en": {LangZH: "English (英文)", LangEN: "English (英文)"},
	"lang.footer": {
		LangZH: "↑/↓ 选择 · Enter 确认 · Esc 取消",
		LangEN: "↑/↓ select · Enter confirm · Esc cancel",
	},
	"lang.switched": {
		LangZH: "✓ 已切换语言: %s",
		LangEN: "✓ Language switched: %s",
	},

	// === Misc ===
	"misc.terminal_too_small": {
		LangZH: "终端太小了。",
		LangEN: "Terminal too small.",
	},
	"misc.interrupted": {
		LangZH: "\n\n_已中断_\n\n",
		LangEN: "\n\n_Interrupted_\n\n",
	},
	"misc.input_placeholder": {
		LangZH: "Type a message…  Enter 发送 · Option/Alt+Enter 换行 · Esc 中断",
		LangEN: "Type a message…  Enter to send · Option/Alt+Enter newline · Esc interrupt",
	},
	"misc.history_suffix": {
		LangZH: "_(以上为历史对话,共 %d 条)_",
		LangEN: "_(history above, %d entries)_",
	},
	"misc.skills.empty": {
		LangZH: "_(没有可用 skill)_",
		LangEN: "_(no skills available)_",
	},
	"misc.skills.list_title": {
		LangZH: "可用 skill:\n",
		LangEN: "Available skills:\n",
	},

	// === 版本升级提醒 ===
	"upgrade.available": {
		LangZH: "🎉 **新版本可用**: `v%s` → `v%s`\n```\n%s\n```",
		LangEN: "🎉 **New version available**: `v%s` → `v%s`\n```\n%s\n```",
	},

	// === 复制提示 ===
	"copy.done": {
		LangZH: "✓ 已复制",
		LangEN: "✓ Copied",
	},

	// === Web dashboard ===
	"web.ready": {
		LangZH: "🌐 **本地 Web 面板已就绪** <%s>",
		LangEN: "🌐 **Local web dashboard ready** <%s>",
	},
}
