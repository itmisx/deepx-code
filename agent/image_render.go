package agent

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"deepx/tools"
)

// imagePlaceholderRe 匹配消息文本里的 [Image #N] 占位符(N 对应 ChatMessage.ImagePaths 的第 N 张)。
var imagePlaceholderRe = regexp.MustCompile(`\[Image #(\d+)\]`)

// 带图消息尾部追加的提醒语 —— 显式告诉模型"该不该看图/该不该调 OCR",直接而可靠。
// 放在用户那条消息里(非 system/工具表),不影响前缀缓存;且对每条带图消息都确定性追加
// (不只加最后一条),历史渲染才稳定、缓存不 miss。
const (
	visionReminder    = "(注:你是视觉模型,无需调用 OCR 工具,请直接识别本条消息中的图片。)"
	nonVisionReminder = "(注:你不是视觉模型,看不到图片本身,请调用 OCR 工具按本条消息中给出的图片路径逐一识别;不要凭空猜测图片内容。)"
)

// renderConvoImages 把携带图片(ImagePaths 非空)的消息,按"当轮要跑的模型支不支持视觉"即时渲染成
// 发送形态;其余消息原样返回。返回的是副本,**不改动 convo 的规范形态**(规范形态只存路径不存
// base64 —— 历史小、缓存友好;base64 只在发请求这一刻临时生成)。
//
//   - vision=true  → 文本(去掉 [Image #N])+ 各图 base64 image_url,模型直接看图;
//   - vision=false → [Image #N] 替换成图片绝对路径放进文本,模型用 img_ocr 按路径识别。
//
// 入口路由和中途 SwitchModel 都走这里 —— 同一条带图消息,发给视觉模型是 base64、发给非视觉模型
// 是路径+OCR,所以模型中途从视觉切到非视觉也不会因带着 base64 被 4xx 拒掉。
func renderConvoImages(convo []ChatMessage, vision bool) []ChatMessage {
	out := make([]ChatMessage, len(convo))
	for i, m := range convo {
		switch {
		case len(m.ImagePaths) > 0 && vision:
			out[i] = renderImageVision(m)
		case len(m.ImagePaths) > 0:
			out[i] = renderImageOCR(m)
		case !vision && hasImageParts(m):
			// 铁律兜底:消息里带着(来历不明的)base64 图片 part,但当轮模型不支持视觉 →
			// 一律剥成纯文本。非视觉模型永远收不到图片,从根上杜绝 "no image input" 404。
			out[i] = stripImageParts(m)
		default:
			out[i] = m
		}
	}
	return out
}

// hasImageParts 判断消息的 ContentParts 里有没有图片(image_url)part。
func hasImageParts(m ChatMessage) bool {
	for _, p := range m.ContentParts {
		if p.Type == "image_url" {
			return true
		}
	}
	return false
}

// stripImageParts 把消息里的图片 part 剥掉,只把文本拼回 Content(给非视觉模型用)。
func stripImageParts(m ChatMessage) ChatMessage {
	var b strings.Builder
	for _, p := range m.ContentParts {
		if p.Type == "text" && p.Text != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(p.Text)
		}
	}
	return ChatMessage{
		Role:             m.Role,
		Content:          b.String(),
		ReasoningContent: m.ReasoningContent,
		ToolCalls:        m.ToolCalls,
		ToolCallID:       m.ToolCallID,
		Name:             m.Name,
	}
}

