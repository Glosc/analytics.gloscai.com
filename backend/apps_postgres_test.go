package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestAppsPostgres(t *testing.T) {
	s, owner, reader, stranger := appsTestServer(t)
	ownerCookie := appsTestCookie(t, s, owner)
	readerCookie := appsTestCookie(t, s, reader)
	strangerCookie := appsTestCookie(t, s, stranger)
	routes := s.routes()
	request := func(method, path string, cookie *http.Cookie, body any) *httptest.ResponseRecorder {
		return appsTestRequest(routes, method, path, cookie, body, nil)
	}

	t.Run("app CRUD, shares and uploads", func(t *testing.T) {
		appsTestStatus(t, request("GET", "/api/_apps", nil, nil), 401)
		appsTestStatus(t, request("POST", "/api/_apps", ownerCookie, map[string]string{"name": "a"}), 400)
		app := appsTestCreate(t, routes, ownerCookie, "My App")
		if !regexp.MustCompile(`^A-DEV-[0-9]{10}$`).MatchString(app.AppKey) || len(app.ID) != 22 {
			t.Fatalf("unexpected app IDs: %+v", app)
		}
		path := "/api/_apps/" + app.ID
		appsTestStatus(t, request("GET", path, strangerCookie, nil), 204)
		appsTestCanRead(t, s, &stranger, app.ID, false)
		info := appsTestJSON[appShareSummary](t, request("GET", path+"/shares/info", ownerCookie, nil), 200)
		if !info.HasOwnership || info.NumberOfOtherShares != 0 {
			t.Fatalf("unexpected share info without shares: %+v", info)
		}
		appsTestStatus(t, request("PUT", path+"/shares/Reader%40Example.Test", ownerCookie, nil), 200)
		appsTestCanRead(t, s, &reader, app.ID, true)
		shared := appsTestJSON[appResponse](t, request("GET", path, readerCookie, nil), 200)
		if shared.ID != app.ID || shared.HasOwnership || shared.AppKey != app.AppKey {
			t.Fatalf("unexpected shared app: %+v", shared)
		}
		listed := appsTestJSON[[]appResponse](t, request("GET", "/api/_apps", readerCookie, nil), 200)
		if len(listed) != 1 || listed[0].ID != app.ID {
			t.Fatalf("unexpected list for grantee: %+v", listed)
		}
		shares := appsTestJSON[[]appShare](t, request("GET", path+"/shares", ownerCookie, nil), 200)
		if len(shares) != 1 || shares[0].Email != reader.Email || shares[0].CreatedAt.IsZero() {
			t.Fatalf("unexpected shares: %+v", shares)
		}
		info = appsTestJSON[appShareSummary](t, request("GET", path+"/shares/info", readerCookie, nil), 200)
		if info.OwnerEmail == nil || *info.OwnerEmail != owner.Email || !info.SharedWithCurrentUser || info.NumberOfOtherShares != 0 {
			t.Fatalf("unexpected grantee share info: %+v", info)
		}
		appsTestStatus(t, request("GET", path+"/shares", readerCookie, nil), 404)
		appsTestStatus(t, request("PUT", path, readerCookie, appInput{Name: "Stolen App"}), 404)
		appsTestStatus(t, request("DELETE", path, readerCookie, nil), 404)
		appsTestStatus(t, request("DELETE", path+"/shares/READER%40EXAMPLE.TEST", ownerCookie, nil), 200)
		appsTestCanRead(t, s, &reader, app.ID, false)

		var pngData bytes.Buffer
		if err := png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 100, 100))); err != nil {
			t.Fatal(err)
		}
		appsTestStatus(t, request("PUT", path, ownerCookie, appInput{Name: "Renamed App", Icon: "invalid"}), 400)
		updated := appsTestJSON[appResponse](t, request("PUT", path, ownerCookie, appInput{
			Name: "Renamed App", Icon: base64.StdEncoding.EncodeToString(pngData.Bytes()),
		}), 200)
		if updated.Name != "Renamed App" || !updated.HasOwnership || !strings.HasPrefix(updated.IconPath, "icons/") {
			t.Fatalf("unexpected updated app: %+v", updated)
		}
		blob := request("GET", "/uploads/"+updated.IconPath, nil, nil)
		appsTestStatus(t, blob, 200)
		if !bytes.Equal(blob.Body.Bytes(), pngData.Bytes()) || blob.Header().Get("Content-Type") != "image/png" || blob.Header().Get("Cache-Control") != "public, max-age=31536000" {
			t.Fatalf("unexpected blob response headers: %v", blob.Header())
		}
		appsTestStatus(t, request("GET", "/uploads/icons/missing.png", nil, nil), 404)
		appsTestStatus(t, request("DELETE", path, ownerCookie, nil), 200)
		appsTestStatus(t, request("GET", path, ownerCookie, nil), 204)
		appsTestCanRead(t, s, &owner, app.ID, false)
	})

	t.Run("share and ownership requests", func(t *testing.T) {
		app := appsTestCreate(t, routes, ownerCookie, "Transfer App")
		path := "/api/_apps/" + app.ID
		pending := path + "/requests"
		appShare := "?purpose=AppShare"
		appOwnership := "?purpose=AppOwnership"
		initial := map[string]string{"targetUserEmail": reader.Email, "purpose": appSharePurpose}
		appsTestStatus(t, request("GET", pending+appShare, ownerCookie, nil), 204)
		appsTestStatus(t, request("POST", pending, ownerCookie, initial), 200)
		appsTestStatus(t, request("POST", pending, ownerCookie, initial), 400)
		status := appsTestJSON[appRequestResponse](t, request("GET", pending+appShare, ownerCookie, nil), 200)
		if status.TargetUserEmail != reader.Email || status.Purpose != appSharePurpose || status.Status != "pending" {
			t.Fatalf("wrong pending request: %+v", status)
		}
		incoming := appsTestJSON[[]incomingAppRequest](t, request("GET", "/api/_app-requests"+appShare, readerCookie, nil), 200)
		if len(incoming) != 1 || incoming[0].AppID != app.ID || incoming[0].AppName != app.Name {
			t.Fatalf("wrong incoming requests: %+v", incoming)
		}
		appsTestStatus(t, request("POST", pending+"/accept"+appShare, strangerCookie, nil), 404)
		appsTestStatus(t, request("POST", pending+"/accept"+appShare, readerCookie, nil), 200)
		appsTestCanRead(t, s, &reader, app.ID, true)
		appsTestStatus(t, request("POST", pending+"/accept"+appShare, readerCookie, nil), 404)

		initial["targetUserEmail"] = stranger.Email
		appsTestStatus(t, request("POST", pending, ownerCookie, initial), 200)
		appsTestStatus(t, request("POST", pending+"/reject"+appShare, strangerCookie, nil), 200)
		appsTestCanRead(t, s, &stranger, app.ID, false)
		appsTestStatus(t, request("POST", pending, ownerCookie, initial), 200)
		appsTestStatus(t, request("DELETE", pending+appShare, ownerCookie, nil), 200)
		appsTestStatus(t, request("POST", pending+"/accept"+appShare, strangerCookie, nil), 404)
		appsTestStatus(t, request("POST", pending, ownerCookie, initial), 200)
		appsTestStatus(t, request("POST", pending, ownerCookie, map[string]string{
			"targetUserEmail": reader.Email, "purpose": appOwnershipPurpose,
		}), 200)

		responses := make(chan int, 2)
		for range 2 {
			go func() { responses <- request("POST", pending+"/accept"+appOwnership, readerCookie, nil).Code }()
		}
		first, second := <-responses, <-responses
		if !(first == 200 && second == 404 || first == 404 && second == 200) {
			t.Fatalf("concurrent transfer resolved as %d and %d", first, second)
		}
		newOwner := appsTestJSON[appResponse](t, request("GET", path, readerCookie, nil), 200)
		if !newOwner.HasOwnership || newOwner.ID != app.ID {
			t.Fatalf("ownership did not transfer: %+v", newOwner)
		}
		appsTestCanRead(t, s, &owner, app.ID, true)
		appsTestStatus(t, request("DELETE", path, ownerCookie, nil), 404)
		appsTestStatus(t, request("POST", pending+"/accept"+appShare, strangerCookie, nil), 404)
	})

	t.Run("deleted recipient cannot reuse a session", func(t *testing.T) {
		app := appsTestCreate(t, routes, ownerCookie, "Deleted Recipient App")
		path := "/api/_apps/" + app.ID
		appsTestStatus(t, request("PUT", path+"/shares/"+stranger.Email, ownerCookie, nil), 200)
		appsTestStatus(t, request("POST", path+"/requests", ownerCookie, map[string]string{
			"targetUserEmail": stranger.Email, "purpose": appOwnershipPurpose,
		}), 200)
		if _, err := s.DB.ExecContext(t.Context(), `DELETE FROM users WHERE id = $1`, stranger.ID); err != nil {
			t.Fatal(err)
		}
		appsTestCanRead(t, s, &stranger, app.ID, false)
		appsTestStatus(t, request("GET", path, strangerCookie, nil), 204)
		appsTestStatus(t, request("POST", path+"/requests/accept?purpose=AppOwnership", strangerCookie, nil), 404)
		appsTestStatus(t, request("POST", "/api/_apps", strangerCookie, appInput{Name: "Orphaned App"}), 401)
	})

	t.Run("flag management and public state", func(t *testing.T) {
		app := appsTestCreate(t, routes, ownerCookie, "Flags App")
		appsTestStatus(t, request("PUT", "/api/_apps/"+app.ID+"/shares/"+reader.Email, ownerCookie, nil), 200)
		admin := "/api/_flags"
		public := "/api/v0/feature-flags"
		appsTestStatus(t, request("GET", admin+"/"+app.ID, nil, nil), 401)
		appsTestStatus(t, request("GET", admin+"/"+app.ID, readerCookie, nil), 404)
		input := appFlagInput{AppID: app.ID, Key: "enable-feature", Value: "yes", Environment: "release"}
		appsTestStatus(t, request("POST", admin, readerCookie, input), 404)
		created := appsTestJSON[appResponse](t, request("POST", admin, ownerCookie, input), 200)
		if created.ID != app.ID {
			t.Fatalf("create returned the wrong parent app: %+v", created)
		}
		flags := appsTestJSON[[]appFlagResponse](t, request("GET", admin+"/"+app.ID, ownerCookie, nil), 200)
		if len(flags) != 1 || flags[0].ID == nil || flags[0].Key != input.Key || flags[0].Environment != "release" {
			t.Fatalf("flag not persisted: %+v", flags)
		}
		flagURL := admin + "/" + *flags[0].ID
		input.Value = "no"
		appsTestStatus(t, request("PUT", flagURL, readerCookie, input), 404)
		appsTestStatus(t, request("DELETE", flagURL, readerCookie, nil), 404)
		appsTestStatus(t, request("PUT", flagURL, ownerCookie, input), 200)

		preflight := appsTestRequest(routes, "OPTIONS", public+"/"+input.Key, nil, nil, map[string]string{
			"Origin": "https://example.test", "Access-Control-Request-Method": "GET", "Access-Control-Request-Headers": "App-Key",
		})
		appsTestStatus(t, preflight, 204)
		if !strings.Contains(preflight.Header().Get("Access-Control-Allow-Methods"), "GET") ||
			!strings.Contains(strings.ToLower(preflight.Header().Get("Access-Control-Allow-Headers")), "app-key") {
			t.Fatalf("public preflight does not allow GET with App-Key: %v", preflight.Header())
		}
		sdk := func(path string) *httptest.ResponseRecorder {
			return appsTestRequest(routes, "GET", path, nil, nil, map[string]string{
				"App-Key": strings.ToLower(app.AppKey), "Origin": "https://example.test",
			})
		}
		appsTestStatus(t, request("GET", public, nil, nil), 404)
		resp := sdk(public + "/" + input.Key)
		flag := appsTestJSON[appFlagResponse](t, resp, 200)
		if flag.ID != nil || flag.AppID != nil || flag.Environment != "" || flag.Value != input.Value || resp.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("wrong public flag response: %+v (%v)", flag, resp.Header())
		}
		if got := appsTestJSON[appResponse](t, request("GET", "/api/_apps/"+app.ID, ownerCookie, nil), 200); !got.HasEvents {
			t.Fatal("legacy App-Key onboarding side effect was not applied")
		}
		if got := appsTestJSON[[]appFlagResponse](t, sdk(public), 200); len(got) != 1 || got[0].Key != input.Key {
			t.Fatalf("wrong public state: %+v", got)
		}
		appsTestStatus(t, request("DELETE", flagURL, ownerCookie, nil), 204)
		appsTestStatus(t, sdk(public+"/"+input.Key), 204)

		if _, err := s.DB.ExecContext(t.Context(), `UPDATE users SET lock_reason = 'T' WHERE id = $1`, owner.ID); err != nil {
			t.Fatal(err)
		}
		appsTestCanRead(t, s, &owner, app.ID, false)
		appsTestCanRead(t, s, &reader, app.ID, false)
		appsTestStatus(t, sdk(public), 400)
		appsTestStatus(t, request("DELETE", "/api/_apps/"+app.ID, ownerCookie, nil), 200)
		appsTestStatus(t, sdk(public), 404)

		// An empty key takes no DB round-trip; these requests fit in one rate window.
		for i := range 11 {
			want := 404
			if i == 10 {
				want = 429
			}
			appsTestStatus(t, appsTestRequest(routes, "GET", public, nil, nil, map[string]string{"X-Real-IP": "192.0.2.2"}), want)
		}
	})
}

