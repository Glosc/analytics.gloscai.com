package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAnalyticsLiquidTemplates(t *testing.T) {
	queries := analyticsQueries{}
	paths, err := filepath.Glob("../etc/clickhouse/queries/*.liquid")
	if err != nil || len(paths) == 0 {
		t.Fatalf("find checked-in queries: %v", err)
	}
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".liquid")
		t.Run(name, func(t *testing.T) {
			tmpl, err := queries.load("../etc", name)
			if err != nil {
				t.Fatal(err)
			}
			for _, granularity := range []string{"Hour", "Day", "Month"} {
				for _, args := range []analyticsArgs{{"granularity": granularity}, {
					"granularity": granularity, "name_column": "country_code", "value_column": "UniqueSessions",
					"app_id": `ab\c'd`, "app_ids": `ab\c'd,other`, "error_type": "Example", "severity": "fatal",
					"kind": "handled", "app_version": "1.2.3", "os_name": "Windows", "country_code": "US",
					"event_name": "demo", "device_model": "test", "limit": "50", "offset": "0",
					"date_from": "2026-01-01 00:00:00", "date_to": "2026-02-01 00:00:00",
				}} {
					sql, err := analyticsRenderQuery(tmpl, args)
					if err != nil || strings.Contains(sql, "{%") || strings.Contains(sql, "{{") {
						t.Fatalf("render query: %v\n%s", err, sql)
					}
					if args["app_id"] != "" && name == "get_errors__v1" {
						for _, fragment := range []string{`app_id = 'ab\\c\'d'`, "severity = 'fatal'", "kind = 'handled'", "LIMIT 50 OFFSET 0"} {
							if !strings.Contains(sql, fragment) {
								t.Errorf("expected %q in %q", fragment, sql)
							}
						}
					}
				}
			}
		})
	}
}

