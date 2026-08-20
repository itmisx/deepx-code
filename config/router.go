package config

// router.yaml 是语义路由样板句的用户存档,分两组:
//
//	patterns        像这些 → 起手用 pro
//	flash_patterns  像这些 → 维持 flash(问概念、小修小补)
//
// 两组都参与判定:消息离哪组更近就走哪边(见 agent/router)。两组都可增删,
// 由 /router-add-pro、/router-add-flash、/router-delete-pro、/router-delete-flash 管理,
// 也支持直接拿编辑器改。
//
// **启动时会自动生成**(EnsureRouterFile):没有文件就把两组内置表整份写进去。
// 这样用户手上永远有一份可直接编辑的起点,不必先摸清 YAML 结构、也不必先跑一次
// /router-add-pro 才把文件催生出来。
//
// 生成即冻结的问题靠指纹解决,**两组各记各的**:
//
//	该组 patterns 的指纹 == 该组的 builtin hash  → 用户没动过 → 内置表升级时自动同步
//	不相等                                       → 用户改过了 → 永不覆盖
//
// 分开记是必要的:只用一个总指纹的话,用户改了 pro 组就连 flash 组也一起冻结,
// 而他根本没碰过 flash 组 —— 那边的内置改进会莫名其妙送不到他手里。
//
// 没有指纹机制的话,自动生成等于把每个用户冻结在初次运行的版本上:
// 以后内置语句调好了也永远送不到他手里,而他根本不知道自己错过了什么。
//
// 存全量而不是增量(只记加了什么/删了什么):增量在展示和排错时都绕 ——
// 用户问"我现在到底有哪些",得先拿内置表再叠两次差集才能回答。
//
// 存的是**完整句子**而不是关键词:路由靠句向量相似度,不是子串匹配(理由见
// agent/router/patterns.go)。写 "重构" 这种词条不会按"包含重构二字"生效,
// 只会作为一个很短的句子参与语义比对,基本匹配不上任何东西。

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const routerFileName = "router.yaml"

// routerFileHeader 写在生成文件顶部。用户直接编辑时,这几行是他唯一的说明书。
const routerFileHeader = `# deepx 语义路由样板句 —— 决定一条消息起手用 flash 还是 pro。
#
# patterns        像这些 → 起手用 pro(动手干活、要通盘理解的任务)
# flash_patterns  像这些 → 维持 flash(问概念、小修小补)
#
# 判定的是消息离哪一组更近,所以两组都要写得像真实说法。
# 比的是**整句语义**,不是关键词包含:写 "重构" 这种词条不会按"消息里出现重构二字"生效,
# 要写成完整的任务陈述。
#
# 直接改这个文件即可,**下一条消息就生效**,不用重启;
# 也可以用 /router-add-pro、/router-add-flash、/router-delete-pro、/router-delete-flash。
# 想恢复最新的内置默认:删掉本文件,或把对应那组清空。
#
# builtin_hash / builtin_flash_hash 是两组内置表各自的指纹:某一组你没改过时,
# deepx 升级会自动同步这一组的新内置语句;你改过的那一组则永远不再被覆盖。

`

// RouterPatterns 是两组样板句。任一组为空 = 该组用内置默认。
type RouterPatterns struct {
	Pro   []string // 像这些 → pro
	Flash []string // 像这些 → 维持 flash
}

// routerFile 是 router.yaml 的序列化结构。
type routerFile struct {
	BuiltinHash      string   `yaml:"builtin_hash,omitempty"`
	BuiltinFlashHash string   `yaml:"builtin_flash_hash,omitempty"`
	Patterns         []string `yaml:"patterns"`
	FlashPatterns    []string `yaml:"flash_patterns"`
}

// RouterPath 返回 ~/.deepx/router.yaml 绝对路径。
func RouterPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法获取用户目录: %w", err)
	}
	return filepath.Join(home, dirName, routerFileName), nil
}

