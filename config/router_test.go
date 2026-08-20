package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testBuiltin 是这些测试里充当"内置表"的那份,用来算 builtin_hash。
var testBuiltin = RouterPatterns{
	Pro:   []string{"内置甲", "内置乙", "内置丙"},
	Flash: []string{"反例甲", "反例乙"},
}

// router.yaml 的核心语义:**空 = 用内置默认**。
// 文件不存在、内容为空、patterns 为空,三种都必须回落默认,而不是"一个词都不匹配"
// —— 后者等于样板句路由整体失效、只剩长度兜底,不会是用户想要的。
func TestLoadRouterPatterns_EmptyMeansDefault(t *testing.T) {
	cases := []struct {
		name    string
		content *string // nil = 不创建文件
	}{
		{"文件不存在", nil},
		{"空文件", ptr("")},
		{"只有注释", ptr("# 什么都没配\n")},
		{"patterns 为空列表", ptr("patterns: []\n")},
		{"patterns 为 null", ptr("patterns:\n")},
		{"只有空白项", ptr("patterns:\n  - \"\"\n  - \"   \"\n")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			if c.content != nil {
				writeRouterYAML(t, *c.content)
			}
			got, err := LoadRouterPatterns()
			if err != nil {
				t.Fatalf("不该报错: %v", err)
			}
			if got.Pro != nil || got.Flash != nil {
				t.Errorf("应回落默认(两组都 nil),got %+v", got)
			}
		})
	}
}

func TestLoadRouterPatterns_ReadsList(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeRouterYAML(t, "patterns:\n  - 重构\n  - refactor\n  - \"  架构  \"\n")

	cur, err := LoadRouterPatterns()
	if err != nil {
		t.Fatalf("%v", err)
	}
	got := cur.Pro
	want := []string{"重构", "refactor", "架构"} // 首尾空白被裁掉
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] want %q got %q", i, want[i], got[i])
		}
	}
}

// 解析失败必须报错,不能静默回落默认 —— 用户改坏了 yaml 却以为生效了,最难查。
func TestLoadRouterPatterns_BadYAMLErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeRouterYAML(t, "patterns: [不闭合\n")
	if _, err := LoadRouterPatterns(); err == nil {
		t.Error("坏 yaml 应报错,而不是静默用默认表")
	}
}

// 存空表 = 恢复默认,直接删文件。留一个空 patterns 会让用户困惑
// "我明明有这个文件为什么还是默认行为"。
func TestSaveRouterPatterns_EmptyRemovesFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := SaveRouterPatterns(RouterPatterns{Pro: []string{"重构"}}, testBuiltin); err != nil {
		t.Fatalf("%v", err)
	}
	p, _ := RouterPath()
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("应已写盘: %v", err)
	}

	if err := SaveRouterPatterns(RouterPatterns{}, testBuiltin); err != nil {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("存空表应删掉文件(= 恢复默认)")
	}
	// 删过之后再存空表不该报错
	if err := SaveRouterPatterns(RouterPatterns{}, testBuiltin); err != nil {
		t.Errorf("重复恢复默认不该报错: %v", err)
	}
}

func TestSaveThenLoad_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	want := []string{"重构", "refactor", "灰度发布"}
	if err := SaveRouterPatterns(RouterPatterns{Pro: want}, testBuiltin); err != nil {
		t.Fatalf("%v", err)
	}
	cur, err := LoadRouterPatterns()
	if err != nil {
		t.Fatalf("%v", err)
	}
	got := cur.Pro
	if len(got) != len(want) {
		t.Fatalf("want %v got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] want %q got %q", i, want[i], got[i])
		}
	}
}

func writeRouterYAML(t *testing.T, content string) {
	t.Helper()
	p, err := RouterPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func ptr(s string) *string { return &s }

// === 启动时自动生成 router.yaml(EnsureRouterFile)===

// 没有文件就生成一份内置表 —— 用户得有个能直接编辑的起点。
func TestEnsureRouterFile_CreatesWithBuiltin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	res, err := EnsureRouterFile(testBuiltin)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !res.Created {
		t.Errorf("首次应为 Created, got %+v", res)
	}
	got, err := LoadRouterPatterns()
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(got.Pro) != len(testBuiltin.Pro) || len(got.Flash) != len(testBuiltin.Flash) {
		t.Fatalf("两组内置表都该写出来,want %+v got %+v", testBuiltin, got)
	}

	// 生成的文件要能被人读懂:顶部有说明,不是一坨裸 YAML。
	p, _ := RouterPath()
	raw, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(raw), "#") {
		t.Error("生成的文件应带注释头,用户直接编辑时那是他唯一的说明书")
	}
	if !strings.Contains(string(raw), "builtin_hash") {
		t.Error("应写入 builtin_hash,否则无法判断用户后来改没改过")
	}
}

