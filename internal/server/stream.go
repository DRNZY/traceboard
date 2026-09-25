package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"traceboard/internal/live"
)

// clientFrame is the only shape the client sends. Every field is optional: a
// client that only wants updates never sends anything.
type clientFrame struct {
	Type          string `json:"type"`
	RunID         string `json:"run_id"`
	AfterSequence int64  `json:"after_sequence"`
	Sequence      int64  `json:"sequence"`
}

const (
	writeWait      = 10 * time.Second
	readWait       = 70 * time.Second
	writePeriod    = 30 * time.Second
	pongWait       = 70 * time.Second
	pingPeriod     = 30 * time.Second
	maxMessageSize = 4096
)

var upgrader = websocket.Upgrader{
	HandshakeTimeout: 5 * time.Second,
	ReadBufferSize:   1024,
	WriteBufferSize:  4096,
	// Same-origin is enforced by the session cookie and the Origin check in
	// middleware, so no origin list is duplicated here.
	CheckOrigin: func(*http.Request) bool { return true },
}

// handleWebSocket streams committed changes to an already-authenticated
// dashboard. The first frame carries the current stream ID so a client that
// reconnects can detect whether it missed anything.
func (s *Server) handleWebSocket(writer http.ResponseWriter, request *http.Request) {
	connection, err := upgrader.Upgrade(writer, request, nil)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "upgrade_failed", "the live connection could not be established")
		return
	}
	defer connection.Close()

	connection.SetReadLimit(maxMessageSize)
	if err := connection.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		return
	}
	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(pongWait))
	})
	// Client frames only adjust the server's view of what a client holds. They
	// never mutate stored data, and an unreadable frame is ignored.
	go func() {
		defer connection.Close()
		for {
			_, raw, err := connection.ReadMessage()
			if err != nil {
				return
			}
			var frame clientFrame
			if err := json.Unmarshal(raw, &frame); err != nil {
				continue
			}
			if frame.Type == "ping" {
				_ = s.deps.Hub.Publish(live.Ping())
			}
		}
	}()

	messages, cancel := s.deps.Hub.Subscribe()
	defer cancel()

	// The first frame states the current stream ID, so a reconnecting client can
	// tell whether it missed anything before it starts reading.
	if err := connection.WriteMessage(websocket.TextMessage, s.deps.Hub.Hello()); err != nil {
		return
	}

	ticker := time.NewTicker(writePeriod)
	defer ticker.Stop()

	done := request.Context().Done()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			_ = connection.SetWriteDeadline(time.Now().Add(writeWait))
			if err := connection.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case message, ok := <-messages:
			if !ok {
				return
			}
			_ = connection.SetWriteDeadline(time.Now().Add(writeWait))
			if err := connection.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}
		}
	}
}
