package store

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- KDF 版本化与遗留密文回退 ---

// TestKDFParams 锁定派生参数:新加密 600000 轮,遗留回退 4096 轮。
func TestKDFParams(t *testing.T) {
	if currentKDF.iterations != 600000 {
		t.Fatalf("currentKDF.iterations = %d, want 600000", currentKDF.iterations)
	}
	if legacyKDF.iterations != 4096 {
		t.Fatalf("legacyKDF.iterations = %d, want 4096", legacyKDF.iterations)
	}
}

// TestCryptoNewCiphertextUsesCurrentKDF 验证新密文按当前参数加密:当前身份
// 可解、无需重加密,且遗留参数身份解不开。
func TestCryptoNewCiphertextUsesCurrentKDF(t *testing.T) {
	c, err := NewCrypto("master-1")
	if err != nil {
		t.Fatal(err)
	}
	enc, err := c.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	if c.NeedsReencrypt(enc) {
		t.Fatal("fresh ciphertext must not need re-encryption")
	}
	payload, _ := strings.CutPrefix(enc, encPrefix+cryptoVersion+":")
	raw, err := base64.RawStdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.openWith(raw, kdfIdentity{params: legacyKDF, masterSlot: 0}); err == nil {
		t.Fatal("legacy params must not decrypt a current ciphertext")
	}
}

// TestCryptoLegacyParamsFallback 同一主密钥、旧参数(4096 轮)密文:可解,
// 但必须标记需要按当前参数重加密。
func TestCryptoLegacyParamsFallback(t *testing.T) {
	c, err := NewCrypto("same-master")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := c.sealWith("same-master", legacyKDF, "legacy-secret")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := c.Decrypt(legacy)
	if err != nil || plain != "legacy-secret" {
		t.Fatalf("legacy-params blob must decrypt, got %q, %v", plain, err)
	}
	if !c.NeedsReencrypt(legacy) {
		t.Fatal("legacy-params ciphertext must need re-encryption")
	}
	fresh, err := c.Encrypt("legacy-secret")
	if err != nil {
		t.Fatal(err)
	}
	if c.NeedsReencrypt(fresh) {
		t.Fatal("re-encrypted blob must not need re-encryption again")
	}
}

// TestCryptoLegacyDefaultMasterFallback 旧版本未设 DB_CLIENT_MASTER 的真实
// 场景:密文 = 内置默认主密钥 + 4096 轮;新主密钥的 Crypto 必须经回退链
// 解出并标记迁移。
func TestCryptoLegacyDefaultMasterFallback(t *testing.T) {
	old, err := NewCrypto(DefaultMasterPassword)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := old.sealWith(DefaultMasterPassword, legacyKDF, "old-default-secret")
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewCrypto("brand-new-master")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := c.Decrypt(legacy)
	if err != nil || plain != "old-default-secret" {
		t.Fatalf("legacy-default-master blob must decrypt via fallback, got %q, %v", plain, err)
	}
	if !c.NeedsReencrypt(legacy) {
		t.Fatal("legacy-default-master ciphertext must need re-encryption")
	}
}

// TestCryptoUnknownMasterStillFails 未知主密钥(旧环境变量密钥丢失)不能解。
func TestCryptoUnknownMasterStillFails(t *testing.T) {
	c1, _ := NewCrypto("user-old-env-master")
	legacy, err := c1.sealWith("user-old-env-master", legacyKDF, "s")
	if err != nil {
		t.Fatal(err)
	}
	c2, _ := NewCrypto("another-master")
	if _, err := c2.Decrypt(legacy); err == nil {
		t.Fatal("unknown master must not decrypt")
	}
	if c2.NeedsReencrypt(legacy) {
		t.Fatal("undecryptable blob must not be flagged for rewrite")
	}
}

// --- Open 时自动迁移旧库 ---

