// Package ws implements the small RFC 6455 subset Open Remote Commander needs.
// It deliberately has no compression support and enforces masking, frame-size,
// fragmentation, and control-frame rules on both peers.
package ws

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- required by RFC 6455 Sec-WebSocket-Accept
	"crypto/tls"
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

const magicGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

var (
	ErrClosed   = errors.New("websocket closed")
	ErrProtocol = errors.New("websocket protocol error")
)

type Role uint8

const (
	Server Role = iota
	Client
)

type Conn struct {
	conn net.Conn
	br   *bufio.Reader
	role Role
	max  int64
	wmu  sync.Mutex
}

func Upgrade(w http.ResponseWriter, r *http.Request, maxMessageBytes int64) (*Conn, error) {
	if r.Method != http.MethodGet || !tokenContains(r.Header.Get("Connection"), "upgrade") || !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		return nil, fmt.Errorf("%w: invalid upgrade headers", ErrProtocol)
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		return nil, fmt.Errorf("%w: unsupported version", ErrProtocol)
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(decoded) != 16 {
		return nil, fmt.Errorf("%w: invalid key", ErrProtocol)
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("http server does not support hijacking")
	}
	nc, rw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	h := sha1.Sum([]byte(key + magicGUID)) // #nosec G401 -- RFC 6455 mandates SHA-1 here
	accept := base64.StdEncoding.EncodeToString(h[:])
	if _, err := fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept); err != nil {
		_ = nc.Close()
		return nil, err
	}
	if err := rw.Flush(); err != nil {
		_ = nc.Close()
		return nil, err
	}
	if maxMessageBytes <= 0 {
		maxMessageBytes = 4 << 20
	}
	return &Conn{conn: nc, br: rw.Reader, role: Server, max: maxMessageBytes}, nil
}

func Dial(ctx context.Context, rawURL, bearer string, maxMessageBytes int64) (*Conn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "wss" && (u.Scheme != "ws" || !isLoopbackHost(u.Hostname())) {
		return nil, errors.New("websocket URL must use wss (ws is allowed only for loopback)")
	}
	hostPort := u.Host
	if !strings.Contains(hostPort, ":") {
		if u.Scheme == "wss" {
			hostPort += ":443"
		} else {
			hostPort += ":80"
		}
	}
	var d net.Dialer
	nc, err := d.DialContext(ctx, "tcp", hostPort)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "wss" {
		tlsConn := tls.Client(nc, &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = nc.Close()
			return nil, err
		}
		nc = tlsConn
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		_ = nc.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(nonce[:])
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	bw := bufio.NewWriter(nc)
	if _, err := fmt.Fprintf(bw, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\n", path, u.Host, key); err != nil {
		_ = nc.Close()
		return nil, err
	}
	if bearer != "" {
		if strings.ContainsAny(bearer, "\r\n") {
			_ = nc.Close()
			return nil, errors.New("invalid bearer token")
		}
		_, _ = fmt.Fprintf(bw, "Authorization: Bearer %s\r\n", bearer)
	}
	_, _ = fmt.Fprint(bw, "\r\n")
	if err := bw.Flush(); err != nil {
		_ = nc.Close()
		return nil, err
	}
	br := bufio.NewReader(nc)
	req := &http.Request{Method: http.MethodGet, URL: u}
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		_ = nc.Close()
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols || !tokenContains(resp.Header.Get("Connection"), "upgrade") || !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") {
		_ = nc.Close()
		return nil, fmt.Errorf("websocket upgrade rejected: %s", resp.Status)
	}
	h := sha1.Sum([]byte(key + magicGUID)) // #nosec G401 -- RFC 6455 mandated handshake
	want := base64.StdEncoding.EncodeToString(h[:])
	if resp.Header.Get("Sec-WebSocket-Accept") != want {
		_ = nc.Close()
		return nil, fmt.Errorf("%w: bad accept key", ErrProtocol)
	}
	if maxMessageBytes <= 0 {
		maxMessageBytes = 4 << 20
	}
	return &Conn{conn: nc, br: br, role: Client, max: maxMessageBytes}, nil
}

func (c *Conn) Close() error {
	_ = c.writeFrame(0x8, []byte{})
	return c.conn.Close()
}

func (c *Conn) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
func (c *Conn) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }

