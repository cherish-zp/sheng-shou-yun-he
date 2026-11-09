package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"sheng-shou-yun-he/backend/internal/model"
)

// rawPathForTest 返回临时库路径(供 Open 双实例迁移用例)。
func rawPathForTest(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "config.db")
}

// insertLegacySSHRow 直插一行带 SSH 隧道配置的连接,ssh.password 为给定密文
// (模拟旧版本写出的形态)。
func insertLegacySSHRow(t *testing.T, dbPath, id, typ, encPassword string) {
	t.Helper()
	rawCfg := fmt.Sprintf(
		`{"host":"db.internal","port":3306,"username":"root","password":"","ssh":{"enabled":true,"host":"jump.internal","port":22,"username":"jumper","auth_type":"password","password":%q}}`,
		encPassword,
	)
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
		id, "legacy-ssh", typ, rawCfg,
	); err != nil {
		t.Fatal(err)
	}
}

// sshTestConnection 构造带 SSH 隧道凭据的连接配置(mysql 与 redis 各一)。
func mysqlConnWithSSH(id string) *model.Connection {
	return &model.Connection{
		ID:   id,
		Name: "mysql-jump",
		Type: model.ConnectionTypeMySQL,
		Config: model.MustConfigJSON(model.MysqlConfig{
			Host: "db.internal", Port: 3306, Username: "root", Password: "db-pass",
			SSH: &model.SSHTunnelConfig{
				Enabled: true, Host: "jump.internal", Port: 22, Username: "jumper",
				AuthType:   model.SSHAuthPrivateKey,
				Password:   "ssh-password-secret",
				PrivateKey: "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----",
				Passphrase: "key-passphrase-secret",
			},
		}),
	}
}

func redisConnWithSSH(id string) *model.Connection {
	return &model.Connection{
		ID:   id,
		Name: "redis-jump",
		Type: model.ConnectionTypeRedis,
		Config: model.MustConfigJSON(model.RedisConfig{
			Addr: "127.0.0.1:6379", Password: "redis-pass",
			SSH: &model.SSHTunnelConfig{
				Enabled: true, Host: "jump.internal", Port: 2222, Username: "jumper",
				AuthType: model.SSHAuthPassword,
				Password: "ssh-password-secret",
			},
		}),
	}
}

// assertSSHFieldsEncrypted 断言落盘 JSON 中 ssh 三个敏感字段均带 enc: 前缀
// 且不含任何明文。
func assertSSHFieldsEncrypted(t *testing.T, raw string) {
	t.Helper()
	if strings.Contains(raw, "ssh-password-secret") ||
		strings.Contains(raw, "key-passphrase-secret") ||
		strings.Contains(raw, "OPENSSH PRIVATE KEY") {
		t.Fatalf("stored config_json must not contain plaintext ssh credentials: %s", raw)
	}
	var root struct {
		SSH struct {
			Password   string `json:"password"`
			PrivateKey string `json:"private_key"`
			Passphrase string `json:"passphrase"`
		} `json:"ssh"`
	}
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		t.Fatalf("decode stored config: %v", err)
	}
	prefix := encPrefix + cryptoVersion + ":"
	for name, v := range map[string]string{
		"password": root.SSH.Password, "private_key": root.SSH.PrivateKey, "passphrase": root.SSH.Passphrase,
	} {
		// 空字段(未使用该凭据)合法缺省;非空字段必须已是密文。
		if v == "" {
			continue
		}
		if !strings.HasPrefix(v, prefix) {
			t.Fatalf("stored ssh %s must be encrypted (prefix %q), got %.24s...", name, prefix, v)
		}
	}
}

