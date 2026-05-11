package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

type AccountType string

const (
	AccountUnified AccountType = "unified"
	AccountClassic AccountType = "classic"
)

type Environment string

const (
	EnvMainnet Environment = "mainnet"
	EnvTestnet Environment = "testnet"
	EnvDemo    Environment = "demo"
)

type Config struct {
	ListenAddr      string      `json:"listen_addr"`
	Environment     Environment `json:"environment"`
	Region          string      `json:"region,omitempty"`
	AccountType     AccountType `json:"account_type"`
	APIKey          string      `json:"api_key"`
	APISecret       string      `json:"api_secret"`
	PasswordHash    string      `json:"password_hash"`
	SessionSecret   string      `json:"session_secret"`
	SessionTTLHours int         `json:"session_ttl_hours"`
	CookieInsecure  bool        `json:"cookie_insecure,omitempty"`
}

func (c *Config) SessionTTL() time.Duration {
	if c.SessionTTLHours <= 0 {
		return 24 * time.Hour
	}
	return time.Duration(c.SessionTTLHours) * time.Hour
}

func (c *Config) bybitHost() string {
	if c.Region == "" {
		return "bybit.com"
	}
	return "bybit." + c.Region
}

func (c *Config) BybitRESTURL() string {
	host := c.bybitHost()
	switch c.Environment {
	case EnvTestnet:
		return "https://api-testnet." + host
	case EnvDemo:
		return "https://api-demo." + host
	default:
		return "https://api." + host
	}
}

func (c *Config) BybitWSURL() string {
	host := c.bybitHost()
	switch c.Environment {
	case EnvTestnet:
		return "wss://stream-testnet." + host + "/v5/private"
	case EnvDemo:
		return "wss://stream-demo." + host + "/v5/private"
	default:
		return "wss://stream." + host + "/v5/private"
	}
}

// BybitPublicLinearWSURL — поток public/linear для тиков markPrice.
// Demo trading на Bybit делит public стрим с mainnet, отдельного demo-public
// не существует; testnet же имеет свой stream-testnet.
func (c *Config) BybitPublicLinearWSURL() string {
	host := c.bybitHost()
	if c.Environment == EnvTestnet {
		return "wss://stream-testnet." + host + "/v5/public/linear"
	}
	return "wss://stream." + host + "/v5/public/linear"
}

// BybitPublicOptionWSURL — поток public/option для тиков markPrice по опциям.
// Опции живут на отдельном endpoint'е от linear (у Bybit раздельные feed'ы
// по категориям); demo/testnet — как у linear.
func (c *Config) BybitPublicOptionWSURL() string {
	host := c.bybitHost()
	if c.Environment == EnvTestnet {
		return "wss://stream-testnet." + host + "/v5/public/option"
	}
	return "wss://stream." + host + "/v5/public/option"
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	switch c.AccountType {
	case "":
		c.AccountType = AccountUnified
	case AccountUnified, AccountClassic:
	default:
		return fmt.Errorf("invalid account_type %q (use unified or classic)", c.AccountType)
	}
	switch c.Environment {
	case "":
		c.Environment = EnvTestnet
	case EnvMainnet, EnvTestnet, EnvDemo:
	default:
		return fmt.Errorf("invalid environment %q (use mainnet, testnet or demo)", c.Environment)
	}
	if c.ListenAddr == "" {
		c.ListenAddr = ":8080"
	}
	if c.APIKey == "" || c.APISecret == "" {
		return errors.New("api_key and api_secret are required")
	}
	if c.PasswordHash == "" {
		return errors.New("password_hash is required (use cmd/genpass to create one)")
	}
	if len(c.SessionSecret) < 16 {
		return errors.New("session_secret must be at least 16 chars")
	}
	return nil
}