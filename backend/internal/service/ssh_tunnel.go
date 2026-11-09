package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"sheng-shou-yun-he/backend/internal/model"
)

// sshDialTimeout bounds the TCP dial + SSH handshake + auth of a new tunnel,
// and the per-connection channel open toward the target. The caller's context
// (connection build) may already carry a deadline; this default only applies
// when it does not.
const sshDialTimeout = 10 * time.Second

// sshTunnel 是一条到跳板机的 SSH 连接 + 本地端口转发器:本地 127.0.0.1 随机
// 端口监听,每个进入连接开一条 direct-tcpip channel 透传到目标 host:port。
// 生命周期随宿主 DB 客户端(RedisClient/MysqlClient):Close 先关本地
// listener(停止收新连接),等在途转发排空,再断 SSH 连接。
type sshTunnel struct {
	listener net.Listener
	client   *ssh.Client
	// target 是转发目标 host:port(direct-tcpip 的目标地址)。
	target string
	// wg 跟踪在途转发 goroutine,Close 时等它们退出后再断 SSH。
	wg sync.WaitGroup
	// closeOnce 保证并发 Close(池淘汰 + 手动断开)只执行一次清理。
	closeOnce sync.Once
	closeErr  error
}

// localAddr returns the local forwarded address ("127.0.0.1:<port>") that DB
// drivers should dial instead of the real host:port.
func (t *sshTunnel) localAddr() string {
	return t.listener.Addr().String()
}

// startSSHTunnel dials the jump host per cfg (password or private-key auth),
// starts listening on a random local port and forwards every accepted
// connection to targetHost:targetPort over direct-tcpip channels. ctx bounds
// the dial/handshake phase only; the tunnel itself outlives it (it serves the
// pooled client until Close).
func startSSHTunnel(ctx context.Context, cfg *model.SSHTunnelConfig, targetHost string, targetPort int) (*sshTunnel, error) {
	if cfg == nil || !cfg.Enabled {
		return nil, errors.New("ssh tunnel is not enabled")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// 调用方 ctx 无 deadline 时补默认超时:SSH 握手挂死不能拖垮整条建连链。
	dialCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, sshDialTimeout)
		defer cancel()
	}

	auth, err := sshAuthMethods(cfg)
	if err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	// 桌面工具跳板机指纹校验留待后续:固定指纹/known_hosts 录入 UI 尚无载体,
	// 先跳过主机指纹校验以兼容首次连接(与 DBX ssh-tunnel 对齐)。
	clientConfig := &ssh.ClientConfig{
		User:            cfg.Username,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	dialer := net.Dialer{}
	conn, err := dialer.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	// 握手/认证阶段也受 deadline 约束(NewClientConn 本身不带 ctx)。
	if deadline, ok := dialCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, clientConfig)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ssh handshake %s: %w", addr, err)
	}
	// 握手完成,恢复无期限:隧道长期存活,转发不受建连超时影响。
	_ = conn.SetDeadline(time.Time{})
	client := ssh.NewClient(sshConn, chans, reqs)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ssh tunnel listen: %w", err)
	}
	t := &sshTunnel{
		listener: listener,
		client:   client,
		target:   net.JoinHostPort(targetHost, strconv.Itoa(targetPort)),
	}
	go t.acceptLoop()
	return t, nil
}

// sshAuthMethods 按认证方式构造认证列表:password 用口令;private_key 解析
// PEM(带口令私钥在无口令解析报 PassphraseMissingError 后用口令重试)。
func sshAuthMethods(cfg *model.SSHTunnelConfig) ([]ssh.AuthMethod, error) {
	switch cfg.AuthType {
	case model.SSHAuthPrivateKey:
		pemBytes := []byte(cfg.PrivateKey)
		signer, err := ssh.ParsePrivateKey(pemBytes)
		if err != nil {
			var missing *ssh.PassphraseMissingError
			if errors.As(err, &missing) && cfg.Passphrase != "" {
				signer, err = ssh.ParsePrivateKeyWithPassphrase(pemBytes, []byte(cfg.Passphrase))
			}
			if err != nil {
				return nil, fmt.Errorf("parse ssh private key: %w", err)
			}
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	default:
		// password(含归一后的空 AuthType)。
		return []ssh.AuthMethod{ssh.Password(cfg.Password)}, nil
	}
}

// acceptLoop accepts local connections until the listener closes (via Close);
// accept errors after shutdown are the expected exit path.
func (t *sshTunnel) acceptLoop() {
	for {
		local, err := t.listener.Accept()
		if err != nil {
			return
		}
		t.wg.Add(1)
		go t.handleConn(local)
	}
}

// handleConn opens a direct-tcpip channel to the target for one local
// connection and pumps bytes both ways until either side closes.
func (t *sshTunnel) handleConn(local net.Conn) {
	defer t.wg.Done()
	dialCtx, cancel := context.WithTimeout(context.Background(), sshDialTimeout)
	defer cancel()
	remote, err := t.client.DialContext(dialCtx, "tcp", t.target)
	if err != nil {
		_ = local.Close()
		return
	}
	defer remote.Close()

	// 单向结束即整体收尾:关掉对端连接,另一方向的 Copy 随即报错退出。
	// (ssh channel 经 net.Conn 接口暴露,无半关闭语义,整连关闭即可。)
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(remote, local)
		_ = remote.Close()
		close(done)
	}()
	_, _ = io.Copy(local, remote)
	_ = local.Close()
	<-done
}

// Close shuts the tunnel down: stop accepting, drain in-flight forwards, then
// drop the SSH connection. 并发安全(closeOnce);重复 Close 返回首次结果。
func (t *sshTunnel) Close() error {
	t.closeOnce.Do(func() {
		lnErr := t.listener.Close()
		t.wg.Wait()
		clErr := t.client.Close()
		if lnErr != nil {
			t.closeErr = lnErr
		} else {
			t.closeErr = clErr
		}
	})
	return t.closeErr
}
