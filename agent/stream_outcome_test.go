package agent

import (
	"deepx/agent/commitment"
	"strings"
	"testing"
)

// TestClassifyStreamResult 覆盖 issue #169 的两类异常响应分类:
// tool_call 被长度截断 / 空响应,以及正常情形不被误判。
func TestClassifyStreamResult(t *testing.T) {
	oneTool := []ToolCall{{ID: "x", Function: ToolCallFunc{Name: "Write", Arguments: `{"path":"a"`}}}

	cases := []struct {
		name      string
		content   string
		reasoning string
		toolCalls []ToolCall
		truncated bool
		want      streamOutcome
	}{
		// 会话 A:超大 Write 撞输出上限,tool_call arguments 残缺。
		{"truncated tool call", "", "", oneTool, true, outcomeTruncatedTool},
		// 有 content 的截断也归为截断工具(只要还带着工具调用)。
		{"truncated with content and tool", "思考中", "", oneTool, true, outcomeTruncatedTool},
		// 会话 B:供应商返回完全空。
		{"empty response", "", "", nil, false, outcomeEmpty},
		// finish_reason=length 但什么都没生成 → 也当空响应,催重试。
		{"truncated but nothing generated", "", "", nil, true, outcomeEmpty},
		// 正常:有文本、无工具。
		{"normal text", "结果如下", "", nil, false, outcomeNormal},
		// 正常:只有 reasoning(thinking 模型),非空。
		{"reasoning only", "", "让我想想", nil, false, outcomeNormal},
		// 正常:完整工具调用,未截断。
		{"complete tool call", "", "", oneTool, false, outcomeNormal},
		// 纯文本被截断(无工具)→ 走正常路径,交给 completionGate 催继续,不算截断工具。
		{"truncated plain text", "写到一半就断了", "", nil, true, outcomeNormal},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyStreamResult(c.content, c.reasoning, c.toolCalls, c.truncated)
			if got != c.want {
				t.Fatalf("classifyStreamResult(%q,%q,tools=%d,trunc=%v) = %v, want %v",
					c.content, c.reasoning, len(c.toolCalls), c.truncated, got, c.want)
			}
		})
	}
}

// TestCountBlockingTasks 验证:只统计 TodoAction 型、Progress==0 的项;
// Verification/Review 型不阻塞 gate。
func TestCountBlockingTasks(t *testing.T) {
	todo := []PlanItem{
		{Title: "写 config.py", Status: PlanStatusPending, Progress: 0, Type: TodoAction},        // 未开始 Action → 计
		{Title: "创建 models.py", Status: PlanStatusPending, Progress: 2, Type: TodoAction},       // 已执行 → 不计
		{Title: "运行测试", Status: PlanStatusPending, Progress: 0, Type: TodoAction},              // 未开始 → 计
		{Title: "验证文件数", Status: PlanStatusPending, Progress: 0, Type: TodoVerification},      // 非 Action → 不计
		{Title: "检查格式", Status: PlanStatusPending, Progress: 0, Type: TodoReview},              // 非 Action → 不计
		{Title: "写 db.py", Status: PlanStatusDone, Progress: 0, Type: TodoAction},                // done → 不计
	}
	if got := countBlockingTasks(todo); got != 2 {
		t.Fatalf("应统计 2 项阻塞待办(仅 Action 未开始), got %d", got)
	}
	// Write 成功推进所有未开始的 Action 型项(不依赖标题含写词:
	// 实测批次标题如"批次1: settings/..."无写词,按标题会漏推进 → gate 误催)。
	if n := advanceTodos(todo, "Write"); n != 3 {
		t.Fatalf("Write 应推进全部 Action 项, got %d", n)
	}
	if todo[0].Progress != 1 || todo[1].Progress != 3 || todo[2].Progress != 1 || todo[3].Progress != 0 || todo[4].Progress != 0 {
		t.Fatalf("应推进 Action 项、Verification/Review 不动, got %+v", todo[0:5])
	}
	if got := countBlockingTasks(todo); got != 0 {
		t.Fatalf("推进后应无阻塞待办, got %d", got)
	}
	// Bash 不推进写类项。
	if n := advanceTodos(todo, "Bash"); n != 0 {
		t.Fatalf("Bash 不应推进写类 todo, got %d", n)
	}
}

