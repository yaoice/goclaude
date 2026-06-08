package wsclient

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestAcceptKey(t *testing.T) {
	// RFC6455 example
	got := acceptKey("dGhlIHNhbXBsZSBub25jZQ==")
	want := "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// readFrameRaw 从 server 端解析单帧（解 mask）
func readFrameRaw(t *testing.T, r *bufio.Reader) (op byte, payload []byte) {
	t.Helper()
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(r, hdr); err != nil {
		t.Fatal(err)
	}
	op = hdr[0] & 0x0F
	masked := hdr[1]&0x80 != 0
	plen := int(hdr[1] & 0x7F)
	switch plen {
	case 126:
		var ext [2]byte
		_, _ = io.ReadFull(r, ext[:])
		plen = int(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		_, _ = io.ReadFull(r, ext[:])
		plen = int(binary.BigEndian.Uint64(ext[:]))
	}
	var mask [4]byte
	if masked {
		_, _ = io.ReadFull(r, mask[:])
	}
	payload = make([]byte, plen)
	if plen > 0 {
		_, _ = io.ReadFull(r, payload)
		if masked {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}
	}
	return op, payload
}

// writeServerFrame 服务端写一帧（不 masked）
func writeServerFrame(w net.Conn, op byte, payload []byte) error {
	var buf bytes.Buffer
	buf.WriteByte(0x80 | op)
	buf.WriteByte(byte(len(payload)))
	buf.Write(payload)
	_, err := w.Write(buf.Bytes())
	return err
}

func TestConn_RoundTrip(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	cConn := &Conn{netConn: client, reader: bufio.NewReader(client)}
	sReader := bufio.NewReader(server)

	// client → server
	go func() {
		_ = cConn.WriteText([]byte("hello"))
	}()
	op, payload := readFrameRaw(t, sReader)
	if op != 0x1 {
		t.Errorf("opcode = 0x%x", op)
	}
	if string(payload) != "hello" {
		t.Errorf("payload = %q", payload)
	}

	// server → client
	go func() {
		_ = writeServerFrame(server, 0x1, []byte("world!"))
	}()
	got, err := cConn.ReadText()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "world!" {
		t.Errorf("got %q", got)
	}
}

func TestConn_PingAutoReply(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	cConn := &Conn{netConn: client, reader: bufio.NewReader(client)}
	sReader := bufio.NewReader(server)

	// 异步在 server 端持续读所有帧、记录第一个 pong
	var (
		mu       sync.Mutex
		pongSeen bool
	)
	pongDone := make(chan struct{})
	go func() {
		for {
			op, _ := readFrameRaw(t, sReader)
			if op == byte(opPong) {
				mu.Lock()
				pongSeen = true
				mu.Unlock()
				close(pongDone)
				return
			}
		}
	}()

	// 客户端读：会先收到 ping → 自动 pong → 再读到 text
	textCh := make(chan []byte, 1)
	errCh := make(chan error, 1)
	go func() {
		data, err := cConn.ReadText()
		if err != nil {
			errCh <- err
			return
		}
		textCh <- data
	}()

	// server 发 ping 然后立刻发 text
	go func() {
		_ = writeServerFrame(server, byte(opPing), []byte("p"))
		_ = writeServerFrame(server, byte(opText), []byte("ok"))
	}()

	select {
	case <-pongDone:
	case <-time.After(2 * time.Second):
		t.Fatal("pong not received")
	}
	mu.Lock()
	got := pongSeen
	mu.Unlock()
	if !got {
		t.Error("pong not seen")
	}

	select {
	case data := <-textCh:
		if string(data) != "ok" {
			t.Errorf("got %q", data)
		}
	case err := <-errCh:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("ReadText timeout")
	}
}