// TestSSHConfigEncryptedAtRest mysql/tidb 与 redis 的 SSH 凭据落盘必须加密,
// 读取路径透明解密(与顶层 password 同一机制)。
func TestSSHConfigEncryptedAtRest(t *testing.T) {
	t.Run("mysql", func(t *testing.T) {
		s := newTestStore(t)
		if err := s.CreateConnection(mysqlConnWithSSH("my1")); err != nil {
			t.Fatalf("create: %v", err)
		}
		var raw string
		if err := s.db.QueryRow(`SELECT config_json FROM connections WHERE id = ?`, "my1").Scan(&raw); err != nil {
			t.Fatal(err)
		}
		assertSSHFieldsEncrypted(t, raw)

		got, err := s.GetConnection("my1")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		var cfg model.MysqlConfig
		if err := json.Unmarshal(got.Config, &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg.SSH == nil || cfg.SSH.Password != "ssh-password-secret" ||
			cfg.SSH.PrivateKey != "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----" ||
			cfg.SSH.Passphrase != "key-passphrase-secret" {
			t.Fatalf("ssh credentials must decrypt transparently, got %+v", cfg.SSH)
		}
	})

	t.Run("redis", func(t *testing.T) {
		s := newTestStore(t)
		if err := s.CreateConnection(redisConnWithSSH("rd1")); err != nil {
			t.Fatalf("create: %v", err)
		}
		var raw string
		if err := s.db.QueryRow(`SELECT config_json FROM connections WHERE id = ?`, "rd1").Scan(&raw); err != nil {
			t.Fatal(err)
		}
		assertSSHFieldsEncrypted(t, raw)

		got, err := s.GetConnection("rd1")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		var cfg model.RedisConfig
		if err := json.Unmarshal(got.Config, &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg.SSH == nil || cfg.SSH.Password != "ssh-password-secret" {
			t.Fatalf("redis ssh password must decrypt transparently, got %+v", cfg.SSH)
		}
	})
}

// TestSSHConfigUpdateReencrypts 编辑连接更换 SSH 凭据:新值按当前参数重新
// 加密,读回为新明文。
func TestSSHConfigUpdateReencrypts(t *testing.T) {
	s := newTestStore(t)
	conn := mysqlConnWithSSH("my2")
	if err := s.CreateConnection(conn); err != nil {
		t.Fatalf("create: %v", err)
	}
	conn.Config = model.MustConfigJSON(model.MysqlConfig{
		Host: "db.internal", Port: 3306, Username: "root",
		SSH: &model.SSHTunnelConfig{
			Enabled: true, Host: "jump.internal", Port: 22, Username: "jumper",
			AuthType: model.SSHAuthPassword, Password: "rotated-ssh-pass",
		},
	})
	if err := s.UpdateConnection(conn); err != nil {
		t.Fatalf("update: %v", err)
	}
	var raw string
	if err := s.db.QueryRow(`SELECT config_json FROM connections WHERE id = ?`, "my2").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "rotated-ssh-pass") || strings.Contains(raw, "ssh-password-secret") {
		t.Fatalf("rotated ssh credentials must not appear in plaintext: %s", raw)
	}
	got, err := s.GetConnection("my2")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(string(got.Config), "rotated-ssh-pass") {
		t.Fatalf("rotated ssh password must round-trip: %s", got.Config)
	}
}

// TestKDFMigrationCoversNestedSSH EnsureCurrentKDF 的扫描对 ssh 子对象内的
// 遗留密文同样生效(encryptedBlobs 递归收集),Open 后重写为当前参数。
func TestKDFMigrationCoversNestedSSH(t *testing.T) {
	c, err := NewCrypto("user-master")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := c.sealWith("user-master", legacyKDF, "legacy-ssh-pass")
	if err != nil {
		t.Fatal(err)
	}
	dbPath := rawPathForTest(t)
	insertLegacySSHRow(t, dbPath, "my3", "mysql", legacy)

	s, err := Open(dbPath, "user-master")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	var raw string
	if err := s.db.QueryRow(`SELECT config_json FROM connections WHERE id = ?`, "my3").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	for _, blob := range encryptedBlobs(raw) {
		if s.crypto.NeedsReencrypt(blob) {
			t.Fatalf("nested ssh credential must be migrated to current KDF: %.32s", blob)
		}
	}
	got, err := s.GetConnection("my3")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(string(got.Config), "legacy-ssh-pass") {
		t.Fatalf("legacy ssh credential must decrypt after migration: %s", got.Config)
	}
}
