package config

// provider.yaml 是「已配置供应商」的存档,供 /provider 快捷切换。
//
// 每次 /config 完成会把当前配置按供应商名(deepseek / mimo / kimi / qwen / custom)
// upsert 进来;/provider 从这里读名字列表、把选中供应商的 flash/pro 写回 model.yaml。
//
// YAML 结构(供应商名 → 该供应商的 {flash, pro},与 model.yaml 的 Config 同构):
//
//	deepseek:
//	  flash: {base_url, model, api_key, context_window, max_tokens}
//	  pro:   {base_url, model, api_key, context_window, max_tokens}
//	mimo:
//	  flash: {...}
//	  pro:   {...}

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const providerFileName = "provider.yaml"

// providerNameRe 限定自定义供应商名的字符集:小写字母/数字开头,其后可跟小写字母/数字/. _ -,总长 1..32。
// 这个名字既是 provider.yaml 的 YAML key,也是 `/provider <名字>` 的参数,所以不收空格、不收大小写歧义。
var providerNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

// NormalizeProviderName 规范化用户输入的供应商名:去首尾空白 + 转小写。
// 空输入回退到 ProviderCustom —— 不起名字就还是占 "custom" 那个槽,保持老行为。
func NormalizeProviderName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ProviderCustom
	}
	return s
}

// ValidProviderName 报告规范化后的名字是否符合字符集要求。
func ValidProviderName(name string) bool { return providerNameRe.MatchString(name) }

// IsPresetProvider 报告 name 是否为预设供应商 id(deepseek / mimo / kimi / qwen)。
// ProviderCustom 不算 —— 它是"没起名字的自定义"的默认落点,允许被自定义配置占用。
//
// 预设名对自定义配置是保留字:那些槽由「选预设供应商 + 填 api_key」的流程维护(base_url/model 都套
// modelConfig 默认),自定义配置占了名字会让 /provider 列表里的同名项名不副实。
func IsPresetProvider(name string) bool {
	for _, p := range ProviderOptions {
		if p == name {
			return p != ProviderCustom
		}
	}
	return false
}

// Providers 是 provider.yaml 的反序列化目标:供应商名 → 该供应商的 {flash, pro} 配置。
type Providers map[string]Config

// ProviderPath 返回 ~/.deepx/provider.yaml 绝对路径。
func ProviderPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法获取用户目录: %w", err)
	}
	return filepath.Join(home, dirName, providerFileName), nil
}

// LoadProviders 读 provider.yaml。文件不存在视为空(返回空 map,非错误);解析失败返回 err。
func LoadProviders() (Providers, error) {
	p, err := ProviderPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return Providers{}, nil
	}
	if err != nil {
		return nil, err
	}
	var ps Providers
	if err := yaml.Unmarshal(data, &ps); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", p, err)
	}
	if ps == nil {
		ps = Providers{}
	}
	return ps, nil
}

// SaveProvider 把一份配置按供应商名 upsert 进 provider.yaml(其余供应商原样保留)。
// custom 统一占 "custom" 一个槽,后配置的覆盖旧的。文件权限 0600(含 api key)。
func SaveProvider(name string, c *Config) error {
	if name == "" || c == nil {
		return nil
	}
	ps, err := LoadProviders()
	if err != nil {
		// 读失败(文件损坏)就从空开始,免得一份坏文件永久挡住存档。
		ps = Providers{}
	}
	ps[name] = *c
	return saveProviders(ps)
}

func saveProviders(ps Providers) error {
	p, err := ProviderPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	data, err := yaml.Marshal(ps)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0600)
}

// DeleteProvider 从 provider.yaml 删掉一个供应商存档。名字不存在视为成功(幂等)。
// 只动存档 —— model.yaml 里当前生效的配置不受影响。
func DeleteProvider(name string) error {
	ps, err := LoadProviders()
	if err != nil {
		return err
	}
	if _, ok := ps[name]; !ok {
		return nil
	}
	delete(ps, name)
	return saveProviders(ps)
}

// LoadProvider 取单个供应商的存档配置;不存在返回 (nil, false, nil)。
func LoadProvider(name string) (*Config, bool, error) {
	ps, err := LoadProviders()
	if err != nil {
		return nil, false, err
	}
	c, ok := ps[name]
	if !ok {
		return nil, false, nil
	}
	return &c, true, nil
}

// ProviderNames 返回 provider.yaml 中已存的供应商名,顺序供 /provider 选择器稳定展示:
//
//  1. 预设供应商(deepseek / mimo / kimi / qwen)按 ProviderOptions 的顺序排在最前;
//  2. 用户起了名字的自定义供应商按字母序跟在后面;
//  3. ProviderCustom("custom")固定垫底 —— 它是"没起名字"的杂物槽,内容随时被下一次匿名
//     自定义覆盖,排在有名有姓的配置前面只会碍眼。
//
// 文件为空时返回空切片。
func ProviderNames() ([]string, error) {
	ps, err := LoadProviders()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(ps))
	seen := make(map[string]bool, len(ps))
	for _, p := range ProviderOptions {
		if p == ProviderCustom {
			continue // 留到最后
		}
		if _, ok := ps[p]; ok {
			names = append(names, p)
			seen[p] = true
		}
	}
	rest := make([]string, 0)
	for k := range ps {
		if !seen[k] && k != ProviderCustom {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	names = append(names, rest...)
	if _, ok := ps[ProviderCustom]; ok {
		names = append(names, ProviderCustom)
	}
	return names, nil
}
