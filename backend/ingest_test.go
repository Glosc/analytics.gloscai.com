package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIngestSDKPaths(t *testing.T) {
	s := &Server{}
	s.Ingest = &Ingestion{
		geo:     &ingestGeo{cloud: true, regions: map[string]string{"US-CA": "California"}},
		privacy: newIngestPrivacy(nil), events: newIngestQueue("events", 30), errors: newIngestQueue("error_events", 1),
		apps: newIngestCache[string, ingestApp](10), rejections: &s.rejections,
	}
	now := time.Now().UTC()
	until := now.Add(time.Hour)
	s.Ingest.apps.remember("ACTIVE", ingestApp{id: "app"}, until)
	s.Ingest.apps.remember("LOCKED", ingestApp{id: "locked", locked: true}, until)
	s.Ingest.apps.remember("UNKNOWN", ingestApp{}, until)
	for _, day := range []time.Time{now, now.AddDate(0, 0, 1)} {
		s.Ingest.privacy.salts.remember(ingestSaltKey{"app", day.Format(time.DateOnly)}, [16]byte{9}, until)
	}
	mux := http.NewServeMux()
	s.registerIngestion(mux)
	request := func(path, key, body, ip string, expected int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("App-Key", key)
		r.Header.Set("X-Real-IP", ip)
		r.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0) Chrome/143.0 Safari/537.36")
		r.Header.Set("Origin", "https://sdk.example")
		r.Header.Set("CloudFront-Viewer-Country", "us")
		r.Header.Set("CloudFront-Viewer-Country-Region", "ca")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != expected || w.Header().Get("Access-Control-Allow-Origin") != "*" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: got %d %s with %v; want %d, CORS and no-store", path, w.Code, w.Body, w.Header(), expected)
		}
		return w
	}

	preflight := httptest.NewRequest(http.MethodOptions, "/api/v0/error", nil)
	preflight.Header.Set("Origin", "https://sdk.example")
	preflight.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, preflight)
	if w.Code != 204 || !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "App-Key") {
		t.Fatalf("bad preflight: %d %v", w.Code, w.Header())
	}

	for _, key := range []string{"", "unknown", "locked"} {
		w := request("/api/v0/event", key, "malformed", "192.0.2.1", 200)
		if !strings.Contains(w.Body.String(), "Events are not being recorded.") {
			t.Fatalf("expected legacy drop response, got %s", w.Body)
		}
	}
	request("/api/v0/error", "", "malformed", "192.0.2.1", 400)
	request("/api/v0/error", "unknown", "malformed", "192.0.2.1", 401)
	request("/api/v0/error", "locked", "malformed", "192.0.2.1", 400)
	request("/api/v0/event", "active", "null", "192.0.2.1", 400)
	request("/api/v0/events", "active", "null", "192.0.2.1", 400)
	request("/api/v0/events", "active", "["+strings.Repeat("{},", 25)+"{}]", "192.0.2.1", 400)
	oversize := httptest.NewRequest(http.MethodPost, "/api/v0/event", nil)
	oversize.ContentLength = maxIngestBody + 1
	oversize.Header.Set("App-Key", "active")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, oversize)
	if w.Code != 413 {
		t.Fatalf("oversize request returned %d", w.Code)
	}

	event := fmt.Sprintf(`{"eventName":"open","timestamp":%q,"sessionId":"a-session","systemProps":{"sdkVersion":"test@1","locale":"en_US","isDebug":true},"props":"{\"s\":true,\"n\":2.5,\"o\":{}}"}`, now.Add(time.Hour).Format(time.RFC3339Nano))
	request("/api/v0/event", "active", event, "192.0.2.1", 200)
	request("/api/v0/events", "active", "[null,{},"+event+"]", "192.0.2.2", 200)
	events, _ := s.Ingest.events.take()
	if len(events) != 2 {
		t.Fatalf("got %d event rows, want two", len(events))
	}
	a, b := events[0].data, events[1].data
	if a["app_id"] != "app_DEBUG" || a["os_name"] != "Windows" || a["engine_name"] != "Chrome" || b["os_name"] != "" || a["user_id"] != b["user_id"] ||
		a["country_code"] != "US" || a["region_name"] != "California" || a["city"] != "" || a["locale"] != "en-us" ||
		a["string_props"] != `{"o":"{Object}","s":"true"}` || a["numeric_props"] != `{"n":2.5}` ||
		a["ttl"].(time.Time).Sub(a["timestamp"].(time.Time)) != 182*24*time.Hour || a["timestamp"].(time.Time).After(time.Now()) {
		t.Fatalf("unexpected rows: %+v %+v", a, b)
	}
	if hash := a["user_id"].(string); !regexp.MustCompile(`^[0-9A-F]{16}$`).MatchString(hash) {
		t.Fatalf("wrong hash format: %q", hash)
	}
	for _, field := range []string{"client_ip", "ip", "user_agent"} {
		if _, ok := a[field]; ok {
			t.Fatalf("anonymous event carries %s", field)
		}
	}

	errorJSON := `{"errorType":"Type","errorMessage":"Could not send to x@example.com","stackTrace":"from [2001:db8::1]","severity":"FaTaL","kind":"TASKEXCEPTION","isDebug":true}`
	request("/api/v0/error", "active", errorJSON, "192.0.2.1", 202)
	w = request("/api/v0/error", "active", errorJSON, "192.0.2.1", 503)
	if w.Header().Get("Retry-After") != "5" {
		t.Fatalf("full queue does not advertise transient failure: %v", w.Header())
	}
	rows, _ := s.Ingest.errors.take()
	if len(rows) != 1 {
		t.Fatalf("got %d error rows, want one", len(rows))
	}
	e := rows[0].data
	if e["app_id"] != "app_DEBUG" || e["error_message"] != "Could not send to [EMAIL_REDACTED]" ||
		e["stack_trace"] != "from [[IP_REDACTED]]" || e["severity"] != "fatal" || e["kind"] != "taskException" || e["ttl"].(time.Time).Sub(e["timestamp"].(time.Time)) != 180*24*time.Hour ||
		!regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(e["error_id"].(string)) {
		t.Fatalf("unexpected error row: %+v", e)
	}
	for n := range 21 {
		code := 200
		if n == 20 {
			code = 429
		}
		request("/api/v0/events", "", "malformed", "192.0.2.3", code)
	}
}

