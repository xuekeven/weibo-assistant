package browser

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"strings"
	"time"
)

type cdpClient struct {
	conn    net.Conn
	reader  *bufio.Reader
	nextID  int
	pending []cdpEnvelope
}

type cdpEnvelope struct {
	ID     int             `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Message string `json:"message"`
		Data    string `json:"data"`
	} `json:"error,omitempty"`
}

func newCDPClient(ctx context.Context, wsURL string) (*cdpClient, error) {
	parsed, err := neturl.Parse(wsURL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "ws" {
		return nil, fmt.Errorf("unsupported DevTools websocket scheme %q", parsed.Scheme)
	}

	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", parsed.Host)
	if err != nil {
		return nil, err
	}

	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		_ = conn.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)

	path := parsed.RequestURI()
	if path == "" {
		path = "/"
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	}

	req := strings.Join([]string{
		fmt.Sprintf("GET %s HTTP/1.1", path),
		"Host: " + parsed.Host,
		"Upgrade: websocket",
		"Connection: Upgrade",
		"Sec-WebSocket-Key: " + key,
		"Sec-WebSocket-Version: 13",
		"",
		"",
	}, "\r\n")
	if _, err := io.WriteString(conn, req); err != nil {
		_ = conn.Close()
		return nil, err
	}

	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if !strings.Contains(statusLine, " 101 ") {
		_ = conn.Close()
		return nil, fmt.Errorf("websocket upgrade failed: %s", strings.TrimSpace(statusLine))
	}

	headers := make(http.Header)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if ok {
			headers.Add(strings.TrimSpace(name), strings.TrimSpace(value))
		}
	}

	if err := validateWebSocketAccept(headers.Get("Sec-WebSocket-Accept"), key); err != nil {
		_ = conn.Close()
		return nil, err
	}

	_ = conn.SetDeadline(time.Time{})
	return &cdpClient{
		conn:   conn,
		reader: reader,
	}, nil
}

func (c *cdpClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *cdpClient) evaluate(ctx context.Context, expression string, value any) error {
	return c.evaluateWithOptions(ctx, expression, false, value)
}

func (c *cdpClient) evaluatePromise(ctx context.Context, expression string, value any) error {
	return c.evaluateWithOptions(ctx, expression, true, value)
}

func (c *cdpClient) evaluateWithOptions(ctx context.Context, expression string, awaitPromise bool, value any) error {
	var result struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails json.RawMessage `json:"exceptionDetails"`
	}
	if err := c.call(ctx, "Runtime.evaluate", map[string]any{
		"expression":            expression,
		"returnByValue":         true,
		"awaitPromise":          awaitPromise,
		"includeCommandLineAPI": false,
	}, &result); err != nil {
		return err
	}
	if len(result.ExceptionDetails) > 0 {
		return fmt.Errorf("runtime exception: %s", string(result.ExceptionDetails))
	}
	if len(result.Result.Value) == 0 {
		return errors.New("runtime evaluation returned no value")
	}
	return json.Unmarshal(result.Result.Value, value)
}

func (c *cdpClient) call(ctx context.Context, method string, params any, result any) error {
	c.nextID++
	id := c.nextID

	payload, err := json.Marshal(map[string]any{
		"id":     id,
		"method": method,
		"params": params,
	})
	if err != nil {
		return err
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetDeadline(deadline)
		defer c.conn.SetDeadline(time.Time{})
	}

	if err := c.writeFrame(1, payload); err != nil {
		return err
	}

	for {
		message, err := c.readMessage()
		if err != nil {
			return err
		}

		var envelope cdpEnvelope
		if err := json.Unmarshal(message, &envelope); err != nil {
			return err
		}
		if envelope.ID != id {
			c.pending = append(c.pending, envelope)
			continue
		}
		if envelope.Error != nil {
			if envelope.Error.Data != "" {
				return fmt.Errorf("%s: %s", envelope.Error.Message, envelope.Error.Data)
			}
			return errors.New(envelope.Error.Message)
		}
		if result == nil {
			return nil
		}
		return json.Unmarshal(envelope.Result, result)
	}
}

func (c *cdpClient) readEnvelope(ctx context.Context) (cdpEnvelope, error) {
	for {
		if err := ctx.Err(); err != nil {
			return cdpEnvelope{}, err
		}
		if len(c.pending) > 0 {
			envelope := c.pending[0]
			copy(c.pending, c.pending[1:])
			c.pending = c.pending[:len(c.pending)-1]
			return envelope, nil
		}
		deadline := time.Now().Add(time.Second)
		if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
			deadline = ctxDeadline
		}
		_ = c.conn.SetReadDeadline(deadline)
		message, err := c.readMessage()
		_ = c.conn.SetReadDeadline(time.Time{})
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			return cdpEnvelope{}, err
		}

		var envelope cdpEnvelope
		if err := json.Unmarshal(message, &envelope); err != nil {
			return cdpEnvelope{}, err
		}
		return envelope, nil
	}
}

func (c *cdpClient) writeFrame(opcode byte, payload []byte) error {
	header := []byte{0x80 | opcode}
	length := len(payload)
	switch {
	case length < 126:
		header = append(header, 0x80|byte(length))
	case length <= 0xffff:
		header = append(header, 0x80|126, byte(length>>8), byte(length))
	default:
		header = append(header, 0x80|127)
		size := make([]byte, 8)
		binary.BigEndian.PutUint64(size, uint64(length))
		header = append(header, size...)
	}

	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	header = append(header, mask...)

	masked := make([]byte, len(payload))
	for i, b := range payload {
		masked[i] = b ^ mask[i%4]
	}

	if _, err := c.conn.Write(header); err != nil {
		return err
	}
	_, err := c.conn.Write(masked)
	return err
}

func (c *cdpClient) readMessage() ([]byte, error) {
	var message []byte
	for {
		frame, opcode, final, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		switch opcode {
		case 0:
			message = append(message, frame...)
			if final {
				return message, nil
			}
		case 1, 2:
			message = append(message[:0], frame...)
			if final {
				return message, nil
			}
		case 8:
			return nil, io.EOF
		case 9:
			_ = c.writeFrame(10, frame)
		case 10:
		default:
			return nil, fmt.Errorf("unexpected websocket opcode %d", opcode)
		}
	}
}

func (c *cdpClient) readFrame() ([]byte, byte, bool, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(c.reader, header); err != nil {
		return nil, 0, false, err
	}

	final := header[0]&0x80 != 0
	opcode := header[0] & 0x0f
	masked := header[1]&0x80 != 0
	length := uint64(header[1] & 0x7f)

	switch length {
	case 126:
		ext := make([]byte, 2)
		if _, err := io.ReadFull(c.reader, ext); err != nil {
			return nil, 0, false, err
		}
		length = uint64(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		if _, err := io.ReadFull(c.reader, ext); err != nil {
			return nil, 0, false, err
		}
		length = binary.BigEndian.Uint64(ext)
	}

	var mask []byte
	if masked {
		mask = make([]byte, 4)
		if _, err := io.ReadFull(c.reader, mask); err != nil {
			return nil, 0, false, err
		}
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(c.reader, payload); err != nil {
		return nil, 0, false, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}

	return payload, opcode, final, nil
}

func validateWebSocketAccept(actual string, key string) error {
	hash := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	expected := base64.StdEncoding.EncodeToString(hash[:])
	if actual != expected {
		return fmt.Errorf("invalid websocket accept header")
	}
	return nil
}
