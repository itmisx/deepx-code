package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SegmenterConfig 分词器独立配置, 存储于 ~/.deepx/segmenter.yaml。
// SegmenterConfig 分词器独立配置, 存储于 ~/.deepx/segmenter.yaml。
type SegmenterConfig struct {
	// TopicTracking 是否启用主题追踪(话题切换检测 + 侧重点摘要)。
	// 设为 true 时自动创建分词器和嵌入器。默认 false。
	TopicTracking bool `yaml:"topic_tracking,omitempty"`
	// Routing 是否启用语义模型路由(4 级: simple/medium/complex/deep)。
	// 与 TopicTracking 互相独立: 可只开路由不开主题追踪, 或反之。
	// 设为 true 且配置了 Embedder 时创建语义路由。默认 false。
	Routing bool `yaml:"routing,omitempty"`
	// DictURL 自定义词典下载地址。空则使用默认 jieba 词典。
	DictURL string `yaml:"dict_url,omitempty"`
	// Embedder 嵌入器类型: "tfidf"(默认) / "onnx"(语义级)。
	// 设为 "onnx" 时启用 ONNX Sentence Embeddings, 首次使用自动下载模型。
	Embedder string `yaml:"embedder,omitempty"`
	// ONNXModel ONNX 语义模型名。默认 "bge-small-zh-v1.5"(自动下载)。
	// 其他模型需手动下载, 未下载时回退 TF-IDF。
	ONNXModel string `yaml:"onnx_model,omitempty"`
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
