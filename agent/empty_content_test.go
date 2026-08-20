package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// 出站消息里 user / system 必须始终带 content 字段,哪怕内容是空串。
//
// 省掉它服务端直接 400:`Param Incorrect: "content" is not set`,整轮对话失败。
// 而空 user 消息是真会出现的 —— 只贴图不打字的消息,在图片读不出来 / 当轮模型不支持视觉
// 被剥成纯文本之后内容就空了(见 image_render.go)。那几个产出点各自兜了底,
// 这里是最后一道防线:少一句话 vs 整轮失败,后果差着量级。
func TestMarshal_UserSystemAlwaysHaveContent(t *testing.T) {
	for _, m := range []ChatMessage{
		{Role: "user"},
		{Role: "system"},
		{Role: "user", Content: ""},
		{Role: "user", ImagePaths: []string{"/nonexistent.png"}}, // 贴图不打字
	} {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `"content"`) {
			t.Errorf("%s 消息缺 content 字段 → 服务端会报 `content` is not set:%s", m.Role, b)
		}
	}
}

// assistant 带 tool_calls 时省略 content 是 OpenAI 的标准形状,别一起改坏了。
func TestMarshal_AssistantWithToolCallsKeepsOmitting(t *testing.T) {
	b, _ := json.Marshal(ChatMessage{
		Role:      "assistant",
		ToolCalls: []ToolCall{{ID: "x", Type: "function"}},
	})
	if strings.Contains(string(b), `"content"`) {
		t.Errorf("assistant + tool_calls 不该带 content:%s", b)
	}
}

// 贴图不打字:两条渲染路径都不能产出空内容的消息。
func TestRenderImages_NeverEmptyContent(t *testing.T) {
	imageOnly := ChatMessage{
		Role:       "user",
		Content:    "", // 用户只贴了图,一个字没打
		ImagePaths: []string{"/definitely/not/there.png"},
	}
	for _, vision := range []bool{true, false} {
		out := renderConvoImages([]ChatMessage{imageOnly}, vision)[0]
		if strings.TrimSpace(out.Content) == "" && len(out.ContentParts) == 0 {
			t.Errorf("vision=%v:渲染出了空内容的 user 消息 → 会被 API 400 拒绝", vision)
		}
	}

	// 只含图片 part、没有文本 part 的消息,给非视觉模型剥图后同样不能空。
	partsOnly := ChatMessage{
		Role: "user",
		ContentParts: []ContentPart{
			{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,AAAA"}},
		},
	}
	if out := stripImageParts(partsOnly); strings.TrimSpace(out.Content) == "" {
		t.Error("剥掉图片 part 后内容为空 → 会被 API 400 拒绝")
	}
}

// 工具返回空字符串时,写进历史的必须是占位文本而不是空串。
//
// 空的 tool 消息序列化出去没有 content 字段 → 服务端 400。现有工具都各自兜了底,
// 但那是二十多个返回点各自记得的约定;这里是唯一汇聚点,守住它新工具就不用再操心。
func TestClampTurnToolOutput_NeverEmpty(t *testing.T) {
	budget := 0
	got := clampTurnToolOutput("Glob", "", &budget)
	if strings.TrimSpace(got) == "" {
		t.Fatal("空工具结果没有被换成占位文本 → 会生成没有 content 字段的 tool 消息")
	}

	b, _ := json.Marshal(ChatMessage{Role: "tool", ToolCallID: "a", Name: "Glob", Content: got})
	if !strings.Contains(string(b), `"content"`) {
		t.Errorf("tool 消息仍然缺 content:%s", b)
	}
}
