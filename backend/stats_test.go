package main

import (
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestStatsPrefixedQuery(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/_stats/metrics?AppId=allowed&body.AppId=other&StartDate=2026-01-01&Body.BuildMode=DEBUG", nil)
	q := statsQueryValues(r, true)
	if q.Get("appid") != "other" || q.Get("buildmode") != "DEBUG" || q.Has("startdate") {
		t.Fatalf("unexpected complex query: %v", q)
	}
	q = statsQueryValues(r, false)
	if q.Get("appid") != "allowed" || q.Get("buildmode") != "" || !q.Has("startdate") {
		t.Fatalf("unexpected simple query: %v", q)
	}
}

func TestStatsClickHouseRoutes(t *testing.T) {
	s, owner, reader, stranger := appsTestServer(t)
	s.Analytics = analyticsTestClickHouse(t)
	mux := http.NewServeMux()
	s.registerApps(mux)
	s.registerStats(mux)
	ownerCookie := appsTestCookie(t, s, owner)
	readerCookie := appsTestCookie(t, s, reader)
	strangerCookie := appsTestCookie(t, s, stranger)
	app := appsTestCreate(t, mux, ownerCookie, "Example App")
	otherApp := appsTestCreate(t, mux, strangerCookie, "Other App")
	appsTestStatus(t, appsTestRequest(mux, "PUT", "/api/_apps/"+app.ID+"/shares/Reader%40Example.Test", ownerCookie, nil, nil), 200)
	get := func(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
		return appsTestRequest(mux, "GET", path, cookie, nil, nil)
	}
	now := time.Now().UTC().Truncate(time.Second)
	ttl := now.AddDate(0, 0, 90)
	ctx := t.Context()
	debugApp := app.ID + "_DEBUG"
	if err := s.Analytics.Write(ctx, "events", []map[string]any{{
		"app_id": debugApp, "timestamp": now, "ttl": ttl, "event_name": "demo", "user_id": "user-1", "session_id": "session-1",
		"country_code": "US", "region_name": "California", "string_props": `{"source":"test"}`, "numeric_props": `{"price":10}`,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Analytics.Write(ctx, "error_events", []map[string]any{{
		"app_id": debugApp, "timestamp": now, "ttl": ttl, "error_id": "report-1", "error_type": "Example", "error_message": "test", "severity": "fatal", "kind": "handled",
	}, {
		"app_id": debugApp, "timestamp": now, "ttl": ttl, "error_id": "report-2", "error_type": "Example", "error_message": "test", "severity": "error", "kind": "handled",
	}}); err != nil {
		t.Fatal(err)
	}
	q := url.Values{
		"appId": {app.ID}, "buildMode": {"DEBUG"}, "startDate": {now.Add(-time.Hour).Format(time.RFC3339)}, "endDate": {now.Add(time.Hour).Format(time.RFC3339)},
		"appName": {"Example & App!"},
	}
	t.Run("stats scoping and metrics", func(t *testing.T) {
		path := "/api/_stats/metrics?" + q.Encode()
		appsTestStatus(t, get(path, nil), 401)
		appsTestStatus(t, get(path+"&body.AppId="+otherApp.ID, ownerCookie), 403)
		appsTestStatus(t, get(path+"&body.AppId="+otherApp.ID, readerCookie), 403)
		metrics := appsTestJSON[struct{ Current, Previous statsMetric }](t, get(path, readerCookie), 200)
		if metrics.Current.Events != 1 || metrics.Previous.Events != 0 {
			t.Fatalf("wrong metrics: %+v", metrics)
		}
		top := appsTestJSON[[]statsTopItem](t, get("/api/_stats/top-events?"+q.Encode(), ownerCookie), 200)
		if len(top) != 1 || top[0].Name != "demo" || top[0].Value != 1 {
			t.Fatalf("wrong top events: %+v", top)
		}
		geo := appsTestJSON[[]statsGeo](t, get("/api/_stats/live-geo?"+q.Encode(), ownerCookie), 200)
		if len(geo) != 1 || geo[0].CountryCode != "US" || geo[0].Latitude == 0 || geo[0].Longitude == 0 {
			t.Fatalf("wrong geo: %+v", geo)
		}
		session := appsTestJSON[statsTimeline](t, get("/api/_stats/live-session-details?sessionId=session-1&"+q.Encode(), ownerCookie), 200)
		if session.ID != "session-1" || session.EventsCount != 1 || len(session.EventsTimestamp) != 1 || !session.EventsTimestamp[0].Equal(now) {
			t.Fatalf("wrong session: %+v", session)
		}
	})
	t.Run("errors list, count, types and detail", func(t *testing.T) {
		path := "/api/v0/apps/" + app.ID + "/errors"
		appsTestStatus(t, get(path+"?"+q.Encode(), strangerCookie), 403)
		result := appsTestJSON[struct {
			Errors     []statsError
			Pagination statsErrorPage
		}](t, get(path+"?severity=fatal&kind=handled&limit=101&offset=-1&"+q.Encode(), readerCookie), 200)
		if len(result.Errors) != 1 || result.Errors[0].ErrorID != "report-1" || result.Errors[0].AppID != debugApp || !result.Errors[0].Timestamp.Equal(now) ||
			result.Pagination != (statsErrorPage{Limit: 50, Total: 1}) {
			t.Fatalf("wrong filtered result: %+v", result)
		}
		types := appsTestJSON[[]statsTopItem](t, get(path+"/types?"+q.Encode(), readerCookie), 200)
		if len(types) != 1 || types[0].Name != "Example" || types[0].Value != 2 {
			t.Fatalf("wrong error types: %+v", types)
		}
		detail := appsTestJSON[statsError](t, get(path+"/report-2?"+q.Encode(), readerCookie), 200)
		if detail.Severity != "error" || detail.ErrorID != "report-2" {
			t.Fatalf("wrong error detail: %+v", detail)
		}
		appsTestStatus(t, get(path+"/report-2?buildMode=release", readerCookie), 404)
	})
	t.Run("csv and parquet", func(t *testing.T) {
		path := "/api/_export/download?" + q.Encode()
		appsTestStatus(t, get(path+"&body.AppId="+otherApp.ID, ownerCookie), 403)
		res := get(path+"&format=csv", ownerCookie)
		appsTestStatus(t, res, 200)
		if content := res.Header().Get("Content-Disposition"); !strings.HasPrefix(content, "attachment; filename=exampleapp-debug-") || !strings.HasSuffix(content, ".csv") {
			t.Fatalf("wrong filename: %s", content)
		}
		csvRows, err := csv.NewReader(res.Body).ReadAll()
		if err != nil || len(csvRows) != 2 || len(csvRows[1]) != 16 || csvRows[0][14] != "country_name" || csvRows[1][14] != "United States" || csvRows[1][3] != "demo" {
			t.Fatalf("wrong CSV: %#v, %v", csvRows, err)
		}
		res = get(path+"&format=parquet", ownerCookie)
		appsTestStatus(t, res, 200)
		if !strings.HasPrefix(res.Body.String(), "PAR1") || !strings.HasSuffix(res.Body.String(), "PAR1") {
			t.Fatalf("wrong Parquet: %d bytes", res.Body.Len())
		}
		usage := appsTestJSON[[]exportMonth](t, get("/api/_export/usage?"+q.Encode(), ownerCookie), 200)
		if len(usage) == 0 || len(usage) > 5 || usage[0].Events != 1 {
			t.Fatalf("wrong monthly usage: %+v", usage)
		}
	})
}
