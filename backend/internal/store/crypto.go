package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"sheng-shou-yun-he/backend/internal/model"
)

const (
	cryptoVersion = "v1"
	encPrefix     = "enc:"
	keyLen        = 32 // AES-256
	saltLen       = 16
)

// DefaultMasterPassword 是历史版本在未设置 DB_CLIENT_MASTER 时缺省回退的
// 内置主密钥。源码公开意味着它不具保密性:只用于解密旧库密文的兜底身份
// 与最后回退,新密文绝不以它加密。
const DefaultMasterPassword = "dbclient-default-master"

// kdfParams 描述 PBKDF2 派生参数。版本化演进:currentKDF 用于所有新加密,
// legacyKDF 仅用于解密历史密文的回退;解开的遗留密文由调用方触发重加密
// 迁移(见 Store.EnsureCurrentKDF)。
type kdfParams struct {
	iterations int
}

var (
	currentKDF = kdfParams{iterations: 600000} // OWASP 对 PBKDF2-SHA256 的建议值
	legacyKDF  = kdfParams{iterations: 4096}   // 旧版本参数,仅解密回退
)

// gcmNonceLen 是 AES-GCM 的标准 nonce 长度(12 字节),包加载期探测一次。
var gcmNonceLen = func() int {
	block, _ := aes.NewCipher(make([]byte, keyLen))
	gcm, _ := cipher.NewGCM(block)
	return gcm.NonceSize()
}()

// Crypto encrypts sensitive fields (e.g. Kafka passwords) at rest using
// AES-256-GCM. The key is derived from a user master password with PBKDF2
// and a random per-encryption salt.
type Crypto struct {
	master string
	// fallbackMasters 是解密历史密文时在当前主密钥之后按序尝试的旧主密钥
	// (当前为内置默认主密钥——旧版本未设置 DB_CLIENT_MASTER 时的落盘密钥)。
	fallbackMasters []string
	mu              sync.Mutex
	// keyCache 缓存 salt+派生身份 → AES 密钥:当前参数单次 PBKDF2 派生成本
	// 高(600000 轮),同一密文的重复解密(List/Get 反复读同一行)必须命中
	// 缓存。密钥只存在内存,绝不上日志。
	keyCache map[string][]byte
}

// NewCrypto returns a Crypto bound to the given master password.
func NewCrypto(masterPassword string) (*Crypto, error) {
	if masterPassword == "" {
		return nil, errors.New("master password must not be empty")
	}
	c := &Crypto{master: masterPassword, keyCache: map[string][]byte{}}
	if masterPassword != DefaultMasterPassword {
		c.fallbackMasters = append(c.fallbackMasters, DefaultMasterPassword)
	}
	return c, nil
}

// kdfIdentity 唯一标识一次派生尝试(参数 × 主密钥槽位),用作缓存键;
// current 标记是否为「当前加密身份」——新加密只会产出该身份的密文。
type kdfIdentity struct {
	params     kdfParams
	masterSlot int // 0=当前主密钥,1..=fallbackMasters 顺序
	current    bool
}

// decryptIdentities 返回解密回退链:先按当前参数+当前主密钥派生,失败再按
// 遗留参数(4096 轮)+当前主密钥、最后按遗留参数+内置默认主密钥尝试——
// 覆盖旧版本库的两种落盘形态。成功解开的遗留密文由调用方重加密回写。
func (c *Crypto) decryptIdentities() []kdfIdentity {
	ids := []kdfIdentity{
		{params: currentKDF, masterSlot: 0, current: true},
		{params: legacyKDF, masterSlot: 0},
	}
	for i := range c.fallbackMasters {
		ids = append(ids, kdfIdentity{params: legacyKDF, masterSlot: i + 1})
	}
	return ids
}

func (c *Crypto) masterForSlot(slot int) string {
	if slot == 0 {
		return c.master
	}
	return c.fallbackMasters[slot-1]
}

// deriveKey 按身份派生 AES 密钥,同一 salt+身份只派生一次(缓存)。
func (c *Crypto) deriveKey(salt []byte, id kdfIdentity) ([]byte, error) {
	cacheKey := fmt.Sprintf("%s|%d|%d", hex.EncodeToString(salt), id.params.iterations, id.masterSlot)
	c.mu.Lock()
	if key, ok := c.keyCache[cacheKey]; ok {
		c.mu.Unlock()
		return key, nil
	}
	c.mu.Unlock()
	key, err := pbkdf2.Key(sha256.New, c.masterForSlot(id.masterSlot), salt, id.params.iterations, keyLen)
	if err != nil {
		return nil, fmt.Errorf("derive key: %w", err)
	}
	c.mu.Lock()
	c.keyCache[cacheKey] = key
	c.mu.Unlock()
	return key, nil
}

