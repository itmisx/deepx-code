package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	ort "github.com/getcharzp/onnxruntime_purego"
)

// ortEngine 共享的 ONNX Runtime 引擎(单例)。
var (
	ortEngineOnce sync.Once
	ortEngineInst *ortEngine
	ortEngineErr  error
)

type ortEngine struct {
	engine *ort.Engine
}

// GetORTEngine 返回共享的 ONNX Runtime 引擎。
// libDir 是 ONNX Runtime 共享库所在目录(应与 OCR 共享库同目录)。
func GetORTEngine(libDir string) (*ortEngine, error) {
	ortEngineOnce.Do(func() {
		ortEngineInst, ortEngineErr = newORTEngine(libDir)
	})
	return ortEngineInst, ortEngineErr
}

func newORTEngine(libDir string) (*ortEngine, error) {
	libPath := filepath.Join(libDir, ortLibName)
	if _, err := os.Stat(libPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("ONNX Runtime 共享库不存在: %s (请确保 OCR 已初始化)", libPath)
	}
	engine, err := ort.NewEngine(libPath)
	if err != nil {
		return nil, fmt.Errorf("初始化 ONNX Runtime 失败: %w", err)
	}
	return &ortEngine{engine: engine}, nil
}

// NewSession 创建 ONNX 推理会话。
func (e *ortEngine) NewSession(modelPath string, threads int) (*ort.Session, error) {
	opts, err := e.engine.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	defer opts.Destroy()

	if threads <= 0 {
		threads = runtime.NumCPU() / 2
		if threads < 1 {
			threads = 1
		}
	}
	_ = opts.SetIntraOpNumThreads(int32(threads))
	_ = opts.SetCpuMemArena(true)

	session, err := e.engine.NewSession(modelPath, opts)
	if err != nil {
		return nil, fmt.Errorf("加载模型失败: %w", err)
	}
	return session, nil
}
