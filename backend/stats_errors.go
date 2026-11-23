package main

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (s *Server) registerErrorReads(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v0/apps/{appId}/errors", s.statsErrors)
	mux.HandleFunc("GET /api/v0/apps/{appId}/errors/types", s.statsErrorTypes)
	mux.HandleFunc("GET /api/v0/apps/{appId}/errors/{errorId}", s.statsErrorDetail)
}

type statsError struct {
	ErrorID      string        `json:"errorId"`
	AppID        string        `json:"appId"`
	Timestamp    analyticsTime `json:"timestamp"`
	ErrorMessage string        `json:"errorMessage"`
	ErrorType    string        `json:"errorType"`
	StackTrace   string        `json:"stackTrace"`
	Platform     string        `json:"platform"`
	OSName       string        `json:"osName"`
	OSVersion    string        `json:"osVersion"`
	AppVersion   string        `json:"appVersion"`
	SDKVersion   string        `json:"sdkVersion"`
	SessionID    string        `json:"sessionId"`
	Severity     string        `json:"severity"`
	Kind         string        `json:"kind"`
}

type statsErrorPage struct {
	Offset int            `json:"offset"`
	Limit  int            `json:"limit"`
	Total  analyticsCount `json:"total"`
}

func (s *Server) statsErrors(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("appId")
	if !s.analyticsCanRead(w, r, appID, true) {
		return
	}
	q := statsQueryValues(r, false)
	args, err := statsErrorDateArgs(q, appID)
	if err != nil {
		statsBadQuery(w, "Invalid start or end date.")
		return
	}
	limit, limitErr := statsErrorInt(q, "limit", 50)
	offset, offsetErr := statsErrorInt(q, "offset", 0)
	if limitErr != nil || offsetErr != nil {
		statsBadQuery(w, "Invalid limit or offset.")
		return
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset = max(offset, 0)
	for param, key := range map[string]string{"errortype": "error_type", "osname": "os_name", "severity": "severity", "kind": "kind"} {
		if v := q.Get(param); strings.TrimSpace(v) != "" {
			args[key] = v
		}
	}
	ctx := r.Context()
	args["limit"], args["offset"] = strconv.Itoa(limit), strconv.Itoa(offset)
	rows, err := analyticsRows[statsError](ctx, s.Analytics, "get_errors__v1", args)
	if err != nil {
		statsRespond(w, r, nil, err)
		return
	}
	delete(args, "limit")
	delete(args, "offset")
	count, err := analyticsFirst[struct{ Count analyticsCount }](ctx, s.Analytics, "get_error_count__v1", args)
	statsRespond(w, r, struct {
		Errors     []statsError   `json:"errors"`
		Pagination statsErrorPage `json:"pagination"`
	}{rows, statsErrorPage{Offset: offset, Limit: limit, Total: count.Count}}, err)
}

func (s *Server) statsErrorTypes(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("appId")
	if !s.analyticsCanRead(w, r, appID, true) {
		return
	}
	args, err := statsErrorDateArgs(statsQueryValues(r, false), appID)
	if err != nil {
		statsBadQuery(w, "Invalid start or end date.")
		return
	}
	rows, err := analyticsRows[struct {
		Name  string         `json:"name"`
		Value analyticsCount `json:"value"`
	}](r.Context(), s.Analytics, "get_error_types__v1", args)
	statsRespond(w, r, rows, err)
}

func (s *Server) statsErrorDetail(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("appId")
	if !s.analyticsCanRead(w, r, appID, true) {
		return
	}
	q := statsQueryValues(r, false)
	args := analyticsArgs{"app_id": analyticsBuildAppID(appID, q.Get("buildmode")), "error_id": r.PathValue("errorId")}
	rows, err := analyticsRows[statsError](r.Context(), s.Analytics, "get_error_by_id__v1", args)
	if err == nil && len(rows) == 0 {
		jsonResponse(w, http.StatusNotFound, map[string]string{"error": "Error not found"})
		return
	}
	if err != nil {
		statsRespond(w, r, nil, err)
		return
	}
	statsRespond(w, r, rows[0], nil)
}

func statsErrorDateArgs(q url.Values, appID string) (analyticsArgs, error) {
	start, end, err := statsDateRange(q)
	if err != nil {
		return nil, err
	}
	if end.IsZero() {
		end = time.Now().UTC()
	}
	if start.IsZero() {
		start = end.AddDate(0, 0, -7)
	}
	return analyticsArgs{
		"app_id":     analyticsBuildAppID(appID, q.Get("buildmode")),
		"start_date": start.Format(analyticsDateTime),
		"end_date":   end.Format(analyticsDateTime),
	}, nil
}

func statsErrorInt(q url.Values, name string, fallback int) (int, error) {
	value, exists := q[name]
	if !exists {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(value[0]), 10, 32)
	return int(parsed), err
}
