package bybit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/p-repin/bybit_mirror/internal/hub"
)

// WSPublicClient держит public-стрим Bybit конкретной категории (linear или
// option) и подписывается на tickers.<symbol> для открытых позиций этой
// категории. Тики markPrice летят в hub через ApplyMarkPrice — private-топик
// `position` mark-тики не присылает. Linear и option у Bybit на разных
// endpoint'ах, поэтому держим два экземпляра клиента.
type WSPublicClient struct {
	url      string
	category string // "linear" или "option" — пробрасывается в ApplyMarkPrice
	hub      *hub.Hub

	mu      sync.Mutex
	conn    *websocket.Conn
	desired map[string]struct{} // что должно быть подписано
	active  map[string]struct{} // что реально отправлено в текущий conn
	notify  chan struct{}
	writeMu sync.Mutex
}

func NewPublicWS(url, category string, h *hub.Hub) *WSPublicClient {
	return &WSPublicClient{
		url:      url,
		category: category,
		hub:      h,
		desired:  make(map[string]struct{}),
		active:   make(map[string]struct{}),
		notify:   make(chan struct{}, 1),
	}
}

// SetSymbols обновляет целевой набор подписок. Diffится в раннере,
// чтобы можно было звать из любой горутины без блокировок на сети.
func (c *WSPublicClient) SetSymbols(symbols []string) {
	next := make(map[string]struct{}, len(symbols))
	for _, s := range symbols {
		if s == "" {
			continue
		}
		next[s] = struct{}{}
	}
	c.mu.Lock()
	c.desired = next
	c.mu.Unlock()
	select {
	case c.notify <- struct{}{}:
	default:
	}
}

func (c *WSPublicClient) Run(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		err := c.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		slog.Warn("bybit public ws disconnected", "category", c.category, "err", err, "retry_in", backoff)
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

func (c *WSPublicClient) runOnce(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	conn, _, err := websocket.DefaultDialer.DialContext(dialCtx, c.url, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	slog.Info("bybit public ws connected", "category", c.category, "url", c.url)

	c.mu.Lock()
	c.conn = conn
	c.active = make(map[string]struct{})
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()
	}()

	connCtx, connCancel := context.WithCancel(ctx)
	defer connCancel()

	go func() {
		<-connCtx.Done()
		_ = conn.Close()
	}()

	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-connCtx.Done():
				return
			case <-t.C:
				if err := c.send(map[string]any{"op": "ping"}); err != nil {
					return
				}
			}
		}
	}()

	// На каждый новый коннект сразу синкаем подписки + слушаем апдейты
	// desired-сета пока conn жив.
	go func() {
		c.syncSubs()
		for {
			select {
			case <-connCtx.Done():
				return
			case <-c.notify:
				c.syncSubs()
			}
		}
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		c.dispatch(msg)
	}
}

func (c *WSPublicClient) send(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("no conn")
	}
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return conn.WriteJSON(v)
}

// syncSubs шлёт sub/unsub по диффу desired vs active. Active обновляется
// оптимистично — если сервер вернёт ошибку, увидим её в логах через
// dispatch (op success=false), но повторно слать не пробуем: следующая
// смена desired или реконнект всё перепошлют.
func (c *WSPublicClient) syncSubs() {
	c.mu.Lock()
	var toSub, toUnsub []string
	for s := range c.desired {
		if _, ok := c.active[s]; !ok {
			toSub = append(toSub, "tickers."+s)
		}
	}
	for s := range c.active {
		if _, ok := c.desired[s]; !ok {
			toUnsub = append(toUnsub, "tickers."+s)
		}
	}
	next := make(map[string]struct{}, len(c.desired))
	for s := range c.desired {
		next[s] = struct{}{}
	}
	c.active = next
	c.mu.Unlock()

	// Bybit лимитирует ~10 args на один op-сообщение.
	for _, batch := range chunkArgs(toSub, 10) {
		if err := c.send(map[string]any{"op": "subscribe", "args": batch}); err != nil {
			return
		}
	}
	for _, batch := range chunkArgs(toUnsub, 10) {
		if err := c.send(map[string]any{"op": "unsubscribe", "args": batch}); err != nil {
			return
		}
	}
}

func chunkArgs(xs []string, n int) [][]string {
	if len(xs) == 0 {
		return nil
	}
	out := make([][]string, 0, (len(xs)+n-1)/n)
	for i := 0; i < len(xs); i += n {
		end := i + n
		if end > len(xs) {
			end = len(xs)
		}
		out = append(out, xs[i:end])
	}
	return out
}

type pubMsg struct {
	Topic   string          `json:"topic"`
	Type    string          `json:"type"`
	Op      string          `json:"op"`
	Success *bool           `json:"success,omitempty"`
	RetMsg  string          `json:"ret_msg,omitempty"`
	Data    json.RawMessage `json:"data"`
}

type tickerData struct {
	Symbol    string `json:"symbol"`
	MarkPrice string `json:"markPrice"`
}

func (c *WSPublicClient) dispatch(raw []byte) {
	var m pubMsg
	if err := json.Unmarshal(raw, &m); err != nil {
		return
	}
	if m.Op != "" {
		if m.Success != nil && !*m.Success {
			slog.Warn("bybit public ws op failed", "op", m.Op, "msg", m.RetMsg)
		}
		return
	}
	if !strings.HasPrefix(m.Topic, "tickers.") {
		return
	}
	var d tickerData
	if err := json.Unmarshal(m.Data, &d); err != nil {
		return
	}
	if d.MarkPrice == "" || d.Symbol == "" {
		return
	}
	c.hub.ApplyMarkPrice(c.category, d.Symbol, d.MarkPrice)
}
