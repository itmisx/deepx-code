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
	"cmd.provider.desc": {
		LangZH: "在已配置的供应商间快捷切换:/provider [名字]",
		LangEN: "Quick-switch between configured providers: /provider [name]",
	},
	"cmd.skills.desc": {
		LangZH: "列出可用 skill",
		LangEN: "List available skills",
	},
	"cmd.skill-add.desc": {
		LangZH: "搜 Clawhub 装 skill(也可粘 GitHub URL,含 /tree/<branch>/<dir> 子目录形式)",
		LangEN: "Search Clawhub & install (or paste GitHub URL, incl. /tree/<branch>/<dir> form)",
	},
	"cmd.skill-delete.desc": {
		LangZH: "删除 ~/.deepx/skills/ 下的 skill(弹窗)",
		LangEN: "Delete a skill from ~/.deepx/skills/ (popup)",
	},
	"cmd.ultracode.desc": {
		LangZH: "据描述创建 workflow:/ultracode <描述>",
		LangEN: "Create a workflow from a description: /ultracode <description>",
	},
	"cmd.workflows.desc": {
		LangZH: "列出可用 workflow",
		LangEN: "List available workflows",
	},
	"cmd.workflow.desc": {
		LangZH: "跑 workflow:/workflow <名字> [键=值 …]",
		LangEN: "Run a workflow: /workflow <name> [key=value …]",
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
		LangZH: "手动压缩会话历史(保留 15%)",
		LangEN: "Manually compact session history (keep 15%)",
	},
	"cmd.undo.desc": {
		LangZH: "撤销上一轮对话(原输入回填输入框)",
		LangEN: "Undo the last exchange (restores your input)",
	},
	"undo.done": {
		LangZH: "↩ 已撤销上一轮对话,原输入已回填输入框",
		LangEN: "↩ Undid the last exchange; your input is back in the box",
	},
	"undo.nothing": {
		LangZH: "没有可撤销的对话",
		LangEN: "Nothing to undo",
	},
	"undo.streaming": {
		LangZH: "正在生成中,先按 Esc 停止再 /undo",
		LangEN: "Still streaming — press Esc to stop, then /undo",
	},
	"cmd.new.desc": {
		LangZH: "开启一个全新对话(当前对话已保存,可在 /sessions 找回)",
		LangEN: "Start a brand-new conversation (current one is saved, see /sessions)",
	},
	"cmd.sessions.desc": {
		LangZH: "历史对话列表:↑/↓ 选择,Enter 切换",
		LangEN: "Conversation history: ↑/↓ select, Enter switch",
	},
	"cmd.sessionrename.desc": {
		LangZH: "重命名当前对话:/session-rename <新标题>",
		LangEN: "Rename current conversation: /session-rename <title>",
	},
	"cmd.sessiondelete.desc": {
		LangZH: "弹框选择要删除的对话(默认对话不可删)",
		LangEN: "Pick a conversation to delete (default cannot be deleted)",
	},
	"cmd.status.desc": {
		LangZH: "显示/隐藏右侧状态栏(也可按 Ctrl+B)",
		LangEN: "Show/hide the right status panel (or press Ctrl+B)",
	},
	"mouse.passthrough.on": {
		LangZH: "🖱 鼠标穿透已开启:文字选择与右键粘贴交回终端处理(deepx 的选区/复制、滚轮、拖拽暂停)。再按 F2 恢复。",
		LangEN: "🖱 Mouse passthrough ON: the terminal now handles text selection and right-click paste (deepx selection/copy, wheel and drag are paused). Press F2 to restore.",
	},
	"mouse.passthrough.off": {
		LangZH: "🖱 鼠标捕获已恢复:deepx 接管选区/复制、滚轮与右键粘贴。",
		LangEN: "🖱 Mouse capture restored: deepx handles selection/copy, wheel and right-click paste.",
	},
	"mouse.passthrough.badge": {
		LangZH: "鼠标穿透(F2 切换)",
		LangEN: "mouse passthrough (F2)",
	},
	"cmd.thinking.desc": {
		LangZH: "显示/隐藏模型思考过程(reasoning),暗显在对话流",
		LangEN: "Show/hide the model's thinking (reasoning), dimmed in the chat",
	},
	"thinking.on": {
		LangZH: "**已开启思考显示** — 之后回合会把模型的思考过程暗显在对话流(仅交互界面,不影响管道输出)。",
		LangEN: "**Thinking display on** — the model's reasoning will show dimmed in the chat on following turns (interactive only, not piped output).",
	},
	"thinking.off": {
		LangZH: "**已关闭思考显示** — 之后回合只用 spinner 表示思考,不再打印思考内容。",
		LangEN: "**Thinking display off** — following turns show only a spinner while thinking, without printing the reasoning.",
	},
	"cmd.web-config.desc": {
		LangZH: "配置 web 面板的绑定 IP 与端口(立即生效并显示新地址)",
		LangEN: "Configure web dashboard bind IP & port (applied immediately, shows the new URL)",
	},
	"cmd.sandbox.desc": {
		LangZH: "沙箱模式:off(关闭)/ native(OS 隔离,默认)/ docker(容器隔离)",
		LangEN: "Sandbox mode: off / native (OS isolation, default) / docker (container isolation)",
	},
	"cmd.workingmode.desc": {
		LangZH: "工作模式:kp(务实)/ openspec(规格驱动)/ sp(全流程严谨)",
		LangEN: "Working mode: kp (pragmatic) / openspec (spec-driven) / sp (rigorous)",
	},
	"workingmode.switched": {
		LangZH: "已切到工作模式:%s。此后每轮对话都会引导用对应 skill、并排除另外两个。",
		LangEN: "Switched to working mode: %s. Each turn now steers toward its skill and excludes the other two.",
	},
	"workingmode.unknown": {
		LangZH: "未知工作模式:%s(可选 kp/karpathy、openspec/spec、sp/superpowers)",
		LangEN: "Unknown working mode: %s (choose kp/karpathy, openspec/spec, sp/superpowers)",
	},
	"sandbox.title": {
		LangZH: "选择沙箱模式",
		LangEN: "Choose Sandbox Mode",
	},
	"sandbox.opt.native": {
		LangZH: "native — OS 隔离(推荐)",
		LangEN: "native — OS isolation (recommended)",
	},
	"sandbox.opt.off": {
		LangZH: "off    — 无防护",
		LangEN: "off    — no protection",
	},
	"sandbox.opt.docker": {
		LangZH: "docker — 容器隔离",
		LangEN: "docker — container isolation",
	},
	"sandbox.switched_off": {
		LangZH: "已关闭沙箱(off):命令不做任何隔离,Write/Edit 也不再限制 workspace。仅在你完全信任时用。",
		LangEN: "Sandbox turned off: commands run with no isolation and Write/Edit are no longer confined to the workspace. Use only when you fully trust the workload.",
	},
	"sandbox.switched_native_os": {
		LangZH: "已切到 native 沙箱(OS 隔离:文件只能写 workspace 内,host 其余只读;读和网络不限)",
		LangEN: "Switched to native sandbox (OS isolation: writes confined to workspace, rest of host read-only; reads and network unrestricted)",
	},
	"sandbox.switched_native_soft": {
		LangZH: "已切到 native 沙箱(软策略:本平台无 OS 隔离,仅黑名单拦明显危险命令;防误操作,非硬隔离)",
		LangEN: "Switched to native sandbox (soft policy: no OS isolation on this platform, only a blacklist for obviously dangerous commands; guards accidents, not a hard boundary)",
	},
	"sandbox.docker_unavailable": {
		LangZH: "Docker 不可用:%s。仍保持 native。",
		LangEN: "Docker unavailable: %s. Staying on native.",
	},
	"sandbox.pulling": {
		LangZH: "拉取镜像",
		LangEN: "pulling image",
	},
	"sandbox.pull_failed": {
		LangZH: "镜像拉取失败:%s。保持 native。",
		LangEN: "Image pull failed: %s. Staying on native.",
	},
	"sandbox.pull_canceled": {
		LangZH: "已取消拉取镜像,保持 native。",
		LangEN: "Image pull canceled. Staying on native.",
	},
	"sandbox.switched_docker": {
		LangZH: "已切到 docker 沙箱(镜像 %s):命令在容器里跑,workspace 挂载到 /workspace。首次命令会拉镜像+起容器,可能稍慢。",
		LangEN: "Switched to docker sandbox (image %s): commands run in a container with the workspace mounted at /workspace. The first command pulls the image and starts the container — may be slow.",
	},
	"sandbox.unknown": {
		LangZH: "未知沙箱模式:%s(可选 off / native / docker)",
		LangEN: "Unknown sandbox mode: %s (choose off / native / docker)",
	},
	// %d = 当前上下文占窗口的百分比。只有过了 TopicHintTokens 这条线才会发,所以这个数一定不小。
	"topic.shift": {
		LangZH: "会话主题已切换,建议 /new 开一段新对话 —— 旧话题的上下文对新问题只是噪音,还占着 %d%% 的窗口。",
		LangEN: "Topic changed — consider /new for a fresh conversation. The old context is just noise for this question, and it's holding %d%% of the window.",
	},
	"session.new": {
		LangZH: "已开启全新对话。上一段对话已保存,/sessions 可找回。",
		LangEN: "Started a new conversation. The previous one is saved — see /sessions.",
	},
	"session.switched": {
		LangZH: "↩ 已切换到对话:%s",
		LangEN: "↩ Switched to conversation: %s",
	},
	"session.streaming": {
		LangZH: "正在生成中,先按 Esc 停止再切换/新建对话",
		LangEN: "Still streaming — press Esc to stop before switching/new conversation",
	},
	"session.rename.usage": {
		LangZH: "用法:/session-rename <新标题>",
		LangEN: "Usage: /session-rename <title>",
	},
	"session.renamed": {
		LangZH: "✎ 已重命名当前对话为:%s",
		LangEN: "✎ Renamed current conversation to: %s",
	},
	"session.deleted_named": {
		LangZH: "已删除对话:%s",
		LangEN: "Deleted conversation: %s",
	},
	"session.delete.cant_default": {
		LangZH: "默认对话不可删除。",
		LangEN: "The default conversation cannot be deleted.",
	},
	"session.modal.title": {
		LangZH: "历史对话",
		LangEN: "Conversations",
	},
	"session.modal.title_delete": {
		LangZH: "删除对话",
		LangEN: "Delete Conversation",
	},
	"session.modal.footer": {
		LangZH: "↑/↓ 选择 · Enter 切换 · Esc 取消",
		LangEN: "↑/↓ select · Enter switch · Esc cancel",
	},
	"session.modal.footer_delete": {
		LangZH: "↑/↓ 选择 · Enter 删除 · Esc 取消",
		LangEN: "↑/↓ select · Enter delete · Esc cancel",
	},
	"session.modal.empty": {
		LangZH: "(暂无历史对话)",
		LangEN: "(no conversations yet)",
	},
	"session.untitled": {
		LangZH: "(未命名)",
		LangEN: "(untitled)",
	},
	"session.current": {
		LangZH: "当前对话",
		LangEN: "current",
	},
	"cmd.routerlistpro.desc": {
		LangZH: "查看「升级 pro」的语义样板句",
		LangEN: "List the sentence patterns that route to pro",
	},
	"cmd.routerlistflash.desc": {
		LangZH: "查看「维持 flash」的语义样板句",
		LangEN: "List the sentence patterns that keep flash",
	},
	"cmd.routeraddpro.desc": {
		LangZH: "添加一条「升级 pro」的样板句",
		LangEN: "Add a sentence pattern that routes to pro",
	},
	"cmd.routeraddflash.desc": {
		LangZH: "添加一条「维持 flash」的样板句",
		LangEN: "Add a sentence pattern that keeps flash",
	},
	"cmd.routerdeletepro.desc": {
		LangZH: "删除一条「升级 pro」的样板句",
		LangEN: "Remove a sentence pattern that routes to pro",
	},
	"cmd.routerdeleteflash.desc": {
		LangZH: "删除一条「维持 flash」的样板句",
		LangEN: "Remove a sentence pattern that keeps flash",
	},
	"cmd.help.desc": {
		LangZH: "帮助",
		LangEN: "Help",
	},
	"cmd.exit.desc": {
		LangZH: "退出 deepx",
		LangEN: "Quit deepx",
	},

	// === /help 输出 ===
	"help.body": {
		LangZH: "\n**Slash 命令**\n\n" +
			"- `/plan` — 切到只读模式(仅 Read / List / Grep / Glob / Tree / Search / Fetch / Memory)\n" +
			"- `/auto` — 切回全工具模式(默认)\n" +
			"- `/review` — 切到审核模式(Write/Update/Bash 需人工确认)\n" +
			"- `/mode` — 显示当前模式\n" +
			"- `/config` — 重新配置 API key (覆盖 `~/.deepx/model.yaml`)\n" +
			"- `/provider` — 在已配置的供应商间快捷切换(`/provider [名字]`;配置存于 `~/.deepx/provider.yaml`。自定义供应商可在 /config 里起名,多份配置各占一槽)\n" +
			"- `/skills` — 列出可用 skill\n" +
			"- `/skill-add` `/skill-delete` — 搜索安装 / 删除 skill\n" +
			"- `/mcp-list` `/mcp-add` `/mcp-delete` — 管理 MCP server\n" +
			"- `/ultracode` — 据一句描述生成并保存一个 workflow:`/ultracode <描述>`\n" +
			"- `/workflows` — 列出已有 workflow(`.deepx/workflows/` + `.claude/workflows/`)\n" +
			"- `/workflow` — 运行已保存的 workflow:`/workflow <名字> [键=值 …]`(运行前确认)\n" +
			"- `/lang` — 切换语言 (中/英)\n" +
			"- `/reasoning` — 设置 thinking / reasoning_effort(per-role,空值不发)\n" +
			"- `/compact` — 手动压缩会话历史(保留尾部 15%)\n" +
			"- `/new` — 开启全新对话(当前对话已保存,可在 /sessions 找回)\n" +
			"- `/sessions` — 历史对话列表(↑/↓ 选,Enter 切换)\n" +
			"- `/status` — 显示/隐藏右侧状态栏(也可按 Ctrl+B)\n" +
			"- `/thinking` — 显示/隐藏模型思考过程(reasoning)\n" +
			"- `/web-config` — 配置 web 面板绑定 IP / 端口(局域网访问,立即生效并显示新地址)\n" +
			"- `/sandbox` — 沙箱模式:`off`(关闭)/ `native`(OS 隔离,默认)/ `docker`(容器隔离)\n" +
			"- `/working-mode` — 工作模式:`karpathy`(务实)/ `openspec`(规格驱动)/ `superpowers`(全流程严谨),默认 karpathy\n" +
			"- `/undo` — 撤销上一轮对话(原输入回填输入框)\n" +
			"- `/help` — 帮助\n" +
			"- `/exit` — 退出 deepx\n\n" +
			"**输入**\n\n" +
			"- `@` — 引用文件(弹出文件选择器,选中后插入路径,模型按需读取)\n\n" +
			"**快捷键**\n\n" +
			"- `Enter` — 发送;模型回答中按 Enter 则把输入排队,本轮结束自动发出\n" +
			"- `ctrl+j` — 换行(多行输入)\n" +
			"- `Ctrl+B` — 显示/隐藏右侧状态栏\n" +
			"- `Ctrl+V` — 粘贴(含图片)\n" +
			"- `F2` — 鼠标穿透:交回终端做文字选择/右键粘贴(WSL2、Windows Terminal 常用),重启保持\n" +
			"- `鼠标右键` — 在输入框内粘贴剪贴板文本(穿透关闭时由 deepx 处理)\n" +
			"- `Esc` — 中断当前对话(按两次;防 vim 习惯误触。也可 Ctrl+C 单按即停)\n" +
			"- `Ctrl+C` — 清空输入框;输入为空时按两次退出程序(1 秒内;弹窗内则关弹窗)",
		LangEN: "\n**Slash commands**\n\n" +
			"- `/plan` — Switch to read-only mode (Read / List / Grep / Glob / Tree / Search / Fetch / Memory only)\n" +
			"- `/auto` — Switch back to full-tools mode (default)\n" +
			"- `/review` — Switch to review mode (Write/Update/Bash require confirmation)\n" +
			"- `/mode` — Show current mode\n" +
			"- `/config` — Reconfigure API key (overwrites `~/.deepx/model.yaml`)\n" +
			"- `/provider` — Quick-switch between configured providers (`/provider [name]`; saved in `~/.deepx/provider.yaml`. Name your custom providers in /config so each gets its own slot)\n" +
			"- `/skills` — List available skills\n" +
			"- `/skill-add` `/skill-delete` — Search-install / delete skills\n" +
			"- `/mcp-list` `/mcp-add` `/mcp-delete` — Manage MCP servers\n" +
			"- `/ultracode` — Generate & save a workflow from a description: `/ultracode <description>`\n" +
			"- `/workflows` — List saved workflows (`.deepx/workflows/` + `.claude/workflows/`)\n" +
			"- `/workflow` — Run a saved workflow: `/workflow <name> [key=value …]` (confirms before running)\n" +
			"- `/lang` — Switch language (zh/en)\n" +
			"- `/reasoning` — Set thinking / reasoning_effort (per-role, empty = don't send)\n" +
			"- `/compact` — Manually compact session history (keep last 15%)\n" +
			"- `/new` — Start a brand-new conversation (current one is saved, see /sessions)\n" +
			"- `/sessions` — Conversation history (↑/↓ select, Enter switch)\n" +
			"- `/status` — Show/hide the right status panel (or press Ctrl+B)\n" +
			"- `/thinking` — Show/hide the model's thinking (reasoning)\n" +
			"- `/web-config` — Configure web dashboard bind IP / port (LAN access, applied immediately, shows the new URL)\n" +
			"- `/sandbox` — Sandbox mode: `off` / `native` (OS isolation, default) / `docker` (container isolation)\n" +
			"- `/working-mode` — Working mode: `karpathy` (pragmatic) / `openspec` (spec-driven) / `superpowers` (rigorous), default karpathy\n" +
			"- `/undo` — Undo the last exchange (restores your input)\n" +
			"- `/help` — Help\n" +
			"- `/exit` — Quit deepx\n\n" +
			"**Input**\n\n" +
			"- `@` — Reference a file (opens a picker; inserts the path for the model to read)\n\n" +
			"**Keybindings**\n\n" +
			"- `Enter` — Send; while the model is responding, Enter queues your input and it's sent when the turn ends\n" +
			"- `ctrl+j` — Newline (multi-line input)\n" +
			"- `Ctrl+B` — Show/hide the right status panel\n" +
			"- `Ctrl+V` — Paste (including images)\n" +
			"- `F2` — Mouse passthrough: let the terminal handle selection / right-click paste (handy on WSL2, Windows Terminal); remembered across restarts\n" +
			"- `Right click` — Paste clipboard text into the input (handled by deepx when passthrough is off)\n" +
			"- `Esc` — Interrupt current turn (press twice; avoids vim-habit misfires. Or Ctrl+C to stop in one press)\n" +
			"- `Ctrl+C` — Clear the input; when empty, press twice within 1s to quit (closes modal if open)",
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
	"model.locked": {
		LangZH: "已锁定 %s 模型（本会话不再自动路由，也不会被自动升级）。",
		LangEN: "Locked to the %s model (no auto-routing or auto-upgrade this session).",
	},
	"model.unlocked": {
		LangZH: "已切换为自动选择模型（按任务语义路由 flash/pro）。",
		LangEN: "Switched to automatic model selection (routed flash/pro by task).",
	},
	"model.pin.global": {
		LangZH: "已记为全局默认：之后新开的会话起手用 %s（已有会话仍按各自锁定的模型）。",
		LangEN: "Saved as the global default — new conversations start with %s (existing ones keep their own).",
	},
	"model.modal.title": {
		LangZH: "选择模型",
		LangEN: "Select model",
	},
	"model.opt.auto": {
		LangZH: "auto   — 按任务自动路由 flash / pro",
		LangEN: "auto   — route flash / pro by task",
	},
	"model.opt.flash": {
		LangZH: "flash  — 锁定 flash（快、省）",
		LangEN: "flash  — pin flash (fast, cheap)",
	},
	"model.opt.pro": {
		LangZH: "pro    — 锁定 pro（强、贵）",
		LangEN: "pro    — pin pro (stronger, pricier)",
	},
	"model.footer": {
		LangZH: "↑/↓ 选择 · Enter 确认 · Esc 取消",
		LangEN: "↑/↓ select · Enter confirm · Esc cancel",
	},
	"cmd.model.desc": {
		LangZH: "锁定/自动选择模型 (auto|flash|pro)",
		LangEN: "Pin/auto-select model (auto|flash|pro)",
	},
	"cmd.reasoning.desc": {
		LangZH: "设置 thinking / reasoning_effort(per-role,空值不发)",
		LangEN: "Set thinking / reasoning_effort (per-role, empty = don't send)",
	},
	"reasoning.modal.title": {
		LangZH: "推理参数(空值 = 不发送,走 API 默认 / 兼容 MiMo 等)",
		LangEN: "Reasoning params (empty = don't send, falls back to API default / MiMo-safe)",
	},
	"reasoning.modal.footer": {
		LangZH: "↑/↓ 选行 · ←/→ 改值(立即生效) · Enter / Esc 关闭",
		LangEN: "↑/↓ row · ←/→ change value (applied instantly) · Enter / Esc close",
	},

	// === Right panel section titles ===
	// 注:section() 会 strings.ToUpper(title) — 对英文是大写化,对中文是 no-op,两边都能用。
	"panel.vendor":    {LangZH: "模型厂商", LangEN: "Vendor"},
	"panel.workspace": {LangZH: "工作区", LangEN: "Workspace"},
	"panel.routing":   {LangZH: "模型路由", LangEN: "Routing"},
	"panel.curmodel":  {LangZH: "当前模型", LangEN: "Model"},
	"panel.permmode":  {LangZH: "权限模式", LangEN: "Permission"},
	"panel.context":   {LangZH: "上下文", LangEN: "Context"},
	"panel.help":      {LangZH: "帮助", LangEN: "Help"},
	"panel.codegraph": {LangZH: "代码图谱", LangEN: "CodeGraph"},
	"panel.sandbox":   {LangZH: "沙箱", LangEN: "Sandbox"},
	"panel.workmode":  {LangZH: "工作模式", LangEN: "Working mode"},
	"panel.topic":     {LangZH: "会话主题", LangEN: "Topic"},
	"panel.todo":      {LangZH: "待办", LangEN: "Todo"}, // Todo 工具:主 agent 顺序清单
	"panel.plan":      {LangZH: "计划", LangEN: "Plan"}, // CreatePlan:并发子 agent DAG

	// === Right panel labels ===
	"panel.label.used":     {LangZH: "占用", LangEN: "Used"},
	"panel.label.output":   {LangZH: "输出", LangEN: "Output"},
	"panel.label.cache":    {LangZH: "缓存", LangEN: "Cache"},
	"panel.label.sbmode":   {LangZH: "隔离", LangEN: "Isolation"},
	"panel.label.wmode":    {LangZH: "方法", LangEN: "Method"},
	"panel.label.endpoint": {LangZH: "接口", LangEN: "Endpoint"},
	"panel.label.balance":  {LangZH: "余额", LangEN: "Balance"},
	"panel.topic.none":     {LangZH: "(尚未识别)", LangEN: "(not identified yet)"},

	// === Status values ===
	"status.idle":      {LangZH: "idle", LangEN: "idle"},
	"status.thinking":  {LangZH: "thinking", LangEN: "thinking"},
	"status.streaming": {LangZH: "streaming", LangEN: "streaming"},
	"status.tool":      {LangZH: "tool", LangEN: "tool"},
	"status.error":     {LangZH: "error", LangEN: "error"},

	// === 输入框上方活动行 / 完成行 ===
	// footer 状态词单独成键(不复用右栏紧凑的 status.* 英文 token),这样能给中文模式
	// 提供本地化文案。footer.* 的 key 后缀与 m.status 取值一一对应(thinking/streaming/tool)。
	"footer.interrupt": {LangZH: "Esc×2 中断", LangEN: "Esc×2 to interrupt"},
	"footer.thinking":  {LangZH: "思考中", LangEN: "Thinking"},
	"footer.streaming": {LangZH: "输出中", LangEN: "Responding"},
	"footer.tool":      {LangZH: "调用工具", LangEN: "Running tool"},
	"footer.error":     {LangZH: "出错", LangEN: "Error"},
	"done.done":        {LangZH: "完成", LangEN: "Done"},
	"done.tools":       {LangZH: "次工具调用", LangEN: "tool calls"},

	// === CodeGraph 状态值 ===
	"codegraph.idle":     {LangZH: "—", LangEN: "—"},
	"codegraph.loading":  {LangZH: "加载", LangEN: "loading"},
	"codegraph.ready":    {LangZH: "就绪", LangEN: "ready"},
	"codegraph.stale":    {LangZH: "更新", LangEN: "stale"},
	"codegraph.disabled": {LangZH: "已禁用", LangEN: "disabled"},
	"codegraph.degraded": {LangZH: "降级", LangEN: "degraded"},

	// === Setup modal ===
	"setup.title": {
		LangZH: "deepx — 配置 API Key",
		LangEN: "deepx — Configure API Key",
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
	"setup.provider_label":  {LangZH: "选择模型提供商 (↑/↓ 切换):", LangEN: "Choose model provider (↑/↓ to switch):"},
	"setup.saved_section":   {LangZH: "── 已保存的自定义(选中即可修改)──", LangEN: "── Saved custom (select to edit) ──"},
	"setup.editing_hint":    {LangZH: "(修改已保存的配置)", LangEN: "(editing saved config)"},
	"setup.input_label":     {LangZH: "API key:", LangEN: "API key:"},
	"setup.provider.custom": {LangZH: "其它(自定义)", LangEN: "Other (custom)"},
	"setup.cur_provider":    {LangZH: "提供商:", LangEN: "Provider:"},
	"setup.save_path_hint":  {LangZH: "保存在 %s", LangEN: "saved to %s"},
	"setup.error.custom_flash": {
		LangZH: "Flash 模型需填全 base_url / model / api_key",
		LangEN: "Flash model needs base_url / model / api_key",
	},
	"setup.error.custom_name": {
		LangZH: "供应商名只能用小写字母 / 数字 / . _ -,以字母或数字开头,最长 32 位",
		LangEN: "Provider name: lowercase letters / digits / . _ - only, must start with a letter or digit, max 32 chars",
	},
	"setup.error.custom_name_reserved": {
		LangZH: "「%s」是预设供应商名,请换一个(留空则存为 custom)",
		LangEN: "\"%s\" is a preset provider name — pick another (leave blank to save as custom)",
	},
	"setup.renamed_from": {
		LangZH: "↩ 供应商「%s」已改名为「%s」(旧名字已从 provider.yaml 移除)",
		LangEN: "↩ Provider \"%s\" renamed to \"%s\" (old name removed from provider.yaml)",
	},
	"setup.archived_as": {
		LangZH: "✓ 已存为供应商「%s」,之后可用 `/provider %s` 切回来",
		LangEN: "✓ Archived as provider \"%s\" — switch back later with `/provider %s`",
	},
	"setup.footer.step_provider": {
		LangZH: "↑/↓ 选择 · Enter 下一步 · Esc 取消 · Ctrl+C 退出",
		LangEN: "↑/↓ select · Enter next · Esc cancel · Ctrl+C quit",
	},
	"setup.footer.delete_hint": {
		LangZH: " · d 删除",
		LangEN: " · d delete",
	},
	"setup.delete_confirm": {
		LangZH: "⚠ 再按一次 d 删除供应商「%s」· 其它键取消",
		LangEN: "⚠ Press d again to delete provider \"%s\" · any other key cancels",
	},
	"setup.error.delete_not_saved": {
		LangZH: "只能删除「已保存的自定义」里的供应商",
		LangEN: "Only providers under \"Saved custom\" can be deleted",
	},
	"setup.error.delete": {
		LangZH: "删除失败: %v",
		LangEN: "Delete failed: %v",
	},
	"setup.deleted": {
		LangZH: "✓ 已删除供应商「%s」(仅移除 provider.yaml 里的存档,当前生效的 model.yaml 不变)",
		LangEN: "✓ Deleted provider \"%s\" (removes the provider.yaml archive only; the active model.yaml is untouched)",
	},
	"setup.footer.step_preset": {
		LangZH: "Enter 保存 · Esc 返回上一步 · Ctrl+C 退出",
		LangEN: "Enter save · Esc back · Ctrl+C quit",
	},
	"setup.footer.step_custom": {
		LangZH: "Tab/↑↓ 切换字段 · Enter 保存 · Esc 返回 · Ctrl+C 退出",
		LangEN: "Tab/↑↓ fields · Enter save · Esc back · Ctrl+C quit",
	},
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
		LangZH: "Review Required",
		LangEN: "Review Required",
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
		LangZH: "选择语言 / Choose Language",
		LangEN: "Choose Language / 选择语言",
	},
	"lang.option.zh": {LangZH: "中文 (Chinese)", LangEN: "中文 (Chinese)"},
	"lang.option.en": {LangZH: "English (英文)", LangEN: "English (英文)"},
	"lang.footer": {
		LangZH: "↑/↓ 选择 · Enter 确认 · Esc 取消",
		LangEN: "↑/↓ select · Enter confirm · Esc cancel",
	},
	"provider.title": {
		LangZH: "切换模型供应商",
		LangEN: "Switch Model Provider",
	},
	"provider.empty": {
		LangZH: "还没有已保存的供应商配置。先用 /config 配置一个,会自动存档到 provider.yaml。",
		LangEN: "No saved provider configs yet. Configure one with /config first — it's archived to provider.yaml automatically.",
	},
	"provider.unknown": {
		LangZH: "未找到供应商「%s」。/provider 看可选列表。",
		LangEN: "Provider \"%s\" not found. Run /provider to see the list.",
	},
	"provider.switched": {
		LangZH: "↩ 已切换到供应商「%s」(flash: %s · pro: %s)",
		LangEN: "↩ Switched to provider \"%s\" (flash: %s · pro: %s)",
	},
	"provider.error.load": {
		LangZH: "读取 provider.yaml 失败:%v",
		LangEN: "Failed to read provider.yaml: %v",
	},
	"provider.error.save": {
		LangZH: "切换供应商失败:%v",
		LangEN: "Failed to switch provider: %v",
	},
	"workingmode.title": {
		LangZH: "选择工作模式",
		LangEN: "Choose Working Mode",
	},
	"workingmode.opt.karpathy": {
		LangZH: "karpathy    — 务实快速",
		LangEN: "karpathy    — pragmatic",
	},
	"workingmode.opt.openspec": {
		LangZH: "openspec    — 规格驱动",
		LangEN: "openspec    — spec-driven",
	},
	"workingmode.opt.superpowers": {
		LangZH: "superpowers — 全流程严谨",
		LangEN: "superpowers — rigorous",
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
	"misc.ctrlc_again_to_quit": {
		LangZH: "再按一次 Ctrl+C 退出 deepx(1 秒内)",
		LangEN: "Press Ctrl+C again to quit deepx (within 1 second)",
	},
	"misc.esc_again_to_interrupt": {
		LangZH: "再按一次 Esc 中断生成(1 秒内;或按 Ctrl+C 单按即停)",
		LangEN: "Press Esc again to interrupt (within 1s; or Ctrl+C to stop in one press)",
	},
	"misc.input_placeholder": {
		LangZH: "Type a message…  Enter 发送 · ctrl+j 换行 · ctrl+c 清空 · Esc×2 中断",
		LangEN: "Type a message…  Enter send · ctrl+j newline · ctrl+c clear · Esc×2 interrupt",
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
		LangZH: "**新版本可用**: `v%s` → `v%s`\n```\n%s\n```",
		LangEN: "**New version available**: `v%s` → `v%s`\n```\n%s\n```",
	},

	// === 复制提示 ===
	"copy.done": {
		LangZH: "✓ 已复制",
		LangEN: "✓ Copied",
	},

	// === Web dashboard ===
	"web.ready": {
		LangZH: "**本地 Web 面板已就绪** <%s>",
		LangEN: "**Local web dashboard ready** <%s>",
	},
	"web.ready.lan": {
		LangZH: "该地址绑定到了局域网:同网络的设备都能访问并**控制本会话(可执行命令)**。链接含访问令牌且为明文 HTTP,请仅在可信网络使用;改回仅本机用 `/web-config` 把 IP 设为 `127.0.0.1`(或留空)。",
		LangEN: "This address is bound to your LAN: any device on the network can access and **control this session (run commands)**. The link carries an access token over plain HTTP — use only on trusted networks. To revert to local-only, run `/web-config` and set the IP to `127.0.0.1` (or leave it empty).",
	},
	"web.config.title": {
		LangZH: "配置 Web 面板",
		LangEN: "Configure Web Dashboard",
	},
	"web.config.hint": {
		LangZH: "填「IP [端口]」,空格分隔:\n  IP 留空 / 127.0.0.1 = 仅本机;0.0.0.0 = 局域网(手机/平板可访问);也可填某网卡 IP\n  端口可省略(随机)。例:0.0.0.0 8080\n  ⚠️ 面板可控制会话、执行命令,且明文 HTTP —— 对外暴露仅限可信局域网",
		LangEN: "Enter \"IP [port]\" (space-separated):\n  empty / 127.0.0.1 = local only;  0.0.0.0 = LAN (phone/tablet);  or a specific NIC IP\n  port optional (random). e.g. 0.0.0.0 8080\n  ⚠️ The panel can control the session & run commands over plain HTTP — expose only on trusted LANs",
	},
	"web.config.footer": {
		LangZH: "Enter 保存(重启生效) · Esc 取消",
		LangEN: "Enter to save (effective on restart) · Esc to cancel",
	},
	"web.config.saved": {
		LangZH: "✓ 已保存 Web 面板配置:IP %s,端口 %s。",
		LangEN: "✓ Web dashboard config saved: IP %s, port %s.",
	},
	"web.config.relisten_failed": {
		LangZH: "配置已保存,但按新地址重启服务失败:%s。下次启动 deepx 会按新配置生效。",
		LangEN: "Config saved, but restarting the server on the new address failed: %s. It will take effect next time you start deepx.",
	},
	"web.config.port.random": {
		LangZH: "随机",
		LangEN: "random",
	},
	"web.config.err.host": {
		LangZH: "IP 不合法:请填合法 IP(如 0.0.0.0 / 192.168.1.5)、127.0.0.1 或留空",
		LangEN: "Invalid IP: enter a valid IP (e.g. 0.0.0.0 / 192.168.1.5), 127.0.0.1, or leave empty",
	},
	"web.config.err.port": {
		LangZH: "端口不合法:请填 0–65535 的整数(留空 = 随机)",
		LangEN: "Invalid port: enter an integer 0–65535 (empty = random)",
	},
	"welcome": {
		LangZH: "欢迎使用 **deepx-code**,输入 `/help` 查看命令与快捷键。",
		LangEN: "Welcome to **deepx-code** — type `/help` for commands and shortcuts.",
	},
}
