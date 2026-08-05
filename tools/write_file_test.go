package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateWriteContent_Valid(t *testing.T) {
	cases := []string{
		strings.Repeat("hello world\n", 10), // 正常长内容
		"hello",                             // 极短但正常,不命中占位符
		strings.Repeat("a", 100) + "marker" + strings.Repeat("b", 100), // 长内容含非占位符字样放行
	}
	for _, c := range cases {
		if err := validateWriteContent(c); err != nil {
			t.Fatalf("应放行: %q, err=%v", c, err)
		}
	}
}

func TestValidateWriteContent_Rejected(t *testing.T) {
	cases := []string{
		"",
		"   \n ",
		"<elided>",
		"<omitted>",
		"<missing>",
		"<redacted>",
		"[已写入 a.txt",
		"[参数已折叠",
		"content omitted",
		"内容已省略",
		// 长占位符(实测 61~70 字节)也必须拒 —— 去掉长度门槛后命中模式即拒,
		// 不再因为"超 32 字节"而放行最该拦的折叠文本。
		"[已写入 config.yaml,1247 字节/42 行;需要内容用 Read 查看]",
		"[已写入 a.go,100 字节/5 行;需要内容用 Read 查看]",
		"这是一个很长的描述:内容已省略,请用 Read 查看原文,这里写的是摘要信息",
	}
	for _, c := range cases {
		if err := validateWriteContent(c); err == nil {
			t.Fatalf("应拒绝: %q", c)
		}
	}
}

func TestWriteFile_RejectsPlaceholder(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.txt")
	res := WriteFile(map[string]any{"path": p, "content": "<elided>"})
	if res.Success {
		t.Fatalf("占位符写入应被拒绝, got=%+v", res)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("拒绝后不应产生文件, err=%v", err)
	}
}

func TestValidateWriteContent_EmptyMessageMentionsPython(t *testing.T) {
	// 空内容提示应引导 python 而非 touch(Windows 无 touch)。
	err := validateWriteContent("")
	if err == nil {
		t.Fatalf("空内容应被拒绝")
	}
	if !strings.Contains(err.Error(), "python") {
		t.Fatalf("空内容提示应给出 python 建空文件方法, got=%q", err.Error())
	}
}

func TestWriteFile_AllowsRealContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "real.txt")
	content := "line1\nline2\n" + strings.Repeat("fill\n", 30)
	res := WriteFile(map[string]any{"path": p, "content": content})
	if !res.Success {
		t.Fatalf("真实内容应写入成功, got=%+v", res)
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != content {
		t.Fatalf("写入内容不一致, err=%v", err)
	}
	// 成功结果应给出确定性元信息(字节数/行数/校验值),让模型无需 Read 验证。
	for _, want := range []string{"bytes", "行", "sha256"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("成功结果应含 %q(确定性元信息), got=%q", want, res.Output)
		}
	}
}
