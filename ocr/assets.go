package ocr

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 资源 URL。det/rec 用 MeKo-Christian/paddleocr-onnx (paddle2onnx 自动转 +
// GH Actions release),dict 取 PaddleOCR 主仓的官方版本。
const (
	detModelURL = "https://github.com/MeKo-Christian/paddleocr-onnx/releases/download/v1.0.0/PP-OCRv5_mobile_det.onnx"
	recModelURL = "https://github.com/MeKo-Christian/paddleocr-onnx/releases/download/v1.0.0/PP-OCRv5_mobile_rec.onnx"
	dictURL     = "https://raw.githubusercontent.com/PaddlePaddle/PaddleOCR/main/ppocr/utils/dict/ppocrv5_dict.txt"

	detModelFile = "PP-OCRv5_mobile_det.onnx"
	recModelFile = "PP-OCRv5_mobile_rec.onnx"
	dictFile     = "ppocrv5_dict.txt"
	readyMarker  = ".ready"
)

// ProgressFunc 下载进度回调,total 为 -1 表示未知。
type ProgressFunc func(name string, current, total int64)

// EnsureAssets 检测 cacheDir 下的全套资产 (ORT 动态库 + det/rec 模型 + dict)
// 是否齐备,缺什么补什么。完成后写入 .ready 标记,后续启动幂等跳过。
func EnsureAssets(cacheDir string, onProgress ProgressFunc) error {
	if isReady(cacheDir) {
		return nil
	}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}

	// 1. ORT 动态库
	libPath := filepath.Join(cacheDir, ortLibName)
	if !fileExists(libPath) {
		if err := downloadAndExtractORT(cacheDir, onProgress); err != nil {
			return fmt.Errorf("下载 onnxruntime 动态库失败: %w", err)
		}
	}

	// 2. det 模型
	detPath := filepath.Join(cacheDir, detModelFile)
	if !fileExists(detPath) {
		if err := downloadFile(mirrorCandidates(detModelURL), detPath, detModelFile, onProgress); err != nil {
			return fmt.Errorf("下载 det 模型失败: %w", err)
		}
	}

	// 3. rec 模型
	recPath := filepath.Join(cacheDir, recModelFile)
	if !fileExists(recPath) {
		if err := downloadFile(mirrorCandidates(recModelURL), recPath, recModelFile, onProgress); err != nil {
			return fmt.Errorf("下载 rec 模型失败: %w", err)
		}
	}

	// 4. 字典
	dictPath := filepath.Join(cacheDir, dictFile)
	if !fileExists(dictPath) {
		if err := downloadFile(mirrorCandidates(dictURL), dictPath, dictFile, onProgress); err != nil {
			return fmt.Errorf("下载字典失败: %w", err)
		}
	}

	return os.WriteFile(filepath.Join(cacheDir, readyMarker), []byte("ok"), 0644)
}

func isReady(cacheDir string) bool {
	_, err := os.Stat(filepath.Join(cacheDir, readyMarker))
	return err == nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// downloadClient 给所有 OCR 资源下载共用:
//   - ResponseHeaderTimeout 8s = dead/卡顿镜像快速跳过,健康服务器 headers 通常 <1s
//   - TLSHandshakeTimeout 6s = 防国外 CDN 在国内握手卡死
//   - 不设总体 Timeout,大模型文件 body 可能十几秒到几分钟
var downloadClient = &http.Client{
	Transport: &http.Transport{
		ResponseHeaderTimeout: 8 * time.Second,
		TLSHandshakeTimeout:   6 * time.Second,
	},
}

// downloadFile 按 urls 顺序尝试,任一成功即返回。所有都失败时把最后一个错误带上下文返回。
// 每个 URL 失败 (网络错误 / 非 200 / body 写入失败) 都自动跳下一个,不重试同一 URL。
func downloadFile(urls []string, destPath, displayName string, onProgress ProgressFunc) error {
	if len(urls) == 0 {
		return fmt.Errorf("downloadFile: 没有候选 URL")
	}
	var lastErr error
	for i, url := range urls {
		err := tryDownload(url, destPath, displayName, onProgress)
		if err == nil {
			return nil
		}
		lastErr = err
		// 还有候选时不打印错误,免得正常回退也吓到用户
		_ = i
	}
	return fmt.Errorf("所有镜像都失败,最后错误: %w", lastErr)
}

func tryDownload(url, destPath, displayName string, onProgress ProgressFunc) error {
	resp, err := downloadClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}

	tmpPath := destPath + ".tmp"
	dst, err := os.Create(tmpPath)
	if err != nil {
		return err
	}

	var reader io.Reader = resp.Body
	if onProgress != nil {
		reader = &progressReader{
			reader: resp.Body, total: resp.ContentLength,
			name: displayName, callback: onProgress,
		}
	}

	_, err = io.Copy(dst, reader)
	dst.Close()
	if err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, destPath)
}

