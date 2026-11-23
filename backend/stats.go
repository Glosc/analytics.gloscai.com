package main

import (
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

func (s *Server) registerStats(mux *http.ServeMux) {
	for _, top := range []struct{ path, column, value string }{
		{"top-countries", "country_code", "UniqueSessions"},
		{"top-osversions", "os_version", "UniqueSessions"},
		{"top-devices", "device_model", "UniqueSessions"},
		{"top-operatingsystems", "os_name", "UniqueSessions"},
		{"top-regions", "region_name", "UniqueSessions"},
		{"top-events", "event_name", "TotalEvents"},
		{"top-appversions", "app_version", "UniqueSessions"},
		{"top-appbuildnumbers", "app_build_number", "UniqueSessions"},
	} {
		mux.HandleFunc("GET /api/_stats/"+top.path, s.statsRead(func(w http.ResponseWriter, r *http.Request, p statsQuery) {
			p.args["name_column"], p.args["value_column"] = top.column, top.value
			rows, err := analyticsRows[statsTopItem](r.Context(), s.Analytics, "top_n__v2", p.args)
			statsRespond(w, r, rows, err)
		}))
	}
	mux.HandleFunc("GET /api/_stats/metrics", s.statsRead(s.statsMetrics))
	mux.HandleFunc("GET /api/_stats/periodic", s.statsRead(func(w http.ResponseWriter, r *http.Request, p statsQuery) {
		rows, err := analyticsRows[statsPeriodic](r.Context(), s.Analytics, "key_metrics_periodic__v2", p.args)
		statsRespond(w, r, rows, err)
	}))
	mux.HandleFunc("GET /api/_stats/top-props", s.statsRead(func(w http.ResponseWriter, r *http.Request, p statsQuery) {
		rows, err := analyticsRows[statsEventProp](r.Context(), s.Analytics, "top_props__v2", p.args)
		statsRespond(w, r, rows, err)
	}))
	mux.HandleFunc("GET /api/_stats/live-geo", s.statsRead(s.statsLiveGeo))
	mux.HandleFunc("GET /api/_stats/live-sessions", s.statsRead(func(w http.ResponseWriter, r *http.Request, p statsQuery) {
		rows, err := analyticsRows[statsSession](r.Context(), s.Analytics, "live_sessions__v1", analyticsArgs{"app_id": p.args["app_id"]})
		statsRespond(w, r, rows, err)
	}))
	mux.HandleFunc("GET /api/_stats/live-session-details", s.statsRead(s.statsLiveSessionDetails))
	mux.HandleFunc("GET /api/_stats/historical-sessions", s.statsRead(func(w http.ResponseWriter, r *http.Request, p statsQuery) {
		delete(p.args, "device_model")
		delete(p.args, "granularity")
		if p.sessionID != "" {
			p.args["session_id"] = p.sessionID
		}
		rows, err := analyticsRows[statsSession](r.Context(), s.Analytics, "historical_sessions__v1", p.args)
		statsRespond(w, r, rows, err)
	}))
	s.registerExport(mux)
	s.registerErrorReads(mux)
}

type statsQuery struct {
	args       analyticsArgs
	start, end time.Time
	sessionID  string
}

func (s *Server) statsRead(handle func(http.ResponseWriter, *http.Request, statsQuery)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := statsQueryValues(r, true)
		appID := q.Get("appid")
		if !s.analyticsCanRead(w, r, appID, true) {
			return
		}
		granularity := "Hour"
		switch strings.ToLower(q.Get("granularity")) {
		case "", "hour", "0":
		case "day", "1":
			granularity = "Day"
		case "month", "2":
			granularity = "Month"
		default:
			statsBadQuery(w, "Invalid granularity.")
			return
		}
		start, end, err := statsDateRange(q)
		if err != nil {
			statsBadQuery(w, "Invalid start or end date.")
			return
		}
		args := analyticsArgs{"app_id": analyticsBuildAppID(appID, q.Get("buildmode")), "granularity": granularity}
		for param, key := range map[string]string{
			"countrycode": "country_code", "osname": "os_name", "devicemodel": "device_model",
			"eventname": "event_name", "appversion": "app_version",
		} {
			if value := q.Get(param); strings.TrimSpace(value) != "" {
				args[key] = value
			}
		}
		statsSetDateArg(args, "date_from", start)
		statsSetDateArg(args, "date_to", end)
		handle(w, r, statsQuery{args: args, start: start, end: end, sessionID: q.Get("sessionid")})
	}
}

