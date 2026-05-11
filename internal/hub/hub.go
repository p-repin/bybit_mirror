package hub

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
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
	TradeMode *int `json:"tradeMode,omitempty"`
	Category  string `json:"category"`
	// SettleCoin — в какой валюте денежные поля этой позиции. Bybit V5
	// в position/list иногда отдаёт, иногда нет; и для опционов
	// settleCoinFromSymbol-эвристика ломается (символы вида
	// ETHUSDT-12MAY26-2325-C). Поэтому в REST-снапшоте досыпаем сами
	// исходя из того, с каким settleCoin'ом запрашивали.
	SettleCoin  string `json:"settleCoin,omitempty"`
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
	// onPositionsChanged срабатывает после ApplyPositions если набор символов
	// (а не только их поля) изменился — оркестратор использует это, чтобы
	// синхронизировать подписки public WS (tickers.<symbol>).
	onPositionsChanged func()
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

func (h *Hub) SetOnPositionsChanged(cb func()) {
	h.mu.Lock()
	h.onPositionsChanged = cb
	h.mu.Unlock()
}

// Symbols возвращает уникальные symbol'ы открытых позиций для category.
// Если category пуст — возвращает все.
func (h *Hub) Symbols(category string) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	seen := make(map[string]struct{}, len(h.state.Positions))
	out := make([]string, 0, len(h.state.Positions))
	for _, p := range h.state.Positions {
		if category != "" && p.Category != category {
			continue
		}
		if _, ok := seen[p.Symbol]; ok {
			continue
		}
		seen[p.Symbol] = struct{}{}
		out = append(out, p.Symbol)
	}
	return out
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
	h.recomputeWalletLocked()
	out := *h.state.Wallet
	h.mu.Unlock()
	h.broadcast(Envelope{Type: "wallet", Data: out})
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
	before := symbolSet(h.state.Positions)
	for _, p := range positions {
		// WS-private дельта на новый символ может прийти без category — без
		// неё key/Symbols фильтр отбросит позицию, public-WS никогда не
		// подпишется на tickers.<sym> и mark не тикнет до рестарта сервиса
		// (REST-снапшот тегает category из параметра запроса).
		if p.Category == "" {
			p.Category = categoryFromSymbol(p.Symbol)
		}
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
	after := symbolSet(h.state.Positions)
	h.recomputeWalletLocked()
	out := sortedPositions(h.state.Positions)
	var walletEnv *Envelope
	if h.state.Wallet != nil {
		w := *h.state.Wallet
		walletEnv = &Envelope{Type: "wallet", Data: w}
	}
	cb := h.onPositionsChanged
	h.mu.Unlock()
	h.broadcast(Envelope{Type: "positions", Data: out})
	if walletEnv != nil {
		h.broadcast(*walletEnv)
	}
	if cb != nil && !sameSet(before, after) {
		cb()
	}
}

// sortedPositions материализует мапу в детерминированно отсортированный
// слайс. Без этого порядок в каждом broadcast'е разный (Go рандомизирует
// итерацию по map), и UI постоянно перетасовывает строки.
func sortedPositions(m map[string]Position) []Position {
	out := make([]Position, 0, len(m))
	for _, p := range m {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Symbol != out[j].Symbol {
			return out[i].Symbol < out[j].Symbol
		}
		if out[i].Side != out[j].Side {
			return out[i].Side < out[j].Side
		}
		return out[i].PositionIdx < out[j].PositionIdx
	})
	return out
}

func symbolSet(m map[string]Position) map[string]struct{} {
	s := make(map[string]struct{}, len(m))
	for _, p := range m {
		s[p.Symbol] = struct{}{}
	}
	return s
}

