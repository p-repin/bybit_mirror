package server

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/p-repin/bybit_mirror/internal/hub"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		// expected to run behind nginx reverse proxy on the same origin;
		// origin filtering happens at the proxy layer.
		return true
	},
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	client := hub.NewClient(64)
	s.hub.Register(client)
	defer s.hub.Unregister(client)

	conn.SetReadLimit(1024)
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))
		return nil
	})
	_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	pingT := time.NewTicker(30 * time.Second)
	defer pingT.Stop()

	for {
		select {
		case <-closed:
			return
		case <-client.Done():
			return
		case msg := <-client.Send():
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-pingT.C:
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
