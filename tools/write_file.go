package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// placeholderPatterns:疑似「缺失值/占位标记」的文本模式(小写匹配)。
// 命中说明模型很可能把元描述/占位符当 content 写入了(上下文污染),应拒绝并提示重写。
// 注意:错误提示里刻意不回显这些具体字符串 —— 错误信息会进模型上下文,
// 回显占位符等于再把污染样本喂回去(对话消毒原则)。
var placeholderPatterns = []string{
	"<elided>", "<omitted>", "<missing>", "<empty>", "<redacted>", "<truncated>",
	"[已写入", "[write参数", "[参数已折叠", "[content ", "content omitted",
	"file content omitted", "[truncated]", "... (truncated)", "内容已省略", "内容省略",
}

// validateWriteContent 写入前校验 content 是否为疑似占位符/缺失值文本:
//   - 命中占位符模式 → 拒绝(不设长度门槛):最该拦的折叠文本(如 "[已写入 ...]" 61~70
//     字节)远超旧阈值 32 字节,长度门槛会把它放过去;模式表本身足够精确
//     (<elided>/[已写入 不会出现在真实代码),去掉门槛不增加实际误伤;
//   - 空内容 → 放行:建空文件(__init__.py/.gitkeep)是合法需求,plan 模式只读工具集
//     无 Bash、建不了空文件 —— 拒绝空内容属于功能回退,启发式防护不值这个代价。
//
// 错误提示只描述性质、给修正方向,不回显占位符文本。
func validateWriteContent(content string) error {
	low := strings.ToLower(content)
	for _, p := range placeholderPatterns {
		if strings.Contains(low, p) {
			return fmt.Errorf("Write 拒绝: content 疑似缺失值/占位标记,而非真实文件内容。" +
				"这通常是上下文污染导致模型把元描述当内容输出。请重新提供完整的真实内容;" +
				"若确需写入极短内容,请补充上下文或调整写法")
		}
	}
	return nil
}

// WriteFile 写入（覆盖）文本文件。
// 参数:
//
//	path    (string) 文件路径
//	content (string) 写入的内容
//
// 成功结果给出确定性元信息(字节数/行数/sha256 前缀),让模型"确实写入了"、
// 无需立即 Read 验证;不放内容预览(预览会成为新的模仿源)。
func WriteFile(args map[string]any) ToolResult {
	path, _ := args["path"].(string)
	if path == "" {
		return ToolResult{Output: "错误: path 参数为空", Success: false}
	}
	content, _ := args["content"].(string)
	if err := validateWriteContent(content); err != nil {
		return ToolResult{Output: err.Error(), Success: false}
	}

	absPath, err := confineToWorkspace(path)
	if err != nil {
		return ToolResult{Output: err.Error(), Success: false}
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return ToolResult{Output: fmt.Sprintf("创建父目录失败: %v", err), Success: false}
	}
	if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
		return ToolResult{Output: fmt.Sprintf("写入失败: %v", err), Success: false}
	}
	CodeGraphInvalidate() // 文件变了,代码图谱缓存失效,下次查询重建
	sum := sha256.Sum256([]byte(content))
	lines := strings.Count(content, "\n") + 1
	return ToolResult{
		Output:  fmt.Sprintf("已写入 %s (%d bytes, %d 行, sha256: %s)", absPath, len(content), lines, hex.EncodeToString(sum[:4])),
		Success: true,
	}
}