func TestAnalyticsTinybird(t *testing.T) {
	var wrote atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected authorization header %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/v0/pipes/get_error_count__v1.json":
			if r.Method != "GET" || r.URL.Query().Get("app_id") != "app_DEBUG" || r.URL.Query().Get("error_type") != "A&B" {
				t.Errorf("unexpected query request %s %s", r.Method, r.URL)
			}
			fmt.Fprint(w, `{"data":[{"Count":"42"}]}`)
		case "/v0/events":
			if r.Method != "POST" || r.URL.Query().Get("name") != "error_events" || r.URL.Query().Get("wait") != "true" {
				t.Errorf("unexpected events request %s %s", r.Method, r.URL)
			}
			var row map[string]any
			if err := json.NewDecoder(r.Body).Decode(&row); err != nil || row["appId"] != "app_DEBUG" || row["errorMessage"] != "test" || row["timestamp"] != "2026-01-01T00:00:00Z" {
				t.Errorf("bad write: %v: %#v", err, row)
			}
			wrote.Store(true)
			fmt.Fprint(w, `{"successful_rows":1,"quarantined_rows":0}`)
		case "/v0/sql":
			if body, _ := io.ReadAll(r.Body); r.Method != "POST" || string(body) != "SELECT 1" {
				t.Errorf("unexpected raw SQL request %s %q", r.Method, body)
			}
			fmt.Fprint(w, "1\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	a, err := newAnalytics(Config{TinybirdURL: srv.URL, TinybirdToken: "test-token", EtcDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	count, err := analyticsFirst[struct{ Count analyticsCount }](ctx, a, "get_error_count__v1", analyticsArgs{"app_id": "app_DEBUG", "error_type": "A&B"})
	if err != nil || count.Count != 42 {
		t.Fatalf("count: %d, %v", count.Count, err)
	}
	if err := a.Write(ctx, "error_events", []map[string]any{{"app_id": "app_DEBUG", "error_message": "test", "timestamp": time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}); err != nil || !wrote.Load() {
		t.Fatalf("write: %v", err)
	}
	stream, err := a.stream(ctx, "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if body, err := io.ReadAll(stream); err != nil || string(body) != "1\n" {
		t.Fatalf("raw SQL: %q, %v", body, err)
	}
}

func TestAnalyticsTinybirdPartialWrite(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"successful_rows":1,"quarantined_rows":1}`)
	}))
	defer srv.Close()
	a, err := newAnalytics(Config{TinybirdURL: srv.URL, EtcDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	err = a.Write(t.Context(), "events", []map[string]any{{"app_id": "app"}, {"app_id": "app"}})
	var permanent interface{ Permanent() bool }
	if !errors.As(err, &permanent) || !permanent.Permanent() {
		t.Fatalf("partial write must not be retried: %v", err)
	}
}

func TestAnalyticsClickHouse(t *testing.T) {
	configured := analyticsTestClickHouse(t)
	if err := configured.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	end := now.Add(time.Hour)
	ttl := now.AddDate(0, 0, 90)
	if err := configured.Write(t.Context(), "events", []map[string]any{{
		"app_id": "app_DEBUG", "timestamp": now, "ttl": ttl, "event_name": "demo", "user_id": "user-1", "session_id": "session-1",
		"country_code": "US", "region_name": "California", "string_props": `{"source":"test"}`, "numeric_props": `{"price":10}`,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := configured.Write(t.Context(), "error_events", []map[string]any{{
		"app_id": "app_DEBUG", "timestamp": now, "ttl": ttl, "error_id": "error-1", "error_type": "Example", "error_message": "test", "severity": "fatal", "kind": "handled",
	}, {
		"app_id": "app_DEBUG", "timestamp": now, "ttl": ttl, "error_id": "error-2", "error_type": "Example", "error_message": "test", "severity": "error", "kind": "handled",
	}}); err != nil {
		t.Fatal(err)
	}
	args := analyticsArgs{
		"app_id": "app_DEBUG", "app_ids": "app_DEBUG,another_app", "period": time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format(time.DateOnly), "granularity": "Hour",
		"date_from": now.Add(-time.Hour).Format(analyticsDateTime), "date_to": end.Format(analyticsDateTime),
		"start_date": now.Format(analyticsDateTime), "end_date": end.Format(analyticsDateTime),
		"name_column": "country_code", "value_column": "UniqueSessions", "session_id": "session-1",
		"error_type": "Example", "error_id": "error-1", "severity": "fatal", "kind": "handled", "limit": "50", "offset": "0",
	}
	files, err := filepath.Glob("../etc/clickhouse/queries/*.liquid")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".liquid")
		t.Run(name, func(t *testing.T) {
			rows, err := analyticsRows[map[string]any](t.Context(), configured, name, args)
			if err != nil {
				t.Fatal(err)
			}
			if name == "get_errors__v1" && (len(rows) != 1 || rows[0]["ErrorId"] != "error-1") {
				t.Fatalf("wrong error list: %#v", rows)
			}
			if name == "get_error_count__v1" && (len(rows) != 1 || fmt.Sprint(rows[0]["Count"]) != "1") {
				t.Fatalf("wrong error count: %#v", rows)
			}
		})
	}
}

func analyticsTestClickHouse(t *testing.T) *Analytics {
	t.Helper()
	endpoint := os.Getenv("ANALYTICS_TEST_CLICKHOUSE_URL")
	if endpoint == "" {
		t.Skip("set ANALYTICS_TEST_CLICKHOUSE_URL to run against a local ClickHouse server")
	}
	a, err := newAnalytics(Config{ClickHouseURL: endpoint, EtcDir: "../etc"})
	if err != nil {
		t.Fatal(err)
	}
	database := "aptabase_go_test_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	analyticsTestExec(t, a, "CREATE DATABASE "+database)
	t.Cleanup(func() { analyticsTestExec(t, a, "DROP DATABASE "+database) })
	u := *a.endpoint
	query := u.Query()
	query.Set("database", database)
	u.RawQuery = query.Encode()
	configured, err := newAnalytics(Config{ClickHouseURL: u.String(), EtcDir: "../etc"})
	if err != nil {
		t.Fatal(err)
	}
	if err := configured.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return configured
}

func analyticsTestExec(t *testing.T, a *Analytics, sql string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := a.stream(ctx, sql)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := io.Copy(io.Discard, stream); err != nil {
		t.Fatal(err)
	}
}
