package router

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"deepx/ocr"
)

// === 语义路由的资产:模型 + 分词器 ===
//
// 全部异步下载,**绝不阻塞启动**。没下完就不做入口路由(起手一律 flash),下完了下次启动
// 自动启用。这是有意为之:路由是每轮对话的必经之路,不能因为一个可选增强而让用户等在启动界面。

const (
	modelFile     = "e5-small-quantized.onnx"
	tokenizerFile = "e5-small-tokenizer.json"

	// 期望大小。下完校验一遍 —— 断点续传、CDN 截断、磁盘满都会留下一个"看起来存在"的残文件,
	// 而残文件喂给 onnxruntime 只会得到一句意义不明的加载错误。
	//
	// 这两个数也是**多源的一致性凭证**:ModelScope 与 HuggingFace 上的同名文件字节数完全相同,
	// 所以无论从哪个源下下来,校验口径都是同一个。
	modelBytes     = 118308185
	tokenizerBytes = 17082730
)

// 下载源按顺序尝试,前一个失败就换下一个。
//
// **ModelScope 排第一是因为国内可直连**:它是阿里的模型社区、机房在境内,不需要任何代理。
// hf-mirror.com 虽然也是为国内做的镜像,但它是第三方个人维护、没有 SLA;huggingface.co
// 则在国内基本要走代理。三个源上的文件字节数完全一致(见 modelBytes / tokenizerBytes)。
//
// 多源不是锦上添花:关键词表删除后,语义模型是入口路由的唯一判据 ——
// 下不下来直接决定这个功能有没有(见 router.go 包注释),单点源撑不起这个责任。
//
// 模型用 Xenova 的**量化版**(118 MB)而不是全精度(470 MB):这东西每个用户都要下一遍,
// 而路由只需要"够用的语义区分度",不是检索精度。Xenova 那份还是自包含单文件,
// 没有 external-data(.onnx_data)外挂权重 —— 官方 intfloat 那份和 onnx-community 的 bge
// 都是外挂式,只下主文件会加载失败。
var (
	modelURLs = []string{
		"https://modelscope.cn/models/Xenova/multilingual-e5-small/resolve/master/onnx/model_quantized.onnx",
		"https://hf-mirror.com/Xenova/multilingual-e5-small/resolve/main/onnx/model_quantized.onnx",
		"https://huggingface.co/Xenova/multilingual-e5-small/resolve/main/onnx/model_quantized.onnx",
	}
	// 配套的 HF tokenizer.json(Unigram,25 万词表)。
	tokenizerURLs = []string{
		"https://modelscope.cn/models/Xenova/multilingual-e5-small/resolve/master/tokenizer.json",
		"https://hf-mirror.com/Xenova/multilingual-e5-small/resolve/main/tokenizer.json",
		"https://huggingface.co/Xenova/multilingual-e5-small/resolve/main/tokenizer.json",
	}
)

// Status 是资产就绪状态,给 UI 显示用。
type Status int

const (
	StatusDisabled    Status = iota // 未启用(用户没开)
	StatusDownloading               // 下载中
	StatusReady                     // 就绪,语义路由生效
	StatusFailed                    // 下载/加载失败,入口路由不启用
)

func (s Status) String() string {
	switch s {
	case StatusDownloading:
		return "下载中"
	case StatusReady:
		return "就绪"
	case StatusFailed:
		return "下载失败"
	default:
		return "未启用"
	}
}

var (
	status  atomic.Int32
	lastErr atomic.Pointer[string]
)

// CurrentStatus 返回当前资产状态与失败原因(成功时为空)。
// 失败必须能被看见:关键词表已删除,语义没起来就等于完全不做入口路由,用户有权知道。
func CurrentStatus() (Status, string) {
	s := Status(status.Load())
	msg := ""
	if p := lastErr.Load(); p != nil {
		msg = *p
	}
	return s, msg
}

func setStatus(s Status, err error) {
	status.Store(int32(s))
	if err == nil {
		lastErr.Store(nil)
		return
	}
	m := err.Error()
	lastErr.Store(&m)
}

// AssetDir 返回语义路由资产目录 ~/.deepx/router。
func AssetDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".deepx", "router"), nil
}

// assetsReady 判断模型和分词器都已完整落盘(按字节数校验)。
func assetsReady(dir string) bool {
	return fileSized(filepath.Join(dir, modelFile), modelBytes) &&
		fileSized(filepath.Join(dir, tokenizerFile), tokenizerBytes)
}

func fileSized(path string, want int64) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Size() == want
}

var ensureOnce sync.Once

