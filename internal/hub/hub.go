package hub

import (
	"encoding/json"
	"sync"
	"time"
)

type Coin struct {
	Coin                string `json:"coin"`
	Equity              string `json:"equity"`
	WalletBalance       string `json:"walletBalance"`
	UsdValue            string `json:"usdValue"`
	AvailableToWithdraw string `json:"availableToWithdraw"`
	UnrealisedPnl       string `json:"unrealisedPnl"`
	CumRealisedPnl      string `json:"cumRealisedPnl"`
}

type Wallet struct {
	AccountType           string `json:"accountType"`
	TotalEquity           string `json:"totalEquity"`
	TotalWalletBalance    string `json:"totalWalletBalance"`
	TotalAvailableBalance string `json:"totalAvailableBalance"`
	TotalMarginBalance    string `json:"totalMarginBalance"`
	TotalPerpUPL          string `json:"totalPerpUPL"`
	Coins                 []Coin `json:"coin"`
	// MarginMode тянется из /v5/account/info (отдельный REST), а не приходит
	// в WS-wallet топике — сохраняем при перезаписи Wallet, см. ApplyWallet.
	MarginMode string `json:"marginMode,omitempty"`
}

type Position struct {
	Symbol         string `json:"symbol"`
	Side           string `json:"side"`
	Size           string `json:"size"`
	PositionIdx    int    `json:"positionIdx"`
	AvgPrice       string `json:"avgPrice"`
	MarkPrice      string `json:"markPrice"`
	UnrealisedPnl  string `json:"unrealisedPnl"`
	CumRealisedPnl string `json:"cumRealisedPnl"`
	LiqPrice       string `json:"liqPrice"`
	PositionValue  string `json:"positionValue"`
	Leverage       string `json:"leverage"`
	// TradeMode: 0 = cross, 1 = isolated. Указатель — чтобы отличить
	// «не пришло в WS-дельте» (nil) от «реально 0=cross».
	TradeMode   *int   `json:"tradeMode,omitempty"`
	Category    string `json:"category"`
	UpdatedTime string `json:"updatedTime"`
}