// insertLegacyRow 以旧版本形态直插一行连接(密文由 sealWith 生成),模拟
// 旧版本二进制写出的 config.db。
func insertLegacyRow(t *testing.T, dbPath, id, typ string, master string, params kdfParams, plain string) {
	t.Helper()
	c, err := NewCrypto(master)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := c.sealWith(master, params, plain)
	if err != nil {
		t.Fatal(err)
	}
	rawCfg := fmt.Sprintf(`{"host":"db","port":5432,"username":"u","password":%q,"database":"app"}`, enc)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO connections (id, name, type, config_json, created_at, updated_at, sort_order) VALUES (?, ?, ?, ?, 1, 1, 0)`,
		id, "legacy", typ, rawCfg,
	); err != nil {
		t.Fatal(err)
	}
}

// TestOpenMigratesLegacyKDFCiphertext 覆盖两种遗留形态的自动迁移:旧参数+
// 旧默认主密钥、旧参数+同主密钥。Open 后旧密文被按当前参数重写,新主密钥
// 直接可解。
func TestOpenMigratesLegacyKDFCiphertext(t *testing.T) {
	t.Run("旧默认主密钥加旧参数", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "config.db")
		insertLegacyRow(t, dbPath, "pg1", "postgres", DefaultMasterPassword, legacyKDF, "legacy-password")
		s, err := Open(dbPath, "brand-new-master")
		if err != nil {
			t.Fatalf("Open with new master must succeed: %v", err)
		}
		defer s.Close()
		if _, err := s.GetConnection("pg1"); err != nil {
			t.Fatalf("migrated connection must be readable: %v", err)
		}
		var raw string
		if err := s.db.QueryRow(`SELECT config_json FROM connections WHERE id = ?`, "pg1").Scan(&raw); err != nil {
			t.Fatal(err)
		}
		blobs := encryptedBlobs(raw)
		if len(blobs) != 1 {
			t.Fatalf("expected one encrypted blob, got %d", len(blobs))
		}
		if s.crypto.NeedsReencrypt(blobs[0]) {
			t.Fatal("stored blob must be re-encrypted with current KDF after Open")
		}
	})

	t.Run("同主密钥加旧参数", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "config.db")
		insertLegacyRow(t, dbPath, "pg2", "postgres", "user-master", legacyKDF, "same-master-secret")
		s, err := Open(dbPath, "user-master")
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer s.Close()
		got, err := s.GetConnection("pg2")
		if err != nil {
			t.Fatalf("migrated connection must be readable: %v", err)
		}
		if !strings.Contains(string(got.Config), "same-master-secret") {
			t.Fatalf("password must round-trip after migration: %s", got.Config)
		}
	})
}

// TestOpenKeepsUndecryptableRows 未知主密钥的行原样保留,Open 不报错(错误
// 由正常读取路径暴露),也绝不重写。
func TestOpenKeepsUndecryptableRows(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "config.db")
	insertLegacyRow(t, dbPath, "pg3", "postgres", "forgotten-master", legacyKDF, "x")
	before := rawConfig(t, dbPath, "pg3")
	s, err := Open(dbPath, "current-master")
	if err != nil {
		t.Fatalf("Open must not fail on undecryptable rows: %v", err)
	}
	defer s.Close()
	if after := rawConfig(t, dbPath, "pg3"); before != after {
		t.Fatal("undecryptable row must be left untouched")
	}
	if _, err := s.GetConnection("pg3"); err == nil {
		t.Fatal("reading an undecryptable row must fail")
	}
}

func rawConfig(t *testing.T, dbPath, id string) string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var raw string
	if err := db.QueryRow(`SELECT config_json FROM connections WHERE id = ?`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

// --- 主密钥解析优先级 ---

// TestResolveMasterPasswordPriority 覆盖:环境变量 > 密钥文件 > 首次生成
// (0600 落盘)> 全部不可用回退内置默认并警告(警告不含密钥内容)。
func TestResolveMasterPasswordPriority(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "master.key")

	m, src := ResolveMasterPassword("  env-master  ", keyPath, nil)
	if m != "env-master" || src != MasterKeySourceEnv {
		t.Fatalf("env must win and be trimmed, got %q (%s)", m, src)
	}

	if err := os.WriteFile(keyPath, []byte("file-master\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, src = ResolveMasterPassword("", keyPath, nil)
	if m != "file-master" || src != MasterKeySourceFile {
		t.Fatalf("key file must be next, got %q (%s)", m, src)
	}

	// 文件不存在:生成 32 字节随机 hex 并以 0600 落盘。
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	m, src = ResolveMasterPassword("", keyPath, nil)
	if src != MasterKeySourceGenerated || len(m) != 64 {
		t.Fatalf("expected generated 64-char hex master, got %q (%s)", m, src)
	}
	if _, err := hex.DecodeString(m); err != nil {
		t.Fatalf("generated master must be hex: %v", err)
	}
	data, err := os.ReadFile(keyPath)
	if err != nil || string(data) != m {
		t.Fatalf("generated master must persist verbatim: %q, %v", data, err)
	}
	if fi, err := os.Stat(keyPath); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("master.key must be 0600, got %v, %v", fi, err)
	}

	// 空白文件视同缺失:重新生成覆盖。
	if err := os.WriteFile(keyPath, []byte("   \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, src = ResolveMasterPassword("", keyPath, nil); src != MasterKeySourceGenerated {
		t.Fatalf("blank key file must regenerate, got %s", src)
	}

	// 全部不可用(父路径被普通文件挡住):回退默认并向 warn 警告,且警告
	// 不出现任何密钥内容。
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	m, src = ResolveMasterPassword("", filepath.Join(blocker, "master.key"), &buf)
	if m != DefaultMasterPassword || src != MasterKeySourceDefault {
		t.Fatalf("expected built-in default fallback, got %q (%s)", m, src)
	}
	out := buf.String()
	if !strings.Contains(out, "正在使用内置默认主密钥") {
		t.Fatalf("warning must be emitted, got %q", out)
	}
	if strings.Contains(out, m) {
		t.Fatal("warning must never contain key material")
	}
}

func TestCryptoRoundTrip(t *testing.T) {
	c, err := NewCrypto("master-password-123")
	if err != nil {
		t.Fatalf("NewCrypto failed: %v", err)
	}
	plain := "super-secret-kafka-password"
	enc, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	if enc == plain {
		t.Fatal("ciphertext must differ from plaintext")
	}
	dec, err := c.Decrypt(enc)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if dec != plain {
		t.Fatalf("round trip mismatch: got %q want %q", dec, plain)
	}
}

func TestCryptoRandomizedCiphertext(t *testing.T) {
	c, _ := NewCrypto("master-password")
	enc1, _ := c.Encrypt("same value")
	enc2, _ := c.Encrypt("same value")
	if enc1 == enc2 {
		t.Fatal("two encryptions of the same plaintext must differ (random nonce/salt)")
	}
}

func TestCryptoWrongPasswordFails(t *testing.T) {
	c1, _ := NewCrypto("correct-password")
	enc, _ := c1.Encrypt("secret")
	c2, _ := NewCrypto("wrong-password")
	if _, err := c2.Decrypt(enc); err == nil {
		t.Fatal("decrypt with wrong password must fail")
	}
}

func TestCryptoTamperedCiphertextFails(t *testing.T) {
	c, _ := NewCrypto("password")
	enc, _ := c.Encrypt("secret")
	// Flip a byte in the payload portion.
	idx := strings.LastIndexByte(enc, ':') + 1
	chars := []byte(enc)
	chars[idx] = byte('A' + (chars[idx]-byte('A')+1)%26)
	tampered := string(chars)
	if _, err := c.Decrypt(tampered); err == nil {
		t.Fatal("tampered ciphertext must fail authentication")
	}
}

func TestCryptoRejectsMalformedBlob(t *testing.T) {
	c, _ := NewCrypto("password")
	if _, err := c.Decrypt("not-a-valid-blob"); err == nil {
		t.Fatal("malformed blob must fail")
	}
}

func TestNewCryptoEmptyPassword(t *testing.T) {
	if _, err := NewCrypto(""); err == nil {
		t.Fatal("empty master password must be rejected")
	}
}