func downloadAndExtractORT(cacheDir string, onProgress ProgressFunc) error {
	tmp, err := os.CreateTemp("", "ort-*."+ortArchiveFormat)
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	if err := downloadFile(mirrorCandidates(ortDownloadURL), tmpPath, ortLibName, onProgress); err != nil {
		return err
	}

	dest := filepath.Join(cacheDir, ortLibName)
	switch ortArchiveFormat {
	case "tgz":
		if err := extractFromTgz(tmpPath, ortArchiveLibPath, dest); err != nil {
			return err
		}
	case "zip":
		if err := extractFromZip(tmpPath, ortArchiveLibPath, dest); err != nil {
			return err
		}
	default:
		return fmt.Errorf("不支持的归档格式: %s", ortArchiveFormat)
	}
	return os.Chmod(dest, 0755)
}

func extractFromTgz(archivePath, target, dest string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		// 全路径匹配 或 文件名后缀匹配,后者用来兜底版本号略有出入的情形
		if h.Name == target || strings.HasSuffix(h.Name, "/"+filepath.Base(target)) {
			out, err := os.Create(dest)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, tr)
			out.Close()
			return err
		}
	}
	return fmt.Errorf("归档中未找到: %s", target)
}

func extractFromZip(archivePath, target, dest string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, zf := range r.File {
		if zf.Name == target || strings.HasSuffix(zf.Name, "/"+filepath.Base(target)) {
			src, err := zf.Open()
			if err != nil {
				return err
			}
			defer src.Close()
			out, err := os.Create(dest)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, src)
			out.Close()
			return err
		}
	}
	return fmt.Errorf("归档中未找到: %s", target)
}

type progressReader struct {
	reader   io.Reader
	total    int64
	current  int64
	name     string
	callback ProgressFunc
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.reader.Read(b)
	p.current += int64(n)
	p.callback(p.name, p.current, p.total)
	return n, err
}

// === 供其它模块复用的导出入口 ===
//
// ORT 动态库的下载/解压/命名在各平台都不一样(见 ort_*.go),而且版本要跟 purego 绑定的
// 那份对齐。语义路由(agent/router)也要跑 ONNX,与其复制一份、以后两处各自漂移,
// 不如把这里已经调通的东西导出去共用 —— 库只下一次,放同一个 cacheDir。

// ORTLibName 返回当前平台的 ONNX Runtime 动态库文件名。
func ORTLibName() string { return ortLibName }

// EnsureORT 只确保 ORT 动态库就位(不碰 OCR 的模型/字典),供只需要跑推理、
// 不需要 OCR 的调用方使用。已存在则直接返回。
func EnsureORT(cacheDir string, onProgress ProgressFunc) error {
	if fileExists(filepath.Join(cacheDir, ortLibName)) {
		return nil
	}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}
	return downloadAndExtractORT(cacheDir, onProgress)
}

// DownloadTo 把 url 下到 destPath(带镜像回退与进度回调),供其它模块下载自己的模型资产。
func DownloadTo(url, destPath, displayName string, onProgress ProgressFunc) error {
	return downloadFile(mirrorCandidates(url), destPath, displayName, onProgress)
}
