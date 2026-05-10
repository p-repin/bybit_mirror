package bybit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/gorilla/websocket"

	"github.com/p-repin/bybit_mirror/internal/hub"
)

type WSClient struct {
	url    string
	apiKey string
	secret string
	hub    *hub.Hub
}

func NewWS(url, apiKey, apiSecret string, h *hub.Hub) *WSClient {
	return &WSClient{url: url, apiKey: apiKey, secret: apiSecret, hub: h}
}

func (c *WSClient) Run(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		err := c.runOnce(ctx)
		c.hub.SetStatus(false, errString(err))
		if ctx.Err() != nil {
			return
		}
		slog.Warn("bybit ws disconnected", "err", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (c *WSClient) runOnce(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	conn, _, err := websocket.DefaultDialer.DialContext(dialCtx, c.url, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	slog.Info("bybit ws connected", "url", c.url)

	writeMu := make(chan struct{}, 1)
	writeMu <- struct{}{}
	send := func(v any) error {
		<-writeMu
		defer func() { writeMu <- struct{}{} }()
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteJSON(v)
	}

	expires := time.Now().Add(10 * time.Second).UnixMilli()
	authSig := sign(c.secret, "GET/realtime"+strconv.FormatInt(expires, 10))
	if err := send(map[string]any{
		"op":   "auth",
		"args": []any{c.apiKey, expires, authSig},
	}); err != nil {
		return fmt.Errorf("auth send: %w", err)
	}
	if err := send(map[string]any{
		"op":   "subscribe",
		"args": []string{"wallet", "position"},
	}); err != nil {
		return fmt.Errorf("subscribe send: %w", err)
	}

	c.hub.SetStatus(true, "")

	connCtx, connCancel := context.WithCancel(ctx)
	defer connCancel()

	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-connCtx.Done():
				return
			case <-t.C:
				if err := send(map[string]any{"op": "ping"}); err != nil {
					return
				}
			}
		}
	}()

	go func() {
		<-connCtx.Done()
		_ = conn.Close()
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		c.dispatch(msg)
	}
}

type wsMsg struct {
	Topic   string          `json:"topic"`
	Type    string          `json:"type"`
	Op      string          `json:"op"`
	Success *bool           `json:"success,omitempty"`
	RetMsg  string          `json:"ret_msg,omitempty"`
	Data    json.RawMessage `json:"data"`
}

func (c *WSClient) dispatch(raw []byte) {
	var m wsMsg
	if err := json.Unmarshal(raw, &m); err != nil {
		return
	}
	if m.Op != "" {
		if m.Success != nil && !*m.Success {
			slog.Warn("bybit ws op failed", "op", m.Op, "msg", m.RetMsg)
		}
		return
	}
	switch m.Topic {
	case "wallet":
		var ws []hub.Wallet
		if err := json.Unmarshal(m.Data, &ws); err != nil {
			return
		}
		for _, w := range ws {
			c.hub.ApplyWallet(w)
		}
	case "position":
		var ps []hub.Position
		if err := json.Unmarshal(m.Data, &ps); err != nil {
			return
		}
		c.hub.ApplyPositions(ps)
	}
}
