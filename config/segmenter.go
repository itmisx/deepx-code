package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SegmenterConfig 分词器独立配置, 存储于 ~/.deepx/segmenter.yaml。
type SegmenterConfig struct {
	// Language 分词语言。空/不配置 = 不启用。目前支持: "zh"(中文)
	Language string `yaml:"language,omitempty"`
	// DictURL 自定义词典下载地址。空则使用默认 jieba 词典。
	DictURL string `yaml:"dict_url,omitempty"`
}

const segmenterFileName = "segmenter.yaml"

// SegmenterPath 返回 ~/.deepx/segmenter.yaml 绝对路径。
func SegmenterPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法获取用户目录: %w", err)
	}
	return filepath.Join(home, dirName, segmenterFileName), nil
}

// SegmenterDir 返回 ~/.deepx/segmenter/ 目录(词典缓存用)。
func SegmenterDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法获取用户目录: %w", err)
	}
	return filepath.Join(home, dirName, "segmenter"), nil
}

// LoadSegmenter 读 segmenter.yaml。文件不存在返回空配置(不启用), 不报错。
func LoadSegmenter() (*SegmenterConfig, error) {
	p, err := SegmenterPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &SegmenterConfig{}, nil
		}
		return nil, err
	}
	var c SegmenterConfig
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", p, err)
	}
	return &c, nil
}

// SaveSegmenter 写 segmenter.yaml。
func SaveSegmenter(c *SegmenterConfig) error {
	p, err := SegmenterPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0600)
}
