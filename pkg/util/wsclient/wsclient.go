// Package wsclient 极简 RFC6455 WebSocket 客户端
//
// 仅覆盖 MCP WebSocket transport 实际用到的特性：
//   - 文本消息发送/接收（newline-delimited 不适用，按整帧切分）
//   - 客户端→服务端必须 masking
//   - 自动响应 ping → pong
//   - close 帧的发送与解析
//
// 不支持：消息分片（fragmentation）、压缩扩展、subprotocol 协商之外的握手细节。
// 这些都不是 MCP WebSocket 传输的常见需求。
package wsclient

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Conn 已升级的 WebSocket 连接
type Conn struct {
	netConn net.Conn
	reader  *bufio.Reader

	writeMu sync.Mutex
}

// Opcode RFC6455 操作码
type opcode byte

const (
	opContinuation opcode = 0x0
	opText         opcode = 0x1
	opBinary       opcode = 0x2
	opClose        opcode = 0x8
	opPing         opcode = 0x9
	opPong         opcode = 0xA
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Dial 建立 WebSocket 连接
//
// rawURL 支持 ws:// 与 wss://；wss 走 net/http 默认 dialer（透明 TLS）。
// headers 是发送给服务端的额外 HTTP 头（如 Authorization）。
// 超时由调用方通过 context 之外的方式控制（这里用 dialer.Timeout）。
func Dial(rawURL string, headers http.Header) (*Conn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}

	var (
		host       = u.Host
		secure     bool
		defaultPort = "80"
	)
	switch strings.ToLower(u.Scheme) {
	case "ws":
	case "wss":
		secure = true
		defaultPort = "443"
	default:
		return nil, fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}
	if !strings.Contains(host, ":") {
		host += ":" + defaultPort
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	if secure {
		// 使用 http.Transport 的 TLS 行为：通过 net.Dial + tls.Client 简化为内置
		conn, err = tlsDial(dialer, "tcp", host)
	} else {
		conn, err = dialer.Dial("tcp", host)
	}
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}

	// 生成 16 字节随机 key
	var keyBytes [16]byte
	if _, err := rand.Read(keyBytes[:]); err != nil {
		conn.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes[:])

	// 构造 HTTP 升级请求
	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "GET %s HTTP/1.1\r\n", path)
	fmt.Fprintf(&sb, "Host: %s\r\n", u.Host)
	sb.WriteString("Upgrade: websocket\r\n")
	sb.WriteString("Connection: Upgrade\r\n")
	fmt.Fprintf(&sb, "Sec-WebSocket-Key: %s\r\n", key)
	sb.WriteString("Sec-WebSocket-Version: 13\r\n")
	for k, vs := range headers {
		for _, v := range vs {
			fmt.Fprintf(&sb, "%s: %s\r\n", k, v)
		}
	}
	sb.WriteString("\r\n")
	if _, err := conn.Write([]byte(sb.String())); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write handshake: %w", err)
	}

	// 解析响应
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read response: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		return nil, fmt.Errorf("ws handshake: http %d", resp.StatusCode)
	}
	// 校验 Sec-WebSocket-Accept
	accept := resp.Header.Get("Sec-WebSocket-Accept")
	if want := acceptKey(key); accept != want {
		conn.Close()
		return nil, fmt.Errorf("ws handshake: bad accept (got %q want %q)", accept, want)
	}

	return &Conn{netConn: conn, reader: reader}, nil
}

// WriteText 发送一条文本消息
func (c *Conn) WriteText(payload []byte) error {
	return c.writeFrame(opText, payload)
}

// ReadText 阻塞读取一条文本消息
//
// 控制帧（ping/close）会被自动处理；ping 会被立即回 pong。
// 收到 close 帧时返回 io.EOF。
func (c *Conn) ReadText() ([]byte, error) {
	for {
		op, data, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		switch op {
		case opText:
			return data, nil
		case opBinary:
			// MCP over WS 不用二进制，但接收时按 text 返回也无害
			return data, nil
		case opPing:
			// 回 pong（同 payload）
			if err := c.writeFrame(opPong, data); err != nil {
				return nil, err
			}
		case opPong:
			// 忽略
		case opClose:
			// 回 close
			_ = c.writeFrame(opClose, data)
			return nil, io.EOF
		case opContinuation:
			// 我们不支持分片消息：直接报错
			return nil, errors.New("ws: fragmented frames not supported")
		default:
			return nil, fmt.Errorf("ws: unexpected opcode 0x%x", op)
		}
	}
}

// Close 关闭连接
func (c *Conn) Close() error {
	// 尝试发 close 帧（忽略错误）
	_ = c.writeFrame(opClose, nil)
	return c.netConn.Close()
}

// --- 帧读写 ----------------------------------------------------------------

// writeFrame 发送一帧（fin=1，必须 masked）
func (c *Conn) writeFrame(op opcode, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	header := make([]byte, 0, 14)
	// fin=1, rsv=0, opcode
	header = append(header, 0x80|byte(op))

	// mask=1（客户端必须 mask）
	length := len(payload)
	switch {
	case length < 126:
		header = append(header, 0x80|byte(length))
	case length <= 0xFFFF:
		header = append(header, 0x80|126)
		var buf [2]byte
		binary.BigEndian.PutUint16(buf[:], uint16(length))
		header = append(header, buf[:]...)
	default:
		header = append(header, 0x80|127)
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], uint64(length))
		header = append(header, buf[:]...)
	}

	// 随机 mask key
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	header = append(header, mask[:]...)

	// 对 payload 做异或
	masked := make([]byte, length)
	for i, b := range payload {
		masked[i] = b ^ mask[i%4]
	}

	if _, err := c.netConn.Write(header); err != nil {
		return err
	}
	if length > 0 {
		if _, err := c.netConn.Write(masked); err != nil {
			return err
		}
	}
	return nil
}

// readFrame 读取单帧（不处理分片，返回完整 payload）
func (c *Conn) readFrame() (opcode, []byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(c.reader, hdr[:]); err != nil {
		return 0, nil, err
	}
	op := opcode(hdr[0] & 0x0F)
	masked := hdr[1]&0x80 != 0 // 服务端→客户端通常不 mask
	plen := int(hdr[1] & 0x7F)

	switch plen {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.reader, ext[:]); err != nil {
			return 0, nil, err
		}
		plen = int(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.reader, ext[:]); err != nil {
			return 0, nil, err
		}
		plen = int(binary.BigEndian.Uint64(ext[:]))
	}

	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(c.reader, maskKey[:]); err != nil {
			return 0, nil, err
		}
	}

	payload := make([]byte, plen)
	if plen > 0 {
		if _, err := io.ReadFull(c.reader, payload); err != nil {
			return 0, nil, err
		}
		if masked {
			for i := range payload {
				payload[i] ^= maskKey[i%4]
			}
		}
	}
	return op, payload, nil
}

// acceptKey 计算 Sec-WebSocket-Accept = base64(SHA-1(key + GUID))
func acceptKey(clientKey string) string {
	h := sha1.New()
	h.Write([]byte(clientKey))
	h.Write([]byte(wsGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
