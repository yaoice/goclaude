package wsclient

import (
	"crypto/tls"
	"net"
)

// tlsDial 用同一个 dialer 建立 TCP，然后做 TLS 握手
//
// 抽出独立文件方便测试时按需 stub。
func tlsDial(dialer *net.Dialer, network, host string) (net.Conn, error) {
	rawConn, err := dialer.Dial(network, host)
	if err != nil {
		return nil, err
	}
	// 从 host 中提取 SNI（去掉端口）
	sni := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		sni = h
	}
	tlsConn := tls.Client(rawConn, &tls.Config{ServerName: sni})
	if err := tlsConn.Handshake(); err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	return tlsConn, nil
}
