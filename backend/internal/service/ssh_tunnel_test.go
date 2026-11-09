package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/alicebob/miniredis/v2"

	"sheng-shou-yun-he/backend/internal/model"
)

// --- 进程内最小 SSH 服务端:仅支持 direct-tcpip 转发,认证按测试需求配置 ---

// testSSHServer 模拟跳板机:ed25519 host key + password/公钥两种认证 +
// direct-tcpip channel 向目标 TCP 透传。真实数据面,不 mock 协议行为。
type testSSHServer struct {
	ln     net.Listener
	config *ssh.ServerConfig
}

// newTestSSHServer 启动服务端并返回监听地址。password 非空启用密码认证
// (用户名固定 jumper);pubKey 非空启用公钥认证。targetAddr 为 direct-tcpip
// 的合法转发目标。
func newTestSSHServer(t *testing.T, password string, pubKey ssh.PublicKey, targetAddr string) *testSSHServer {
	t.Helper()
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatalf("host signer: %v", err)
	}
	config := &ssh.ServerConfig{}
	config.AddHostKey(hostSigner)
	if password != "" {
		config.PasswordCallback = func(conn ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if conn.User() == "jumper" && string(pass) == password {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("password auth rejected for %q", conn.User())
		}
	}
	if pubKey != nil {
		config.PublicKeyCallback = func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if strings.EqualFold(string(key.Type()), string(pubKey.Type())) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("publickey auth rejected for %q", conn.User())
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ssh server listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	s := &testSSHServer{ln: ln, config: config}
	go s.acceptLoop(targetAddr)
	return s
}

func (s *testSSHServer) addr() string { return s.ln.Addr().String() }

func (s *testSSHServer) acceptLoop(targetAddr string) {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		sconn, chans, reqs, err := ssh.NewServerConn(conn, s.config)
		if err != nil {
			continue // 认证失败/客户端主动断开,等下一个连接
		}
		go ssh.DiscardRequests(reqs)
		go s.handleChannels(sconn, chans, targetAddr)
	}
}

// parseDirectTCPIP 解析 direct-tcpip 请求载荷:
// string host, uint32 port, string originator ip, uint32 originator port。
func parseDirectTCPIP(payload []byte) (string, uint32, error) {
	if len(payload) < 4 {
		return "", 0, fmt.Errorf("payload too short")
	}
	hostLen := binary.BigEndian.Uint32(payload[:4])
	if uint32(len(payload)) < 4+hostLen+8 {
		return "", 0, fmt.Errorf("payload truncated")
	}
	host := string(payload[4 : 4+hostLen])
	port := binary.BigEndian.Uint32(payload[4+hostLen : 8+hostLen])
	return host, port, nil
}

func (s *testSSHServer) handleChannels(conn *ssh.ServerConn, chans <-chan ssh.NewChannel, defaultTarget string) {
	defer conn.Close()
	for newChannel := range chans {
		if newChannel.ChannelType() != "direct-tcpip" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "unsupported channel type")
			continue
		}
		host, port, err := parseDirectTCPIP(newChannel.ExtraData())
		if err != nil {
			_ = newChannel.Reject(ssh.ConnectionFailed, "bad payload")
			continue
		}
		// 目标必须落在服务端声明的转发目标上(校验隧道请求的目标语义)。
		requested := net.JoinHostPort(host, fmt.Sprintf("%d", port))
		target := defaultTarget
		if target == "" {
			target = requested
		}
		if requested != target {
			_ = newChannel.Reject(ssh.Prohibited, "target not allowed")
			continue
		}
		channel, reqs, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go ssh.DiscardRequests(reqs)
		go s.pipe(channel, target)
	}
}

func (s *testSSHServer) pipe(channel ssh.Channel, target string) {
	remote, err := net.Dial("tcp", target)
	if err != nil {
		_ = channel.Close()
		return
	}
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(remote, channel)
		_ = remote.Close()
		close(done)
	}()
	_, _ = io.Copy(channel, remote)
	_ = channel.Close()
	<-done
}

// startEchoTarget 起一个本地 TCP「数据库端口」:收到什么就回什么,供透传
// 断言。
func startEchoTarget(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo target listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, _ = io.Copy(conn, conn)
				_ = conn.Close()
			}()
		}
	}()
	return ln.Addr().String()
}

