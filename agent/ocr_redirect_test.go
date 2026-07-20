package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestOcrImageFilePath 验证 #194 的判定:OCR 的 path 指向真实本地图片文件才命中,
// 不存在 / 目录 / 非图片扩展名 / 坏 JSON 一律不命中。
func TestOcrImageFilePath(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "pic.png")
	if err := os.WriteFile(img, []byte("\x89PNG"), 0o644); err != nil {
		t.Fatal(err)
	}
	txt := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(txt, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	imgDir := filepath.Join(dir, "d.png")
	if err := os.Mkdir(imgDir, 0o755); err != nil {
		t.Fatal(err)
	}

	args := func(p string) string {
		b, _ := json.Marshal(map[string]string{"path": p})
		return string(b)
	}

	cases := []struct {
		name    string
		argJSON string
		wantOK  bool
		wantAbs string
	}{
		{"真实图片", args(img), true, img},
		{"不存在", args(filepath.Join(dir, "nope.png")), false, ""},
		{"非图片扩展名", args(txt), false, ""},
		{"图片扩展名的目录", args(imgDir), false, ""},
		{"空 path", args(""), false, ""},
		{"坏 JSON", "{not json", false, ""},
	}
	for _, c := range cases {
		got, ok := ocrImageFilePath(c.argJSON)
		if ok != c.wantOK {
			t.Errorf("%s: ok=%v want %v", c.name, ok, c.wantOK)
			continue
		}
		if ok && got != c.wantAbs {
			t.Errorf("%s: abs=%q want %q", c.name, got, c.wantAbs)
		}
	}
}
