//go:build windows

package tui

import (
	"errors"
	"runtime"
	"syscall"
	"unsafe"
)

// Windows 写系统剪贴板:用 user32 SetClipboardData + CF_UNICODETEXT(UTF-16),
// 不走 clip.exe —— clip.exe 按控制台当前代码页(中文 Windows 是 GBK/CP936)解码 stdin,
// 喂 UTF-8 文本会被当 GBK 解 → 选中复制粘贴出来全是乱码(见 issue #74)。
// 原生 API 直接收 UTF-16,从根上消除编码歧义。user32/kernel32 及 Open/Close/Lock/Unlock
// 句柄在 clipboard_windows.go 已声明,这里复用。
const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

var (
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	procGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	procGlobalFree       = kernel32.NewProc("GlobalFree")
)

func writeClipboardText(text string) error {
	// 转 UTF-16(含结尾 NUL),CF_UNICODETEXT 要求以 NUL 结尾。
	u16, err := syscall.UTF16FromString(text)
	if err != nil {
		return err // 文本里含 NUL 才会失败,选区文本不该有
	}

	// 锁住 OS 线程:Windows 剪贴板按线程归属,不锁则 OpenClipboard 与 defer CloseClipboard
	// 可能落在不同线程 → 关闭失败、剪贴板被全局锁死(见 readClipboardImage / issue #195)。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// OpenClipboard 可能因别人持有而短暂失败,重试几次(同 readClipboardImage)。
	var opened uintptr
	for i := 0; i < 5; i++ {
		if opened, _, _ = procOpenClipboard.Call(0); opened != 0 {
			break
		}
	}
	if opened == 0 {
		return errors.New("OpenClipboard failed")
	}
	defer procCloseClipboard.Call()

	procEmptyClipboard.Call()

	// 分配 GMEM_MOVEABLE 全局内存,锁定后拷入 UTF-16 数据。
	size := uintptr(len(u16) * 2) // 每个 uint16 两字节
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, size)
	if h == 0 {
		return errors.New("GlobalAlloc failed")
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		procGlobalFree.Call(h)
		return errors.New("GlobalLock failed")
	}
	// GlobalLock 返回内核全局内存指针,解锁前稳定;拷贝完即不再持有(同 readClipboardImage 的注释)。
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), len(u16)), u16)
	procGlobalUnlock.Call(h)

	// SetClipboardData 成功后,该内存所有权移交系统,不能再 GlobalFree;失败才需自己释放。
	if r, _, _ := procSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		procGlobalFree.Call(h)
		return errors.New("SetClipboardData failed")
	}
	return nil
}