// sshConfigFor 构造指向测试跳板机的隧道配置。
func sshConfigFor(sshAddr, authType, password, privateKey, passphrase string) *model.SSHTunnelConfig {
	host, port, err := splitSeedHostPort(sshAddr)
	if err != nil {
		panic(err)
	}
	return &model.SSHTunnelConfig{
		Enabled: true, Host: host, Port: port, Username: "jumper",
		AuthType: authType, Password: password,
		PrivateKey: privateKey, Passphrase: passphrase,
	}
}

// --- 隧道集成测试 ---

func TestStartSSHTunnel_PasswordAuthForwardsData(t *testing.T) {
	target := startEchoTarget(t)
	srv := newTestSSHServer(t, "jump-pass", nil, target)

	tn, err := startSSHTunnel(context.Background(), sshConfigFor(srv.addr(), model.SSHAuthPassword, "jump-pass", "", ""), targetHostOf(target), targetPortOf(target))
	if err != nil {
		t.Fatalf("startSSHTunnel: %v", err)
	}
	defer tn.Close()

	// 本地监听地址必须是回环随机端口。
	if got := tn.localAddr(); !strings.HasPrefix(got, "127.0.0.1:") {
		t.Fatalf("local addr must be 127.0.0.1:<port>, got %s", got)
	}

	// 经隧道拨号:连接 → 写入 → 原样读回(透传成立)。
	conn, err := net.Dial("tcp", tn.localAddr())
	if err != nil {
		t.Fatalf("dial through tunnel: %v", err)
	}
	defer conn.Close()
	payload := "SELECT 1; -- over ssh tunnel"
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(conn, payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != payload {
		t.Fatalf("payload must round-trip verbatim, got %q", string(buf))
	}
}

func TestStartSSHTunnel_WrongPasswordRejected(t *testing.T) {
	target := startEchoTarget(t)
	srv := newTestSSHServer(t, "jump-pass", nil, target)

	_, err := startSSHTunnel(context.Background(), sshConfigFor(srv.addr(), model.SSHAuthPassword, "wrong", "", ""), targetHostOf(target), targetPortOf(target))
	if err == nil {
		t.Fatal("wrong password must fail the tunnel")
	}
	if !strings.Contains(err.Error(), "handshake") {
		t.Fatalf("error must come from the ssh handshake/auth phase, got %v", err)
	}
}

func TestStartSSHTunnel_PrivateKeyAuth(t *testing.T) {
	target := startEchoTarget(t)
	// 客户端 ed25519 密钥(带口令),服务端公钥认证。
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "test@shengshouyunhe", []byte("key-pass"))
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	pemKey := string(pem.EncodeToMemory(block))
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("new public key: %v", err)
	}
	srv := newTestSSHServer(t, "", sshPub, target)

	// 带口令私钥:认证成功且透传成立。
	tn, err := startSSHTunnel(context.Background(), sshConfigFor(srv.addr(), model.SSHAuthPrivateKey, "", pemKey, "key-pass"), targetHostOf(target), targetPortOf(target))
	if err != nil {
		t.Fatalf("private key auth must succeed: %v", err)
	}
	defer tn.Close()
	conn, err := net.Dial("tcp", tn.localAddr())
	if err != nil {
		t.Fatalf("dial through tunnel: %v", err)
	}
	_, _ = io.WriteString(conn, "ping")
	buf := make([]byte, 4)
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	_ = conn.Close()

	// 口令错误:解析必须失败。
	if _, err := startSSHTunnel(context.Background(), sshConfigFor(srv.addr(), model.SSHAuthPrivateKey, "", pemKey, "bad-pass"), targetHostOf(target), targetPortOf(target)); err == nil {
		t.Fatal("wrong passphrase must fail key parsing/auth")
	}
}

func TestStartSSHTunnel_DisabledConfigRejected(t *testing.T) {
	if _, err := startSSHTunnel(context.Background(), &model.SSHTunnelConfig{Enabled: false}, "127.0.0.1", 1); err == nil {
		t.Fatal("disabled config must be rejected by the tunnel constructor")
	}
}

