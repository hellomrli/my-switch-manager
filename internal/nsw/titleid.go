// Package nsw 负责读取 Switch 游戏文件本身：容器（PFS0/HFS0/XCI）、NCA 头、CNMT 元数据与 TitleID 规则。
package nsw

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// TitleID 是 64 位的内容标识。
//
// 分配规则（任天堂为每个游戏预留 0x2000 的号段）：
//
//	本体 APP   base            低 13 位全 0
//	更新 UPD   base + 0x800
//	DLC        base + 0x1000 + n   (n = 1..0xFFF)
type TitleID uint64

type Kind int

const (
	KindUnknown Kind = iota
	KindApp
	KindPatch
	KindAddOn
)

func (k Kind) String() string {
	switch k {
	case KindApp:
		return "APP"
	case KindPatch:
		return "UPD"
	case KindAddOn:
		return "DLC"
	}
	return "未知"
}

// ParseTitleID 解析 16 位十六进制串（大小写均可）
func ParseTitleID(s string) (TitleID, bool) {
	s = strings.TrimSpace(s)
	if len(s) != 16 {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 16, 64)
	if err != nil || v == 0 {
		return 0, false
	}
	return TitleID(v), true
}

func (t TitleID) String() string {
	if t == 0 {
		return ""
	}
	return fmt.Sprintf("%016X", uint64(t))
}

// Lower 是 titledb 里 versions.json / cnmts.json 使用的键格式
func (t TitleID) Lower() string { return fmt.Sprintf("%016x", uint64(t)) }

func (t TitleID) Kind() Kind {
	if t == 0 {
		return KindUnknown
	}
	low := uint64(t) & 0x1FFF
	switch {
	case low == 0:
		return KindApp
	case low == 0x800:
		return KindPatch
	case low&0x1000 != 0 && low&0xFFF != 0:
		return KindAddOn
	}
	return KindUnknown
}

// Base 返回所属游戏本体的 TitleID。
//
// 旧版用 TitleID>>16 做分组键，会把同一个 0x10000 号段里的不同游戏
// （例如 …C000 与 …E000）并成一组。按 0x2000 号段对齐才是正确的归属规则。
func (t TitleID) Base() TitleID {
	if t.Kind() == KindUnknown {
		return t
	}
	return t &^ 0x1FFF
}

func (t TitleID) PatchID() TitleID { return t.Base() + 0x800 }

// 国内资源站常把 ID 直接粘在字母后面（xxxDeluxe01006B601380E000），不能用 \b。
var tidRe = regexp.MustCompile(`(?i)01[0-9a-f]{14}`)

// TitleIDFromName 从文件名中找 TitleID
func TitleIDFromName(name string) TitleID {
	for _, m := range tidRe.FindAllString(name, -1) {
		if t, ok := ParseTitleID(m); ok && t.Kind() != KindUnknown {
			return t
		}
	}
	return 0
}

// FormatVersion 把内部版本号显示成人能看懂的形式。
//
// 版本号的高 16 位是补丁序号（v65536 = 第 1 个补丁），并不是游戏里显示的 "1.0.1"
// 这类显示版本——后者只存在于 Control NCA 的 NACP 里。旧版把它拆成 a.b.c 会误导。
func FormatVersion(v int64) string {
	switch {
	case v < 0:
		return "未知"
	case v == 0:
		return "v0"
	case v%65536 == 0:
		return fmt.Sprintf("v%d（补丁 #%d）", v, v>>16)
	}
	return fmt.Sprintf("v%d", v)
}
