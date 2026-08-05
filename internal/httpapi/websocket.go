package httpapi

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

type websocketHub struct {
	mu      sync.Mutex
	clients map[*websocketClient]struct{}
}

type websocketClient struct {
	conn net.Conn
	send chan websocketOutbound
}

type websocketOutbound struct {
	opcode  byte
	payload []byte
}

func newWebsocketHub() *websocketHub {
	return &websocketHub{clients: make(map[*websocketClient]struct{})}
}

func (h *websocketHub) add(client *websocketClient) {
	h.mu.Lock()
	h.clients[client] = struct{}{}
	count := len(h.clients)
	h.mu.Unlock()
	slog.Info("网页已连接实时消息", "当前网页连接数", count)
}

func (h *websocketHub) remove(client *websocketClient) {
	h.mu.Lock()
	delete(h.clients, client)
	count := len(h.clients)
	h.mu.Unlock()
	slog.Info("网页已断开实时消息", "剩余网页连接数", count)
}

func (h *websocketHub) broadcast(value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		slog.Warn("实时消息整理失败，无法推送到网页", "错误", err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	clientCount := len(h.clients)
	if clientCount == 0 {
		slog.Info("实时消息暂未推送：当前没有网页连接", "数据大小（字节）", len(payload))
	} else {
		slog.Info("实时消息已推送到网页", "网页连接数", clientCount, "数据大小（字节）", len(payload))
	}
	for client := range h.clients {
		select {
		case client.send <- websocketOutbound{opcode: 0x1, payload: payload}:
		default:
			delete(h.clients, client)
			_ = client.conn.Close()
		}
	}
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") ||
		!strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
		writeError(w, http.StatusBadRequest, errors.New("websocket upgrade required"))
		return
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		w.Header().Set("Sec-WebSocket-Version", "13")
		writeError(w, http.StatusUpgradeRequired, errors.New("unsupported websocket version"))
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && !s.isAllowedOrigin(origin) {
		writeError(w, http.StatusForbidden, errors.New("websocket origin is not allowed"))
		return
	}

	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		writeError(w, http.StatusBadRequest, errors.New("Sec-WebSocket-Key is required"))
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("http hijacker is not available"))
		return
	}
	conn, reader, err := hijacker.Hijack()
	if err != nil {
		slog.Warn("websocket hijack failed", "error", err)
		return
	}

	accept := websocketAcceptKey(key)
	_, err = fmt.Fprintf(conn,
		"HTTP/1.1 101 Switching Protocols\r\n"+
			"Upgrade: websocket\r\n"+
			"Connection: Upgrade\r\n"+
			"Sec-WebSocket-Accept: %s\r\n"+
			"\r\n",
		accept,
	)
	if err != nil {
		_ = conn.Close()
		return
	}

	client := &websocketClient{
		conn: conn,
		send: make(chan websocketOutbound, 32),
	}
	s.wsHub.add(client)
	s.wsHub.broadcast(map[string]any{
		"type": "client_count_changed",
	})

	go s.websocketWriter(client)
	s.websocketReader(reader, client)
	s.wsHub.remove(client)
	_ = conn.Close()
}

func websocketAcceptKey(key string) string {
	hash := sha1.Sum([]byte(key + websocketGUID))
	return base64.StdEncoding.EncodeToString(hash[:])
}

func (s *Server) websocketWriter(client *websocketClient) {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case message, ok := <-client.send:
			if !ok {
				_ = writeWebsocketFrame(client.conn, 0x8, []byte{})
				return
			}
			_ = client.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := writeWebsocketFrame(client.conn, message.opcode, message.payload); err != nil {
				_ = client.conn.Close()
				return
			}
		case <-ticker.C:
			_ = client.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := writeWebsocketFrame(client.conn, 0x9, []byte("ping")); err != nil {
				_ = client.conn.Close()
				return
			}
		}
	}
}

func (s *Server) websocketReader(reader *bufio.ReadWriter, client *websocketClient) {
	for {
		_ = client.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		opcode, payload, err := readWebsocketFrame(reader.Reader)
		if err != nil {
			return
		}
		switch opcode {
		case 0x8:
			return
		case 0x9:
			select {
			case client.send <- websocketOutbound{opcode: 0xA, payload: payload}:
			default:
				return
			}
		}
	}
}

func writeWebsocketFrame(conn net.Conn, opcode byte, payload []byte) error {
	header := []byte{0x80 | opcode}
	length := len(payload)
	switch {
	case length < 126:
		header = append(header, byte(length))
	case length <= 65535:
		header = append(header, 126, byte(length>>8), byte(length))
	default:
		header = append(header, 127)
		var lengthBuf [8]byte
		binary.BigEndian.PutUint64(lengthBuf[:], uint64(length))
		header = append(header, lengthBuf[:]...)
	}
	if _, err := conn.Write(header); err != nil {
		return err
	}
	if length == 0 {
		return nil
	}
	_, err := conn.Write(payload)
	return err
}

func readWebsocketFrame(reader *bufio.Reader) (byte, []byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return 0, nil, err
	}
	opcode := header[0] & 0x0f
	masked := header[1]&0x80 != 0
	if !masked {
		return 0, nil, errors.New("client websocket frames must be masked")
	}
	length := uint64(header[1] & 0x7f)
	switch length {
	case 126:
		var extended [2]byte
		if _, err := io.ReadFull(reader, extended[:]); err != nil {
			return 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(extended[:]))
	case 127:
		var extended [8]byte
		if _, err := io.ReadFull(reader, extended[:]); err != nil {
			return 0, nil, err
		}
		length = binary.BigEndian.Uint64(extended[:])
	}
	if length > 2*1024*1024 {
		return 0, nil, errors.New("websocket frame is too large")
	}

	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(reader, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(reader, payload); err != nil {
			return 0, nil, err
		}
	}
	if masked {
		for index := range payload {
			payload[index] ^= mask[index%4]
		}
	}
	return opcode, payload, nil
}
