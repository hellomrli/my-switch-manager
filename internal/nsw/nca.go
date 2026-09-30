package nsw

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
)

// ============================ 密钥 ============================
//
// 可选功能：提供 prod.keys 后，直接从 CNMT NCA 解出精确的 TitleID、版本号与包含内容，
// 不再依赖文件名。用到的只有 header_key 与 key_area_key_application_XX。
// 本工具不附带、也不生成任何密钥。

type Keys struct {
	Path      string
	headerKey []byte     // 32 字节 AES-XTS 密钥
	kaekApp   [32][]byte // key_area_key_application_00..1f
	Count     int        // 读入的键数量（仅用于展示）
}

func LoadKeys(path string) (*Keys, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	k := &Keys{Path: path}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		name, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		name = strings.ToLower(strings.TrimSpace(name))
		b, err := hex.DecodeString(strings.TrimSpace(val))
		if err != nil {
			continue
		}
		k.Count++
		switch {
		case name == "header_key" && len(b) == 32:
			k.headerKey = b
		case strings.HasPrefix(name, "key_area_key_application_") && len(b) == 16:
			var idx int
			if _, err := fmt.Sscanf(name[len("key_area_key_application_"):], "%x", &idx); err == nil && idx < len(k.kaekApp) {
				k.kaekApp[idx] = b
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if k.headerKey == nil {
		return nil, errors.New("密钥文件里没有 header_key")
	}
	return k, nil
}

// Fingerprint 标识一组密钥的内容（不泄露密钥本身）。扫描缓存据此判断：
// 换了密钥文件后，之前解密失败的文件值得再试一次。
func (k *Keys) Fingerprint() string {
	h := sha256.New()
	h.Write(k.headerKey)
	for _, v := range k.kaekApp {
		h.Write([]byte{byte(len(v))})
		h.Write(v)
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// MaxKeyGeneration 返回可用的最高 key_area_key 代数（+1 后即对应的 keygen）
func (k *Keys) MaxKeyGeneration() int {
	max := -1
	for i, v := range k.kaekApp {
		if v != nil {
			max = i
		}
	}
	return max
}

// ============================ NCA 头 ============================

type NCAHeader struct {
	ContentType byte // 0 Program 1 Meta 2 Control 3 Manual 4 Data 5 PublicData
	KeyGen      int  // 取 0x206 与 0x220 的较大者
	KAEKIndex   byte
	ProgramID   TitleID
	RightsID    [16]byte
	Sections    [4]ncaSection
	keyArea     [4][16]byte
	fsHeaders   [4][]byte
}

type ncaSection struct {
	Start, End int64 // 字节偏移（已乘 0x200）
}

func (h *NCAHeader) HasRightsID() bool {
	for _, b := range h.RightsID {
		if b != 0 {
			return true
		}
	}
	return false
}

// ReadNCAHeader 解密 NCA 头（0xC00 字节，AES-XTS，扇区 0x200，任天堂用大端扇区号作 tweak）
func (k *Keys) ReadNCAHeader(r io.ReaderAt, base int64) (*NCAHeader, error) {
	if !validRange(base, 0xC00, math.MaxInt64) {
		return nil, errors.New("NCA 头偏移异常")
	}
	enc := make([]byte, 0xC00)
	if err := readFullAt(r, enc, base); err != nil {
		return nil, fmt.Errorf("读取 NCA 头失败: %w", err)
	}
	dec, err := xtsDecrypt(k.headerKey, enc, 0x200, 0)
	if err != nil {
		return nil, err
	}
	magic := string(dec[0x200:0x204])
	if magic != "NCA3" {
		if magic == "NCA2" {
			return nil, errors.New("暂不支持 NCA2 格式")
		}
		return nil, errors.New("NCA 头解密失败（header_key 不匹配？）")
	}
	h := &NCAHeader{
		ContentType: dec[0x205],
		KAEKIndex:   dec[0x207],
		ProgramID:   TitleID(binary.LittleEndian.Uint64(dec[0x210:])),
	}
	h.KeyGen = int(dec[0x206])
	if g := int(dec[0x220]); g > h.KeyGen {
		h.KeyGen = g
	}
	copy(h.RightsID[:], dec[0x230:0x240])
	for i := 0; i < 4; i++ {
		e := dec[0x240+i*0x10:]
		h.Sections[i] = ncaSection{
			Start: int64(binary.LittleEndian.Uint32(e[0:4])) * 0x200,
			End:   int64(binary.LittleEndian.Uint32(e[4:8])) * 0x200,
		}
		copy(h.keyArea[i][:], dec[0x300+i*0x10:])
		h.fsHeaders[i] = dec[0x400+i*0x200 : 0x600+i*0x200]
	}
	return h, nil
}

// ============================ CNMT ============================

// Content 是文件里包含的一个 Title（本体/更新/DLC 之一）
type Content struct {
	TitleID TitleID `json:"tid"`
	Version int64   `json:"ver"` // -1 = 未知
}

func (c Content) Kind() Kind { return c.TitleID.Kind() }

// 单个 CNMT NCA 的上限；真实文件通常只有几 KB
const maxMetaNCASize = 4 << 20

// CNMTInfo 同时保留 CNMT 声明的必需 NCA，以验证整个包而非仅有元数据。
type CNMTInfo struct {
	Content
	Required []string
}

// ReadCNMT 兼容只需要 TitleID 与版本的调用方。
func (k *Keys) ReadCNMT(r io.ReaderAt, base, size int64) (Content, error) {
	info, err := k.ReadCNMTInfo(r, base, size)
	return info.Content, err
}

// ReadCNMTInfo 解密 Meta NCA 的第 0 分区，读取 Title 与必需内容清单。
func (k *Keys) ReadCNMTInfo(r io.ReaderAt, base, size int64) (CNMTInfo, error) {
	if size < 0xC00 || size > maxMetaNCASize || !validRange(base, size, math.MaxInt64) {
		return CNMTInfo{}, fmt.Errorf("CNMT NCA 体积异常: %d", size)
	}
	h, err := k.ReadNCAHeader(r, base)
	if err != nil {
		return CNMTInfo{}, err
	}
	if h.ContentType != 1 {
		return CNMTInfo{}, errors.New("不是 Meta NCA")
	}
	if h.HasRightsID() {
		return CNMTInfo{}, errors.New("Meta NCA 使用了票据加密（不应出现）")
	}
	if h.KAEKIndex != 0 {
		return CNMTInfo{}, fmt.Errorf("不支持的密钥区索引 %d", h.KAEKIndex)
	}
	gen := h.KeyGen - 1
	if gen < 0 {
		gen = 0
	}
	if gen >= len(k.kaekApp) || k.kaekApp[gen] == nil {
		return CNMTInfo{}, fmt.Errorf("缺少 key_area_key_application_%02x（该文件需要更新的密钥）", gen)
	}
	kaek, _ := aes.NewCipher(k.kaekApp[gen])
	var sectionKey [16]byte
	kaek.Decrypt(sectionKey[:], h.keyArea[2][:])

	fs := h.fsHeaders[0]
	sec := h.Sections[0]
	if sec.Start < 0xC00 || sec.End <= sec.Start || sec.End > size {
		return CNMTInfo{}, errors.New("NCA 分区表异常")
	}
	if fs[0x2] != 1 { // fs type: 1 = PFS0
		return CNMTInfo{}, errors.New("Meta NCA 第 0 分区不是 PFS0")
	}
	if fs[0x4] != 3 { // encryption type: 3 = AES-CTR
		return CNMTInfo{}, fmt.Errorf("不支持的分区加密方式 %d", fs[0x4])
	}
	// 分区数据整体解密（CTR 计数器 = 分区 ctr（倒序）|| 绝对偏移/16（大端））
	raw := make([]byte, sec.End-sec.Start)
	if err := readFullAt(r, raw, base+sec.Start); err != nil {
		return CNMTInfo{}, err
	}
	var iv [16]byte
	for i := 0; i < 8; i++ {
		iv[i] = fs[0x147-i]
	}
	binary.BigEndian.PutUint64(iv[8:], uint64(sec.Start)>>4)
	blk, _ := aes.NewCipher(sectionKey[:])
	cipher.NewCTR(blk, iv[:]).XORKeyStream(raw, raw)

	// HierarchicalSha256 超级块里第二个区域就是 PFS0 的位置
	pfsOff := int64(binary.LittleEndian.Uint64(fs[0x8+0x38:]))
	if pfsOff < 0 || pfsOff >= int64(len(raw)) {
		return CNMTInfo{}, errors.New("PFS0 偏移异常")
	}
	return parseCNMTPartition(raw, pfsOff)
}

// parseCNMTPartition 单独解析已解密的数据，所有偏移在切片前完成校验。
func parseCNMTPartition(raw []byte, pfsOff int64) (CNMTInfo, error) {
	br := byteReaderAt(raw)
	ents, err := readPartition(br, pfsOff, int64(len(raw)), "PFS0", 0x18)
	if err != nil {
		return CNMTInfo{}, fmt.Errorf("Meta 分区: %w", err)
	}
	for _, e := range ents {
		if !strings.HasSuffix(strings.ToLower(e.Name), ".cnmt") {
			continue
		}
		if e.Size < 0x20 || !validRange(e.Offset, e.Size, int64(len(raw))) {
			return CNMTInfo{}, errors.New("CNMT 文件范围异常")
		}
		b := raw[e.Offset : e.Offset+e.Size]
		info := CNMTInfo{Content: Content{
			TitleID: TitleID(binary.LittleEndian.Uint64(b[0:8])),
			Version: int64(binary.LittleEndian.Uint32(b[8:12])),
		}}
		if info.TitleID.Kind() == KindUnknown {
			return CNMTInfo{}, fmt.Errorf("CNMT 中的 TitleID %s 无法识别", info.TitleID)
		}
		start := int64(0x20) + int64(binary.LittleEndian.Uint16(b[0xe:0x10]))
		count := int64(binary.LittleEndian.Uint16(b[0x10:0x12]))
		metaCount := int64(binary.LittleEndian.Uint16(b[0x12:0x14]))
		if !validRange(start, count*0x38+metaCount*0x10, int64(len(b))) {
			return CNMTInfo{}, errors.New("CNMT 内容表范围异常")
		}
		for i := int64(0); i < count; i++ {
			id := b[start+i*0x38+0x20 : start+i*0x38+0x30]
			info.Required = append(info.Required, hex.EncodeToString(id))
		}
		return info, nil
	}
	return CNMTInfo{}, errors.New("Meta 分区里没有 .cnmt 文件")
}

type byteReaderAt []byte

func (b byteReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(b)) {
		return 0, io.EOF
	}
	n := copy(p, b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// ============================ AES-XTS（任天堂变体） ============================
//
// 与标准 XTS 的唯一区别：tweak 用大端扇区号。golang.org/x/crypto/xts 是小端，不能直接用。
func xtsDecrypt(key, data []byte, sectorSize int, firstSector uint64) ([]byte, error) {
	if len(key) != 32 || len(data)%sectorSize != 0 {
		return nil, errors.New("XTS 参数错误")
	}
	k1, _ := aes.NewCipher(key[:16])
	k2, _ := aes.NewCipher(key[16:])
	out := make([]byte, len(data))
	for s := 0; s*sectorSize < len(data); s++ {
		var tweak [16]byte
		binary.BigEndian.PutUint64(tweak[8:], firstSector+uint64(s))
		k2.Encrypt(tweak[:], tweak[:])
		for off := s * sectorSize; off < (s+1)*sectorSize; off += 16 {
			var blk [16]byte
			for i := 0; i < 16; i++ {
				blk[i] = data[off+i] ^ tweak[i]
			}
			k1.Decrypt(blk[:], blk[:])
			for i := 0; i < 16; i++ {
				out[off+i] = blk[i] ^ tweak[i]
			}
			// tweak *= α（GF(2^128)，小端比特序）
			carry := tweak[15] >> 7
			for i := 15; i > 0; i-- {
				tweak[i] = tweak[i]<<1 | tweak[i-1]>>7
			}
			tweak[0] <<= 1
			if carry != 0 {
				tweak[0] ^= 0x87
			}
		}
	}
	return out, nil
}
