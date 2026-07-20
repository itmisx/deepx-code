//go:build windows

package tui

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"runtime"
	"syscall"
	"unsafe"
)

// clipboardTextHint 在 Windows 上恒为空 —— user32 syscall 直读剪贴板,不依赖外部二进制。
func clipboardTextHint() string { return "" }

const cfDIB = 8

var (
	user32                         = syscall.NewLazyDLL("user32.dll")
	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")

	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procGlobalLock   = kernel32.NewProc("GlobalLock")
	procGlobalUnlock = kernel32.NewProc("GlobalUnlock")
	procGlobalSize   = kernel32.NewProc("GlobalSize")
)

// readClipboardImage 通过 user32 syscall 拉取 CF_DIB,本地拼成 BMP 再解码为 PNG。
// 全程纯 Go syscall,不需要 cgo。
func readClipboardImage() ([]byte, error) {
	avail, _, _ := procIsClipboardFormatAvailable.Call(cfDIB)
	if avail == 0 {
		return nil, errNoClipboardImage
	}

	// Windows 剪贴板按 OS 线程归属:OpenClipboard 在哪个线程开,必须在同一线程 CloseClipboard
	// 才能成功关闭。Go 的 goroutine 会在 syscall 间被调度到别的 OS 线程 —— 尤其本函数下面
	// 还要跑 dibToPNG(PNG 编码,耗时+分配)期间剪贴板一直开着,极易迁线程 → defer CloseClipboard
	// 落到另一线程 → 关闭失败 → 剪贴板被全局锁死(issue #195)。锁住当前线程,把 open→close 钉在一起。
	// defer 后进先出:UnlockOSThread 在 CloseClipboard 之后执行,保证关闭时仍在同一线程。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var opened uintptr
	// OpenClipboard 可能因为别人正在持有而短暂失败,重试几次。
	for i := 0; i < 5; i++ {
		opened, _, _ = procOpenClipboard.Call(0)
		if opened != 0 {
			break
		}
	}
	if opened == 0 {
		return nil, errors.New("OpenClipboard failed")
	}
	defer procCloseClipboard.Call()

	h, _, _ := procGetClipboardData.Call(cfDIB)
	if h == 0 {
		return nil, errNoClipboardImage
	}
	size, _, _ := procGlobalSize.Call(h)
	if size == 0 {
		return nil, errNoClipboardImage
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return nil, errors.New("GlobalLock failed")
	}
	defer procGlobalUnlock.Call(h)

	// GlobalLock 返回的指针指向内核管理的全局内存,Go GC 不会移动它,
	// 解锁(GlobalUnlock,见 defer)之前是稳定的。这里的 uintptr→unsafe.Pointer
	// 转换会被 go vet 的 unsafeptr 检查告警,这是 Win32 API 调用的已知误报,
	// 在 defer GlobalUnlock 释放前就 copy 出来到 Go 堆,之后不再持有原指针。
	dib := make([]byte, size)
	copy(dib, unsafe.Slice((*byte)(unsafe.Pointer(ptr)), int(size)))

	return dibToPNG(dib)
}

// dibToPNG 把 BITMAPINFOHEADER + 像素 转成 PNG。
// 只覆盖最常见的 24 位 BI_RGB 与 32 位 BI_RGB / BI_BITFIELDS;
// 这够覆盖 Windows 截图工具(Snipping Tool / Snip & Sketch / 大多数浏览器复制图片)的输出。
// 遇到不支持的格式会明确报错,而不是产出错误的 PNG。
func dibToPNG(dib []byte) ([]byte, error) {
	if len(dib) < 40 {
		return nil, errors.New("DIB too small")
	}
	headerSize := binary.LittleEndian.Uint32(dib[0:4])
	width := int(int32(binary.LittleEndian.Uint32(dib[4:8])))
	heightRaw := int32(binary.LittleEndian.Uint32(dib[8:12]))
	height := int(heightRaw)
	topDown := false
	if height < 0 {
		// BMP 约定:height 为负表示 top-down 行序,与平时 bottom-up 相反
		height = -height
		topDown = true
	}
	bitCount := binary.LittleEndian.Uint16(dib[14:16])
	compression := binary.LittleEndian.Uint32(dib[16:20])

	if compression != 0 && compression != 3 { // BI_RGB | BI_BITFIELDS
		return nil, fmt.Errorf("unsupported BMP compression: %d", compression)
	}
	if bitCount != 24 && bitCount != 32 {
		return nil, fmt.Errorf("unsupported BMP bit count: %d", bitCount)
	}

	pixelOffset := int(headerSize)
	if compression == 3 {
		// BI_BITFIELDS 在 V3 头之后多 3 个 DWORD 的 R/G/B 掩码
		pixelOffset += 12
	}
	if pixelOffset > len(dib) {
		return nil, errors.New("pixel offset out of range")
	}
	pixels := dib[pixelOffset:]

	rowSize := ((int(bitCount)*width + 31) / 32) * 4
	need := rowSize * height
	if len(pixels) < need {
		return nil, fmt.Errorf("pixel data short: have %d need %d", len(pixels), need)
	}

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		srcRow := height - 1 - y
		if topDown {
			srcRow = y
		}
		row := pixels[srcRow*rowSize : srcRow*rowSize+rowSize]
		for x := 0; x < width; x++ {
			var r, g, b, a byte
			if bitCount == 24 {
				b, g, r, a = row[x*3], row[x*3+1], row[x*3+2], 255
			} else {
				b, g, r, a = row[x*4], row[x*4+1], row[x*4+2], row[x*4+3]
				if a == 0 {
					// BI_RGB 32 位的 alpha 字节常常是垃圾值;若全 0 视为不透明
					a = 255
				}
			}
			img.SetNRGBA(x, y, color.NRGBA{R: r, G: g, B: b, A: a})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
