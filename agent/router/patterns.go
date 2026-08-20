package router

// complexPatterns 是"复杂任务"的语义样板句 —— 路由的**唯一判据**。
// 用户消息与其中任一足够相似(见 simThreshold),起手就用 pro。
//
// 这里原先还有一张关键词表(子串匹配),已整体删除。原因是实测数据:24 条中文语料上,
// 关键词表贡献了全部 7 条误判,而且全是同一种病 —— 子串匹配没有语义,
// 「设计」命中「设计稿在哪个目录」、「优化」命中「优化一下这行代码的写法」、
// 「安全」命中「这个安全吗」。这类误判没法靠调词表修好:那些词本身是对的,
// 错的是"出现即算数"这个机制。换成整句比对后,中文误判从 7 降到 2。
//
// 挑选原则:
//  1. **写成完整的任务陈述**,不是关键词罗列 —— 句向量比的是整句语义,
//     喂"重构 架构 设计"这种词串,得到的向量和真实用户消息差得远。
//  2. **中英各写一套,不靠跨语言对齐**。multilingual-e5 确实能把中英同义句拉近
//     (实测同义不同语 0.894 > 异义同语 0.827),但那点差距不够用:英文句子对
//     中文样板句的相似度只有 0.84~0.90,正好压在中文琐事的 0.87~0.90 上,
//     一个阈值分不开两种语言。同语言内部才有足够区分度(同义 0.92+ / 琐事 0.87)。
//     所以两套句子一一对应,让英文去匹配英文。
//  3. **只放正样本,不放琐事反例**。语义只做加法(见 router.go),
//     放反例也没有降级通道去用它。
var complexPatterns = []string{
	// === 中文 ===

	// 结构调整
	"把某个模块拆成独立的服务",
	"这几个文件的职责混在一起,需要重新划分",
	"把散落在各处的重复逻辑抽取成公共部分",
	"这个函数太长了,需要拆分成多个小函数",

	// 根因排查
	"线上偶发的错误需要定位根本原因",
	"这段代码为什么会内存泄漏",
	"帮忙看一下这个并发竞态问题",
	"程序在特定条件下会卡住,需要查清楚原因",

	// 全局理解
	"梳理这几个包之间的调用关系和依赖",
	"理解整个模块的执行流程和数据流向",
	"评估这个改动会影响到哪些地方",

	// 迁移与选型
	"把数据库从一种换成另一种",
	"在几种技术方案之间做取舍和选择",
	"制定分阶段的改造计划和回滚预案",

	// 质量工程
	"给这个模块补上完整的单元测试和集成测试",
	"审查代码里潜在的安全漏洞和边界条件",
	"通读这个 PR 的全部改动,找出其中的安全隐患和风险点",
	"定位这个性能瓶颈到底出在哪里",

	// === English ===
	// 与上面中文逐条对应。英文用户的消息靠这批句子匹配,不依赖跨语言对齐。

	// 结构调整
	"split this module into a standalone service",
	"these files have muddled responsibilities and need to be reorganized",
	"extract the duplicated logic scattered across the codebase into a shared helper",
	"this function is too long and should be broken into smaller pieces",

	// 根因排查
	"track down the root cause of an intermittent production failure",
	"why does this code leak memory",
	"investigate this concurrency race condition",
	"the program hangs under certain conditions and I need to find out why",

	// 全局理解
	"map out the call graph and dependencies between these packages",
	"understand the execution flow and data flow through the whole module",
	"assess which parts of the system this change will affect",

	// 迁移与选型
	"migrate the database from one engine to another",
	"weigh the trade-offs between several technical approaches",
	"draw up a phased migration plan with a rollback strategy",

	// 质量工程
	"add thorough unit and integration tests for this module",
	"audit the code for security vulnerabilities and edge cases",
	"read through all the changes in this PR and find the security risks",
	"pin down where this performance bottleneck actually comes from",
}

// simplePatterns 是**负样本**:求知问句和琐碎改动。消息离它们更近就维持 flash。
//
// 为什么需要负样本(而不是只靠一个阈值):
// 「为什么要做代码重构」与任务样板句的相似度是 0.912,比真任务里最低的
// 「线上偶发 502,帮我定位」(0.916)只差 0.004 —— 单一阈值切不开这两类。
// 但它离「为什么要用消息队列」这种问概念的句子近得多,两类一比就分得很清楚。
//
// 原先这件事是靠一组前缀 / 后缀表(什么是… / how to… / …是什么)硬挡的,
// 已随关键词表一并删除:字面匹配没有语义,「why」既挡掉了求知也挡掉了排查
// (「why does this code leak memory」是排查,不是求知)。
// 换成负样本后,判据仍然只有语义一条,而且正负两边都能靠 /router-add 调。
//
// 挑选原则与正样本一致:写成完整句子;覆盖"问概念 / 小修小补"这两类真实说法。
// 负样本离正样本太远的话起不到分界作用 —— 所以这里刻意包含带"重构""架构""优化"
// 字眼的问句,那正是最容易被正样本吸走的一类。
var simplePatterns = []string{
	// === 中文 ===

	// 求知:问概念 / 问原理
	"什么是依赖注入这个概念",
	"为什么要做代码重构,它的好处是什么",
	"解释一下微服务架构的基本原理",
	"性能优化一般都有哪些常见思路",
	"这两种技术之间有什么区别",

	// 求知:问用法 / 问配置
	"这个命令行工具应该怎么用",
	"某个中间件的配置文件该怎么写",

	// 琐碎改动
	"改一个错别字或者调整一下措辞",
	"在这里加一行日志",
	"把这个变量改个名字",
	"调整一下这行代码的写法",

	// === English ===

	// 求知
	"what does this concept mean",
	"why do people do refactoring and what are the benefits",
	"explain the basic idea behind microservice architecture",
	"what are the common approaches to performance optimization",
	"what is the difference between these two technologies",
	"how do I use this command line tool",
	"how should this middleware config file be written",

	// 琐碎改动
	"fix a typo or reword this sentence",
	"add one log line here",
	"rename this variable",
	"tweak the style of this one line",
}
