package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	authCookieName      = "auth-session"
	authSessionDuration = 365 * 24 * time.Hour
	authMaxCookieSize   = 32 << 10
)

type authSession struct {
	IssuedAt   int64 `json:"issuedAt"`
	ExpiresAt  int64 `json:"expiresAt"`
	Persistent bool  `json:"persistent"`
	legacy     bool
}

type authSessionTicket struct {
	Identity
	authSession
}

func (s *Server) authCipher(purpose string) (cipher.AEAD, error) {
	secret := strings.TrimSpace(s.Config.AuthSecret)
	if len(secret) < 32 {
		return nil, errors.New("AUTH_SECRET must be at least 32 bytes")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("aptabase/auth/v1/" + purpose))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *Server) authSeal(purpose string, value any) (string, error) {
	aead, err := s.authCipher(purpose)
	if err != nil {
		return "", err
	}
	plaintext, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	payload := aead.Seal(nonce, nonce, plaintext, nil)
	return "v1." + base64.RawURLEncoding.EncodeToString(payload), nil
}

func (s *Server) authOpen(purpose, value string, dst any) (bool, error) {
	encoded, ok := strings.CutPrefix(value, "v1.")
	if !ok || len(value) > authMaxCookieSize {
		return false, nil
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return false, nil
	}
	aead, err := s.authCipher(purpose)
	if err != nil {
		return false, err
	}
	if len(payload) < aead.NonceSize()+aead.Overhead() {
		return false, nil
	}
	plaintext, err := aead.Open(nil, payload[:aead.NonceSize()], payload[aead.NonceSize():], nil)
	return err == nil && json.Unmarshal(plaintext, dst) == nil, nil
}

func (s *Server) authSignIn(w http.ResponseWriter, r *http.Request, identity Identity, persistent bool) error {
	now := time.Now().UTC()
	return s.authWriteSession(w, r, authSessionTicket{
		Identity: identity,
		authSession: authSession{
			IssuedAt: now.Unix(), ExpiresAt: now.Add(authSessionDuration).Unix(), Persistent: persistent,
		},
	})
}

func (s *Server) authWriteSession(w http.ResponseWriter, r *http.Request, ticket authSessionTicket) error {
	value, err := s.authSeal("session", ticket)
	if err != nil {
		return err
	}
	s.authClearChunks(w, r)
	cookie := s.authSessionCookie(r, authCookieName)
	cookie.Value = value
	cookie.MaxAge = max(1, int(ticket.ExpiresAt-time.Now().Unix()))
	if ticket.Persistent {
		cookie.Expires = time.Unix(ticket.ExpiresAt, 0).UTC()
	}
	http.SetCookie(w, cookie)
	return nil
}

func (s *Server) identity(r *http.Request) (*Identity, error) {
	value := authReadCookie(r)
	if value == "" {
		return nil, nil
	}
	var ticket authSessionTicket
	if strings.HasPrefix(value, "v1.") {
		ok, err := s.authOpen("session", value, &ticket)
		if !ok || err != nil {
			return nil, err
		}
	} else {
		legacy, err := readLegacyAuthSession(r.Context(), value, s.Config.Region, &authManagedLegacyKeys)
		if legacy == nil || err != nil {
			return nil, err
		}
		ticket = *legacy
	}
	now := time.Now().Unix()
	if ticket.ID == "" || ticket.IssuedAt > now || ticket.ExpiresAt <= now {
		return nil, nil
	}
	// ASP.NET OAuth tickets only contained id, name and email.
	if ticket.legacy && s.DB != nil && (ticket.Role == "" || ticket.Username == "") {
		account, err := s.authAccountByID(r.Context(), ticket.ID)
		if err != nil {
			return nil, err
		}
		if account != nil {
			ticket.Role, ticket.Username = account.Role, account.Username
		}
	}
	ticket.Identity.session = ticket.authSession
	return &ticket.Identity, nil
}

func (s *Server) refreshAuthSession(w http.ResponseWriter, r *http.Request, identity *Identity) {
	if identity == nil || identity.session.ExpiresAt == 0 {
		return
	}
	session := identity.session
	now := time.Now().Unix()
	var err error
	if now-session.IssuedAt > session.ExpiresAt-now {
		err = s.authSignIn(w, r, *identity, session.Persistent)
	} else if session.legacy {
		// Exchange an ASP.NET session for a Go session without extending its lifetime.
		err = s.authWriteSession(w, r, authSessionTicket{Identity: *identity, authSession: session})
	}
	if err != nil {
		slog.Error("could not refresh auth session", "error", err)
	}
}

func (s *Server) authSecureCookie(r *http.Request) bool {
	base, _ := url.Parse(s.Config.BaseURL)
	return !s.Config.Development || r.TLS != nil || (base != nil && base.Scheme == "https")
}

func (s *Server) authSessionCookie(r *http.Request, name string) *http.Cookie {
	return &http.Cookie{
		Name: name, Path: "/", HttpOnly: true, Secure: s.authSecureCookie(r), SameSite: http.SameSiteStrictMode,
	}
}

func authReadCookie(r *http.Request) string {
	cookie, err := r.Cookie(authCookieName)
	if err != nil || len(cookie.Value) > authMaxCookieSize {
		return ""
	}
	count, ok := authChunkCount(cookie.Value)
	if !ok {
		return cookie.Value
	}
	var value strings.Builder
	for i := 1; i <= count; i++ {
		chunk, err := r.Cookie(authCookieName + "C" + strconv.Itoa(i))
		if err != nil || value.Len()+len(chunk.Value) > authMaxCookieSize {
			return ""
		}
		value.WriteString(chunk.Value)
	}
	return value.String()
}

func authChunkCount(marker string) (int, bool) {
	count, ok := strings.CutPrefix(marker, "chunks-")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(count)
	return n, err == nil && n > 0 && n <= 16
}

func (s *Server) authClearChunks(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(authCookieName)
	if err != nil {
		return
	}
	if count, ok := authChunkCount(cookie.Value); ok {
		for i := 1; i <= count; i++ {
			authExpireCookie(w, s.authSessionCookie(r, authCookieName+"C"+strconv.Itoa(i)))
		}
	}
}

func authExpireCookie(w http.ResponseWriter, cookie *http.Cookie) {
	cookie.Expires = time.Unix(0, 0).UTC()
	cookie.MaxAge = -1
	http.SetCookie(w, cookie)
}

func (s *Server) authSignOut(w http.ResponseWriter, r *http.Request) {
	s.authClearChunks(w, r)
	authExpireCookie(w, s.authSessionCookie(r, authCookieName))
	s.authRedirect(w, "/auth")
}

func (s *Server) authRedirect(w http.ResponseWriter, suffix string) {
	w.Header().Set("Location", strings.TrimRight(s.Config.BaseURL, "/")+suffix)
	w.WriteHeader(http.StatusFound)
}