func (s *Server) analyticsCanRead(w http.ResponseWriter, r *http.Request, appID string, rateLimit bool) bool {
	w.Header().Set("Cache-Control", "no-store, no-cache")
	w.Header().Set("Pragma", "no-cache")
	if rateLimit && !s.limit(w, r, "Stats") {
		return false
	}
	user := s.requireUser(w, r)
	if user == nil {
		return false
	}
	if strings.TrimSpace(appID) == "" {
		statsBadQuery(w, "Invalid app id.")
		return false
	}
	allowed, err := s.canReadApp(r.Context(), user, appID)
	if err != nil {
		statsRespond(w, r, nil, err)
		return false
	}
	if !allowed {
		w.WriteHeader(http.StatusForbidden)
	}
	return allowed
}

func statsBadQuery(w http.ResponseWriter, message string) {
	jsonResponse(w, http.StatusBadRequest, map[string]string{"error": message})
}

func statsRespond(w http.ResponseWriter, r *http.Request, body any, err error) {
	if err != nil {
		slog.ErrorContext(r.Context(), "analytics request failed", "route", r.Pattern, "error", err)
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "An error occurred while processing your request."})
		return
	}
	jsonResponse(w, http.StatusOK, body)
}

// ASP.NET binds complex query parameters using either body.* or unprefixed
// keys, never both. Access checks and queries must use the same resolved ID.
func statsQueryValues(r *http.Request, complex bool) url.Values {
	q := r.URL.Query()
	prefix := ""
	if complex {
		for key := range q {
			if strings.HasPrefix(strings.ToLower(key), "body.") {
				prefix = "body."
				break
			}
		}
	}
	values := make(url.Values, len(q))
	for _, key := range slices.Sorted(maps.Keys(q)) {
		name := strings.ToLower(key)
		if name, ok := strings.CutPrefix(name, prefix); ok {
			values[name] = append(values[name], q[key]...)
		}
	}
	return values
}

func statsDateRange(q url.Values) (start, end time.Time, err error) {
	if value := q.Get("startdate"); strings.TrimSpace(value) != "" {
		start, err = analyticsParseDate(strings.TrimSpace(value))
		if err != nil {
			return
		}
	}
	if value := q.Get("enddate"); strings.TrimSpace(value) != "" {
		end, err = analyticsParseDate(strings.TrimSpace(value))
	}
	return
}

func statsSetDateArg(args analyticsArgs, key string, date time.Time) {
	if !date.IsZero() {
		args[key] = date.Format(analyticsDateTime)
	}
}

func analyticsBuildAppID(appID, mode string) string {
	if strings.EqualFold(mode, "debug") {
		return appID + "_DEBUG"
	}
	return appID
}

type statsMetric struct {
	DailyUsers      analyticsFloat `json:"dailyUsers"`
	Sessions        analyticsCount `json:"sessions"`
	Events          analyticsCount `json:"events"`
	DurationSeconds analyticsFloat `json:"durationSeconds"`
}