func (c *Conn) WriteMessage(payload []byte) error {
	if int64(len(payload)) > c.max {
		return errors.New("websocket message exceeds configured maximum")
	}
	return c.writeFrame(0x1, payload)
}

func (c *Conn) ReadMessage() ([]byte, error) {
	var out []byte
	started := false
	for {
		fin, opcode, payload, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		switch opcode {
		case 0x8:
			return nil, ErrClosed
		case 0x9:
			if err := c.writeFrame(0xA, payload); err != nil {
				return nil, err
			}
			continue
		case 0xA:
			continue
		case 0x1, 0x2:
			if started {
				return nil, fmt.Errorf("%w: unexpected data frame", ErrProtocol)
			}
			started = true
			out = append(out, payload...)
		case 0x0:
			if !started {
				return nil, fmt.Errorf("%w: unexpected continuation", ErrProtocol)
			}
			out = append(out, payload...)
		default:
			return nil, fmt.Errorf("%w: opcode %d", ErrProtocol, opcode)
		}
		if int64(len(out)) > c.max {
			return nil, errors.New("websocket message exceeds configured maximum")
		}
		if started && fin {
			return out, nil
		}
	}
}

func (c *Conn) readFrame() (bool, byte, []byte, error) {
	h, err := c.br.ReadByte()
	if err != nil {
		return false, 0, nil, err
	}
	if h&0x70 != 0 {
		return false, 0, nil, fmt.Errorf("%w: RSV bits set", ErrProtocol)
	}
	fin := h&0x80 != 0
	opcode := h & 0x0f
	b2, err := c.br.ReadByte()
	if err != nil {
		return false, 0, nil, err
	}
	masked := b2&0x80 != 0
	if (c.role == Server && !masked) || (c.role == Client && masked) {
		return false, 0, nil, fmt.Errorf("%w: invalid masking", ErrProtocol)
	}
	length := uint64(b2 & 0x7f)
	switch length {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(c.br, b[:]); err != nil {
			return false, 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(b[:]))
		if length < 126 {
			return false, 0, nil, fmt.Errorf("%w: non-canonical length", ErrProtocol)
		}
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(c.br, b[:]); err != nil {
			return false, 0, nil, err
		}
		length = binary.BigEndian.Uint64(b[:])
		if length&(1<<63) != 0 || length < 65536 {
			return false, 0, nil, fmt.Errorf("%w: invalid length", ErrProtocol)
		}
	}
	if opcode >= 0x8 && (!fin || length > 125) {
		return false, 0, nil, fmt.Errorf("%w: invalid control frame", ErrProtocol)
	}
	if length > uint64(c.max) {
		return false, 0, nil, errors.New("websocket frame exceeds configured maximum")
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(c.br, mask[:]); err != nil {
			return false, 0, nil, err
		}
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(c.br, payload); err != nil {
		return false, 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i&3]
		}
	}
	return fin, opcode, payload, nil
}

func (c *Conn) writeFrame(opcode byte, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if opcode >= 0x8 && len(payload) > 125 {
		return errors.New("control frame too large")
	}
	var header [14]byte
	n := 0
	header[n] = 0x80 | opcode
	n++
	masked := c.role == Client
	maskBit := byte(0)
	if masked {
		maskBit = 0x80
	}
	switch l := len(payload); {
	case l < 126:
		header[n] = maskBit | byte(l)
		n++
	case l <= 65535:
		header[n] = maskBit | 126
		n++
		binary.BigEndian.PutUint16(header[n:n+2], uint16(l))
		n += 2
	default:
		header[n] = maskBit | 127
		n++
		binary.BigEndian.PutUint64(header[n:n+8], uint64(l))
		n += 8
	}
	out := payload
	if masked {
		var mask [4]byte
		if _, err := rand.Read(mask[:]); err != nil {
			return err
		}
		copy(header[n:n+4], mask[:])
		n += 4
		out = append([]byte(nil), payload...)
		for i := range out {
			out[i] ^= mask[i&3]
		}
	}
	if _, err := c.conn.Write(header[:n]); err != nil {
		return err
	}
	_, err := c.conn.Write(out)
	return err
}

func tokenContains(header, token string) bool {
	for _, p := range strings.Split(header, ",") {
		if strings.EqualFold(strings.TrimSpace(p), token) {
			return true
		}
	}
	return false
}

func isLoopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
