package bybit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/p-repin/bybit_mirror/internal/hub"
)

const recvWindow = "5000"

type RESTClient struct {
	base   string
	apiKey string
	secret string
	http   *http.Client
}

func NewREST(baseURL, apiKey, apiSecret string) *RESTClient {
	return &RESTClient{
		base:   baseURL,
		apiKey: apiKey,
		secret: apiSecret,
		http:   &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *RESTClient) signedGET(ctx context.Context, path string, params url.Values) ([]byte, error) {
	qs := params.Encode()
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	sig := sign(c.secret, ts+c.apiKey+recvWindow+qs)

	full := c.base + path
	if qs != "" {
		full += "?" + qs
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-BAPI-API-KEY", c.apiKey)
	req.Header.Set("X-BAPI-TIMESTAMP", ts)
	req.Header.Set("X-BAPI-RECV-WINDOW", recvWindow)
	req.Header.Set("X-BAPI-SIGN", sig)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bybit %s: %d %s", path, resp.StatusCode, string(body))
	}
	return body, nil
}

type walletResp struct {
	RetCode int    `json:"retCode"`
	RetMsg  string `json:"retMsg"`
	Result  struct {
		List []hub.Wallet `json:"list"`
	} `json:"result"`
}

func (c *RESTClient) WalletBalance(ctx context.Context, accountType string) (*hub.Wallet, error) {
	p := url.Values{}
	p.Set("accountType", strings.ToUpper(accountType))
	body, err := c.signedGET(ctx, "/v5/account/wallet-balance", p)
	if err != nil {
		return nil, err
	}
	var r walletResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	if r.RetCode != 0 {
		return nil, fmt.Errorf("bybit wallet retCode=%d msg=%s", r.RetCode, r.RetMsg)
	}
	if len(r.Result.List) == 0 {
		return nil, nil
	}
	return &r.Result.List[0], nil
}

type positionResp struct {
	RetCode int    `json:"retCode"`
	RetMsg  string `json:"retMsg"`
	Result  struct {
		Category       string         `json:"category"`
		List           []hub.Position `json:"list"`
		NextPageCursor string         `json:"nextPageCursor"`
	} `json:"result"`
}

func (c *RESTClient) Positions(ctx context.Context, category, settleCoin string) ([]hub.Position, error) {
	var all []hub.Position
	cursor := ""
	for {
		p := url.Values{}
		p.Set("category", category)
		p.Set("settleCoin", settleCoin)
		p.Set("limit", "200")
		if cursor != "" {
			p.Set("cursor", cursor)
		}
		body, err := c.signedGET(ctx, "/v5/position/list", p)
		if err != nil {
			return nil, err
		}
		var r positionResp
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, err
		}
		if r.RetCode != 0 {
			return nil, fmt.Errorf("bybit positions retCode=%d msg=%s", r.RetCode, r.RetMsg)
		}
		for i := range r.Result.List {
			if r.Result.List[i].Category == "" {
				r.Result.List[i].Category = r.Result.Category
			}
		}
		all = append(all, r.Result.List...)
		if r.Result.NextPageCursor == "" {
			break
		}
		cursor = r.Result.NextPageCursor
	}
	return all, nil
}