func sameSet(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

// ApplyMarkPrice апдейтит MarkPrice + пересчитывает UnrealisedPnl и
// PositionValue для всех позиций по этому symbol/category. Bybit WS-топик
// `position` event-driven и не шлёт mark-тики; реал-таймовый mark тянется
// из public-стрима tickers.SYMBOL. Формулы для linear:
//   pnl   = (mark - avg) * size, знак по Side
//   value = size * mark (notional, всегда положительный)
// Дополнительно дёргаем recomputeWalletLocked, чтобы шапка (totalEquity,
// totalPerpUPL) и вкладка «Монеты» тикали синхронно с позициями.
func (h *Hub) ApplyMarkPrice(category, symbol, markPrice string) {
	if markPrice == "" || symbol == "" {
		return
	}
	mark, err := strconv.ParseFloat(markPrice, 64)
	if err != nil {
		return
	}
	h.mu.Lock()
	changed := false
	for k, p := range h.state.Positions {
		if p.Symbol != symbol || p.Category != category {
			continue
		}
		if p.MarkPrice == markPrice {
			continue
		}
		p.MarkPrice = markPrice
		size, errS := strconv.ParseFloat(p.Size, 64)
		avg, errA := strconv.ParseFloat(p.AvgPrice, 64)
		if errS == nil && errA == nil && size > 0 && avg > 0 {
			pnl := (mark - avg) * size
			if p.Side == "Sell" {
				pnl = -pnl
			}
			p.UnrealisedPnl = strconv.FormatFloat(pnl, 'f', -1, 64)
			p.PositionValue = strconv.FormatFloat(size*mark, 'f', -1, 64)
		}
		h.state.Positions[k] = p
		changed = true
	}
	if !changed {
		h.mu.Unlock()
		return
	}
	h.recomputeWalletLocked()
	out := sortedPositions(h.state.Positions)
	var walletEnv *Envelope
	if h.state.Wallet != nil {
		w := *h.state.Wallet
		walletEnv = &Envelope{Type: "wallet", Data: w}
	}
	h.mu.Unlock()
	h.broadcast(Envelope{Type: "positions", Data: out})
	if walletEnv != nil {
		h.broadcast(*walletEnv)
	}
}

// categoryFromSymbol — fallback когда Bybit WS-position дельта пришла с
// пустым Category. Опционные символы у Bybit вида ETHUSDT-12MAY26-2325-C,
// linear-перпы без дефиса. Inverse у нас не поддержан.
func categoryFromSymbol(symbol string) string {
	if strings.Contains(symbol, "-") {
		return "option"
	}
	return "linear"
}

// recomputeWalletLocked пересчитывает только totalPerpUPL по сумме UPL текущих
// позиций. Остальные wallet-поля (totalEquity, coin.equity/usdValue/unrealisedPnl,
// totalAvailableBalance, totalWalletBalance) — Bybit canonical и обновляются
// через REST-полл /v5/account/wallet-balance (см. client.go::pollWallet).
// Bybit считает их с учётом bonus / IM-локов / спот-цены не-стейбл монет —
// реплицировать всё это локально нерально и расходится с приложением.
// totalPerpUPL держим тикающим из позиций, чтобы шапка двигалась в такт mark-
// апдейтам public-WS между REST-поллами. Должна вызываться под h.mu.Lock.
func (h *Hub) recomputeWalletLocked() {
	if h.state.Wallet == nil {
		return
	}
	var total float64
	for _, p := range h.state.Positions {
		pnl, err := strconv.ParseFloat(p.UnrealisedPnl, 64)
		if err != nil {
			continue
		}
		total += pnl
	}
	h.state.Wallet.TotalPerpUPL = strconv.FormatFloat(total, 'f', -1, 64)
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
		SettleCoin:     pick(upd.SettleCoin, old.SettleCoin),
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
	return map[string]any{
		"wallet":    h.state.Wallet,
		"positions": sortedPositions(h.state.Positions),
		"status":    h.state.Status,
	}
}

func (h *Hub) snapshotEnvelope() []byte {
	payload := map[string]any{
		"wallet":    h.state.Wallet,
		"positions": sortedPositions(h.state.Positions),
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
