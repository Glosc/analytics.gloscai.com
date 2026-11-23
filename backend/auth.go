package main

import (
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

type Identity struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	session  authSession
}

type authAccount struct {
	Identity
	AvatarURL  string  `json:"avatarUrl"`
	LockReason *string `json:"lockReason"`
}

func (s *Server) registerAuth(mux *http.ServeMux) {
	register := func(pattern string, handler http.HandlerFunc) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			authNoCache(w)
			handler(w, r)
		})
	}
	mux.HandleFunc("POST /api/_auth/register", s.authRegister)
	mux.HandleFunc("POST /api/_auth/login", s.authLogin)
	register("GET /api/_auth/providers", s.authProviders)
	register("GET /api/_auth/me", s.authMe)
	register("POST /api/_auth/account/delete", s.authDelete)
	mux.HandleFunc("OPTIONS /api/_auth/me", authPreflight)
	mux.HandleFunc("OPTIONS /api/_auth/account/delete", authPreflight)
	register("POST /api/_auth/signout", s.authSignOut)
	for _, name := range []string{"github", "google"} {
		path := "/api/_auth/" + name
		register("GET "+path, func(w http.ResponseWriter, r *http.Request) { s.authChallenge(w, r, name) })
		register("GET "+path+"/callback", func(w http.ResponseWriter, r *http.Request) { s.authCallback(w, r, name) })
	}
}

func authNoCache(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store,no-cache")
	w.Header().Set("Pragma", "no-cache")
}

type authCredentials struct {
	Username *string `json:"username"`
	Password *string `json:"password"`
}

type authRequest struct {
	Name *string `json:"name"`
	authCredentials
}

var authUsernamePattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

func authReadRequest(w http.ResponseWriter, r *http.Request, signup bool) *authRequest {
	if r.ContentLength != 0 {
		mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mediaType != "application/json" && mediaType != "text/json" &&
			!(strings.HasPrefix(mediaType, "application/") && strings.HasSuffix(mediaType, "+json")) {
			authProblem(w, http.StatusUnsupportedMediaType, nil)
			return nil
		}
	}
	var name, username, password string
	credentials := &authCredentials{Username: &username, Password: &password}
	request := &authRequest{Name: &name, authCredentials: *credentials}
	var err error
	if signup {
		err = readJSON(r, &request)
	} else {
		err = readJSON(r, &credentials)
		if credentials == nil {
			request = nil
		} else {
			request.authCredentials = *credentials
		}
	}
	if err != nil || request == nil {
		errs := map[string][]string{"$": {"The JSON request body is invalid."}, "body": {"The body field is required."}}
		if r.ContentLength == 0 || (err == nil && request == nil) {
			delete(errs, "$")
			errs[""] = []string{"A non-empty request body is required."}
		}
		authProblem(w, http.StatusBadRequest, errs)
		return nil
	}
	errs := make(map[string][]string)
	if request.Username == nil || strings.TrimSpace(*request.Username) == "" {
		errs["Username"] = []string{"The Username field is required."}
	}
	if signup && request.Username != nil {
		if n := authUTF16Len(*request.Username); n < 3 || n > 40 {
			errs["Username"] = append(errs["Username"], "Username must be between 3 and 40 characters.")
		}
		if *request.Username != "" && !authUsernamePattern.MatchString(*request.Username) {
			errs["Username"] = append(errs["Username"], "Username may only contain letters, numbers, dots, dashes and underscores.")
		}
	}
	if request.Password == nil || strings.TrimSpace(*request.Password) == "" {
		errs["Password"] = []string{"The Password field is required."}
	}
	if signup && request.Password != nil {
		if n := authUTF16Len(*request.Password); n < 8 || n > 128 {
			errs["Password"] = append(errs["Password"], "Password must be at least 8 characters.")
		}
	}
	if signup {
		if request.Name == nil {
			errs["Name"] = []string{"The Name field is required."}
		} else if n := authUTF16Len(*request.Name); n < 2 || n > 40 {
			errs["Name"] = []string{"The field Name must be a string with a minimum length of 2 and a maximum length of 40."}
		}
	}
	if len(errs) != 0 {
		authProblem(w, http.StatusBadRequest, errs)
		return nil
	}
	return request
}

func authUTF16Len(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xffff {
			n++
		}
	}
	return n
}

func authProblem(w http.ResponseWriter, status int, validationErrors map[string][]string) {
	var section, title string
	switch status {
	case http.StatusBadRequest:
		section, title = "15.5.1", "One or more validation errors occurred."
	case http.StatusUnauthorized:
		section, title = "15.5.2", "Unauthorized"
	case http.StatusNotFound:
		section, title = "15.5.5", "Not Found"
	case http.StatusUnsupportedMediaType:
		section, title = "15.5.16", "Unsupported Media Type"
	}
	var trace [24]byte
	_, _ = rand.Read(trace[:])
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Type    string              `json:"type"`
		Title   string              `json:"title"`
		Status  int                 `json:"status"`
		Errors  map[string][]string `json:"errors,omitempty"`
		TraceID string              `json:"traceId"`
	}{"https://tools.ietf.org/html/rfc9110#section-" + section, title, status, validationErrors, fmt.Sprintf("00-%x-%x-01", trace[:16], trace[16:])})
}