func (s *Server) statsMetrics(w http.ResponseWriter, r *http.Request, p statsQuery) {
	ctx := r.Context()
	current, err := analyticsFirst[statsMetric](ctx, s.Analytics, "key_metrics__v2", p.args)
	if err != nil {
		statsRespond(w, r, nil, err)
		return
	}
	result := struct {
		Current  statsMetric  `json:"current"`
		Previous *statsMetric `json:"previous"`
	}{Current: current}
	if !p.start.IsZero() && !p.end.IsZero() {
		previousEnd := p.start
		if previousEnd.Equal(previousEnd.Truncate(24 * time.Hour)) {
			previousEnd = previousEnd.Add(-time.Second)
		}
		previousStart := p.start.Add(-p.end.Sub(p.start))
		p.args["date_from"] = previousStart.Format(analyticsDateTime)
		p.args["date_to"] = previousEnd.Format(analyticsDateTime)
		// The legacy comparison query omits the device model filter.
		delete(p.args, "device_model")
		previous, err := analyticsFirst[statsMetric](ctx, s.Analytics, "key_metrics__v2", p.args)
		if err != nil {
			statsRespond(w, r, nil, err)
			return
		}
		result.Previous = &previous
	}
	statsRespond(w, r, result, nil)
}

type statsPeriodic struct {
	Period   analyticsTime  `json:"period"`
	Users    analyticsFloat `json:"users"`
	Sessions analyticsCount `json:"sessions"`
	Events   analyticsCount `json:"events"`
}

type statsTopItem struct {
	Name  string         `json:"name"`
	Value analyticsFloat `json:"value"`
}

type statsEventProp struct {
	StringKey   string         `json:"stringKey"`
	StringValue string         `json:"stringValue"`
	NumericKey  string         `json:"numericKey"`
	Events      analyticsCount `json:"events"`
	Median      analyticsFloat `json:"median"`
	Min         analyticsFloat `json:"min"`
	Max         analyticsFloat `json:"max"`
	Sum         analyticsFloat `json:"sum"`
}

type statsGeo struct {
	CountryCode string         `json:"countryCode"`
	RegionName  string         `json:"regionName"`
	Users       analyticsCount `json:"users"`
	Latitude    float64        `json:"latitude"`
	Longitude   float64        `json:"longitude"`
}

func (s *Server) statsLiveGeo(w http.ResponseWriter, r *http.Request, p statsQuery) {
	rows, err := analyticsRows[statsGeo](r.Context(), s.Analytics, "live_geo__v1", analyticsArgs{"app_id": p.args["app_id"]})
	for i, row := range rows {
		coordinate := s.Analytics.coordinates[row.CountryCode][row.RegionName]
		rows[i].Latitude, rows[i].Longitude = coordinate.Latitude, coordinate.Longitude
	}
	statsRespond(w, r, rows, err)
}

type statsSessionBase struct {
	ID          string         `json:"id"`
	StartedAt   analyticsTime  `json:"startedAt"`
	Duration    analyticsCount `json:"duration"`
	EventsCount analyticsCount `json:"eventsCount"`
	AppVersion  string         `json:"appVersion"`
	CountryCode string         `json:"countryCode"`
	RegionName  string         `json:"regionName"`
	OSName      string         `json:"osName"`
	OSVersion   string         `json:"osVersion"`
}

type statsSession struct {
	statsSessionBase
	DeviceModel string `json:"deviceModel"`
}

type statsTimeline struct {
	statsSessionBase
	EventsName         []string        `json:"eventsName"`
	EventsTimestamp    []analyticsTime `json:"eventsTimestamp"`
	EventsStringProps  []string        `json:"eventsStringProps"`
	EventsNumericProps []string        `json:"eventsNumericProps"`
}

func (s *Server) statsLiveSessionDetails(w http.ResponseWriter, r *http.Request, p statsQuery) {
	args := analyticsArgs{"app_id": p.args["app_id"]}
	if p.sessionID != "" {
		args["session_id"] = p.sessionID
	}
	row, err := analyticsFirst[statsTimeline](r.Context(), s.Analytics, "live_session_details__v1", args)
	if row.EventsName == nil {
		row.EventsName = []string{}
	}
	if row.EventsTimestamp == nil {
		row.EventsTimestamp = []analyticsTime{}
	}
	if row.EventsStringProps == nil {
		row.EventsStringProps = []string{}
	}
	if row.EventsNumericProps == nil {
		row.EventsNumericProps = []string{}
	}
	statsRespond(w, r, row, err)
}
