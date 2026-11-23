package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAuthPasswordHash(t *testing.T) {
	hash, err := authHashPassword("test passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "pbkdf2-sha256$210000$") || !authVerifyPassword("test passphrase", hash) {
		t.Fatal("hash did not round-trip")
	}
	if authVerifyPassword("other passphrase", hash) || authVerifyPassword("test passphrase", "pbkdf2-sha256$0$AA==$AA==") {
		t.Fatal("invalid password or digest was accepted")
	}
}

func TestAuthRequestValidation(t *testing.T) {
	test := func(body string, signup bool) (*authRequest, *httptest.ResponseRecorder) {
		r := httptest.NewRequest(http.MethodPost, "/api/_auth/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		return authReadRequest(w, r, signup), w
	}
	if request, w := test(`{}`, true); request != nil || w.Code != 400 {
		t.Fatal("an invalid registration did not return a validation failure")
	} else {
		var result struct{ Errors map[string][]string }
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Errors["Username"]) != 2 || len(result.Errors["Password"]) != 2 || len(result.Errors["Name"]) != 1 {
			t.Fatalf("unexpected validation failure: %s", w.Body)
		}
	}
	if request, _ := test(`{"name":"  ","USERNAME":"Alice-1","password":"\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00"}`, true); request == nil || *request.Username != "Alice-1" {
		t.Fatal("valid mixed-case and UTF-16-length fields were rejected")
	}
	if request, _ := test(`{"name":123,"username":" Alice-1 ","password":" password "}`, false); request == nil || *request.Password != " password " {
		t.Fatal("login did not ignore unrelated fields or preserve password whitespace")
	}
}

func TestAuthSession(t *testing.T) {
	s := &Server{Config: Config{BaseURL: "https://local.example", AuthSecret: strings.Repeat("k", 32)}}
	r := httptest.NewRequest(http.MethodGet, s.Config.BaseURL+"/api/_auth/me", nil)
	if user, err := s.identity(r); user != nil || err != nil {
		t.Fatalf("missing credential: user=%v, err=%v", user, err)
	}
	w := httptest.NewRecorder()
	if err := s.authSignIn(w, r, Identity{ID: "123", Email: "example@example.test", Role: "admin"}, true); err != nil {
		t.Fatal(err)
	}
	cookie := w.Result().Cookies()[0]
	if cookie.Name != authCookieName || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Expires.IsZero() {
		t.Fatal("unexpected session cookie policy")
	}
	r.AddCookie(cookie)
	user, err := s.identity(r)
	if err != nil || user == nil || user.ID != "123" || user.Email != "example@example.test" || user.Role != "admin" {
		t.Fatalf("session did not round-trip: user=%v, err=%v", user, err)
	}
	for _, value := range []string{authTestAlter(cookie.Value), "chunks-2"} {
		bad := httptest.NewRequest(http.MethodGet, "/", nil)
		bad.AddCookie(&http.Cookie{Name: authCookieName, Value: value})
		if user, err := s.identity(bad); user != nil || err != nil {
			t.Fatal("invalid session was accepted")
		}
	}
	now := time.Now().Unix()
	expired, err := s.authSeal("session", authSessionTicket{Identity: *user, authSession: authSession{IssuedAt: now - 60, ExpiresAt: now - 1}})
	if err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: authCookieName, Value: expired})
	if user, err := s.identity(r); user != nil || err != nil {
		t.Fatal("expired session was accepted")
	}
}