func authInternal(w http.ResponseWriter, err error) {
	slog.Error("authentication request failed", "error", err)
	w.WriteHeader(http.StatusInternalServerError)
}

func authCORS(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") == "https://aptabase.com" {
		w.Header().Set("Access-Control-Allow-Origin", "https://aptabase.com")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
}

func authPreflight(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") == "https://aptabase.com" {
		authCORS(w, r)
		w.Header().Set("Access-Control-Allow-Methods", "GET")
		w.Header().Set("Access-Control-Max-Age", "3600")
	}
	w.WriteHeader(http.StatusNoContent)
}

const authPasswordIterations = 210_000

const authDummyHash = "pbkdf2-sha256$210000$AAAAAAAAAAAAAAAAAAAAAA==$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func authHashPassword(password string) (string, error) {
	var salt [16]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt[:], authPasswordIterations, sha256.Size)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", authPasswordIterations, base64.StdEncoding.EncodeToString(salt[:]), base64.StdEncoding.EncodeToString(key)), nil
}

func authVerifyPassword(password, digest string) bool {
	parts := strings.Split(digest, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iterations, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 32)
	if err != nil || iterations < 1_000 {
		return false
	}
	salt, saltErr := authDecodeBase64(parts[2])
	expected, keyErr := authDecodeBase64(parts[3])
	if saltErr != nil || keyErr != nil || len(salt) == 0 || len(expected) == 0 {
		return false
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, int(iterations), len(expected))
	return err == nil && subtle.ConstantTimeCompare(key, expected) == 1
}

func authDecodeBase64(s string) ([]byte, error) {
	s = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n':
			return -1
		default:
			return r
		}
	}, s)
	return base64.StdEncoding.DecodeString(s)
}

func authNormalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func authAvatar(email, username string) string {
	seed := strings.ToLower(strings.TrimSpace(email))
	if seed == "" {
		seed = username
	}
	return fmt.Sprintf("https://aptabase.com/avatar/%x?s=80&d=404", md5.Sum([]byte(seed)))
}

func (s *Server) authRegister(w http.ResponseWriter, r *http.Request) {
	w.Header().Del("Cache-Control")
	if !s.limit(w, r, "SignUp") {
		return
	}
	request := authReadRequest(w, r, true)
	if request == nil {
		return
	}
	authNoCache(w)
	username := authNormalizeUsername(*request.Username)
	name := strings.TrimSpace(*request.Name)
	if name == "" {
		name = username
	}
	hash, err := authHashPassword(*request.Password)
	if err != nil {
		authInternal(w, err)
		return
	}
	user, err := authInsertAccount(r.Context(), s.DB, name, username, "", &hash)
	if err != nil {
		authInternal(w, err)
		return
	}
	if user == nil {
		jsonResponse(w, http.StatusConflict, map[string]string{"error": "username_taken"})
		return
	}
	s.authRespondSignedIn(w, r, user)
}

func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Del("Cache-Control")
	if !s.limit(w, r, "SignIn") {
		return
	}
	request := authReadRequest(w, r, false)
	if request == nil {
		return
	}
	authNoCache(w)
	user, hash, err := s.authPasswordAccount(r.Context(), authNormalizeUsername(*request.Username))
	if err != nil {
		authInternal(w, err)
		return
	}
	if hash == "" {
		_ = authVerifyPassword(*request.Password, authDummyHash)
	}
	if user == nil || !authVerifyPassword(*request.Password, hash) {
		jsonResponse(w, http.StatusUnauthorized, map[string]string{"error": "invalid_credentials"})
		return
	}
	s.authRespondSignedIn(w, r, user)
}

func (s *Server) authRespondSignedIn(w http.ResponseWriter, r *http.Request, user *authAccount) {
	if err := s.authSignIn(w, r, user.Identity, true); err != nil {
		authInternal(w, err)
		return
	}
	jsonResponse(w, http.StatusOK, user)
}

func (s *Server) authCurrentAccount(w http.ResponseWriter, r *http.Request) *authAccount {
	authCORS(w, r)
	identity, err := s.identity(r)
	if err != nil {
		authInternal(w, err)
		return nil
	}
	if identity == nil {
		authProblem(w, http.StatusUnauthorized, nil)
		return nil
	}
	user, err := s.authAccountByID(r.Context(), identity.ID)
	if err != nil {
		authInternal(w, err)
		return nil
	}
	if user == nil {
		authProblem(w, http.StatusNotFound, nil)
		return nil
	}
	user.session = identity.session
	return user
}

func (s *Server) authMe(w http.ResponseWriter, r *http.Request) {
	if user := s.authCurrentAccount(w, r); user != nil {
		s.refreshAuthSession(w, r, &user.Identity)
		jsonResponse(w, http.StatusOK, user)
	}
}

func (s *Server) authDelete(w http.ResponseWriter, r *http.Request) {
	user := s.authCurrentAccount(w, r)
	if user == nil {
		return
	}
	if err := s.authDeleteAccount(r.Context(), user.ID); err != nil {
		authInternal(w, err)
		return
	}
	jsonResponse(w, http.StatusOK, user)
}