// EnsureAssetsAsync 在后台补齐资产,立即返回。已就绪则直接加载嵌入器。
// 每个进程只跑一次(sync.Once):路由每轮都会问"好了没",不能每次都触发一轮下载。
//
// onDone 在最终状态确定后回调(就绪 / 失败),供 UI 提示;可以为 nil。
func EnsureAssetsAsync(onDone func(Status, string)) {
	ensureOnce.Do(func() {
		go func() {
			s, msg := ensureAssets()
			if onDone != nil {
				onDone(s, msg)
			}
		}()
	})
}

// fetchAsset 把一个资产下到 dir/name,按 urls 的顺序逐个源尝试,直到拿到大小正确的文件。
//
// 大小不符与连不上按同一类处理、都换下一个源:镜像同步不全、CDN 返回一页 HTML 错误页、
// 传输被截断,落到磁盘上都是"文件在但不对",区别只在原因,应对方式一样。
//
// 全部源都失败时,错误里带上每个源各自的原因 —— 只报最后一个会让人误判
// (比如实际是磁盘满,却看到一句"连接超时")。
func fetchAsset(dir string, urls []string, name string, size int64) error {
	dest := filepath.Join(dir, name)
	if fileSized(dest, size) {
		return nil
	}
	tmp := dest + ".part" // 下到临时名再改名:半截文件不会被当成"已就绪"

	var fails []string
	for _, u := range urls {
		_ = os.Remove(tmp) // 清掉上一个源留下的残文件,免得续传逻辑往后追加
		if err := ocr.DownloadTo(u, tmp, name, nil); err != nil {
			fails = append(fails, fmt.Sprintf("%s: %v", srcHost(u), err))
			continue
		}
		if !fileSized(tmp, size) {
			got := int64(-1)
			if fi, e := os.Stat(tmp); e == nil {
				got = fi.Size()
			}
			fails = append(fails, fmt.Sprintf("%s: 大小不符(期望 %d,实得 %d)", srcHost(u), size, got))
			continue
		}
		if err := os.Rename(tmp, dest); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("保存 %s 失败: %w", name, err) // 磁盘问题,换源也没用
		}
		return nil
	}
	_ = os.Remove(tmp)
	return fmt.Errorf("下载 %s 失败,%d 个源都不可用 —— %s",
		name, len(urls), strings.Join(fails, ";"))
}

// srcHost 从 URL 里取主机名,用于错误信息。取不出来就用原串(错误信息不值得为它失败)。
func srcHost(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		return u.Host
	}
	return rawURL
}

func ensureAssets() (Status, string) {
	dir, err := AssetDir()
	if err != nil {
		setStatus(StatusFailed, err)
		s, m := CurrentStatus()
		return s, m
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		setStatus(StatusFailed, err)
		s, m := CurrentStatus()
		return s, m
	}

	if !assetsReady(dir) {
		setStatus(StatusDownloading, nil)
		// ORT 动态库与 OCR 共用一份(见 ocr.EnsureORT):库只下一次,版本也只在一处维护。
		ortDir, err := ortCacheDir()
		if err == nil {
			err = ocr.EnsureORT(ortDir, nil)
		}
		if err != nil {
			setStatus(StatusFailed, fmt.Errorf("准备 onnxruntime 失败: %w", err))
			s, m := CurrentStatus()
			return s, m
		}
		for _, a := range []struct {
			urls []string
			name string
			size int64
		}{
			{modelURLs, modelFile, modelBytes},
			{tokenizerURLs, tokenizerFile, tokenizerBytes},
		} {
			if err := fetchAsset(dir, a.urls, a.name, a.size); err != nil {
				setStatus(StatusFailed, err)
				s, m := CurrentStatus()
				return s, m
			}
		}
	}

	if err := loadEmbedder(dir); err != nil {
		setStatus(StatusFailed, err)
		s, m := CurrentStatus()
		return s, m
	}
	// 趁还在后台,把样板句向量先算出来(约 140ms)。不预热的话这笔开销会落在
	// 用户的第一条消息上 —— 路由是发消息的必经之路,那一下等待很显眼。
	WarmUp()
	setStatus(StatusReady, nil)
	s, m := CurrentStatus()
	return s, m
}

// ortCacheDir 返回 ORT 动态库所在目录 —— 直接取 OCR 的资产目录,库只存一份。
//
// 必须走 ocr.DefaultCacheDir() 而不是自己拼路径:~/.deepx/ocr 下还有个 cache/ 子目录,
// 那是**粘贴图片**的缓存(tools.PasteCacheDir),不是资产目录。两者只差一层,
// 拼错了就是"库明明下好了却报找不到"——PR #219 当初栽的就是这个。
func ortCacheDir() (string, error) {
	dir := ocr.DefaultCacheDir()
	if dir == "" {
		return "", errNoHome
	}
	return dir, nil
}

var errNoHome = errors.New("无法确定用户目录,语义路由资产无处存放")