// renderImageVision:**图文交错**——把文本在每个 [Image #N] 处切开,图片就插在它被引用的位置。
// 这样 "请比较[Image #1]和[Image #2]的区别" 里图1/图2 各就各位,模型不会错配;句子也不被切碎。
// "无需 OCR、直接看图" 的提醒**永远追加在最后**(模型最后读到 → 最显著),不参与交错。
// 越界的占位符静默跳过;没被占位符引用到的挂图兜底追加到末尾(不丢图);
// 一张图都读不出来则退回原文本。
func renderImageVision(m ChatMessage) ChatMessage {
	parts := make([]ContentPart, 0, len(m.ImagePaths)+2)
	used := make([]bool, len(m.ImagePaths))

	// 按 [Image #N] 出现位置把 Content 切成 文本段 / 图片 交替
	locs := imagePlaceholderRe.FindAllStringSubmatchIndex(m.Content, -1)
	prev := 0
	for _, loc := range locs {
		if seg := strings.TrimSpace(m.Content[prev:loc[0]]); seg != "" {
			parts = append(parts, ContentPart{Type: "text", Text: seg})
		}
		prev = loc[1]
		idx, _ := strconv.Atoi(m.Content[loc[2]:loc[3]])
		if idx >= 1 && idx <= len(m.ImagePaths) {
			if part := imagePartFromPath(m.ImagePaths[idx-1]); part != nil {
				parts = append(parts, *part)
				used[idx-1] = true
			}
		}
	}
	// 末尾残余文本
	if seg := strings.TrimSpace(m.Content[prev:]); seg != "" {
		parts = append(parts, ContentPart{Type: "text", Text: seg})
	}
	// 没被占位符引用到的图(比如用户删了占位符)兜底追加,别丢图
	for i, p := range m.ImagePaths {
		if used[i] {
			continue
		}
		if part := imagePartFromPath(p); part != nil {
			parts = append(parts, *part)
		}
	}

	// 一张图都没成功读出 → 退回原文本(纯文)
	hasImg := false
	for _, p := range parts {
		if p.Type == "image_url" {
			hasImg = true
			break
		}
	}
	if !hasImg {
		return ChatMessage{Role: m.Role, Content: m.Content}
	}

	// 提醒压在最后
	parts = append(parts, ContentPart{Type: "text", Text: visionReminder})
	return ChatMessage{Role: m.Role, ContentParts: parts}
}

// imagePartFromPath 读图编 base64,返回一个 image_url part;读不到(被清理/路径失效)返回 nil。
func imagePartFromPath(path string) *ContentPart {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	url := "data:" + imageMimeByExt(path) + ";base64," + base64.StdEncoding.EncodeToString(data)
	return &ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: url}}
}

// renderImageOCR:非视觉模型看不到图。把 [Image #N] 替换成图片绝对路径,并在尾部追加提醒,
// 显式要求"调 OCR 按路径识别、别凭空猜"——之前只塞路径靠模型自觉,结果它不调还拿旧上下文幻觉
// (切到 pro 后没 OCR 却编出别的图标)。提醒语对每条带图消息都加(位置无关),保历史渲染稳定、缓存不 miss。
func renderImageOCR(m ChatMessage) ChatMessage {
	replaced := imagePlaceholderRe.ReplaceAllStringFunc(m.Content, func(match string) string {
		sub := imagePlaceholderRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		idx, _ := strconv.Atoi(sub[1])
		if idx < 1 || idx > len(m.ImagePaths) {
			return match
		}
		return m.ImagePaths[idx-1]
	})
	if strings.TrimSpace(replaced) != "" {
		replaced += "\n\n" + nonVisionReminder
	} else {
		replaced = nonVisionReminder
	}
	return ChatMessage{Role: m.Role, Content: replaced}
}

