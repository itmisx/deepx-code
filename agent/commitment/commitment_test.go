package commitment

import (
	"fmt"
	"testing"
)

// TestDetect 承诺检测:中英承诺 → 提取;解释性/普通陈述 → 不提取;数量提取。
func TestDetect(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int
	}{
		{"中文承诺+数量", "接下来生成 10 个文件", 1},
		{"中文承诺", "好的,先写 config.py 的内容", 1},
		{"英文承诺+数量", "Now batch 3: write all 10 files", 1},
		{"英文承诺", "I will write config.yaml", 1},
		{"英文:next run", "Next, run the verification command", 1},
		{"解释性:说明", "下面说明文件生成方案", 0},
		{"解释性:explain", "I will explain how to write files", 0},
		{"解释性:介绍", "接下来介绍这个模块的设计", 0},
		{"普通陈述", "这个文件的配置项有三个", 0},
		{"空文本", "", 0},
	}
	for _, c := range cases {
		if got := len(Detect(c.text)); got != c.want {
			t.Errorf("[%s] Detect(%q) = %d 个承诺, want %d", c.name, c.text, got, c.want)
		}
	}
	// 数量提取:写 10 个 → Expected=10,类型 WriteFile
	if cs := Detect("接下来写 10 个文件"); len(cs) != 1 || cs[0].Expected != 10 || cs[0].Type != ActionWriteFile {
		t.Fatalf("应提取数量 10 / 类型 WriteFile, got %+v", cs)
	}
	// 动作类型:运行 → RunCommand
	if cs := Detect("我将运行测试"); len(cs) != 1 || cs[0].Type != ActionRunCommand {
		t.Fatalf("'运行'应映射 RunCommand, got %+v", cs)
	}
}

// TestVerify 工具验证:Write 成功推进承诺、达额后 Completed、动作不匹配不推进。
func TestVerify(t *testing.T) {
	s := NewStore()
	c := &Commitment{Type: ActionWriteFile, Expected: 3}
	s.Add(c)

	if !s.Verify("Write", "a.go") {
		t.Fatalf("Write 应推进 WriteFile 承诺")
	}
	if c.Completed != 1 || c.Status != Pending {
		t.Fatalf("应推进到 1/3, got %+v", c)
	}
	s.Verify("Write", "b.go")
	s.Verify("Write", "c.go")
	if c.Status != Completed || s.Pending() {
		t.Fatalf("3/3 后应 Completed 且无 pending, got %+v", c)
	}
	// 动作不匹配:Bash 不推进 WriteFile 承诺
	c2 := &Commitment{Type: ActionWriteFile, Expected: 1}
	s.Add(c2)
	s.Verify("Bash", "")
	if c2.Status != Pending {
		t.Fatalf("Bash 不应推进 WriteFile 承诺, got %+v", c2)
	}
}

// TestMatches 目标匹配:精确 / 目录前缀 / 空目标任意。
func TestMatches(t *testing.T) {
	c := &Commitment{Type: ActionWriteFile, Targets: []string{"config/"}}
	if !Matches(c, "config/a.yaml") {
		t.Fatalf("目录前缀应匹配 config/a.yaml")
	}
	if Matches(c, "src/a.yaml") {
		t.Fatalf("非目标目录不应匹配")
	}
	c2 := &Commitment{Type: ActionWriteFile}
	if !Matches(c2, "anything") {
		t.Fatalf("空目标应任意匹配")
	}
}

