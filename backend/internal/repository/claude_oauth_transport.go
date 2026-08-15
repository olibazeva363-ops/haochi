package repository

import (
	"context"
	"crypto/tls"
	"net"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"

	"github.com/imroc/req/v3"
	utls "github.com/refraction-networking/utls"
)

func createReqClient(proxyURL string) (*req.Client, error) {
	// 禁用 CookieJar，确保每次授权都是干净的会话
	client := req.C().
		SetTimeout(60 * time.Second).
		SetCookieJar(nil) // 禁用 CookieJar

	transport := client.GetTransport()
	// OAuth/token 请求与对话流量同账号同身份：header 层已是 Node 系
	// （claude-cli / axios），TLS 层用 Claude Code (Node.js 24.x) 指纹保持
	// 一致。此前此处用 Chrome 指纹，造成同账号在 token 刷新与对话两条
	// 路径上呈现两种 TLS 家族，本身就是可检测的不一致信号。
	// SetTLSHandshake 在代理路径上同样生效（CONNECT 隧道建立后回调）。
	transport.SetTLSHandshake(func(ctx context.Context, addr string, plainConn net.Conn) (net.Conn, *tls.ConnectionState, error) {
		conn, err := tlsfingerprint.Handshake(ctx, plainConn, nil, addr)
		if err != nil {
			return nil, nil, err
		}
		if tlsConn, ok := conn.(*utls.UConn); ok {
			state := convertUTLSConnectionState(tlsConn.ConnectionState())
			return conn, &state, nil
		}
		return conn, nil, nil
	})
	// 真实 CLI 是 Node undici，协商 HTTP/1.1
	transport.EnableForceHTTP1()

	trimmed, _, err := proxyurl.Parse(proxyURL)
	if err != nil {
		return nil, err
	}
	if trimmed != "" {
		client.SetProxyURL(trimmed)
	}

	return instrumentReqClient(client), nil
}

// convertUTLSConnectionState 把 uTLS 的 ConnectionState 字段级复制为标准库
// 类型（两者字段同名同型但不是别名），供 req.Transport 的握手回调返回。
func convertUTLSConnectionState(state utls.ConnectionState) tls.ConnectionState {
	return tls.ConnectionState{
		Version:                     state.Version,
		HandshakeComplete:           state.HandshakeComplete,
		DidResume:                   state.DidResume,
		CipherSuite:                 state.CipherSuite,
		NegotiatedProtocol:          state.NegotiatedProtocol,
		ServerName:                  state.ServerName,
		PeerCertificates:            state.PeerCertificates,
		VerifiedChains:              state.VerifiedChains,
		SignedCertificateTimestamps: state.SignedCertificateTimestamps,
		OCSPResponse:                state.OCSPResponse,
		TLSUnique:                   state.TLSUnique,
	}
}