// hashPatterns 算一组样板句的指纹。含条数与顺序 —— 换序也算改过,
// 因为那说明用户确实动过这个文件,不该再被自动覆盖。
//
// **入参先归一化再算**,这条不能省:指纹要在"写入时"和"回读时"两侧算出同一个值,
// 而回读经过 YAML 往返 + trimAll。哪一侧漏了归一化,内置表里只要出现一个带空白的条目,
// 文件就会一出生就被判成"已自定义"——用户从此静默地再也收不到内置表更新,
// 而且没有任何现象能让人联想到是一个空格引起的。
func hashPatterns(ps []string) string {
	ps = trimAll(ps)
	h := sha256.New()
	for _, p := range ps {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func trimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// RouterFileResult 说明 EnsureRouterFile 这次做了什么,供 UI 决定要不要吭声。
type RouterFileResult struct {
	Created        bool // 首次生成
	RefreshedPro   bool // pro 组用户没改过,已同步到新内置表
	RefreshedFlash bool // flash 组同上
}

// EnsureRouterFile 保证 router.yaml 存在,且(在用户没改过的那一组上)与内置表同步。
//
// 两组独立判断:用户改过 pro 组、没碰 flash 组时,flash 组照常跟着版本走。
//
// 读不出 / 解析失败一律不动文件并返回 err,**绝不覆盖**:用户可能只是 YAML 写错一个缩进,
// 直接拿默认表把他的内容盖掉是最不能接受的行为。
func EnsureRouterFile(builtin RouterPatterns) (RouterFileResult, error) {
	var res RouterFileResult
	if len(builtin.Pro) == 0 && len(builtin.Flash) == 0 {
		return res, nil
	}
	p, err := RouterPath()
	if err != nil {
		return res, err
	}

	data, err := os.ReadFile(p)
	switch {
	case os.IsNotExist(err):
		res.Created = true
		return res, writeRouterFile(p, builtin, builtin)
	case err != nil:
		return res, err
	}

	var f routerFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return res, fmt.Errorf("解析 %s: %w", p, err)
	}

	cur := RouterPatterns{Pro: trimAll(f.Patterns), Flash: trimAll(f.FlashPatterns)}
	next := cur

	// pristine 判据:该组内容的指纹与文件里记的该组内置指纹相同 = 用户没动过这一组。
	// 指纹缺失(更早版本写的 / 用户删了那行)一律按"改过"处理 —— 异常都倒向不覆盖。
	if f.BuiltinHash != "" && f.BuiltinHash == hashPatterns(cur.Pro) &&
		f.BuiltinHash != hashPatterns(builtin.Pro) {
		next.Pro, res.RefreshedPro = builtin.Pro, true
	}
	if f.BuiltinFlashHash != "" && f.BuiltinFlashHash == hashPatterns(cur.Flash) &&
		f.BuiltinFlashHash != hashPatterns(builtin.Flash) {
		next.Flash, res.RefreshedFlash = builtin.Flash, true
	}
	if !res.RefreshedPro && !res.RefreshedFlash {
		return res, nil
	}
	return res, writeRouterFile(p, next, builtin)
}

// writeRouterFile 落盘。cur 是要存的内容,builtin 用来算两组指纹。
// 两边都先归一化 —— 存进文件的必须与算指纹的是同一份,否则下次回读算出来的指纹
// 对不上自己刚写的那个。
func writeRouterFile(path string, cur, builtin RouterPatterns) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	body, err := yaml.Marshal(routerFile{
		BuiltinHash:      hashPatterns(builtin.Pro),
		BuiltinFlashHash: hashPatterns(builtin.Flash),
		Patterns:         trimAll(cur.Pro),
		FlashPatterns:    trimAll(cur.Flash),
	})
	if err != nil {
		return err
	}
	return os.WriteFile(path, append([]byte(routerFileHeader), body...), 0600)
}

// LoadRouterPatterns 读两组样板句。
// 文件不存在 / 某一组为空,该组返回 nil —— 调用方据此让那一组回落内置默认。
// 解析失败返回 err(别静默吞掉,否则用户改坏了 yaml 却以为生效了)。
func LoadRouterPatterns() (RouterPatterns, error) {
	var out RouterPatterns
	p, err := RouterPath()
	if err != nil {
		return out, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil // 没配过 → 两组都用默认
		}
		return out, err
	}
	var f routerFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return out, fmt.Errorf("解析 %s: %w", p, err)
	}
	if ps := trimAll(f.Patterns); len(ps) > 0 {
		out.Pro = ps
	}
	if ps := trimAll(f.FlashPatterns); len(ps) > 0 {
		out.Flash = ps
	}
	return out, nil
}

// RouterPatternsCustomized 分别报告两组是否真的被改过(而不只是有那么个自动生成的文件)。
// /router-list-* 靠它显示"内置默认"还是"已自定义" —— 自动生成之后文件总是存在,
// 光看"文件在不在"会把每个人都显示成已自定义。
func RouterPatternsCustomized(builtin RouterPatterns) (pro, flash bool) {
	cur, err := LoadRouterPatterns()
	if err != nil {
		return false, false
	}
	pro = len(cur.Pro) > 0 && !slices.Equal(cur.Pro, trimAll(builtin.Pro))
	flash = len(cur.Flash) > 0 && !slices.Equal(cur.Flash, trimAll(builtin.Flash))
	return pro, flash
}

// SaveRouterPatterns 把两组全量表写回 router.yaml。
//
// 指纹存的是"内置表当前指纹",于是
//   - 某组存的内容恰好等于内置表 → 该组指纹自洽 → 之后仍随版本自动同步(他确实没做实质改动)
//   - 有任何差异 → 该组指纹对不上 → 从此不再被覆盖
//
// 两组都空 = 恢复默认 —— 直接删文件(下次启动 EnsureRouterFile 会重新生成一份最新的),
// 而不是留两个空列表:空的语义与"没配过"一致,留着只会让用户困惑
// "我明明有这个文件为什么还是默认行为"。
func SaveRouterPatterns(cur, builtin RouterPatterns) error {
	p, err := RouterPath()
	if err != nil {
		return err
	}
	if len(cur.Pro) == 0 && len(cur.Flash) == 0 {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeRouterFile(p, cur, builtin)
}