func TestIngestPayloadValidation(t *testing.T) {
	now := time.Now().UTC()
	for _, test := range []struct{ session, timestamp, props, want string }{
		{"", now.Format(time.RFC3339), "{}", "Invalid event payload"},
		{"123", now.Format(time.RFC3339), "{}", "Session is too old."},
		{fmt.Sprint(now.Add(time.Hour).Unix() * 100_000_000), now.Format(time.RFC3339), "{}", "Future sessions are not allowed."},
		{"app-session", now.Add(-25 * time.Hour).Format(time.RFC3339), "{}", "Event is too old."},
		{"app-session", now.Format(time.RFC3339), `{" ":true}`, "Property key must not be empty."},
		{"app-session", now.Format(time.RFC3339), `"[1]"`, "Props must be an object or a valid stringified JSON."},
		{"app-session", now.Format(time.RFC3339), `null`, ""},
	} {
		var event ingestEventBody
		if err := json.Unmarshal(fmt.Appendf(nil, `{"eventName":"open","timestamp":%q,"sessionId":%q,"systemProps":{"sdkVersion":"test"},"props":%s}`, test.timestamp, test.session, test.props), &event); err != nil {
			t.Fatal(err)
		}
		if got := event.validate(now); got != test.want {
			t.Errorf("validate(%+v): got %q, want %q", test, got, test.want)
		}
	}
}

func TestIngestShutdownDrainsReservedRows(t *testing.T) {
	q := newIngestQueue("events", 1)
	rows := []ingestRecord{newIngestRecord(map[string]any{"event_name": "accepted"})}
	if !q.reserve(rows) {
		t.Fatal("could not reserve capacity")
	}
	writes := make(chan []map[string]any, 1)
	i := &Ingestion{write: func(ctx context.Context, resource string, rows []map[string]any) error {
		if resource != "events" || ctx.Err() != nil {
			t.Errorf("shutdown used resource %q or cancelled context %v", resource, ctx.Err())
		}
		writes <- rows
		return nil
	}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	finished := make(chan struct{})
	go func() {
		i.runQueue(ctx, q, time.Hour)
		close(finished)
	}()
	q.publish(rows)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("queue did not finish shutdown")
	}
	if q.reserve(rows) || len(writes) != 1 || (<-writes)[0]["event_name"] != "accepted" {
		t.Fatal("a reserved row was lost or the closed queue accepted more work")
	}
}

func TestIngestTinybirdRetryAndPartialWrite(t *testing.T) {
	var attempts atomic.Int32
	var partial atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != "events" {
			t.Errorf("wrong write destination: %s", r.URL)
		}
		if partial.Load() {
			attempts.Add(1)
			fmt.Fprint(w, `{"successful_rows":1,"quarantined_rows":1}`)
			return
		}
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"successful_rows":2,"quarantined_rows":0}`)
	}))
	t.Cleanup(server.Close)
	a, err := newAnalytics(Config{TinybirdURL: server.URL, EtcDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	i := &Ingestion{write: a.Write}
	rows := []ingestRecord{newIngestRecord(map[string]any{"app_id": "one"}), newIngestRecord(map[string]any{"app_id": "two"})}
	if !i.writeIngestBatch(t.Context(), "events", rows) || attempts.Load() != 2 {
		t.Fatalf("transient failure should retry once, got %d attempts", attempts.Load())
	}
	partial.Store(true)
	attempts.Store(0)
	if !i.writeIngestBatch(t.Context(), "events", rows) || attempts.Load() != 1 {
		t.Fatalf("confirmed partial write must not retry, got %d attempts", attempts.Load())
	}
}