func TestSSHTunnel_CloseStopsForwarding(t *testing.T) {
	target := startEchoTarget(t)
	srv := newTestSSHServer(t, "jump-pass", nil, target)

	tn, err := startSSHTunnel(context.Background(), sshConfigFor(srv.addr(), model.SSHAuthPassword, "jump-pass", "", ""), targetHostOf(target), targetPortOf(target))
	if err != nil {
		t.Fatalf("startSSHTunnel: %v", err)
	}
	local := tn.localAddr()
	// Close 前可连通。
	conn, err := net.Dial("tcp", local)
	if err != nil {
		t.Fatalf("dial before close: %v", err)
	}
	_ = conn.Close()
	if err := tn.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Close 后 listener 必须不再接受连接;并发/重复 Close 安全。
	if err := tn.Close(); err != nil {
		t.Fatalf("repeated close must stay idempotent, got %v", err)
	}
	if c, err := net.Dial("tcp", local); err == nil {
		_ = c.Close()
		t.Fatal("dial after close must fail")
	}
}

// --- 客户端构造接入(端到端):miniredis 作真实目标,经测试跳板机建连 ---

func TestNewRedisClient_ThroughSSHTunnel(t *testing.T) {
	// miniredis 模拟隧道另一端的 Redis。
	mr := miniredis.RunT(t)
	srv := newTestSSHServer(t, "jump-pass", nil, mr.Addr())

	cfg := model.RedisConfig{
		Addr: mr.Addr(),
		SSH:  sshConfigFor(srv.addr(), model.SSHAuthPassword, "jump-pass", "", ""),
	}
	client, err := NewRedisClient(cfg)
	if err != nil {
		t.Fatalf("NewRedisClient through ssh tunnel: %v", err)
	}
	ctx := context.Background()
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("ping through tunnel: %v", err)
	}

	// 经隧道写入真实 Redis,再直接从 miniredis 侧验证数据落盘。
	local := client.tunnel.localAddr()
	if !strings.HasPrefix(local, "127.0.0.1:") {
		t.Fatalf("client must dial the tunnel local addr, got %s", local)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Close 链断言:客户端关闭必须连带关掉隧道本地 listener。
	if c, err := net.Dial("tcp", local); err == nil {
		_ = c.Close()
		t.Fatal("tunnel listener must be closed together with the client")
	}
}

func TestNewRedisClient_SSHAuthFailureSurfaces(t *testing.T) {
	mr := miniredis.RunT(t)
	srv := newTestSSHServer(t, "jump-pass", nil, mr.Addr())
	cfg := model.RedisConfig{
		Addr: mr.Addr(),
		SSH:  sshConfigFor(srv.addr(), model.SSHAuthPassword, "nope", "", ""),
	}
	if _, err := NewRedisClient(cfg); err == nil {
		t.Fatal("redis client build must fail when the jump host rejects auth")
	} else if !strings.Contains(err.Error(), "ssh tunnel") {
		t.Fatalf("error must carry the ssh tunnel prefix, got %v", err)
	}
}

// --- DSN 的 TLS ServerName 覆盖(SSH 隧道下 verify-full 用真实主机名) ---

func TestBuildMysqlDSN_TLSServerNameOverride(t *testing.T) {
	dsn, err := buildMysqlDSN(model.MysqlConfig{
		Host: "127.0.0.1", Port: 3307, Username: "root",
		TLSMode: model.MysqlTLSVerifyFull,
	}, "db.real.internal")
	if err != nil {
		t.Fatalf("buildMysqlDSN: %v", err)
	}
	want := "tls=" + mysqlTLSConfigName("db.real.internal")
	if !strings.Contains(dsn, want) {
		t.Fatalf("verify-full over tunnel must register the real host name %q, got %s", want, dsn)
	}
	// 空覆盖回退 cfg.Host,既有行为不变。
	dsn2, err := buildMysqlDSN(model.MysqlConfig{Host: "db", TLSMode: model.MysqlTLSVerifyFull}, "")
	if err != nil {
		t.Fatalf("buildMysqlDSN: %v", err)
	}
	if !strings.Contains(dsn2, "tls="+mysqlTLSConfigName("db")) {
		t.Fatalf("empty override must fall back to cfg.Host, got %s", dsn2)
	}
}

func targetHostOf(addr string) string {
	host, _, _ := net.SplitHostPort(addr)
	return host
}

func targetPortOf(addr string) int {
	_, port, _ := net.SplitHostPort(addr)
	return atoiOrPanic(port)
}

func atoiOrPanic(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			panic("bad port " + s)
		}
		n = n*10 + int(r-'0')
	}
	return n
}