// TestClassifyTodoType 验证启发式分类:验证/检查/格式 → Verification/Review;其余 → Action。
func TestClassifyTodoType(t *testing.T) {
	cases := []struct {
		title string
		want  TodoType
	}{
		{"写 batch 1-5", TodoAction},
		{"生成配置文件", TodoAction},
		{"验证文件数=30", TodoVerification},
		{"校验所有文件 >600 字节", TodoVerification},
		{"verify checksums", TodoVerification},
		{"检查代码格式", TodoReview},
		{"review 输出", TodoReview},
	}
	for _, c := range cases {
		if got := classifyTodoType(c.title); got != c.want {
			t.Errorf("[%s] classifyTodoType = %v, want %v", c.title, got, c.want)
		}
	}
}

// TestCompletionGateCap 验证连续催继续不会无限循环:达到 maxGateNudges 后放行(返回空)。
func TestCompletionGateCap(t *testing.T) {
	store := commitment.NewStore()
	store.Add(&commitment.Commitment{Type: commitment.ActionWriteFile, Expected: 1})
	var nudges int
	got := 0
	for range maxGateNudges + 3 {
		if completionGate(false, nil, store, &nudges) != "" {
			got++
		}
	}
	if got != maxGateNudges {
		t.Fatalf("completionGate 应最多催 %d 次,实际 %d 次", maxGateNudges, got)
	}
}

// TestCompletionGate_Commitment 覆盖承诺状态机核心场景:
//   - 声明写文件但无工具调用 → 催继续;
//   - 正常说明(无执行意图)→ 结束;
//   - 工具执行推进承诺 → 达额后 gate 放行;
//   - 恶意循环(反复声明不执行)→ maxGateNudges 次提醒后退出。
func TestCompletionGate_Commitment(t *testing.T) {
	// Test1:声明未执行 → 催继续。
	store := commitment.NewStore()
	for _, c := range commitment.Detect("Now batch 3: Write all 10 files.") {
		store.Add(c)
	}
	var nudges int
	got := completionGate(false, nil, store, &nudges)
	if got == "" {
		t.Fatalf("声明未执行应催继续")
	}
	if !strings.Contains(got, "工具调用") {
		t.Fatalf("提示应点明未调用工具, got=%q", got)
	}

	// Test4:工具执行推进承诺 → 达额后 gate 放行。
	store2 := commitment.NewStore()
	store2.Add(&commitment.Commitment{Type: commitment.ActionWriteFile, Expected: 2})
	store2.Verify("Write", "a.go")
	if !store2.Pending() {
		t.Fatalf("1/2 应仍 pending")
	}
	store2.Verify("Write", "b.go")
	if store2.Pending() {
		t.Fatalf("2/2 应完成,不再 pending")
	}
	var n2 int
	if got := completionGate(false, nil, store2, &n2); got != "" {
		t.Fatalf("承诺全部完成后应结束, got=%q", got)
	}

	// Test2:正常说明,无执行意图 → 结束。
	if got := completionGate(false, nil, commitment.NewStore(), &nudges); got != "" {
		t.Fatalf("正常说明应结束, got=%q", got)
	}

	// Test5:恶意循环 → 最多催 maxGateNudges 次后退出。
	loop := commitment.NewStore()
	loop.Add(&commitment.Commitment{Type: commitment.ActionWriteFile, Expected: 1})
	var ln int
	nudged := 0
	for range maxGateNudges + 2 {
		if completionGate(false, nil, loop, &ln) != "" {
			nudged++
		}
	}
	if nudged != maxGateNudges {
		t.Fatalf("恶意循环应催 %d 次后退出,实际 %d", maxGateNudges, nudged)
	}
}