func appsTestServer(t *testing.T) (*Server, Identity, Identity, Identity) {
	t.Helper()
	dsn := os.Getenv("APPS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set APPS_TEST_DATABASE_URL to run Apps PostgreSQL integration tests")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = admin.Close() })
	schema := pgx.Identifier{"apps_test_" + strings.ToLower(newID())}.Sanitize()
	if _, err := admin.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = db.Close() })
	if err := migratePostgres(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	makeUser := func(username string) Identity {
		u := Identity{ID: newID(), Email: username + "@example.test"}
		if _, err := db.ExecContext(t.Context(), `INSERT INTO users (id, name, email, username) VALUES ($1, $2, $3, $2)`, u.ID, username, u.Email); err != nil {
			t.Fatal(err)
		}
		return u
	}
	s := &Server{Config: Config{Development: true, BaseURL: "http://example.com", Region: "DEV", AuthSecret: strings.Repeat("test", 8)}, DB: db}
	return s, makeUser("owner"), makeUser("reader"), makeUser("stranger")
}

func appsTestCookie(t *testing.T, s *Server, user Identity) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	if err := s.authSignIn(w, httptest.NewRequest("GET", "/", nil), user, false); err != nil {
		t.Fatal(err)
	}
	return w.Result().Cookies()[0]
}

func appsTestRequest(routes http.Handler, method, path string, cookie *http.Cookie, body any, headers map[string]string) *httptest.ResponseRecorder {
	data, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	if body == nil {
		data = nil
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	routes.ServeHTTP(w, r)
	return w
}

func appsTestCreate(t *testing.T, routes http.Handler, cookie *http.Cookie, name string) appResponse {
	t.Helper()
	return appsTestJSON[appResponse](t, appsTestRequest(routes, "POST", "/api/_apps", cookie, appInput{Name: name}, nil), 200)
}

func appsTestStatus(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status = %d, want %d: %s", w.Code, want, w.Body)
	}
}

func appsTestJSON[T any](t *testing.T, w *httptest.ResponseRecorder, status int) T {
	t.Helper()
	appsTestStatus(t, w, status)
	var value T
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode %q: %v", w.Body, err)
	}
	return value
}

func appsTestCanRead(t *testing.T, s *Server, user *Identity, appID string, want bool) {
	t.Helper()
	if allowed, err := s.canReadApp(t.Context(), user, appID); err != nil || allowed != want {
		t.Fatalf("canReadApp(%v, %q) = %v, %v; want %v", user, appID, allowed, err, want)
	}
}
