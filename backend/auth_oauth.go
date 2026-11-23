package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const authOAuthLifetime = 15 * time.Minute

type authOAuthProvider struct {
	clientID, clientSecret string
	authorize, token, user string
	scope                  string
}

func authProvider(name string) authOAuthProvider {
	var provider authOAuthProvider
	switch name {
	case "github":
		provider = authOAuthProvider{
			authorize: "https://github.com/login/oauth/authorize",
			token:     "https://github.com/login/oauth/access_token",
			user:      "https://api.github.com/user",
			scope:     "read:user user:email",
		}
	case "google":
		provider = authOAuthProvider{
			authorize: "https://accounts.google.com/o/oauth2/v2/auth",
			token:     "https://oauth2.googleapis.com/token",
			user:      "https://www.googleapis.com/oauth2/v3/userinfo",
			scope:     "openid profile email",
		}
	default:
		return provider
	}
	prefix := "OAUTH_" + strings.ToUpper(name) + "_CLIENT_"
	provider.clientID = os.Getenv(prefix + "ID")
	provider.clientSecret = os.Getenv(prefix + "SECRET")
	return provider
}

func (p authOAuthProvider) enabled() bool {
	return strings.TrimSpace(p.clientID) != "" && strings.TrimSpace(p.clientSecret) != ""
}

func (s *Server) authProviders(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, map[string]bool{
		"github": authProvider("github").enabled(), "google": authProvider("google").enabled(),
	})
}

func authOAuthPath(name string) string {
	return "/api/_auth/" + name + "/callback"
}

type authOAuthState struct {
	Challenge string `json:"challenge"`
	ExpiresAt int64  `json:"expiresAt"`
}

func authOAuthChallenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func (s *Server) authOAuthCookie(r *http.Request, name, challenge string) *http.Cookie {
	cookie := &http.Cookie{
		Name: "auth-oauth-" + name + "-" + challenge[:22], Path: authOAuthPath(name),
		HttpOnly: true, Secure: s.authSecureCookie(r),
	}
	if !s.Config.Development {
		cookie.SameSite = http.SameSiteNoneMode
	}
	return cookie
}

func (s *Server) authChallenge(w http.ResponseWriter, r *http.Request, name string) {
	provider := authProvider(name)
	if !provider.enabled() {
		jsonResponse(w, http.StatusNotFound, struct{}{})
		return
	}
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		authInternal(w, err)
		return
	}
	verifier := base64.RawURLEncoding.EncodeToString(entropy[:])
	challenge := authOAuthChallenge(verifier)
	expires := time.Now().Add(authOAuthLifetime)
	state, err := s.authSeal("oauth/"+name, authOAuthState{Challenge: challenge, ExpiresAt: expires.Unix()})
	if err != nil {
		authInternal(w, err)
		return
	}
	cookie := s.authOAuthCookie(r, name, challenge)
	cookie.Value = verifier
	cookie.Expires = expires.UTC()
	cookie.MaxAge = int(authOAuthLifetime.Seconds())
	http.SetCookie(w, cookie)
	query := url.Values{
		"response_type": {"code"}, "client_id": {provider.clientID}, "scope": {provider.scope},
		"redirect_uri": {strings.TrimRight(s.Config.BaseURL, "/") + authOAuthPath(name)},
		"state":        {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}
	w.Header().Set("Location", provider.authorize+"?"+query.Encode())
	w.WriteHeader(http.StatusFound)
}

