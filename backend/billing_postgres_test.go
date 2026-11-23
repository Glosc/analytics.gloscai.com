package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestBillingPostgresLifecycle(t *testing.T) {
	dsn := os.Getenv("BILLING_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set BILLING_TEST_POSTGRES_DSN to validate billing with PostgreSQL")
	}
	pg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*pg)
	defer admin.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	schema := pgx.Identifier{"billing_test_" + newID()}.Sanitize()
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("clean test schema: %v", err)
		}
	}()
	pg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*pg)
	defer db.Close()
	if err := migratePostgres(ctx, db); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("insert billing fixture: %v", err)
		}
	}
	now := time.Now().UTC()
	reminderTime := now.Truncate(24*time.Hour).AddDate(0, 0, 5)
	exec(`INSERT INTO users (id, name, username, email, free_quota, free_trial_ends_at) VALUES
		('owner', 'First Last', 'owner', 'owner@example.test', 100, NULL),
		('expired', 'Expired Person', 'expired', 'expired@example.test', NULL, $1),
		('trial', 'Trial Person', 'trial', 'trial@example.test', NULL, $2),
		('no_email', 'No Email', 'no_email', NULL, 100, NULL)`, now.Add(-time.Hour), reminderTime)
	exec(`INSERT INTO apps (id, owner_id, name, app_key, has_events) VALUES
		('owned1', 'owner', 'First', 'first-key', true), ('owned2', 'owner', 'Second', 'second-key', true),
		('trial_app', 'trial', 'Trial', 'trial-key', true), ('no_email_app', 'no_email', 'No Email', 'null-email-key', true)`)
	analyticsHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/pipes/get_billing_usage__v1.json", "/v0/pipes/billing_historical_usage__v1.json":
			ids := strings.Split(r.URL.Query().Get("app_ids"), ",")
			slices.Sort(ids)
			if !slices.Equal(ids, []string{"owned1", "owned1_DEBUG", "owned2", "owned2_DEBUG"}) {
				t.Errorf("billing measured unexpected apps: %v", ids)
			}
			if strings.Contains(r.URL.Path, "historical") {
				fmt.Fprintf(w, `{"data":[{"Date":%q,"Events":"90"}]}`, billingMonth(now).Format(time.DateOnly))
			} else {
				fmt.Fprint(w, `{"data":[{"Count":"90"}]}`)
			}
		case "/v0/pipes/billing_usage_per_app__v1.json":
			if got := r.URL.Query().Get("period"); got != billingMonth(now).Format(time.DateOnly) {
				t.Errorf("usage period %q", got)
			}
			fmt.Fprint(w, `{"data":[{"AppId":"owned1","Count":"50"},{"AppId":"owned2","Count":"45"},{"AppId":"no_email_app","Count":"100"}]}`)
		default:
			t.Errorf("unexpected analytics endpoint %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer analyticsHTTP.Close()
	cfg := Config{Region: "US", BaseURL: "https://example.test", TinybirdURL: analyticsHTTP.URL}
	analytics, err := newAnalytics(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{DB: db, Config: cfg, Analytics: analytics}
	owner := &Identity{ID: "owner", Email: "owner@example.test"}
	w := httptest.NewRecorder()
	s.billingState(w, httptest.NewRequest(http.MethodGet, "/api/_billing", nil), owner)
	var state struct {
		State string
		Usage uint64
		Plan  billingPlan
	}
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil || w.Code != http.StatusOK ||
		state.State != "OK" || state.Usage != 90 || state.Plan.Name != "Free Plan" || state.Plan.MonthlyEvents != 100 {
		t.Fatalf("incorrect billing state HTTP %d: %s (%v)", w.Code, w.Body, err)
	}
	w = httptest.NewRecorder()
	s.billingHistorical(w, httptest.NewRequest(http.MethodGet, "/api/_billing/historical", nil), owner)
	var history []billingHistoricUsage
	if err := json.Unmarshal(w.Body.Bytes(), &history); err != nil || w.Code != http.StatusOK ||
		len(history) != 7 || history[0].Events != 0 || history[6].Events != 90 {
		t.Fatalf("incorrect billing history HTTP %d: %s (%v)", w.Code, w.Body, err)
	}
	trial, err := s.billingFreePlan(ctx, "trial")
	if err != nil || trial.Name != "Free Trial" || trial.MonthlyEvents != billingTrialMonthlyQuota ||
		trial.FreeTrialEndsAt == nil || !trial.FreeTrialEndsAt.Equal(reminderTime) {
		t.Fatalf("incorrect trial plan: %+v, %v", trial, err)
	}

	var templates []string
	send := func(_ context.Context, to, subject, templateName string, properties map[string]string) error {
		templates = append(templates, templateName)
		return nil
	}
	for range 2 {
		if err := s.billingNotifyOveruse(ctx, now, send); err != nil {
			t.Fatal(err)
		}
		if err := s.billingNotifyTrials(ctx, now, send); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(templates, []string{"UsageLevel90", "TrialEndsSoon"}) {
		t.Fatalf("unexpected alerts: %v", templates)
	}
	assertLock := func(id, reason string) {
		t.Helper()
		var actual string
		err := db.QueryRowContext(ctx, `SELECT coalesce(lock_reason, '') FROM users WHERE id = $1`, id).Scan(&actual)
		if err != nil || strings.TrimSpace(actual) != reason {
			t.Fatalf("user %s has lock %q, want %q (%v)", id, actual, reason, err)
		}
	}
	assertLock("no_email", "O")
	assertLock("owner", "")
	if err := s.billingExpireTrials(ctx, now); err != nil {
		t.Fatal(err)
	}
	assertLock("expired", "T")
	assertLock("trial", "")
	if err := s.billingResetOveruse(ctx, now); err != nil {
		t.Fatal(err)
	}
	assertLock("no_email", "")
	assertLock("expired", "T")

	const secret = "test signing key"
	t.Setenv("LEMONSQUEEZY_SIGNING_SECRET", secret)
	webhook := func(owner, status string, expected int) {
		t.Helper()
		body := fmt.Sprintf(`{"meta":{"event_name":"subscription_updated","custom_data":{"region":"US","user_id":%q}},
		"data":{"id":"123","attributes":{"customer_id":321,"product_id":42,"variant_id":103474,"status":%q,"ends_at":null}}}`, owner, status)
		w := httptest.NewRecorder()
		s.billingWebhook(w, billingTestWebhookRequest(body, secret).WithContext(ctx))
		if w.Code != expected {
			t.Fatalf("unexpected webhook HTTP %d: %s", w.Code, w.Body)
		}
	}
	webhook("expired", "active", http.StatusOK)
	assertLock("expired", "")
	webhook("owner", "expired", http.StatusBadRequest)
	sub, err := s.billingSubscription(ctx, "expired")
	if err != nil || sub == nil || sub.ID != 123 || sub.VariantID != 103474 || sub.Status != "active" {
		t.Fatalf("unexpected stored subscription: %+v (%v)", sub, err)
	}
	webhook("expired", "cancelled", http.StatusOK)
	webhook("expired", "expired", http.StatusOK)
	if err := db.QueryRowContext(ctx, `SELECT id FROM subscriptions WHERE id = 123`).Scan(new(int64)); err != sql.ErrNoRows {
		t.Fatalf("expired subscription not removed: %v", err)
	}
}
