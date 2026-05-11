package bybit

import (
	"context"
	"log/slog"
	"time"

	"github.com/p-repin/bybit_mirror/internal/config"
	"github.com/p-repin/bybit_mirror/internal/hub"
)

type Client struct {
	cfg *config.Config
	hub *hub.Hub
	rst *RESTClient
	ws  *WSClient
}

func NewClient(cfg *config.Config, h *hub.Hub) *Client {
	return &Client{
		cfg: cfg,
		hub: h,
		rst: NewREST(cfg.BybitRESTURL(), cfg.APIKey, cfg.APISecret),
		ws:  NewWS(cfg.BybitWSURL(), cfg.APIKey, cfg.APISecret, h),
	}
}

func (c *Client) Run(ctx context.Context) {
	c.snapshot(ctx)
	go c.pollMarginMode(ctx)
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

	var positions []hub.Position
	for _, settle := range []string{"USDT", "USDC"} {
		ps, err := c.rst.Positions(sctx, "linear", settle)
		if err != nil {
			slog.Warn("initial positions snapshot failed", "settle", settle, "err", err)
			continue
		}
		positions = append(positions, ps...)
	}
	if len(positions) > 0 {
		c.hub.ApplyPositions(positions)
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
