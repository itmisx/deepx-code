package skill

import (
	"embed"
	"os"
	"path/filepath"
)

//go:embed skills/*
var builtinFS embed.FS

const builtinVersion = "3"

// ExtractBuiltins 将内嵌 skill 解压到 ~/.deepx/skills/。
// 通过版本文件判断是否需要更新，避免每次启动都写盘。
// 用户自定义 skill 不受影响（只覆盖同名内置 skill）。
func ExtractBuiltins(home string) (string, error) {
	dest := filepath.Join(home, ".deepx", "skills")
	verFile := filepath.Join(dest, ".builtin_version")

	if data, err := os.ReadFile(verFile); err == nil && string(data) == builtinVersion {
		return dest, nil
	}

	entries, err := builtinFS.ReadDir("skills")
	if err != nil {
		return dest, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		srcDir := "skills/" + e.Name()
		dstDir := filepath.Join(dest, e.Name())
		if err := copyBuiltinDir(srcDir, dstDir); err != nil {
			return dest, err
		}
	}

	os.MkdirAll(dest, 0o755)
	os.WriteFile(verFile, []byte(builtinVersion), 0o644)
	return dest, nil
}

func copyBuiltinDir(src, dst string) error {
	entries, err := builtinFS.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		srcPath := src + "/" + e.Name()
		dstPath := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyBuiltinDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			data, err := builtinFS.ReadFile(srcPath)
			if err != nil {
				return err
			}
			if err := os.WriteFile(dstPath, data, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}