// TestStatusFlow 状态完整流转:Pending→Executing→Completed;失败→Failed;系统放行→Abandoned。
func TestStatusFlow(t *testing.T) {
	s := NewStore()
	c := &Commitment{Type: ActionWriteFile, Expected: 2}
	s.Add(c)
	if c.Status != Pending {
		t.Fatalf("初始应 Pending, got %v", c.Status)
	}
	s.Verify("Write", "a.go")
	if c.Status != Executing {
		t.Fatalf("首次推进应 Executing, got %v", c.Status)
	}
	s.Verify("Write", "b.go")
	if c.Status != Completed {
		t.Fatalf("达额应 Completed, got %v", c.Status)
	}

	// 失败 → Failed
	c2 := &Commitment{Type: ActionRunCommand, Expected: 1}
	s.Add(c2)
	s.Fail("Bash")
	if c2.Status != Failed {
		t.Fatalf("失败应 Failed, got %v", c2.Status)
	}
	if s.Pending() {
		t.Fatalf("Failed 承诺不应计入 pending")
	}

	// 系统放行 → Abandoned(区别于用户取消 Cancelled)
	c3 := &Commitment{Type: ActionSearch, Expected: 1}
	s.Add(c3)
	if got := len(s.PendingList()); got != 1 {
		t.Fatalf("PendingList 应列出 1 项, got %d", got)
	}
	s.AbandonPending()
	if c3.Status != Abandoned {
		t.Fatalf("系统放行应 Abandoned, got %v", c3.Status)
	}
	// 用户取消路径(Cancelled)仍保留语义。
	c4 := &Commitment{Type: ActionReadFile, Expected: 1}
	s.Add(c4)
	s.CancelPending()
	if c4.Status != Cancelled {
		t.Fatalf("用户取消应 Cancelled, got %v", c4.Status)
	}
}

// TestDetect_MoreActions 补全动作映射:搜索/读取/验证。
func TestDetect_MoreActions(t *testing.T) {
	if cs := Detect("接下来搜索配置文件"); len(cs) != 1 || cs[0].Type != ActionSearch {
		t.Fatalf("'搜索'应映射 ActionSearch, got %+v", cs)
	}
	if cs := Detect("I will read the config file"); len(cs) != 1 || cs[0].Type != ActionReadFile {
		t.Fatalf("'read'应映射 ActionReadFile, got %+v", cs)
	}
	if cs := Detect("接下来验证文件数量"); len(cs) != 1 || cs[0].Type != ActionVerify {
		t.Fatalf("'验证'应映射 ActionVerify, got %+v", cs)
	}
	if cs := Detect("I will verify the checksums"); len(cs) != 1 || cs[0].Type != ActionVerify {
		t.Fatalf("'verify'应映射 ActionVerify, got %+v", cs)
	}
}

// TestVerifyBashAdvancesVerify 验证:Bash 成功推进 ActionVerify 型承诺。
func TestVerifyBashAdvancesVerify(t *testing.T) {
	s := NewStore()
	s.Add(&Commitment{Type: ActionVerify, Expected: 1})
	if !s.Verify("Bash", "") {
		t.Fatalf("Bash 应推进 ActionVerify 承诺")
	}
	if s.Pending() {
		t.Fatalf("Verify 承诺应已完成")
	}
}

// TestMultiCommitment 多阶段承诺:Write+Run 均未完成时,任一 pending gate 都继续;
// 全部完成才允许结束(防多阶段任务被"部分完成"误放行)。
func TestMultiCommitment(t *testing.T) {
	s := NewStore()
	s.Add(&Commitment{Type: ActionWriteFile, Expected: 1})
	s.Add(&Commitment{Type: ActionRunCommand, Expected: 1})
	// Write 完成,Run 未完成 → 仍 pending。
	s.Verify("Write", "a.go")
	if !s.Pending() {
		t.Fatalf("Run 承诺未完成,应仍 pending")
	}
	// Run 完成 → 全部完成。
	s.Verify("Bash", "")
	if s.Pending() {
		t.Fatalf("全部完成应无 pending")
	}
}

// TestPartialExecution 部分执行:Expected=10 只写 5 个,承诺仍 pending,
// 完成度门禁必须继续(不允许模型"说完成"就结束)。
func TestPartialExecution(t *testing.T) {
	s := NewStore()
	c := &Commitment{Type: ActionWriteFile, Expected: 10}
	s.Add(c)
	for i := 0; i < 5; i++ {
		s.Verify("Write", fmt.Sprintf("f%d.go", i))
	}
	if c.Completed != 5 || !c.Pending() {
		t.Fatalf("5/10 应仍 pending, got %+v", c)
	}
	// 补满后完成。
	for i := 5; i < 10; i++ {
		s.Verify("Write", fmt.Sprintf("f%d.go", i))
	}
	if c.Status != Completed || s.Pending() {
		t.Fatalf("10/10 应 Completed, got %+v", c)
	}
}