// 用户没改过 → 内置表升级时自动同步。
// 没有这条,自动生成等于把每个用户永久冻结在初次运行的版本上。
func TestEnsureRouterFile_RefreshesWhenUntouched(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := EnsureRouterFile(testBuiltin); err != nil {
		t.Fatalf("%v", err)
	}

	// 同一份内置表再来一次:没变,不该动文件
	if res, _ := EnsureRouterFile(testBuiltin); res.RefreshedPro || res.RefreshedFlash {
		t.Errorf("内置表没变时不该重写, got %+v", res)
	}

	// 只升级 pro 组
	upgraded := testBuiltin
	upgraded.Pro = append(append([]string(nil), testBuiltin.Pro...), "内置丁")
	res, err := EnsureRouterFile(upgraded)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !res.RefreshedPro {
		t.Fatalf("pro 组用户没改过时应自动同步, got %+v", res)
	}
	if res.RefreshedFlash {
		t.Error("flash 组内置表没变,不该被标记为同步过")
	}
	got, _ := LoadRouterPatterns()
	if len(got.Pro) != len(upgraded.Pro) {
		t.Errorf("pro 组应同步为新内置表 %v, got %v", upgraded.Pro, got.Pro)
	}
}

// 用户改过 → 永不覆盖。这是自动生成能被接受的前提。
func TestEnsureRouterFile_NeverOverwritesUserEdits(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := EnsureRouterFile(testBuiltin); err != nil {
		t.Fatalf("%v", err)
	}

	mine := []string{"我自己写的一条任务陈述"}
	if err := SaveRouterPatterns(RouterPatterns{Pro: mine}, testBuiltin); err != nil {
		t.Fatalf("%v", err)
	}

	upgraded := testBuiltin
	upgraded.Pro = append(append([]string(nil), testBuiltin.Pro...), "内置丁")
	if res, _ := EnsureRouterFile(upgraded); res.RefreshedPro {
		t.Fatalf("用户改过之后绝不能被覆盖, got %+v", res)
	}
	got, _ := LoadRouterPatterns()
	if len(got.Pro) != 1 || got.Pro[0] != mine[0] {
		t.Errorf("用户内容被改动了:want %v got %v", mine, got.Pro)
	}
}

// YAML 被改坏时同样不能覆盖 —— 少打一个缩进就丢掉全部自定义,是最不能接受的行为。
func TestEnsureRouterFile_KeepsBrokenYAML(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeRouterYAML(t, "patterns: [不闭合\n")

	res, err := EnsureRouterFile(testBuiltin)
	if res.RefreshedPro || res.RefreshedFlash || res.Created {
		t.Errorf("坏 yaml 不该被覆盖, got %+v", res)
	}
	if err == nil {
		t.Error("坏 yaml 应报错,让用户知道自己改坏了")
	}
	raw, _ := os.ReadFile(mustRouterPath(t))
	if !strings.Contains(string(raw), "不闭合") {
		t.Error("用户的原始内容应原样保留")
	}
}

// RouterPatternsCustomized 必须比内容,不能只看文件在不在 ——
// 文件是启动时自动生成的,看存在性会把每个人都判成"已自定义"。
func TestRouterPatternsCustomized(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := EnsureRouterFile(testBuiltin); err != nil {
		t.Fatalf("%v", err)
	}
	if pro, flash := RouterPatternsCustomized(testBuiltin); pro || flash {
		t.Errorf("刚自动生成、内容等于内置表,不该算已自定义 (pro=%v flash=%v)", pro, flash)
	}
	if err := SaveRouterPatterns(RouterPatterns{Pro: []string{"我改的"}}, testBuiltin); err != nil {
		t.Fatalf("%v", err)
	}
	pro, flash := RouterPatternsCustomized(testBuiltin)
	if !pro {
		t.Error("pro 组内容与内置表不同,应算已自定义")
	}
	if flash {
		t.Error("flash 组没动过,不该被算成已自定义 —— 两组必须独立判断")
	}
}

func mustRouterPath(t *testing.T) string {
	t.Helper()
	p, err := RouterPath()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// 指纹的归一化口径必须两侧一致。
//
// 曾经写错过:写入侧用 hashPatterns(builtin)(未 trim),回读侧用 hashPatterns(trimAll(...))。
// 内置表干净时看不出来,一旦有人在 patterns.go 里多打一个空格,**所有用户的文件一出生
// 就被判成"已自定义",从此静默地再也收不到内置表更新** —— 没有任何现象能让人
// 联想到是一个空格引起的。所以这里刻意用带空白的内置表来守。
func TestEnsureRouterFile_HashNormalizationIsSymmetric(t *testing.T) {
	for _, tc := range []struct {
		name string
		v1   RouterPatterns
	}{
		{"内置表干净(现状)", RouterPatterns{Pro: []string{"正常一条", "另一条"}, Flash: []string{"反例"}}},
		{"内置表某条带空白", RouterPatterns{Pro: []string{"正常一条", "  带空白的一条  "}, Flash: []string{"反例"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			if _, err := EnsureRouterFile(tc.v1); err != nil {
				t.Fatal(err)
			}
			v2 := tc.v1
			v2.Pro = append(append([]string(nil), tc.v1.Pro...), "新版新增的一条")

			res, _ := EnsureRouterFile(v2) // 用户一个字都没动过
			fmt.Printf("  %-18s 用户没动过,升级时 → RefreshedPro=%v  期望 true\n", tc.name, res.RefreshedPro)
			if !res.RefreshedPro {
				t.Errorf("用户没改过却拿不到内置表更新(被误判成已自定义)")
			}
		})
	}
}
