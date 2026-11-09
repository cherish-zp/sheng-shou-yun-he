package model

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// SSH 隧道认证方式与默认端口。AuthType 为空串归一为 password(最常见形态),
// 仅在 Enabled 时做校验与归一——关闭的隧道配置是惰性数据,不拦半填状态。
const (
	SSHAuthPassword   = "password"
	SSHAuthPrivateKey = "private_key"
	SSHDefaultPort    = 22
)

// SSHTunnelConfig 描述经 SSH 跳板机的本地端口转发:应用连到跳板机(Host:
// Port),在本地 127.0.0.1 随机端口监听,把连接转发到数据库真实 host:port;
// DB 驱动只需连本地监听地址。转发目标就是连接自身的 host/addr 字段,因此
// 这里不含 TargetHost/TargetPort。Password/PrivateKey/Passphrase 为敏感
// 凭据,store 层加密落盘(与 config.password 同一机制),App 层回传脱敏。
type SSHTunnelConfig struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	// AuthType 为 password 或 private_key(空串归一为 password)。
	AuthType string `json:"auth_type,omitempty"`
	// Password 为 password 认证的口令(加密字段)。
	Password string `json:"password,omitempty"`
	// PrivateKey 为 PEM 编码私钥全文(private_key 认证,加密字段)。
	PrivateKey string `json:"private_key,omitempty"`
	// Passphrase 为私钥口令(无口令私钥留空,加密字段)。
	Passphrase string `json:"passphrase,omitempty"`
}

// Validate checks the SSH tunnel configuration and normalizes it in place:
// the host and username are required (trimmed), a zero port defaults to 22
// (any other value must be in 1..65535) and the auth type must be one of
// password/private_key ("" normalizes to password). Disabled configs are
// inert:半填状态的关闭配置直接放行,不影响保存。
func (c *SSHTunnelConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	h := strings.TrimSpace(c.Host)
	if h == "" {
		return errors.New("SSH 跳板机 host 不能为空")
	}
	c.Host = h
	if c.Port == 0 {
		c.Port = SSHDefaultPort
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("SSH 跳板机端口超出范围(1..65535): %s", strconv.Itoa(c.Port))
	}
	if strings.TrimSpace(c.Username) == "" {
		return errors.New("SSH 跳板机用户名不能为空")
	}
	c.Username = strings.TrimSpace(c.Username)
	switch a := strings.TrimSpace(c.AuthType); a {
	case "":
		c.AuthType = SSHAuthPassword
	case SSHAuthPassword, SSHAuthPrivateKey:
		c.AuthType = a
	default:
		return fmt.Errorf("不支持的 SSH 认证方式 %q(仅支持 password/private_key)", c.AuthType)
	}
	return nil
}
