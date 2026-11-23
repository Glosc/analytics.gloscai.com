package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	Config     Config
	DB         *sql.DB
	Analytics  *Analytics
	Ingest     *Ingestion
	limiter    rateLimiter
	rejections rejectionStats
}

func newID() string {
	const chars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	var id [22]byte
	var random [32]byte
	for pos := 0; pos < len(id); {
		rand.Read(random[:])
		for _, n := range random {
			if n >= 248 {
				continue
			}
			id[pos] = chars[int(n)%len(chars)]
			pos++
			if pos == len(id) {
				break
			}
		}
	}
	return string(id[:])
}
func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("write response", "error", err)
	}
}
func readJSON(r *http.Request, v any) error {
	d := json.NewDecoder(r.Body)
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}
func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) *Identity {
	user, err := s.identity(r)
	if err != nil {
		slog.Error("read session", "error", err)
		w.WriteHeader(500)
		return nil
	}
	if user == nil {
		w.WriteHeader(401)
	} else {
		s.refreshAuthSession(w, r, user)
	}
	return user
}
func clientIP(r *http.Request) string {
	for _, k := range []string{"Cf-Connecting-Ip", "X-Real-Ip", "X-Forwarded-For"} {
		if v := r.Header.Get(k); v != "" {
			return v
		}
	}
	if values := r.Header.Values("CloudFront-Viewer-Address"); len(values) > 0 {
		// Preserve the original address spelling (including IPv6 brackets) for daily user IDs.
		address := values[0]
		if port := strings.LastIndexByte(address, ':'); port >= 0 {
			return address[:port]
		}
		return address
	}
	v := r.RemoteAddr
	host, _, err := net.SplitHostPort(v)
	if err != nil {
		return v
	}
	return host
}

type rateWindow struct {
	end   time.Time
	count int
}
type rateLimiter struct {
	mu        sync.Mutex
	nextSweep time.Time
	windows   map[string]rateWindow
}

func (s *Server) limit(w http.ResponseWriter, r *http.Request, policy string) bool {
	max, duration := 0, time.Second
	switch policy {
	case "SignUp":
		max, duration = 4, time.Hour
		if v, e := strconv.Atoi(os.Getenv("SIGNUP_RATE_LIMIT")); e == nil && v > 0 {
			max = v
		}
	case "SignIn":
		max, duration = 10, 5*time.Minute
	case "Stats":
		max, duration = 1000, time.Hour
	case "EventIngestion":
		max = 20
	case "FeatureFlags":
		max = 10
	default:
		panic("unknown rate limit policy: " + policy)
	}
	now := time.Now()
	l := &s.limiter
	l.mu.Lock()
	if l.windows == nil {
		l.windows = make(map[string]rateWindow)
	}
	if !now.Before(l.nextSweep) {
		for k, v := range l.windows {
			if !now.Before(v.end) {
				delete(l.windows, k)
			}
		}
		l.nextSweep = now.Add(time.Minute)
	}
	key := policy + "/" + clientIP(r)
	slot := l.windows[key]
	if !now.Before(slot.end) {
		slot = rateWindow{end: now.Add(duration)}
	}
	ok := slot.count < max
	if ok {
		slot.count++
		l.windows[key] = slot
	}
	l.mu.Unlock()
	if !ok {
		s.rejections.record("rate_limit", r)
		w.WriteHeader(http.StatusTooManyRequests)
	}
	return ok
}
func sdkPath(p string) bool {
	return p == "/api/v0/event" || p == "/api/v0/events" || p == "/api/v0/error" || p == "/api/v0/feature-flags" || strings.HasPrefix(p, "/api/v0/feature-flags/")
}
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	s.registerAuth(mux)
	s.registerApps(mux)
	s.registerIngestion(mux)
	s.registerStats(mux)
	s.registerBilling(mux)
	health := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store, no-cache")
		io.WriteString(w, "Healthy")
	}
	healthMux := http.NewServeMux()
	healthMux.Handle("/", mux)
	healthMux.HandleFunc("/healthz", health)
	if s.Config.Development {
		healthMux.HandleFunc("/health", health)
		healthMux.HandleFunc("/alive", health)
	}
	if !s.Config.Development {
		mux.HandleFunc("GET /", s.static)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.Config.Development {
			w.Header().Set("Strict-Transport-Security", "max-age=2592000")
		}
		p := r.URL.Path
		origin := r.Header.Get("Origin")
		if origin != "" {
			switch {
			case sdkPath(p):
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Header().Set("Access-Control-Allow-Headers", "content-type, app-key")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			case (p == "/api/_auth/me" || p == "/api/_auth/account/delete") && origin == "https://aptabase.com":
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Add("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET")
			}
			if r.Method == http.MethodOptions && w.Header().Get("Access-Control-Allow-Origin") != "" && r.Header.Get("Access-Control-Request-Method") != "" {
				w.Header().Set("Access-Control-Max-Age", "3600")
				w.WriteHeader(204)
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, 30_000_000)
		if strings.HasPrefix(p, "/api/_") {
			w.Header().Set("Cache-Control", "no-store")
		}
		healthMux.ServeHTTP(w, r)
	})
}
func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if strings.HasPrefix(p, "api/") || strings.HasPrefix(p, "webhook/") || strings.HasPrefix(p, "uploads/") {
		http.NotFound(w, r)
		return
	}
	root := os.DirFS(s.Config.WebDir)
	if p == "" {
		p = "index.html"
	}
	f, err := root.Open(p)
	if err != nil && path.Ext(p) == "" {
		p = "index.html"
		f, err = root.Open(p)
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	if p == "index.html" {
		w.Header().Set("Cache-Control", "no-store,no-cache")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Content-Security-Policy", "default-src 'self' 'unsafe-inline'; img-src 'self' data: https:; style-src 'self' 'unsafe-inline' https://client.crisp.chat; script-src 'self' 'unsafe-inline' https://client.crisp.chat; font-src 'self' https://client.crisp.chat; connect-src 'self' https://raw.githubusercontent.com wss://client.relay.crisp.chat https://client.crisp.chat;")
	} else if strings.HasPrefix(p, "assets/") {
		w.Header().Set("Cache-Control", "public,max-age=31536000,immutable")
	}
	http.ServeContent(w, r, p, st.ModTime(), f.(io.ReadSeeker))
}
