package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExportTinybirdCSVPages(t *testing.T) {
	s, owner, _, _ := appsTestServer(t)
	queries := make(chan string, 3)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || r.Method != "POST" || r.URL.Path != "/v0/sql" {
			t.Errorf("unexpected SQL request: %s %s: %v", r.Method, r.URL, err)
		}
		query := string(body)
		select {
		case queries <- query:
		default:
			t.Error("unexpected extra SQL request")
		}
		if strings.Contains(query, "COUNT(*)") {
			fmt.Fprint(w, `{"data":[{"event_count":100001}]}`)
			return
		}
		fmt.Fprint(w, "event_name\ndemo\n")
	}))
	defer upstream.Close()
	var err error
	s.Analytics, err = newAnalytics(Config{TinybirdURL: upstream.URL, TinybirdToken: "test-token", EtcDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.registerApps(mux)
	s.registerStats(mux)
	cookie := appsTestCookie(t, s, owner)
	app := appsTestCreate(t, mux, cookie, "Paged App")
	res := appsTestRequest(mux, "GET", "/api/_export/download?appId="+app.ID+"&buildMode=release&appName=Paged+App&startDate=2026-01-01&endDate=2026-01-02", cookie, nil, nil)
	appsTestStatus(t, res, 200)
	if got := res.Body.String(); got != "event_name\ndemo\nevent_name\ndemo\n" {
		t.Fatalf("legacy CSV page output was %q", got)
	}
	for _, fragment := range []string{"AS event_count", "LIMIT 100000 OFFSET 0 FORMAT CSVWithNames", "LIMIT 100000 OFFSET 100000 FORMAT CSVWithNames"} {
		select {
		case query := <-queries:
			if !strings.Contains(query, fragment) {
				t.Errorf("missing %q from SQL: %s", fragment, query)
			}
		default:
			t.Errorf("missing SQL request containing %q", fragment)
		}
	}
}