// ocrTargetsInlinedImage 判断(对视觉模型而言)这次 OCR 调用是不是在"绕路 OCR 它本可直接看的图"。
// 命中两类即拦截:
//
//	(a) 路径精确等于本轮已内联给它的某张图(ImagePaths);
//	(b) 路径落在 deepx 自己的粘贴缓存目录 ~/.deepx/ocr/cache 内 —— 那里的文件对视觉模型一律是
//	    "已内联看过"的图;视觉轮里模型甚至会**瞎猜一个(可能不存在的)缓存路径**来 OCR((a) 抓不到),
//	    用目录前缀把这种猜测也一并拦下。
//
// 解析失败 / 路径既不在 ImagePaths 也不在缓存目录 → false 放行(视觉模型 OCR 一个外部文件是正当需求)。
func ocrTargetsInlinedImage(argsJSON string, convo []ChatMessage) bool {
	var args struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(argsJSON), &args) != nil {
		return false
	}
	target := filepath.Clean(strings.TrimSpace(args.Path))
	if target == "" {
		return false
	}
	// (a) 精确命中本轮已内联的某张图
	for _, m := range convo {
		for _, p := range m.ImagePaths {
			if filepath.Clean(p) == target {
				return true
			}
		}
	}
	// (b) 落在粘贴缓存目录内(含模型瞎猜的、甚至不存在的缓存路径)
	if dir := tools.PasteCacheDir(); dir != "" {
		if target == dir || strings.HasPrefix(target, dir+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// priorOCRResult 在历史里查这个图片路径是否「已经 OCR 过」:命中则返回上次结果文本 + true。
//
// 用途:用户在提示词里给了图片路径(纯文本,赖在历史里删不掉),模型每轮看到路径就反复调 OCR,
// 用户说"别识别了"也拦不住(issue #146)。据此短路:第二次起不再真跑 OCR,直接回上次结果。
//
// 关联方式:OCR 的目标路径在 assistant 消息的 tool_calls[].arguments 里,而结果在对应的 tool 消息正文里,
// 两者靠 tool_call_id 配对。先建 id→path 映射(只收 OCR 调用),再找该路径对应的 OCR 结果消息。
func priorOCRResult(argsJSON string, convo []ChatMessage) (string, bool) {
	var args struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(argsJSON), &args) != nil {
		return "", false
	}
	target := filepath.Clean(strings.TrimSpace(args.Path))
	if target == "" {
		return "", false
	}
	ocrCallPath := map[string]string{} // tool_call_id → OCR 目标路径
	for _, m := range convo {
		for _, tc := range m.ToolCalls {
			if tc.Function.Name != "OCR" {
				continue
			}
			var a struct {
				Path string `json:"path"`
			}
			if json.Unmarshal([]byte(tc.Function.Arguments), &a) != nil {
				continue
			}
			if p := filepath.Clean(strings.TrimSpace(a.Path)); p != "" {
				ocrCallPath[tc.ID] = p
			}
		}
	}
	for _, m := range convo {
		if m.Role == "tool" && ocrCallPath[m.ToolCallID] == target {
			return m.Content, true
		}
	}
	return "", false
}

// imageFileExtRe 匹配图片文件扩展名(结尾)。
var imageFileExtRe = regexp.MustCompile(`(?i)\.(?:png|jpe?g|gif|webp|bmp)$`)

// ocrImageFilePath 解析 OCR 调用的 path 参数,若它指向一个"真实存在的本地图片文件",
// 返回其绝对路径 + true;否则返回 "", false。
//
// 用途(issue #194):视觉模型对一张外部图片路径调 OCR 时,本地 OCR 精度不如模型多模态。
// 命中即改为把该图内联进对话让模型直接看(见 llm.go case "OCR"),不跑本地 OCR。
// 只认扩展名为常见图片格式、且 os.Stat 确认存在的普通文件 —— 不存在 / 目录一律不命中。
func ocrImageFilePath(argsJSON string) (string, bool) {
	var args struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(argsJSON), &args) != nil {
		return "", false
	}
	p := strings.TrimSpace(args.Path)
	if p == "" || !imageFileExtRe.MatchString(p) {
		return "", false
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return "", false
	}
	return abs, true
}

// isImageInputUnsupported 判断错误是否是"该端点/模型不接受图片输入"(发了 base64 才会撞)。
// 命中则上层把该模型降级为无视觉、改走 OCR 重发。匹配宽松些以兼容各家措辞。
//   - MiMo: HTTP 404 "No endpoints found that support image input"
//   - 其它常见: "does not support image", "image input is not supported" 等
func isImageInputUnsupported(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "image input") {
		return true
	}
	return strings.Contains(s, "image") &&
		(strings.Contains(s, "not support") || strings.Contains(s, "no endpoints") || strings.Contains(s, "unsupported"))
}

// imageMimeByExt 按扩展名给出 data URL 的 MIME;粘贴落盘是 PNG,未知一律按 png 兜底。
func imageMimeByExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return "image/png"
	}
}
