package router

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"deepx/config"
)

// 用编辑器改完 router.yaml 不该被要求重启 deepx。
// 每次判定前按需重载(stat 不变就跳过),所以下一条消息就生效。
func TestHotReload_PicksUpFileEdits(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetReloadState()
	t.Cleanup(func() { SetUserPatterns(nil, nil); resetReloadState() })

	// ① 没有文件 → 用内置默认
	patternVectorsSourceForTest()
	if got := activeSource(); !slices.Equal(got, complexPatterns) {
		t.Fatalf("无配置时应用内置默认,got %d 条", len(got))
	}

	// ② 写一份自定义,不做任何"通知" —— 模拟用户直接拿编辑器改
	mine := []string{"把这个服务的灰度发布流程梳理清楚"}
	if err := config.SaveRouterPatterns(config.RouterPatterns{Pro: mine}, builtinBoth()); err != nil {
		t.Fatal(err)
	}
	patternVectorsSourceForTest()
	if got := activeSource(); !slices.Equal(got, mine) {
		t.Fatalf("改文件后应即时生效(无需重启),want %v got %v", mine, got)
	}

	// ③ 删掉文件 → 回落内置默认
	p, _ := config.RouterPath()
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	touchLater(t, filepath.Dir(p))
	patternVectorsSourceForTest()
	if got := activeSource(); !slices.Equal(got, complexPatterns) {
		t.Fatalf("删掉配置后应回落内置默认,got %d 条", len(got))
	}
}

// YAML 改坏的那一刻,应沿用上一次读到的好配置,而不是悄悄换回内置默认 ——
// 换回去的话用户会看到路由行为莫名全变,却联想不到是自己少打了一个缩进。
func TestHotReload_BrokenYAMLKeepsLastGood(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetReloadState()
	t.Cleanup(func() { SetUserPatterns(nil, nil); resetReloadState() })

	mine := []string{"把这个服务的灰度发布流程梳理清楚"}
	if err := config.SaveRouterPatterns(config.RouterPatterns{Pro: mine}, builtinBoth()); err != nil {
		t.Fatal(err)
	}
	patternVectorsSourceForTest()
	if got := activeSource(); !slices.Equal(got, mine) {
		t.Fatalf("前提不成立:want %v got %v", mine, got)
	}

	p, _ := config.RouterPath()
	if err := os.WriteFile(p, []byte("patterns: [不闭合\n"), 0600); err != nil {
		t.Fatal(err)
	}
	patternVectorsSourceForTest()
	if got := activeSource(); !slices.Equal(got, mine) {
		t.Errorf("YAML 坏掉时应沿用上一次的好配置,got %v", got)
	}
}

// 两组独立:只动 pro 组时,flash 组必须原样不动。
func TestHotReload_GroupsAreIndependent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetReloadState()
	t.Cleanup(func() { SetUserPatterns(nil, nil); resetReloadState() })

	before := ActiveFlashPatterns()
	if err := config.SaveRouterPatterns(config.RouterPatterns{Pro: []string{"随便一条"}}, builtinBoth()); err != nil {
		t.Fatal(err)
	}
	patternVectorsSourceForTest()

	if after := ActiveFlashPatterns(); !slices.Equal(before, after) {
		t.Errorf("只改了 pro 组,flash 组不该跟着变:%d → %d 条", len(before), len(after))
	}
	// pro 组那条不能串到 flash 组
	if slices.Contains(ActiveFlashPatterns(), "随便一条") {
		t.Error("pro 组的内容串到 flash 组去了")
	}
}

// === 测试辅助 ===

// activeSource 返回当前生效的正样本(不经过向量化,纯看数据源)。
func activeSource() []string { return srcOf(&userProPatterns, complexPatterns) }

// patternVectorsSourceForTest 触发一次按需重载。不能直接调 patternVectors():
// 那会去做 embedding,而这些测试不需要模型。
func patternVectorsSourceForTest() { reloadUserPatterns() }

// builtinBoth 把两组内置表打包给 config 算指纹。
func builtinBoth() config.RouterPatterns {
	return config.RouterPatterns{Pro: complexPatterns, Flash: simplePatterns}
}

func resetReloadState() {
	reloadMu.Lock()
	lastStamp, stampValid = "", false
	reloadMu.Unlock()
}

// touchLater 保证后续写入的 mtime 与之前不同(某些文件系统 mtime 粒度较粗)。
func touchLater(t *testing.T, dir string) {
	t.Helper()
	time.Sleep(10 * time.Millisecond)
	_ = dir
}

// 反例内置只读,一个进程里只该编码一次。
// 跟正样本共用缓存键的话,用户每 /router-add 一条就要白编 22 句(实测约 50ms)。
//
// 用切片身份(而不是耗时)判断:重编会得到一个新的底层数组,时间则受机器负载干扰太大。
func TestPatternVectors_NegativesEncodedOnce(t *testing.T) {
	requireModel(t)
	SetUserPatterns(nil, nil)
	t.Cleanup(func() { SetUserPatterns(nil, nil) })

	_, neg1 := patternVectors()
	if len(neg1) == 0 {
		t.Fatal("反例向量为空,前提不成立")
	}

	// 改动正样本 —— 正例必须重算,反例不该动
	SetUserPatterns(append(DefaultProPatterns(), "把这个服务的灰度发布流程梳理清楚"), nil)
	pos2, neg2 := patternVectors()

	if len(pos2) != len(DefaultProPatterns())+1 {
		t.Errorf("正样本没有跟着重算:want %d 条 got %d", len(DefaultProPatterns())+1, len(pos2))
	}
	if &neg1[0][0] != &neg2[0][0] {
		t.Error("反例被重新编码了 —— 它是内置只读的,不该随正样本重算")
	}
}
