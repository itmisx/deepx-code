<div align="center">

# deepx-code

**DeepSeek 原生、兼容 OpenAI 接口的终端编程 Agent —— 单二进制、缓存友好、内置代码图谱与本地 OCR**

**已预置 DeepSeek · 小米 MiMo · Kimi · 通义千问,并支持任意自定义 OpenAI 兼容模型**

[![Go](https://img.shields.io/badge/built%20with-Go-00ADD8?logo=go&logoColor=white)](https://go.dev) [![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE) [![Release](https://img.shields.io/github/v/release/itmisx/deepx-code?color=success)](https://github.com/itmisx/deepx-code/releases) [![Downloads](https://img.shields.io/github/downloads/itmisx/deepx-code/total?color=success&label=downloads)](https://github.com/itmisx/deepx-code/releases) [![Stars](https://img.shields.io/github/stars/itmisx/deepx-code?style=flat)](https://github.com/itmisx/deepx-code/stargazers) ![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)

简体中文 · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md)

![deepx-code demo](assets/demo.gif)

</div>

> [!TIP]
> **⚡ 长会话实测 prompt-cache 命中 ~99%**（真实 session：41,591 tokens 中 41,472 命中）。DeepSeek 对命中缓存的输入按未命中价的几十分之一计费（[官方定价](https://api-docs.deepseek.com/quick_start/pricing)），长跑几乎不为重复的上下文重复付费。

---

## ✨ 核心特性

- **🦫 单一 Go 二进制** —— 无 Node / Python 运行时，`curl` 一行装，macOS / Linux / Windows 全覆盖。
- **💰 缓存友好，长会话省钱** —— 围绕 DeepSeek 前缀缓存设计，实测 ~99% 命中；本地语义路由零延迟、零 token 起手。
- **🧭 内置代码图谱（codegraph）** —— 符号级跳定义 / 找调用 / 接口实现 / 影响面分析，Go 经 `go/types` 精确解析，替代满仓库 grep。
- **👀 本地图片 OCR（PaddleOCR）** —— 离线读图，丢一张截图就能识别其中文字，不依赖多模态 API。
- **📎 `@` 文件 / 目录引用** —— 输入框打 `@` 弹本地模糊路径选择器，选中即把 `@路径` 塞进消息；模型按需调 Read（文件）/ List（目录），精准给上下文不用全塞。
- **🧠 双模型自动路由** —— flash 起手省钱，复杂任务自动升 pro；也可用 `/model flash|pro` 锁定模型、`/auto` `/plan` `/review` 切模式。
- **🗂️ 顺序 Todo + 并发 Plan DAG** —— 多步任务用可见待办清单逐步勾选；可并行的独立子任务拆成 DAG 派并发子 agent。
- **🔁 可复用 Workflow** —— 用 JS 脚本把多 agent 流程固化下来反复跑(`agent()` / `parallel()` / `pipeline()`):多视角审查、扇出研究、流水线、循环到无新增等。`/ultracode <描述>` 让模型自动生成并保存,`/workflow <名>` 运行;真并发、可中断 resume、结构化输出走工具强约束、运行前预列全部阶段并实时显示耗时。对齐 Claude Code 的 workflow 脚本约定,脚本可直接互用。
- **💾 无损会话持久化** —— gob 完整保留 `tool_calls` / `tool results` / `reasoning_content`，重启无缝续接；超窗自动分层压缩。
- **🔌 MCP + Skill 生态** —— 原生 MCP；兼容 Claude 的 skill 目录，已有 skill 直接复用。
- **🛡️ 审核模式** —— 写文件 / 执行 Shell 默认需人工确认，安全可控。
- **🧱 原生 OS 级沙箱** —— 默认 `native` 做 OS 隔离（macOS Seatbelt、Linux bubblewrap，写操作限定 workspace + 进程隔离；无 OS 机制平台退软策略），也支持 `docker` 容器隔离或 `off` 关闭，不依赖容器也能给 agent 划安全边界。
- **🎛️ 工作模式（working mode）** —— 一个命令锁定方法论：`karpathy`（务实工匠）/ `openspec`（规格驱动）/ `superpowers`（全流程严谨）；三种互斥，选一禁两、杜绝方法论混搭，切换存入会话、每轮注入不污染历史。
- **⚡ 非交互 `exec` 模式** —— `deepx exec "任务"` 一次性跑完直接把结果打到 stdout，支持管道喂数据、重定向输出、塞进脚本 / CI / cron，**不必进 TUI**（用法见下方「非交互执行」一节）。

## 📊 对比 Claude Code

|                 | **deepx-code**                                                          | Claude Code               |
| :-------------- | :---------------------------------------------------------------------- | :------------------------ |
| 分发            | Go 单二进制，`curl` 一行装                                              | Node（npm）               |
| 开源            | ✅ MIT                                                                  | ❌ 闭源                   |
| 模型            | DeepSeek / 小米 MiMo（OpenAI 兼容，配置时选供应商，flash/pro 自动路由） | Anthropic Claude          |
| 成本            | 长会话 ~99% 缓存命中，几乎不为重复上下文付费                            | 订阅 / 按 Claude API 用量 |
| 内置代码图谱    | ✅ codegraph（Go 走 `go/types` 精确）                                   | ❌（靠 grep / 搜索）      |
| 本地 · 离线 OCR | ✅ PaddleOCR                                                            | ❌（图片走云端多模态）    |
| MCP             | ✅                                                                      | ✅                        |
| Skill 生态      | ✅（兼容 Claude skill 目录）                                            | ✅                        |

> [!NOTE]
> 这张表不比模型质量本身；deepx-code 的取舍是 **成本、开源、单二进制、内置代码图谱与离线 OCR**。

## 🚀 快速开始

**1. 安装**

macOS / Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/itmisx/deepx-code/main/scripts/install.sh | bash && exec $SHELL
```

Windows(PowerShell):

```powershell
irm https://raw.githubusercontent.com/itmisx/deepx-code/main/scripts/install.ps1 | iex
```

🇨🇳 国内用户可用 **Gitee 镜像**加速(源码 + 二进制都从 Gitee 拉,之后 `deepx upgrade` 自动走 Gitee):

macOS / Linux:

```bash
curl -fsSL https://gitee.com/itmisx/deepx-code/raw/main/scripts/install.sh | SOURCE=gitee bash && exec $SHELL
```

Windows PowerShell

```powershell
$env:SOURCE='gitee'; irm https://gitee.com/itmisx/deepx-code/raw/main/scripts/install.ps1 | iex
```

安装到 `~/.local/bin/deepx`，随时用 `deepx upgrade` 升级。

**2. 在终端里进入项目并启动**

deepx 是个**终端程序**:打开一个终端,`cd` 进你的项目目录,运行 `deepx` 即可进入交互式界面。

- 任何终端都行:macOS 自带 Terminal / iTerm2、Linux 终端、Windows Terminal / PowerShell。
- 也推荐 **VS Code 内置终端**(菜单 `Terminal → New Terminal`,或快捷键 `` Ctrl+` ``):它默认就在当前打开的工程目录,`deepx` 起来直接对着这个项目干活,改完文件 VS Code 里实时可见。

```bash
cd <你的项目目录>   # VS Code 内置终端通常已经在项目根,可跳过
deepx               # 进入交互式 TUI
```

**3. 配置**

| 项目         | 怎么做                                                                                                                                                                                                                                                        |
| :----------- | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 供应商 & Key | 首次启动弹出向导：**用 ←/→ 选模型供应商（DeepSeek / 小米 MiMo），再填对应 API Key**，持久化到 `~/.deepx/model.yaml`。各供应商已预置 flash/pro 默认模型与 1M 上下文（DeepSeek `deepseek-v4-flash` / `-pro`，MiMo `mimo-v2.5` / `-pro`）。也可 `/config` 重配。 |
| 手动覆盖     | 可直接编辑 `~/.deepx/model.yaml`，按 role（flash/pro）覆盖 `base_url` / `model` / `api_key` / `max_tokens` / `context_window`；flash 与 pro 也可指向不同供应商。                                                                                              |
| 多供应商切换 | 每次 `/config` 会把配置按供应商名(deepseek/mimo/kimi/qwen/custom)存档到 `~/.deepx/provider.yaml`。之后用 `/provider` 在已配置的供应商间一键切换（切换即把对应 flash/pro 写回 `model.yaml`），无需重填 key。                                                  |
| Skill        | 放到 `<工作区>/.deepx/skills/`，或复用 `~/.claude/skills/` 等已有目录。                                                                                                                                                                                       |
| MCP          | TUI 内 `/mcp-add` 添加，`/mcp-list` 查看。                                                                                                                                                                                                                    |

## ⚡ 非交互执行（`deepx exec`）

不想进全屏 TUI、想把 deepx 塞进脚本时,用 `deepx exec "<任务>"`:跑完把结果直接打到终端(stdout)再退出,只输出结果、不显示中间过程。

```bash
deepx exec "把 README 的功能列表翻译成英文,写到 README.en.md"
```

也支持管道喂数据(`cat error.log | deepx exec "分析这段报错"`)。需先用交互式 `deepx` 配好 API key。

## 🧠 核心机制

<details>
<summary><b>会话持久化（gob 二进制，无损续接）</b></summary>

```
~/.deepx/sessions/<sha1(workspace)[:16]>/
├── meta.json          # 工作区元信息
├── current            # 当前对话指针(空/"default" = 默认对话 = 本目录)
├── state.json         # 压缩状态 + 用量快照
├── YYYY-MM-DD.jsonl   # 文本日志（Memory 搜索用，跨对话）
├── history.gob        # 默认对话的完整历史
└── conversations/     # /new 开的其它对话(各自独立 history.gob / summary / state)
    └── <id>/ ...
```

> 多对话:默认对话就是本目录(老数据零迁移),`/new` 开新对话、`/sessions` 列表切换。

| 格式               | 存储内容                                                                          | 用途                       |
| :----------------- | :-------------------------------------------------------------------------------- | :------------------------- |
| `history.gob`      | system + user + assistant（含 `tool_calls`、`tool results`、`reasoning_content`） | **重启恢复，LLM 无缝续接** |
| `YYYY-MM-DD.jsonl` | user / assistant 纯文本                                                           | Memory 工具搜索            |

重启优先加载 gob，失败回退 JSONL。system prompt 因升级 / skill 变化而变动时，gob 恢复时自动原地替换为当前版本（保持缓存前缀一致）。

</details>

<details>
<summary><b>会话压缩（分层 + 摘要合并）</b></summary>

长对话超出上下文窗口 70% 时自动触发：尾部分层保留约 20K token，旧内容由 LLM 压成连贯摘要并合并已有摘要。压缩后同步更新 gob，重启一致。

</details>

<details>
<summary><b>任务规划：Todo（顺序）vs Plan DAG（并发）vs Workflow（可复用脚本）</b></summary>

- **Todo** —— 多步、强顺序、强上下文的任务（如从零搭一个应用）：模型用可见待办清单列出步骤、逐项勾选,自己一步步执行,给你实时进度。
- **CreatePlan（Plan DAG）** —— 当下这一轮里真正可并行、彼此独立的扇出任务：拆成 DAG,按依赖关系派并发子 agent,每个节点独立选 flash / pro,最后汇总。
- **Workflow（可复用脚本）** —— 把一套**会重复跑**的多 agent 流程固化成 JS 脚本(`agent()` / `parallel()` / `pipeline()` / `phase()`),存进 `.deepx/workflows/` 反复用。和上面两者的区别:Todo/Plan 是模型每轮临时规划;Workflow 是事先写定、可复用、可中断 resume 的固定流程。`/ultracode <描述>` 自动生成,`/workflow <名>` 运行。

```
CreatePlan
  ├─ plan-1: Read  (flash) ─────┐
  ├─ plan-2: Read  (flash) ─────┤
  ├─ plan-3: Grep  (flash) ─────┤
  └─ plan-4: Write (pro)   ─────┘ depends_on: [1,2,3]
```

</details>

<details>
<summary><b>本地 OCR（补齐读图能力）</b></summary>

粘贴图片或给出图片路径 → LLM 通过 `OCR` 工具（PaddleOCR PP-OCRv5）识别其中文字。首次自动下载 OCR 模型（~37MB）与 ONNX runtime，之后**离线、秒级**响应。让你不依赖多模态 API 也能让 agent「看懂」报错截图 / UI 稿。

</details>

### 🚦 模型路由（本地语义，零延迟，零 token）

消息发来时，deepx 在**本地**决定这一轮起手用 flash 还是 pro，不额外消耗任何 LLM token。判据只有两条：

```
消息 > 500 字                                     → pro（不依赖模型，离线也生效）
与「升级 pro」组样板句的最高相似度 ≥ 0.91
        且高于与「维持 flash」组的                 → pro
否则                                              → flash（兜底）
```

比的是**整句语义**而不是关键词包含 —— 「优化一下这行代码的写法」不会因为出现「优化」二字被抬成 pro，「设计稿在哪个目录」也不会因为「设计」中招。首次启动后台下载 multilingual-e5-small 量化模型（118 MB；源按 ModelScope → hf-mirror → HuggingFace 依次尝试，国内可直连），**不阻塞启动**；没就绪或下载失败时不做自动路由，起手一律 flash。

> `/model` 锁定 flash / pro 时绕过路由，`auto`（默认）才走上面的判定。起手模型本轮锁定，模型可用 `SwitchModel` 中途升到 pro，但不再降回来（切模型会让上下文缓存整段失效）。

<details>
<summary><b>调教路由：两组样板句 + 六个命令</b></summary>

两组样板句对等，各修一个方向的错：

| 组                | 内置 | 含义                       | 什么时候往里加                       |
| :---------------- | ---: | :------------------------- | :----------------------------------- |
| **升级 pro**      |   36 | 像这些 → 起手用 pro        | **漏判**：该走 pro 却起手 flash 了   |
| **维持 flash**    |   22 | 像这些 → 拉回 flash        | **误判**：问概念 / 小修小补被抬成 pro |

> 「维持 flash」组是**把已过门槛、但其实是问概念或小修小补的消息拉回来**，不是「命中它才留在 flash」—— 匹配不上任何一组的消息本来就是 flash。

| 命令                                           | 作用                                   |
| :--------------------------------------------- | :------------------------------------- |
| `/router-list-pro` `/router-list-flash`         | 看某组当前样板句（带序号）与生效规则   |
| `/router-add-pro <整句>`                        | 漏判时用                               |
| `/router-add-flash <整句>`                      | 误判时用                               |
| `/router-delete-pro <序号>` `/router-delete-flash <序号>` | 按列表序号删（两组各自从 1 排）        |

写样板句的两条原则：

1. **写完整的任务陈述，不写关键词。** ✅ `把这个服务的灰度发布流程梳理清楚`；❌ `灰度 发布 流程` —— 词串的句向量和真实用户消息差得远，基本匹配不上任何东西。
2. **越具体越安全。** 太通用的句子会把邻近的琐事一起吸过来（实测：一句过于笼统的样板句捞回 2 条的同时误伤了 2 条）。

也可以直接编辑 `~/.deepx/router.yaml`（`patterns` = 升级 pro，`flash_patterns` = 维持 flash），**下一条消息即生效，不用重启**。该文件首次启动时自动生成、内含两组内置默认；某一组你没改过的话，deepx 升级时那组会自动同步到新版内置表，**改过的组永不覆盖**。清空某组或删掉整个文件 = 恢复内置默认。

</details>

### 🧭 代码图谱（codegraph）

内置符号图谱引擎，模型直接做符号级导航 + 调用关系查询，代替满仓库 grep + 一个个翻文件。

<details>
<summary><b>操作速查表（12 个 op）</b></summary>

| op             | 用途                 | 必填参数                   | 说明                                        |
| :------------- | :------------------- | :------------------------- | :------------------------------------------ |
| `def`          | 符号定义在哪         | `name`                     | 函数 / 类型 / 方法 / 变量的定义位置         |
| `refs`         | 谁用到了某符号       | `name`                     | 全部引用（定义 + 调用 + 取值）              |
| `symbols`      | 按名模糊搜索符号     | `name`(可选), `kind`(可选) | `kind`: func/method/type/var/const/field    |
| `outline`      | 一个文件有哪些符号   | `path`                     | 文件大纲                                    |
| `imports`      | 文件 import 了哪些包 | `path`                     | 依赖概览                                    |
| `callers`      | 谁调用了某函数       | `name`                     | **改函数时查影响面**，Go 隐式接口也覆盖     |
| `callees`      | 某函数调用了哪些     | `name`                     | 理解函数内部实现流程                        |
| `implementers` | 谁实现了某接口       | `name`                     | 对 Go 隐式接口**精确到符号级**，grep 查不出 |
| `subtypes`     | 谁继承 / 嵌入某类型  | `name`                     | 子类型追踪                                  |
| `supertypes`   | 某类型派生自什么     | `name`                     | 父类型 / 嵌入接口                           |
| `impact`       | 改某符号牵连哪些下游 | `name`, `depth`(默认 3)    | 传递闭包，blast radius 分析                 |
| `reindex`      | 强制重建索引         | —                          | 缓存异常时手动触发                          |

</details>

**覆盖语言**：Go（stdlib 精确解析）+ TypeScript / JavaScript / Python / Java / Rust / C / C++ / C# / Ruby / PHP / Kotlin / Swift / Scala / Dart / Vue / Svelte。

**工作机制**：启动后台 `Prewarm` 建索引（状态栏 `loading → ready`）；文件被 Write/Update 后标 `stale`，下次查询增量重建；结果按 `文件:行`（含签名 / 调用方）展示并自动分页。

## 🧰 工具集

| 类型     | 工具                               |         plan | auto | review |
| :------- | :--------------------------------- | -----------: | :--: | :----: |
| 文件只读 | `Read` `List` `Tree` `Glob` `Grep` |            ✓ |  ✓   |   ✓    |
| 代码图谱 | `CodeGraph`                        |            ✓ |  ✓   |   ✓    |
| 文件写入 | `Write` `Update`                   |            ✗ |  ✓   |   ⏳   |
| Shell    | `Bash`                             |            ✗ |  ✓   |   ⏳   |
| 联网     | `Search` `Fetch`                   |            ✓ |  ✓   |   ✓    |
| 记忆     | `Memory`                           |            ✓ |  ✓   |   ✓    |
| 技能     | `LoadSkill`                        |            ✓ |  ✓   |   ✓    |
| 图片     | `OCR`                              |            ✓ |  ✓   |   ✓    |
| 规划     | `Todo` `CreatePlan`                | LLM 自主调用 |      |        |
| 升级     | `SwitchModel`                      | LLM 自主调用 |      |        |

> ⏳ = 自动执行，但需人工确认。

## ⌨️ Slash 命令

| 命令                                 | 作用                                                                                                                                                                                                                                                                                                         |
| :----------------------------------- | :----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `/plan` `/auto` `/review`            | 切换模式（只读 / 全自动 / 审核）                                                                                                                                                                                                                                                                             |
| `/model`                             | 弹窗选择模型（auto 按任务路由 / flash / pro 定死）；也可 `/model flash` 直接指定                                                                                                                                                                                                                             |
| `/provider`                          | 在已配置的供应商间快捷切换：弹窗选（也可 `/provider <名字>` 直切）。每次 `/config` 会把配置按供应商名存档到 `~/.deepx/provider.yaml`，切换即把对应 flash/pro 写回 `model.yaml`                                                                                                                                |
| `/reasoning`                         | 弹窗设置 `thinking` / `reasoning_effort`（flash/pro 各自独立；空值=不发该字段，对 MiMo 等不支持的模型零侵入）                                                                                                                                                                                                |
| `/router-list-pro` `/router-list-flash` `/router-add-pro` `/router-add-flash` `/router-delete-pro` `/router-delete-flash` | 调教模型路由的两组样板句（见「🚦 模型路由」）：`list` 带序号查看某组并显示生效规则，`add <整句>` 添加，`delete <序号>` 删除。漏判（该 pro 却起手 flash）加进 `-pro` 组，误判（问概念 / 小修小补被抬成 pro）加进 `-flash` 组；也可直接编辑 `~/.deepx/router.yaml`，下一条消息即生效 |
| `/compact`                           | 手动压缩会话以节省上下文                                                                                                                                                                                                                                                                                     |
| `/new` `/sessions`                   | 开启全新对话 / 历史对话列表（↑↓ 选，Enter 切换）                                                                                                                                                                                                                                                             |
| `/status`                            | 显示/隐藏右侧状态栏（也可按 `Ctrl+B`）                                                                                                                                                                                                                                                                       |
| `/web-config`                        | 弹窗设置 web 面板绑定 IP 与端口（填「IP [端口]」，空格分隔；IP 留空/`127.0.0.1`=仅本机，`0.0.0.0`=局域网手机/平板可访问，端口可省=随机）。保存即热生效并显示新地址，无需重启；配置存入会话 `meta.json`，访问令牌按会话固定、跨重启不变。⚠️ 该面板可控制会话、执行命令，且为明文 HTTP，对外暴露仅限可信局域网 |
| `/sandbox`                           | 沙箱模式：`off`（关闭）/ `native`（默认，OS 隔离：macOS Seatbelt、Linux bubblewrap，写操作限定在 workspace + 进程隔离；无 OS 机制的平台退回软策略黑名单）/ `docker`（容器隔离，`/sandbox docker <镜像>`）                                                                                                    |
| `/working-mode`                      | 工作模式（方法论）：`karpathy`（默认，务实工匠）/ `openspec`（规格驱动）/ `superpowers`（全流程严谨）；弹窗选择，也可 `/working-mode kp\|spec\|sp` 直切。三种模式互斥——选中一种会禁用另外两种对应的 skill，避免方法论混搭。切换后存入会话，每轮自动注入提示且不污染历史                                      |
| `/ultracode` `/workflow` `/workflows` | workflow(JS 多 agent 编排):`/ultracode <描述>` 让模型生成并保存一个 workflow,`/workflow <名> [k=v]` 运行(运行前需确认),`/workflows` 列出已有                                                                                                                                                                |
| `/lang`                              | 切换界面语言（中 / 英）                                                                                                                                                                                                                                                                                      |
| `/mcp-list` `/mcp-add` `/mcp-delete` | 管理 MCP server                                                                                                                                                                                                                                                                                              |
| `/skills` `/config` `/mode`          | 列出 skill / 重配 key / 查看模式                                                                                                                                                                                                                                                                             |
| `/help`                              | 帮助                                                                                                                                                                                                                                                                                                         |
| `/exit`                              | 退出 deepx                                                                                                                                                                                                                                                                                                   |

## 🛡️ 审核模式

| 模式             | Write / Update / Bash | 其余工具 | 切换命令  |
| :--------------- | :-------------------- | :------- | :-------- |
| `review`（默认） | 人工 YES/NO 确认      | 自动执行 | `/review` |
| `auto`           | 自动执行              | 自动执行 | `/auto`   |
| `plan`           | 禁用                  | 自动执行 | `/plan`   |

## 📦 Skills 生态

```
workspace 级  <wd>/.deepx/skills/
global 级     ~/.agents/skills/ → ~/.claude/skills/ → ~/.deepx/skills/
```

- workspace 级可 `git add` 共享给团队
- global 兼容 Claude Code 生态，已有 skill 直接复用

## 🏗️ 架构

<details>
<summary><b>展开数据流</b></summary>

```
单轮对话:
  用户输入
    ↓
  RouteByKeyword (本地) ─► flash 或 pro
    ↓
  StartStream (主循环)
    ├─ 直接答
    ├─ 调工具 → review 拦截写/Shell → 执行 → 结果回灌 → 继续
    ├─ Todo → 可见待办清单(主 agent 自己逐步执行)
    ├─ SwitchModel → 升 pro
    └─ CreatePlan → DAG scheduler → 子 agent 并发 → 汇总

会话持久化:
  HistoryUpdateMsg → SaveGob (history.gob, 完整 fidelity)
  StreamDoneMsg    → Append JSONL (纯文本, Memory 搜索)
  重启             → LoadGob (优先) / JSONL (回退)

会话压缩:
  tokens ≥ ctxWindow × 70% → runCompression (异步)
    → 尾部分层保留 ~20K token → LLM 合并新旧摘要 → 更新 gob + state.json
```

</details>

**目录结构**

```
deepx/
├── main.go
├── agent/      StartStream 工具循环 + 路由 + DAG 调度 + 子 agent
├── config/     ~/.deepx/model.yaml 读写
├── session/    gob 持久化 + JSONL 日志 + 会话压缩状态
├── tools/      全部工具实现（读写 / 搜索 / OCR / Memory / Skill / Plan / CodeGraph）
├── codegraph/  代码图谱：跳定义 / 找调用 / 继承实现 / 影响面
├── skill/      多路径 skill 发现与加载
├── ocr/        PaddleOCR 包装（ONNX Runtime）
├── tui/        bubbletea TUI（输入 / 渲染 / 剪贴板 / 选中 / 仪表盘）
└── scripts/    安装脚本
```

## 💰 Token 经济

- **路由零 token**：纯本地句向量比对，不发 LLM 调用
- **工具不预注入**：`Memory` / `LoadSkill` 只在调用时才进 context
- **system prompt 极简**：仅跨工具规约 + workspace，工具触发条件在各自 description 里
- **DeepSeek KV cache 友好**：tools 数组不随模式 / 角色变化；system prompt gob 恢复时版本感知
- **代码图谱替代盲搜**：从根上减少 read / glob / grep 的 token 浪费

## 🩹 卸载

```bash
# macOS / Linux
rm -f ~/.local/bin/deepx && rm -rf ~/.deepx

# Windows：删除 %LOCALAPPDATA%\Programs\deepx 和 %USERPROFILE%\.deepx
```

## 📄 License

[MIT](LICENSE) © 2026 itmisx
