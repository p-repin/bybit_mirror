package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const cookieName = "bybit_session"

type Manager struct {
	passwordHash []byte
	secret       []byte
	ttl          time.Duration
	secure       bool
}

func New(passwordHash, sessionSecret string, ttl time.Duration, secure bool) (*Manager, error) {
	if len(passwordHash) == 0 {
		return nil, errors.New("empty password hash")
	}
	if len(sessionSecret) < 16 {
		return nil, errors.New("session_secret too short")
	}
	return &Manager{
		passwordHash: []byte(passwordHash),
		secret:       []byte(sessionSecret),
		ttl:          ttl,
		secure:       secure,
	}, nil
}

func (m *Manager) Verify(password string) bool {
	return bcrypt.CompareHashAndPassword(m.passwordHash, []byte(password)) == nil
}

func (m *Manager) IssueCookie(w http.ResponseWriter) error {
	sid := make([]byte, 16)
	if _, err := rand.Read(sid); err != nil {
		return err
	}
	expires := time.Now().Add(m.ttl).Unix()
	payload := hex.EncodeToString(sid) + "." + strconv.FormatInt(expires, 10)
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(payload))
	value := payload + "." + hex.EncodeToString(mac.Sum(nil))
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    value,
		Path:     "/",
		Expires:  time.Unix(expires, 0),
		MaxAge:   int(m.ttl.Seconds()),
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (m *Manager) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (m *Manager) Validate(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 3 {
		return false
	}
	payload := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(parts[2]), []byte(expected)) {
		return false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	return true
}

func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.Validate(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
