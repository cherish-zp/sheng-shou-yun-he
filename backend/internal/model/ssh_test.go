package model

import (
	"strings"
	"testing"
)

func TestSSHTunnelConfigValidate_DisabledIsInert(t *testing.T) {
	// 关闭的隧道是惰性配置:半填状态直接放行,不拦截保存。
	c := &SSHTunnelConfig{Enabled: false}
	if err := c.Validate(); err != nil {
		t.Fatalf("disabled config must pass validation: %v", err)
	}
}

func TestSSHTunnelConfigValidate_EnabledRequiresHostAndUser(t *testing.T) {
	if err := (&SSHTunnelConfig{Enabled: true, Username: "u", AuthType: SSHAuthPassword}).Validate(); err == nil {
		t.Fatal("enabled config without host must be rejected")
	}
	c := &SSHTunnelConfig{Enabled: true, Host: "  jump.example.com ", Port: 0, Username: " u ", AuthType: ""}
	if err := c.Validate(); err != nil {
		t.Fatalf("valid config must pass: %v", err)
	}
	if c.Host != "jump.example.com" || c.Username != "u" {
		t.Fatalf("host/username must be trimmed, got %q/%q", c.Host, c.Username)
	}
	if c.Port != SSHDefaultPort {
		t.Fatalf("port 0 must default to %d, got %d", SSHDefaultPort, c.Port)
	}
	if c.AuthType != SSHAuthPassword {
		t.Fatalf("empty auth_type must normalize to password, got %q", c.AuthType)
	}

	if err := (&SSHTunnelConfig{Enabled: true, Host: "jump", Username: ""}).Validate(); err == nil {
		t.Fatal("enabled config without username must be rejected")
	}
}

func TestSSHTunnelConfigValidate_PortRange(t *testing.T) {
	if err := (&SSHTunnelConfig{Enabled: true, Host: "j", Username: "u", Port: 70000}).Validate(); err == nil {
		t.Fatal("port above 65535 must be rejected")
	}
	if err := (&SSHTunnelConfig{Enabled: true, Host: "j", Username: "u", Port: -1}).Validate(); err == nil {
		t.Fatal("negative port must be rejected")
	}
}

func TestSSHTunnelConfigValidate_AuthTypeEnum(t *testing.T) {
	if err := (&SSHTunnelConfig{Enabled: true, Host: "j", Username: "u", AuthType: "kerberos"}).Validate(); err == nil {
		t.Fatal("unsupported auth_type must be rejected")
	}
	c := &SSHTunnelConfig{Enabled: true, Host: "j", Username: "u", AuthType: " private_key "}
	if err := c.Validate(); err != nil {
		t.Fatalf("private_key auth must be accepted: %v", err)
	}
	if c.AuthType != SSHAuthPrivateKey {
		t.Fatalf("auth_type must be trimmed to private_key, got %q", c.AuthType)
	}
}

// 接入宿主配置:mysql/tidb 与 redis 的 Validate 必须传导 SSH 校验。
func TestHostConfigsValidateSSH(t *testing.T) {
	badSSH := &SSHTunnelConfig{Enabled: true} // 缺 host/username
	m := MysqlConfig{Host: "db", Port: 3306, Username: "root", SSH: badSSH}
	if err := m.Validate(); err == nil {
		t.Fatal("mysql config must reject invalid ssh tunnel config")
	}
	r := RedisConfig{Addr: "127.0.0.1:6379", SSH: badSSH}
	if err := r.Validate(); err == nil {
		t.Fatal("redis config must reject invalid ssh tunnel config")
	}
	// 关闭的 SSH 不拦宿主配置。
	okSSH := &SSHTunnelConfig{Enabled: false}
	m.SSH, r.SSH = okSSH, okSSH
	if err := m.Validate(); err != nil {
		t.Fatalf("disabled ssh must not block mysql config: %v", err)
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("disabled ssh must not block redis config: %v", err)
	}
	// JSON 形状:ssh 为可选键,snake_case(用启用态配置保证字段齐备)。
	enabled := MysqlConfig{Host: "db", Port: 3306, Username: "root",
		SSH: &SSHTunnelConfig{Enabled: true, Host: "j", Username: "u", AuthType: SSHAuthPrivateKey, PrivateKey: "k"}}
	if err := enabled.Validate(); err != nil {
		t.Fatalf("enabled ssh must validate: %v", err)
	}
	raw := string(MustConfigJSON(enabled))
	if !strings.Contains(raw, `"ssh"`) || !strings.Contains(raw, `"auth_type":"private_key"`) {
		t.Fatalf("mysql config JSON must carry ssh/auth_type keys, got %s", raw)
	}
}
