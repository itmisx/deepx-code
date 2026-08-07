package router

import (
	"os"
	"path/filepath"
	"strings"
)

// 默认模式元句，作为文件缺失时的回退。

var DefaultSimplePatterns = []string{
	"简单的文件读写操作不需要深度推理",
	"查看代码定义和简单的搜索匹配",
	"单行或几行的代码修改不用理解全局架构",
	"编译运行和测试执行等机械操作",
	"简单的配置修改和参数调整",
	"查看日志和错误信息不需要深度分析",
	"解释简单概念和回答基础知识问题",
}

var DefaultMediumPatterns = []string{
	"修改单个文件的代码需要理解局部逻辑",
	"添加新功能需要理解模块接口",
	"讲解代码逻辑和执行流程",
	"查找调用链和依赖关系",
	"修复普通bug需要理解函数逻辑",
}

var DefaultComplexPatterns = []string{
	"排查性能瓶颈内存泄漏死锁并发问题并分析根因修复方案",
	"跨模块设计接口和抽象层",
	"分析系统级调用链和依赖关系",
	"重构现有代码架构",
	"设计新的模块接口API规范",
	"分析复杂系统设计文档架构决策调用链和依赖图并推理因果",
	"修复跨模块的复杂bug",
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