func (p Position) key() string {
	return p.Category + "|" + p.Symbol + "|" + itoa(p.PositionIdx)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	buf := [20]byte{}
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

type Status struct {
	Connected bool      `json:"connected"`
	LastError string    `json:"lastError,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type snapshot struct {
	Wallet    *Wallet             `json:"wallet"`
	Positions map[string]Position `json:"positions"`
	Status    Status              `json:"status"`
}

type Envelope struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type Client struct {
	send chan []byte
	done chan struct{}
}

func NewClient(buf int) *Client {
	return &Client{send: make(chan []byte, buf), done: make(chan struct{})}
}

func (c *Client) Send() <-chan []byte     { return c.send }
func (c *Client) Done() <-chan struct{}   { return c.done }
func (c *Client) Close() {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
}

type Hub struct {
	mu       sync.RWMutex
	state    snapshot
	clients  map[*Client]struct{}
	clientMu sync.RWMutex
}

func New() *Hub {
	return &Hub{
		state: snapshot{
			Positions: make(map[string]Position),
			Status:    Status{UpdatedAt: time.Now()},
		},
		clients: make(map[*Client]struct{}),
	}
}

func (h *Hub) Register(c *Client) {
	h.clientMu.Lock()
	h.clients[c] = struct{}{}
	h.clientMu.Unlock()

	h.mu.RLock()
	snap := h.snapshotEnvelope()
	h.mu.RUnlock()
	c.tryDeliver(snap)
}

func (h *Hub) Unregister(c *Client) {
	h.clientMu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
	}
	h.clientMu.Unlock()
	c.Close()
}

func (h *Hub) ApplyWallet(w Wallet) {
	h.mu.Lock()
	if w.MarginMode == "" && h.state.Wallet != nil {
		w.MarginMode = h.state.Wallet.MarginMode
	}
	h.state.Wallet = &w
	h.mu.Unlock()
	h.broadcast(Envelope{Type: "wallet", Data: w})
}

func (h *Hub) ApplyMarginMode(mode string) {
	h.mu.Lock()
	if h.state.Wallet == nil || h.state.Wallet.MarginMode == mode {
		h.mu.Unlock()
		return
	}
	h.state.Wallet.MarginMode = mode
	w := *h.state.Wallet
	h.mu.Unlock()
	h.broadcast(Envelope{Type: "wallet", Data: w})
}

func (h *Hub) ApplyPositions(positions []Position) {
	h.mu.Lock()
	for _, p := range positions {
		k := p.key()
		if p.Size == "0" {
			delete(h.state.Positions, k)
			continue
		}
		if existing, ok := h.state.Positions[k]; ok {
			h.state.Positions[k] = mergePosition(existing, p)
			continue
		}
		if p.Size == "" {
			continue
		}
		h.state.Positions[k] = p
	}
	out := make([]Position, 0, len(h.state.Positions))
	for _, p := range h.state.Positions {
		out = append(out, p)
	}
	h.mu.Unlock()
	h.broadcast(Envelope{Type: "positions", Data: out})
}

// Bybit WS-дельты не всегда несут все поля: неизменившиеся приходят
// пустой строкой. Сохраняем старое значение, если в апдейте пусто.
func mergePosition(old, upd Position) Position {
	pick := func(n, o string) string {
		if n == "" {
			return o
		}
		return n
	}
	tradeMode := old.TradeMode
	if upd.TradeMode != nil {
		tradeMode = upd.TradeMode
	}
	return Position{
		Symbol:         pick(upd.Symbol, old.Symbol),
		Side:           pick(upd.Side, old.Side),
		Size:           pick(upd.Size, old.Size),
		PositionIdx:    upd.PositionIdx,
		AvgPrice:       pick(upd.AvgPrice, old.AvgPrice),
		MarkPrice:      pick(upd.MarkPrice, old.MarkPrice),
		UnrealisedPnl:  pick(upd.UnrealisedPnl, old.UnrealisedPnl),
		CumRealisedPnl: pick(upd.CumRealisedPnl, old.CumRealisedPnl),
		LiqPrice:       pick(upd.LiqPrice, old.LiqPrice),
		PositionValue:  pick(upd.PositionValue, old.PositionValue),
		Leverage:       pick(upd.Leverage, old.Leverage),
		TradeMode:      tradeMode,
		Category:       pick(upd.Category, old.Category),
		UpdatedTime:    pick(upd.UpdatedTime, old.UpdatedTime),
	}
}

func (h *Hub) SetStatus(connected bool, lastErr string) {
	h.mu.Lock()
	h.state.Status = Status{Connected: connected, LastError: lastErr, UpdatedAt: time.Now()}
	st := h.state.Status
	h.mu.Unlock()
	h.broadcast(Envelope{Type: "status", Data: st})
}

func (h *Hub) Snapshot() map[string]any {
	h.mu.RLock()
	defer h.mu.RUnlock()
	positions := make([]Position, 0, len(h.state.Positions))
	for _, p := range h.state.Positions {
		positions = append(positions, p)
	}
	return map[string]any{
		"wallet":    h.state.Wallet,
		"positions": positions,
		"status":    h.state.Status,
	}
}

func (h *Hub) snapshotEnvelope() []byte {
	positions := make([]Position, 0, len(h.state.Positions))
	for _, p := range h.state.Positions {
		positions = append(positions, p)
	}
	payload := map[string]any{
		"wallet":    h.state.Wallet,
		"positions": positions,
		"status":    h.state.Status,
	}
	b, _ := json.Marshal(Envelope{Type: "snapshot", Data: payload})
	return b
}

func (h *Hub) broadcast(env Envelope) {
	b, err := json.Marshal(env)
	if err != nil {
		return
	}
	h.clientMu.RLock()
	defer h.clientMu.RUnlock()
	for c := range h.clients {
		c.tryDeliver(b)
	}
}

func (c *Client) tryDeliver(b []byte) {
	select {
	case c.send <- b:
	case <-c.done:
	default:
		c.Close()
	}
}
