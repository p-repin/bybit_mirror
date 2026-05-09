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
	Category       string `json:"category"`
	UpdatedTime    string `json:"updatedTime"`
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
	h.state.Wallet = &w
	h.mu.Unlock()
	h.broadcast(Envelope{Type: "wallet", Data: w})
}

func (h *Hub) ApplyPositions(positions []Position) {
	h.mu.Lock()
	for _, p := range positions {
		k := p.key()
		if p.Size == "0" || p.Size == "" {
			delete(h.state.Positions, k)
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
