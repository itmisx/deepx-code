package agent

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// probeMarkerPNG 是内置的"暗号"探测图:一张印着 "MELON48" 的小 PNG。
//
//go:embed probe_marker.png
var probeMarkerPNG []byte

// normalizeAlnum 只保留字母数字并转大写,用于宽松匹配暗号(容忍模型加连字符/空格/标点)。
func normalizeAlnum(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// probeMarkerToken 是探测图里印的暗号(取大写词根,容忍数字被认错)。
// 之所以用一个"具体且不易被凭空猜到"的词:非视觉模型要么报错、要么静默忽略图片只回文本,
// 都不可能恰好吐出 "MELON" —— 只有真看到图的模型才会认出它。
const probeMarkerToken = "MELON"

// ProbeVision 用内置暗号图做一次**最小**的 chat/completions 调用,判断该模型是否支持视觉输入。
//
// 这是个裸调用:**不带 system prompt、不带 tools、不带 mcp / skill**,只发一张小图 + 一句问题,
// max_tokens 压到很小 —— 缓存未命中时偶尔探一次,token 成本压到最低(避免走 StartStream 白搭整套前缀)。
//
// 返回 (vision, err) 的语义,决定调用方要不要缓存:
//   - err != nil:网络 / 5xx 等**瞬时**错误 → 调用方不要缓存,下次启动再探;
//   - err == nil:**确定性**结果 —— true=认出暗号(支持视觉);
//     false=要么端点 4xx 拒绝图片输入,要么回了但没认出暗号(静默忽略)→ 视为不支持。
func ProbeVision(ctx context.Context, entry ModelEntry) (bool, error) {
	if entry.Model == "" || entry.BaseURL == "" {
		return false, fmt.Errorf("probe: 模型或 base_url 为空")
	}
	// 本地部署的大模型(llama.cpp 等)首次跑图推理可能较慢,给足 60s;超时按瞬时错误处理
	// (不缓存,下次启动重探),避免把"能识图但慢"的模型误判成不支持视觉(issue #194)。
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(probeMarkerPNG)
	reqBody, err := json.Marshal(chatRequest{
		Model: entry.Model,
		// 给够 token:推理型模型(如 MiMo)会先在 reasoning_content 里思考,token 太小会在
		// 吐出暗号前就被 finish_reason=length 截断,导致漏判。256 足够它把暗号说出来,又不贵。
		MaxTokens: 256,
		Stream:    false,
		Messages: []ChatMessage{{
			Role: "user",
			ContentParts: []ContentPart{
				{Type: "image_url", ImageURL: &ImageURL{URL: dataURL}},
				{Type: "text", Text: "What text is shown in this image? Reply with only that text."},
			},
		}},
		// 关键:不带 Tools / Thinking / ReasoningEffort —— 裸调用,最省 token。
	})
	if err != nil {
		return false, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", entry.BaseURL+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+entry.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err // 瞬时错误,不缓存
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	switch {
	case resp.StatusCode == 200:
		var r struct {
			Choices []struct {
				Message struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return false, err
		}
		reply := ""
		if len(r.Choices) > 0 {
			// content 和 reasoning_content 都看:推理模型可能只在思考里提到暗号(content 被 length 截断)。
			reply = r.Choices[0].Message.Content + " " + r.Choices[0].Message.ReasoningContent
		}
		// 归一化后再比:去掉大小写和所有非字母数字,这样模型把暗号拼成 "M-E-L-O-N" / "M E L O N"
		// 也能命中。认出暗号 = 真看到了图,确定性结果,可缓存。
		return strings.Contains(normalizeAlnum(reply), probeMarkerToken), nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		// 4xx 多半是"该模型不支持图片输入" → 确定性 false,可缓存。
		return false, nil
	default:
		// 5xx 等:瞬时错误,不缓存。
		return false, fmt.Errorf("probe HTTP %d: %s", resp.StatusCode, string(body))
	}
}
