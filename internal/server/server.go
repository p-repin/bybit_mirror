package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/p-repin/bybit_mirror/internal/auth"
	"github.com/p-repin/bybit_mirror/internal/hub"
)

type Server struct {
	auth *auth.Manager
	hub  *hub.Hub
}

func New(a *auth.Manager, h *hub.Hub) *Server {
	return &Server{auth: a, hub: h}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.Handle("GET /api/snapshot", s.auth.Middleware(http.HandlerFunc(s.handleSnapshot)))
	mux.Handle("GET /api/ws", s.auth.Middleware(http.HandlerFunc(s.handleWS)))
	return logRequest(mux)
}

func logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Info("http", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !s.auth.Verify(body.Password) {
		http.Error(w, "invalid password", http.StatusUnauthorized)
		return
	}
	if err := s.auth.IssueCookie(w); err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.auth.ClearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.hub.Snapshot())
}
