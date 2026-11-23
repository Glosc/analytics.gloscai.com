package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestIngestClickHouse(t *testing.T) {
	t.Setenv("ERROR_QUOTA_ENABLED", "true")
	db := ingestTestDB(t)
	a := analyticsTestClickHouse(t)
	ctx := t.Context()
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, username, name) VALUES ('owner', 'owner', 'Owner')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO apps (id, owner_id, name, app_key, has_events, error_quota) VALUES ('app', 'owner', 'App', 'A-DEV-0000000001', false, 1)`); err != nil {
		t.Fatal(err)
	}
	s := &Server{DB: db, Analytics: a, Config: Config{Region: "SH", EtcDir: "../etc"}}
	var err error
	s.Ingest, err = newIngestion(s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Ingest.geo.close)
	mux := http.NewServeMux()
	s.registerIngestion(mux)
	post := func(path, payload string, status int) {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(payload))
		r.Header.Set("App-Key", "A-DEV-0000000001")
		r.Header.Set("X-Real-IP", "8.8.8.8")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s returned %d: %s", path, w.Code, w.Body)
		}
	}
	stamp, err := json.Marshal(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	post("/api/v0/event", `{"eventName":"open","sessionId":"test-session","timestamp":`+string(stamp)+`,"systemProps":{"sdkVersion":"test@1","isDebug":true},"props":{"a":7}}`, 200)
	errorPayload := `{"errorType":"TestError","errorMessage":"Email test@example.com","isDebug":true,"severity":"FATAL","kind":"crash"}`
	post("/api/v0/error", errorPayload, 202)
	post("/api/v0/error", errorPayload, 403)
	workers, cancel := context.WithCancel(ctx)
	cancel()
	s.Ingest.Run(workers)

	read := func(query string) map[string]any {
		t.Helper()
		stream, err := a.stream(ctx, query+" FORMAT JSONEachRow")
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		var result map[string]any
		if err := json.NewDecoder(stream).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if row := read(`SELECT event_name, country_code, user_id, numeric_props FROM events WHERE app_id = 'app_DEBUG'`); row["event_name"] != "open" || row["country_code"] != "US" || row["user_id"] == "" || row["numeric_props"] != `{"a":7}` {
		t.Fatalf("unexpected persisted event: %+v", row)
	}
	if row := read(`SELECT error_message, severity, kind FROM error_events WHERE app_id = 'app_DEBUG'`); row["error_message"] != "Email [EMAIL_REDACTED]" || row["severity"] != "fatal" || row["kind"] != "crash" {
		t.Fatalf("unexpected persisted error: %+v", row)
	}
}

func TestIngestPostgres(t *testing.T) {
	db := ingestTestDB(t)
	ctx := t.Context()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users (id, username, name) VALUES ('owner', 'owner', 'Owner')`)
	exec(`INSERT INTO users (id, username, name, lock_reason) VALUES ('locked', 'locked', 'Locked', 'O')`)
	exec(`INSERT INTO apps (id, owner_id, name, app_key, has_events, error_quota) VALUES
		('app', 'owner', 'My App', 'A-DEV-0000000001', false, 3),
		('locked-app', 'locked', 'Locked App', 'A-DEV-0000000002', true, 3),
		('deleted', 'owner', 'Deleted App', 'A-DEV-0000000003', true, 3)`)
	exec(`UPDATE apps SET deleted_at = now() WHERE id = 'deleted'`)
	s := &Server{DB: db, Config: Config{Region: "EU", EtcDir: "../etc"}}
	i, err := newIngestion(s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(i.geo.close)
	for _, want := range []struct {
		key, id string
		locked  bool
	}{{"A-DEV-0000000001", "app", false}, {"A-DEV-0000000002", "locked-app", true}, {"A-DEV-0000000003", "", false}} {
		found, err := i.lookupApp(ctx, want.key)
		if err != nil || found.id != want.id || found.locked != want.locked {
			t.Fatalf("lookupApp(%q) = %+v %v, want %+v", want.key, found, err, want)
		}
	}
	var onboarded bool
	if err := db.QueryRowContext(ctx, `SELECT has_events FROM apps WHERE id = 'app'`).Scan(&onboarded); err != nil || !onboarded {
		t.Fatalf("onboarding flag not set: %v, %v", onboarded, err)
	}

	now := time.Now().UTC()
	other := newIngestPrivacy(db)
	const ip, ua = "192.0.2.4", "agent"
	type hashResult struct {
		id  string
		err error
	}
	result := make(chan hashResult, 2)
	for _, privacy := range []*ingestPrivacy{i.privacy, other} {
		go func() {
			hash, err := privacy.userID(ctx, now, "app", "shared-session", ip, ua)
			result <- hashResult{hash, err}
		}()
	}
	a, b := <-result, <-result
	if a.err != nil || b.err != nil || a.id == "" || a.id != b.id {
		t.Fatalf("concurrent salt upsert was not stable: %+v, %+v", a, b)
	}
	rotated, err := i.privacy.userID(ctx, now.AddDate(0, 0, -1), "app", "other-session", ip, ua)
	if err != nil || rotated == a.id {
		t.Fatalf("different reporting days shared the same ID: %q, %v", rotated, err)
	}
	stable, err := i.privacy.userID(ctx, now.Add(time.Hour), "app", "shared-session", "192.0.2.5", "changed")
	if err != nil || stable != a.id {
		t.Fatalf("a session changed identity after a network transition: %q, %v", stable, err)
	}
	oldDate := now.AddDate(0, 0, -2).Format(time.DateOnly)
	exec(`INSERT INTO app_salts VALUES ('app', $1, $2)`, oldDate, make([]byte, 16))
	if err := i.privacy.purge(ctx, now); err != nil {
		t.Fatal(err)
	}
	var salts int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM app_salts WHERE app_id = 'app'`).Scan(&salts); err != nil || salts != 2 {
		t.Fatalf("salt purge retained wrong rows: %d %v", salts, err)
	}

	q1, q2 := newIngestQuota(db), newIngestQuota(db)
	var accepted atomic.Int32
	var calls sync.WaitGroup
	for n := range 16 {
		calls.Go(func() {
			q := q1
			if n%2 == 1 {
				q = q2
			}
			ok, err := q.consume(ctx, "app", now)
			if err != nil {
				t.Errorf("consume quota: %v", err)
			}
			if ok {
				accepted.Add(1)
			}
		})
	}
	calls.Wait()
	if got := accepted.Load(); got != 3 {
		t.Fatalf("concurrent quota accepted %d reports, want 3", got)
	}
	nextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 5, 0, 0, time.UTC)
	for _, q := range []*ingestQuota{q1, q2} {
		if ok, err := q.consume(ctx, "app", nextMonth); err != nil || !ok {
			t.Fatalf("new month remains exhausted: %t, %v", ok, err)
		}
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT error_count FROM apps WHERE id = 'app'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("quota reset did not happen exactly once: count=%d (%v)", count, err)
	}
}

func ingestTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set INGEST_TEST_DATABASE_URL to run ingestion PostgreSQL integration tests")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = admin.Close() })
	schema := pgx.Identifier{"ingest_test_" + strings.ToLower(newID())}.Sanitize()
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
	return db
}
