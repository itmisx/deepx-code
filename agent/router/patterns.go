package router

import (
	"os"
	"path/filepath"
	"strings"
)

// 默认模式元句，作为文件缺失时的回退。

var DefaultSimplePatterns = []string{
	"查看代码定义和简单的搜索匹配",
	"单行或几行的代码修改",
	"编译运行和测试执行",
	"查看日志和错误信息",
	"解释简单概念和回答基础知识问题",
	"翻译文档和注释",
	"执行lint和格式化代码检查",
	"简单的配置修改和参数调整",
	"查看变量值和类型定义",
	"读取文件内容不需要分析",
}

var DefaultMediumPatterns = []string{
	"修改单个文件的代码需要理解局部逻辑",
	"添加新功能需要理解模块接口",
	"讲解代码逻辑和执行流程",
	"查找调用链和依赖关系",
	"定位编译错误和警告原因",
	"分析单一模块的代码结构和设计模式",
	"修复普通bug需要理解函数逻辑",
	"理解配置项和功能开关的代码路径",
	"修改组件间通信的单个接口调用",
	"检查单个模块的内存和性能问题",
}

var DefaultComplexPatterns = []string{
	"跨多个文件重构代码结构和调用关系",
	"排查跨模块的复杂bug根因和修复方案",
	"分析现有系统的调用链和依赖关系",
	"重构现有模块的接口和抽象层",
	"合并多个分支的代码解决冲突",
	"排查性能瓶颈和内存泄漏的根因",
	"分析跨模块的数据流和状态管理",
	"设计新的模块接口和API规范",
	"修复跨模块的并发和同步问题",
	"研究技术方案实现策略制定执行计划",
}

var DefaultDeepPatterns = []string{
	"审查代码质量和安全性查找潜在漏洞边界条件和逻辑错误",
	"设计系统级架构和技术选型方案",
	"制定完整的模块开发计划和架构决策",
	"评估系统级变更的影响范围和风险",
	"设计高可用和高性能的系统架构",
	"权衡多种技术方案的利弊做出架构决策",
}

func patternsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".deepx", "router", "patterns")
}

func loadPatterns(filename string, defaults []string) []string {
	dir := patternsDir()
	if dir == "" {
		return defaults
	}
	path := filepath.Join(dir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return defaults
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return defaults
	}
	return out
}

func LoadSimplePatterns() []string  { return loadPatterns("simple.toml", DefaultSimplePatterns) }
func LoadMediumPatterns() []string  { return loadPatterns("medium.toml", DefaultMediumPatterns) }
func LoadComplexPatterns() []string { return loadPatterns("complex.toml", DefaultComplexPatterns) }
func LoadDeepPatterns() []string    { return loadPatterns("deep.toml", DefaultDeepPatterns) }