// Encrypt returns a self-contained, versioned ciphertext string, encrypted
// with the CURRENT KDF params.
// Format: "enc:v1:<base64(salt || nonce || ciphertext)>".
func (c *Crypto) Encrypt(plaintext string) (string, error) {
	return c.sealWith(c.master, currentKDF, plaintext)
}

// sealWith 以指定主密钥与 KDF 参数加密;非当前参数仅供构造遗留密文形态
// (迁移测试/旧库兼容)。
func (c *Crypto) sealWith(master string, p kdfParams, plaintext string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key, err := pbkdf2.Key(sha256.New, master, salt, p.iterations, keyLen)
	if err != nil {
		return "", fmt.Errorf("derive key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), nil)

	blob := make([]byte, 0, len(salt)+len(nonce)+len(sealed))
	blob = append(blob, salt...)
	blob = append(blob, nonce...)
	blob = append(blob, sealed...)

	return encPrefix + cryptoVersion + ":" + base64.RawStdEncoding.EncodeToString(blob), nil
}

// Decrypt reverses Encrypt. It returns an error if the blob was tampered
// with or no identity in the fallback chain can decrypt it.
func (c *Crypto) Decrypt(blob string) (string, error) {
	plain, _, err := c.open(blob)
	return plain, err
}

// NeedsReencrypt reports whether the blob decrypts but was NOT produced by
// the current KDF params with the current master (legacy params and/or the
// built-in default master) — i.e. the caller should re-encrypt it.
// Undecryptable blobs return false: they stay untouched so the regular read
// path surfaces the error instead of silently rewriting data.
func (c *Crypto) NeedsReencrypt(blob string) bool {
	_, current, err := c.open(blob)
	return err == nil && !current
}

// open 按回退链尝试解密,返回明文与是否为当前加密身份。
func (c *Crypto) open(blob string) (string, bool, error) {
	payload, ok := strings.CutPrefix(blob, encPrefix+cryptoVersion+":")
	if !ok {
		return "", false, errors.New("malformed ciphertext: missing version")
	}
	raw, err := base64.RawStdEncoding.DecodeString(payload)
	if err != nil {
		return "", false, fmt.Errorf("decode ciphertext: %w", err)
	}
	for _, id := range c.decryptIdentities() {
		plain, err := c.openWith(raw, id)
		if err == nil {
			return plain, id.current, nil
		}
	}
	return "", false, errors.New("decrypt failed (wrong password or tampered data)")
}

// openWith 以单个身份解密已解码的密文载荷。
func (c *Crypto) openWith(raw []byte, id kdfIdentity) (string, error) {
	if len(raw) < saltLen+gcmNonceLen {
		return "", errors.New("malformed ciphertext: too short")
	}
	salt := raw[:saltLen]
	nonce := raw[saltLen : saltLen+gcmNonceLen]
	sealed := raw[saltLen+gcmNonceLen:]
	key, err := c.deriveKey(salt, id)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt failed (wrong password or tampered data): %w", err)
	}
	return string(plain), nil
}

// --- 旧库 KDF 迁移 ---

// EnsureCurrentKDF 扫描存量连接配置,把以遗留 KDF 参数(4096 轮)或内置
// 默认主密钥加密的字段按当前参数重加密回写:遗留身份解出明文 → 当前身份
// 重新加密落盘。幂等:已属当前参数的行不动;完全解不开的行(主密钥不
// 匹配)原样保留,错误由正常读取路径暴露。Open 时调用一次。
func (s *Store) EnsureCurrentKDF() error {
	rows, err := s.db.Query(`SELECT id, type, config_json FROM connections`)
	if err != nil {
		return fmt.Errorf("scan connections for kdf migration: %w", err)
	}
	type pendingRow struct {
		id, typ, raw string
	}
	var pending []pendingRow
	for rows.Next() {
		var id, typ, raw string
		if err := rows.Scan(&id, &typ, &raw); err != nil {
			rows.Close()
			return fmt.Errorf("scan connection row: %w", err)
		}
		for _, blob := range encryptedBlobs(raw) {
			if s.crypto.NeedsReencrypt(blob) {
				pending = append(pending, pendingRow{id: id, typ: typ, raw: raw})
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate connections for kdf migration: %w", err)
	}
	rows.Close()
	for _, row := range pending {
		// 遗留身份解出 → 当前参数重加密;内容异常的行跳过,不阻断启动。
		cfg, err := s.unmarshalConfig(row.typ, row.raw)
		if err != nil {
			continue
		}
		rewritten, err := s.marshalConfig(model.ConnectionType(row.typ), cfg)
		if err != nil {
			continue
		}
		if _, err := s.db.Exec(`UPDATE connections SET config_json = ? WHERE id = ?`, rewritten, row.id); err != nil {
			return fmt.Errorf("rewrite connection %s for kdf migration: %w", row.id, err)
		}
	}
	return nil
}

// encryptedBlobs 递归收集存储 JSON 里所有 enc: 密文字段(只读不改),供
// KDF 迁移扫描使用;解析失败返回 nil。
func encryptedBlobs(rawJSON string) []string {
	var v any
	if err := json.Unmarshal([]byte(rawJSON), &v); err != nil {
		return nil
	}
	var blobs []string
	var walk func(x any)
	walk = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			for _, child := range t {
				walk(child)
			}
		case []any:
			for _, child := range t {
				walk(child)
			}
		case string:
			if strings.HasPrefix(t, encPrefix+cryptoVersion+":") {
				blobs = append(blobs, t)
			}
		}
	}
	walk(v)
	return blobs
}

// --- 主密钥解析 ---

// MasterKeySource 说明主密钥的解析来源。
type MasterKeySource string

const (
	MasterKeySourceEnv       MasterKeySource = "env"       // DB_CLIENT_MASTER
	MasterKeySourceFile      MasterKeySource = "file"      // master.key 密钥文件
	MasterKeySourceGenerated MasterKeySource = "generated" // 首次生成的随机密钥
	MasterKeySourceDefault   MasterKeySource = "default"   // 内置默认(最后回退)
)

// ResolveMasterPassword 按优先级解析落盘加密主密钥:
//  1. envValue(即 DB_CLIENT_MASTER 的值,非空白即生效);
//  2. keyPath 密钥文件(存在且内容非空白);
//  3. 首次生成 32 字节随机 hex 写入 keyPath(目录 0700、文件 0600);
//  4. 以上全部不可用时回退内置默认主密钥,并向 warn 写一行警告。
//
// 绝不向 warn 输出密钥内容(警告只含固定提示文案)。有意不引入 macOS
// Keychain:未签名应用会触发钥匙串 ACL 弹窗。
func ResolveMasterPassword(envValue, keyPath string, warn io.Writer) (string, MasterKeySource) {
	if v := strings.TrimSpace(envValue); v != "" {
		return v, MasterKeySourceEnv
	}
	if keyPath != "" {
		if data, err := os.ReadFile(keyPath); err == nil {
			if v := strings.TrimSpace(string(data)); v != "" {
				return v, MasterKeySourceFile
			}
		}
		if master, src, err := generateMasterKeyFile(keyPath); err == nil {
			return master, src
		}
	}
	if warn != nil {
		fmt.Fprintln(warn, "正在使用内置默认主密钥,建议设置 DB_CLIENT_MASTER")
	}
	return DefaultMasterPassword, MasterKeySourceDefault
}

// generateMasterKeyFile 生成 32 字节随机 hex 主密钥并写入 path(0600),
// 目录不存在则按 0700 创建。以 O_EXCL 创建:GUI 与 CLI 并发首跑时只有一方
// 落盘,后来方直接读现成文件,避免互相覆盖把对方的数据锁死。
func generateMasterKeyFile(path string) (string, MasterKeySource, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("generate master key: %w", err)
	}
	master := hex.EncodeToString(buf)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", "", fmt.Errorf("create master key dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		// 并发首跑:文件已被别的进程建好——有内容直接复用;空白文件不含
		// 任何密钥材料,无覆盖风险,直接写入新生成的密钥。
		if data, rerr := os.ReadFile(path); rerr == nil {
			if v := strings.TrimSpace(string(data)); v != "" {
				return v, MasterKeySourceFile, nil
			}
			if werr := os.WriteFile(path, []byte(master), 0o600); werr == nil {
				return master, MasterKeySourceGenerated, nil
			}
		}
		return "", "", fmt.Errorf("create master key file: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(master); err != nil {
		return "", "", fmt.Errorf("write master key: %w", err)
	}
	return master, MasterKeySourceGenerated, nil
}