func (s *Server) authCallback(w http.ResponseWriter, r *http.Request, name string) {
	provider := authProvider(name)
	if !provider.enabled() {
		jsonResponse(w, http.StatusNotFound, struct{}{})
		return
	}
	query := r.URL.Query()
	failed := func() { s.authRedirect(w, "/auth?error=oauth_failed") }
	var state authOAuthState
	ok, err := s.authOpen("oauth/"+name, query.Get("state"), &state)
	if err != nil || !ok || state.ExpiresAt <= time.Now().Unix() || len(state.Challenge) != 43 {
		failed()
		return
	}
	correlation := s.authOAuthCookie(r, name, state.Challenge)
	cookie, err := r.Cookie(correlation.Name)
	if err != nil || subtle.ConstantTimeCompare([]byte(authOAuthChallenge(cookie.Value)), []byte(state.Challenge)) != 1 {
		failed()
		return
	}
	authExpireCookie(w, correlation)
	if query.Get("error") == "access_denied" {
		s.authRedirect(w, "/auth")
		return
	}
	code := query.Get("code")
	if code == "" || query.Get("error") != "" {
		failed()
		return
	}
	ctx := r.Context()
	token, err := provider.exchange(ctx, strings.TrimRight(s.Config.BaseURL, "/")+authOAuthPath(name), code, cookie.Value)
	if err == nil {
		var profile authOAuthProfile
		profile, err = provider.profile(ctx, name, token)
		if err == nil {
			var user *authAccount
			user, err = s.authOAuthAccount(ctx, name, profile.subject, profile.name, profile.email)
			if err == nil {
				err = s.authSignIn(w, r, user.Identity, false)
			}
		}
	}
	if err != nil {
		slog.Error("OAuth authentication failed", "provider", name, "error", err)
		failed()
		return
	}
	s.authRedirect(w, "/")
}

var authOAuthHTTP = &http.Client{
	Timeout:       15 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func (p authOAuthProvider) exchange(ctx context.Context, redirectURI, code, verifier string) (string, error) {
	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI},
		"client_id": {p.clientID}, "client_secret": {p.clientSecret}, "code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.token, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var response struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := authOAuthJSON(req, &response); err != nil {
		return "", err
	}
	if response.AccessToken == "" || response.Error != "" {
		return "", errors.New("OAuth token exchange did not return an access token")
	}
	return response.AccessToken, nil
}

type authOAuthProfile struct {
	subject, name, email string
}

type authGitHubEmail struct {
	Email    string `json:"email"`
	Verified bool   `json:"verified"`
	Primary  bool   `json:"primary"`
}

func (p authOAuthProvider) profile(ctx context.Context, name, token string) (authOAuthProfile, error) {
	var profile authOAuthProfile
	if name == "github" {
		var user struct {
			ID    int64  `json:"id"`
			Name  string `json:"name"`
			Login string `json:"login"`
			Email string `json:"email"`
		}
		if err := authOAuthUserJSON(ctx, p.user, token, &user); err != nil {
			return profile, err
		}
		if user.ID <= 0 {
			return profile, errors.New("GitHub profile is missing a user ID")
		}
		profile = authOAuthProfile{strconv.FormatInt(user.ID, 10), user.Name, user.Email}
		if strings.TrimSpace(profile.name) == "" {
			profile.name = user.Login
		}
		if strings.TrimSpace(profile.email) == "" {
			var emails []authGitHubEmail
			if err := authOAuthUserJSON(ctx, p.user+"/emails", token, &emails); err != nil {
				return profile, err
			}
			for _, email := range emails {
				if email.Verified && (profile.email == "" || email.Primary) {
					profile.email = email.Email
					if email.Primary {
						break
					}
				}
			}
		}
	} else {
		var user struct {
			ID       string `json:"sub"`
			Name     string `json:"name"`
			Email    string `json:"email"`
			Verified bool   `json:"email_verified"`
		}
		if err := authOAuthUserJSON(ctx, p.user, token, &user); err != nil {
			return profile, err
		}
		if !user.Verified {
			return profile, errors.New("Google profile email is not verified")
		}
		profile = authOAuthProfile{user.ID, user.Name, user.Email}
	}
	if strings.TrimSpace(profile.subject) == "" || strings.TrimSpace(profile.email) == "" {
		return profile, errors.New("OAuth profile is missing a subject or email")
	}
	return profile, nil
}

func authOAuthUserJSON(ctx context.Context, endpoint, token string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return authOAuthJSON(req, dst)
}

func authOAuthJSON(req *http.Request, dst any) error {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Aptabase")
	response, err := authOAuthHTTP.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("OAuth endpoint %s returned status %d", req.URL.Hostname(), response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(dst)
}
