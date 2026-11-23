package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionWeb(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "assets"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"index.html": "<html>Dashboard</html>", "assets/chart.js": "alert(1)"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{Config: Config{WebDir: root}}
	handler := compressResponse(s.routes())
	for _, tc := range []struct {
		path, encoding, content, cache string
		compressed                     bool
	}{
		{"/dashboard/app", "gzip", "Dashboard", "no-store", true},
		{"/", "br, gzip;q=0.0, *;q=1", "Dashboard", "no-store", false},
		{"/assets/chart.js", "br, gzip;q=0.8", "alert(1)", "immutable", true},
	} {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		r.Header.Set("Accept-Encoding", tc.encoding)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Cache-Control"), tc.cache) || (w.Header().Get("Content-Encoding") == "gzip") != tc.compressed {
			t.Fatalf("%s: %d %v", tc.path, w.Code, w.Header())
		}
		var body io.Reader = w.Body
		if tc.compressed {
			gz, err := gzip.NewReader(body)
			if err != nil {
				t.Fatal(err)
			}
			defer gz.Close()
			body = gz
		}
		b, err := io.ReadAll(body)
		if err != nil || !strings.Contains(string(b), tc.content) {
			t.Fatalf("%s body %q: %v", tc.path, b, err)
		}
	}
}

func TestLibpqDSN(t *testing.T) {
	const conn = "host=127.0.0.1 port=5432 dbname=analytics sslmode=disable"
	if got, err := postgresDSN(conn); err != nil || got != conn {
		t.Fatalf("postgresDSN: %q, %v", got, err)
	}
}

func TestNpgsqlSearchPath(t *testing.T) {
	dsn, err := postgresDSN(`Host=postgres;Database=aptabase;Search Path="metrics, public";Username=aptabase;Password='x;y'`)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("search_path") != "metrics, public" {
		t.Errorf("search path lost: %q", u.Query().Get("search_path"))
	}
	if pw, _ := u.User.Password(); pw != "x;y" {
		t.Error("quoted password was not preserved")
	}
}

func TestDeleteAccountUsesConfiguredSchema(t *testing.T) {
	s, owner, _, _ := appsTestServer(t)
	if err := s.authDeleteAccount(t.Context(), owner.ID); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := s.DB.QueryRowContext(t.Context(), "SELECT EXISTS (SELECT 1 FROM users WHERE id=$1)", owner.ID).Scan(&exists); err != nil || exists {
		t.Fatalf("account still present: %v, error: %v", exists, err)
	}
}

func TestCloudFrontAddressForUserHash(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/v0/event", nil)
	const ip = "[2001:0db8:85a3:0000:0000:8a2e:0370:7334]"
	r.Header.Add("CloudFront-Viewer-Address", ip+":8080")
	r.Header.Add("CloudFront-Viewer-Address", "192.0.2.4:443")
	if got := clientIP(r); got != ip {
		t.Errorf("clientIP = %q, want %q", got, ip)
	}
}