func TestAuthOAuthCorrelation(t *testing.T) {
	t.Setenv("OAUTH_GITHUB_CLIENT_ID", "dummy-github-client")
	t.Setenv("OAUTH_GITHUB_CLIENT_SECRET", "dummy-github-secret")
	t.Setenv("OAUTH_GOOGLE_CLIENT_ID", "dummy-google-client")
	t.Setenv("OAUTH_GOOGLE_CLIENT_SECRET", "dummy-google-secret")
	s := &Server{Config: Config{BaseURL: "https://local.example", AuthSecret: strings.Repeat("k", 32)}}
	mux := http.NewServeMux()
	s.registerAuth(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/_auth/github", nil))
	location, err := url.Parse(w.Header().Get("Location"))
	if w.Code != 302 || err != nil || location.Host != "github.com" || len(w.Result().Cookies()) != 1 {
		t.Fatal("expected an OAuth authorize redirect with correlation cookie")
	}
	cookie := w.Result().Cookies()[0]
	query := location.Query()
	state := query.Get("state")
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteNoneMode || cookie.Path != authOAuthPath("github") ||
		query.Get("code_challenge") != authOAuthChallenge(cookie.Value) || query.Get("code_challenge_method") != "S256" {
		t.Fatal("OAuth correlation policy or PKCE challenge is invalid")
	}
	for _, tc := range []struct {
		provider, state, cookie, redirect string
	}{
		{"github", state, cookie.Value, "/auth"},
		{"github", state, "", "/auth?error=oauth_failed"},
		{"github", state, "wrong-verifier", "/auth?error=oauth_failed"},
		{"github", authTestAlter(state), cookie.Value, "/auth?error=oauth_failed"},
		{"google", state, cookie.Value, "/auth?error=oauth_failed"},
	} {
		r := httptest.NewRequest(http.MethodGet, authOAuthPath(tc.provider)+"?error=access_denied&state="+url.QueryEscape(tc.state), nil)
		if tc.cookie != "" {
			r.AddCookie(&http.Cookie{Name: cookie.Name, Value: tc.cookie})
		}
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 302 || w.Header().Get("Location") != s.Config.BaseURL+tc.redirect {
			t.Errorf("%s access_denied callback: code=%d location=%s", tc.provider, w.Code, w.Header().Get("Location"))
		}
	}
}

func authTestAlter(value string) string {
	pos := len(value) / 2
	replacement := 'A'
	if value[pos] == 'A' {
		replacement = 'B'
	}
	return value[:pos] + string(replacement) + value[pos+1:]
}

// Run only against the isolated, migrated ASP.NET reference service and its PostgreSQL database.
func TestAuthDotNetReference(t *testing.T) {
	reference := os.Getenv("AUTH_TEST_DOTNET_URL")
	if reference == "" {
		t.Skip("set AUTH_TEST_DOTNET_URL to the isolated reference service")
	}
	if os.Getenv("ASPNET_DATA_PROTECTION_KEY_DIR") == "" {
		t.Fatal("set ASPNET_DATA_PROTECTION_KEY_DIR to the isolated reference key ring")
	}
	config, err := loadConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", config.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &Server{Config: config, DB: db}
	mux := http.NewServeMux()
	s.registerAuth(mux)
	client := &http.Client{Timeout: 10 * time.Second}
	const password = "reference-test-password"
	username := "ref-auth-" + strings.ToLower(newID())
	credentials := fmt.Sprintf(`{"name":"Reference Test","username":%q,"password":%q}`, username, password)
	oldRequest := func(method, path, body string, cookie *http.Cookie) (status int, data []byte, cookies []*http.Cookie) {
		t.Helper()
		r, err := http.NewRequestWithContext(context.Background(), method, reference+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err = io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, data, response.Cookies()
	}
	status, _, cookies := oldRequest(http.MethodPost, "/api/_auth/register", credentials, nil)
	if status != 200 || len(cookies) != 1 || cookies[0].Name != authCookieName || !strings.HasPrefix(cookies[0].Value, "CfDJ8") {
		t.Fatalf("reference did not issue an ASP.NET session: status=%d", status)
	}
	legacy := cookies[0]
	defer oldRequest(http.MethodPost, "/api/_auth/account/delete", "", legacy)
	status, oldMe, _ := oldRequest(http.MethodGet, "/api/_auth/me", "", legacy)
	if status != 200 {
		t.Fatalf("reference rejected its own cookie: status=%d", status)
	}
	goRequest := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	assertSameAccount := func(w *httptest.ResponseRecorder) {
		t.Helper()
		if w.Code != 200 {
			t.Fatalf("Go rejected the reference account: status=%d body=%s", w.Code, w.Body)
		}
		var oldAccount, goAccount map[string]any
		if json.Unmarshal(oldMe, &oldAccount) != nil || json.Unmarshal(w.Body.Bytes(), &goAccount) != nil || !reflect.DeepEqual(oldAccount, goAccount) {
			t.Fatalf("account JSON differs: .NET=%s Go=%s", oldMe, w.Body)
		}
	}
	w := goRequest(http.MethodGet, "/api/_auth/me", "", legacy)
	assertSameAccount(w)
	migrated := w.Result().Cookies()
	if len(migrated) != 1 || !strings.HasPrefix(migrated[0].Value, "v1.") || !migrated[0].Expires.Equal(legacy.Expires) {
		t.Fatal("legacy cookie was not migrated with the same expiry")
	}
	assertSameAccount(goRequest(http.MethodGet, "/api/_auth/me", "", migrated[0]))
	assertSameAccount(goRequest(http.MethodPost, "/api/_auth/login", credentials, nil))
	if response := goRequest(http.MethodPost, "/api/_auth/register", credentials, nil); response.Code != 409 {
		t.Fatalf("existing username was not rejected: status=%d", response.Code)
	}
	bad := *legacy
	bad.Value = authTestAlter(legacy.Value)
	if response := goRequest(http.MethodGet, "/api/_auth/me", "", &bad); response.Code != 401 {
		t.Fatalf("modified legacy cookie was accepted: status=%d", response.Code)
	}
	appName := os.Getenv("ASPNET_DATA_PROTECTION_APP_NAME")
	t.Setenv("ASPNET_DATA_PROTECTION_APP_NAME", "/wrong-app/")
	if response := goRequest(http.MethodGet, "/api/_auth/me", "", legacy); response.Code != 401 {
		t.Fatalf("legacy cookie for a different app was accepted: status=%d", response.Code)
	}
	t.Setenv("ASPNET_DATA_PROTECTION_APP_NAME", appName)
	t.Run("SSMLegacyCookie", func(t *testing.T) { testAuthSSMRealCookie(t, legacy) })
	assertSameAccount(goRequest(http.MethodPost, "/api/_auth/account/delete", "", migrated[0]))
	if response := goRequest(http.MethodGet, "/api/_auth/me", "", migrated[0]); response.Code != 404 {
		t.Fatalf("deleted account was not removed: status=%d", response.Code)
	}
	goUsername := "go-AUTH-" + newID()
	goCredentials := fmt.Sprintf(`{"name":"  ","username":%q,"password":%q}`, goUsername, password)
	registered := goRequest(http.MethodPost, "/api/_auth/register", goCredentials, nil)
	goCookies := registered.Result().Cookies()
	if registered.Code != 200 || len(goCookies) != 1 {
		t.Fatalf("Go registration failed: status=%d", registered.Code)
	}
	defer goRequest(http.MethodPost, "/api/_auth/account/delete", "", goCookies[0])
	var goAccount authAccount
	if err := json.Unmarshal(registered.Body.Bytes(), &goAccount); err != nil {
		t.Fatal(err)
	}
	if goAccount.Username != strings.ToLower(goUsername) || goAccount.Name != goAccount.Username ||
		(goAccount.Role != "admin" && goAccount.Role != "user") {
		t.Fatal("unexpected registration name, username or role")
	}
	status, _, _ = oldRequest(http.MethodPost, "/api/_auth/login", goCredentials, nil)
	if status != 200 {
		t.Fatalf(".NET rejected the Go password digest: status=%d", status)
	}
	t.Run("OAuth", func(t *testing.T) { testAuthOAuthFlow(t, s, mux) })
}

type authTestRoundTripper func(*http.Request) (*http.Response, error)

func (f authTestRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testAuthOAuthFlow(t *testing.T, s *Server, mux *http.ServeMux) {
	for _, name := range []string{"GITHUB", "GOOGLE"} {
		t.Setenv("OAUTH_"+name+"_CLIENT_ID", "dummy-client")
		t.Setenv("OAUTH_"+name+"_CLIENT_SECRET", "dummy-secret")
	}
	email := "OAUTH-" + strings.ToLower(newID()) + "@example.test"
	googleSubject := "g-" + newID()
	var verifier string
	var googleVerified bool
	oldClient := authOAuthHTTP
	t.Cleanup(func() { authOAuthHTTP = oldClient })
	mockClient := *authOAuthHTTP
	mockClient.Transport = authTestRoundTripper(func(r *http.Request) (*http.Response, error) {
		var body string
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				return nil, err
			}
			provider := "google"
			if r.URL.Host == "github.com" {
				provider = "github"
			}
			if r.Form.Get("code_verifier") != verifier || r.Form.Get("code") != "mock-code" ||
				r.Form.Get("redirect_uri") != s.Config.BaseURL+authOAuthPath(provider) || r.Form.Get("client_secret") != "dummy-secret" {
				return nil, fmt.Errorf("incorrect token exchange parameters")
			}
			body = `{"access_token":"mock-token"}`
		} else {
			if r.Header.Get("Authorization") != "Bearer mock-token" {
				return nil, fmt.Errorf("incorrect authorization header")
			}
			switch r.URL.Host + r.URL.Path {
			case "api.github.com/user":
				body = fmt.Sprintf(`{"id":%d,"name":null,"login":"Mock GitHub","email":null}`, time.Now().UnixNano())
			case "api.github.com/user/emails":
				body = fmt.Sprintf(`[{"email":"unverified@example.test","primary":true},
					{"email":"secondary@example.test","verified":true},{"email":%q,"verified":true,"primary":true}]`, email)
			case "www.googleapis.com/oauth2/v3/userinfo":
				body = fmt.Sprintf(`{"sub":%q,"name":"Mock Google","email":%q,"email_verified":%t}`, googleSubject, email, googleVerified)
			default:
				return nil, fmt.Errorf("unexpected OAuth user endpoint")
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	authOAuthHTTP = &mockClient
	complete := func(provider string, want string) *http.Cookie {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/_auth/"+provider, nil))
		target, err := url.Parse(w.Header().Get("Location"))
		if err != nil || w.Code != 302 || len(w.Result().Cookies()) != 1 {
			t.Fatalf("OAuth challenge failed: status=%d", w.Code)
		}
		correlation := w.Result().Cookies()[0]
		verifier = correlation.Value
		r := httptest.NewRequest(http.MethodGet, authOAuthPath(provider)+"?code=mock-code&state="+url.QueryEscape(target.Query().Get("state")), nil)
		r.AddCookie(correlation)
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 302 || w.Header().Get("Location") != s.Config.BaseURL+want {
			t.Fatalf("OAuth callback failed: status=%d location=%s", w.Code, w.Header().Get("Location"))
		}
		for _, cookie := range w.Result().Cookies() {
			if cookie.Name == authCookieName {
				return cookie
			}
		}
		return nil
	}
	github := complete("github", "/")
	if github == nil {
		t.Fatal("GitHub callback did not issue a session")
	}
	t.Cleanup(func() {
		r := httptest.NewRequest(http.MethodPost, "/api/_auth/account/delete", nil)
		r.AddCookie(github)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Errorf("clean up OAuth account: status=%d", w.Code)
		}
	})
	if complete("google", "/auth?error=oauth_failed") != nil {
		t.Fatal("unverified Google email issued a session")
	}
	googleVerified = true
	google := complete("google", "/")
	if google == nil {
		t.Fatal("Google callback did not issue a session")
	}
	identity := func(cookie *http.Cookie) *Identity {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/_auth/me", nil)
		r.AddCookie(cookie)
		user, err := s.identity(r)
		if user == nil || err != nil {
			t.Fatalf("invalid OAuth session: %v", err)
		}
		return user
	}
	gh, g := identity(github), identity(google)
	if gh.ID != g.ID || gh.Email != strings.ToLower(email) || gh.Name != "Mock GitHub" || g.Username != authUsernameFromEmail(email) {
		t.Fatal("OAuth accounts did not link by verified email or preserve the existing display name")
	}
}
