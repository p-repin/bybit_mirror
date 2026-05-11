package bybit

import (
	"context"
	"log/slog"
	"time"

	"github.com/p-repin/bybit_mirror/internal/config"
	"github.com/p-repin/bybit_mirror/internal/hub"
)

type Client struct {
	cfg   *config.Config
	hub   *hub.Hub
	rst   *RESTClient
	ws    *WSClient
	wsLin *WSPublicClient
	wsOpt *WSPublicClient
}

func NewClient(cfg *config.Config, h *hub.Hub) *Client {
	return &Client{
		cfg:   cfg,
		hub:   h,
		rst:   NewREST(cfg.BybitRESTURL(), cfg.APIKey, cfg.APISecret),
		ws:    NewWS(cfg.BybitWSURL(), cfg.APIKey, cfg.APISecret, h),
		wsLin: NewPublicWS(cfg.BybitPublicLinearWSURL(), "linear", h),
		wsOpt: NewPublicWS(cfg.BybitPublicOptionWSURL(), "option", h),
	}
}

func (c *Client) Run(ctx context.Context) {
	// Хук на изменение набора открытых символов — синкаем подписки обоих
	// public-стримов (linear и option у Bybit раздельные endpoint'ы).
	c.hub.SetOnPositionsChanged(func() {
		c.wsLin.SetSymbols(c.hub.Symbols("linear"))
		c.wsOpt.SetSymbols(c.hub.Symbols("option"))
	})
	c.snapshot(ctx)
	// Стартовый снапшот уже мог добавить позиции — подтянем подписки сразу.
	c.wsLin.SetSymbols(c.hub.Symbols("linear"))
	c.wsOpt.SetSymbols(c.hub.Symbols("option"))
	go c.pollMarginMode(ctx)
	go c.pollWallet(ctx)
	go c.pollPositions(ctx)
	go c.wsLin.Run(ctx)
	go c.wsOpt.Run(ctx)
	c.ws.Run(ctx)
}

func (c *Client) snapshot(ctx context.Context) {
	sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	accountType := "UNIFIED"
	if c.cfg.AccountType == config.AccountClassic {
		accountType = "CONTRACT"
	}

	// marginMode тащим первым, чтобы при ApplyWallet оно сразу было в hub
	if mode, err := c.rst.AccountInfo(sctx); err != nil {
		slog.Warn("initial account info failed", "err", err)
	} else if mode != "" {
		c.hub.ApplyMarginMode(mode)
	}

	if w, err := c.rst.WalletBalance(sctx, accountType); err != nil {
		slog.Warn("initial wallet snapshot failed", "err", err)
	} else if w != nil {
		c.hub.ApplyWallet(*w)
	}

	// linear + option, оба под USDT и USDC. WS-топик `position` event-driven,
	// так что без явного REST-pull опции (или непопулярные linear) не появятся
	// в стейте до тех пор пока с ними что-то не произойдёт.
	queries := []struct {
		category, settle string
	}{
		{"linear", "USDT"},
		{"linear", "USDC"},
		{"option", "USDT"},
		{"option", "USDC"},
	}
	var positions []hub.Position
	for _, q := range queries {
		ps, err := c.rst.Positions(sctx, q.category, q.settle)
		if err != nil {
			slog.Warn("initial positions snapshot failed",
				"category", q.category, "settle", q.settle, "err", err)
			continue
		}
		positions = append(positions, ps...)
	}
	if len(positions) > 0 {
		c.hub.ApplyPositions(positions)
	}
}

// WS-wallet топик event-driven (фил, funding, leverage change) — не пушит
// per-tick UPL по mark'у, не учитывает Bybit-овский bonus / IM-локи / спот-цену
// не-стейбл монет. Поэтому шапку (totalEquity, coin.equity/usdValue) считаем
// не сами, а тянем REST raz в 2с — Bybit там отдаёт каноничные числа,
// сходящиеся с приложением. totalPerpUPL продолжаем пересчитывать в hub из
// текущих позиций, чтобы шапка тикала вместе с mark-апдейтами public-WS.
func (c *Client) pollWallet(ctx context.Context) {
	accountType := "UNIFIED"
	if c.cfg.AccountType == config.AccountClassic {
		accountType = "CONTRACT"
	}
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			w, err := c.rst.WalletBalance(rctx, accountType)
			cancel()
			if err != nil {
				slog.Warn("poll wallet failed", "err", err)
				continue
			}
			if w != nil {
				c.hub.ApplyWallet(*w)
			}
		}
	}
}

// pollPositions — safety-net поверх WS-private + public-WS. Раз в 10с
// перечитывает все 4 (linear/USDT, linear/USDC, option/USDT, option/USDC),
// зовёт ApplyPositions и в конце унконсиционно пинает SetSymbols на обоих
// public-стримах. Это закрывает несколько углов:
//  1. WS-private может прислать position-дельту с пустым category — наш
//     fallback по символу не 100% (вдруг inverse или странный символ);
//     REST всегда тегает category из контекста запроса.
//  2. Public-WS subscribe может быть отвергнут Bybit'ом (success=false);
//     dispatch снимает символ с active, и принудительный SetSymbols здесь
//     триггерит resub.
//  3. Bybit-овский liqPrice/PnL/avgPrice как baseline между WS-событиями.
func (c *Client) pollPositions(ctx context.Context) {
	queries := []struct {
		category, settle string
	}{
		{"linear", "USDT"},
		{"linear", "USDC"},
		{"option", "USDT"},
		{"option", "USDC"},
	}
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			var positions []hub.Position
			for _, q := range queries {
				ps, err := c.rst.Positions(rctx, q.category, q.settle)
				if err != nil {
					slog.Warn("poll positions failed",
						"category", q.category, "settle", q.settle, "err", err)
					continue
				}
				positions = append(positions, ps...)
			}
			cancel()
			if len(positions) > 0 {
				c.hub.ApplyPositions(positions)
			}
			// Принудительный пинок public-WS: если что-то отвалилось, syncSubs
			// сейчас увидит расхождение desired vs active и пересубается.
			c.wsLin.SetSymbols(c.hub.Symbols("linear"))
			c.wsOpt.SetSymbols(c.hub.Symbols("option"))
		}
	}
}

// Bybit не шлёт изменение marginMode в WS — поэтому пуллим REST раз в 30с,
// чтобы переключение Cross↔Isolated в Bybit-app отображалось у нас в UI.
func (c *Client) pollMarginMode(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			mode, err := c.rst.AccountInfo(rctx)
			cancel()
			if err != nil {
				slog.Warn("poll account info failed", "err", err)
				continue
			}
			if mode != "" {
				c.hub.ApplyMarginMode(mode)
			}
		}
	}
}
