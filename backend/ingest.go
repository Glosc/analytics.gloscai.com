package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	maxIngestBody = 30_000_000
	maxEventBatch = 25
)

type Ingestion struct {
	db         *sql.DB
	write      func(context.Context, string, []map[string]any) error
	geo        *ingestGeo
	privacy    *ingestPrivacy
	events     *ingestQueue
	errors     *ingestQueue
	quota      *ingestQuota
	rejections *rejectionStats

	apps       *ingestCache[string, ingestApp]
	lookupMu   sync.Mutex
	lookups    map[string]*ingestAppLookup
	lookupGate chan struct{}
}

func newIngestion(s *Server) (*Ingestion, error) {
	geo, err := newIngestGeo(s.Config.EtcDir, s.Config.Region)
	if err != nil {
		return nil, err
	}
	i := &Ingestion{
		db: s.DB, write: s.Analytics.Write, geo: geo, privacy: newIngestPrivacy(s.DB), rejections: &s.rejections,
		events: newIngestQueue("events", 8192), errors: newIngestQueue("error_events", 2048),
		apps: newIngestCache[string, ingestApp](32768), lookups: make(map[string]*ingestAppLookup),
		lookupGate: make(chan struct{}, 16),
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("ERROR_QUOTA_ENABLED")), "true") {
		i.quota = newIngestQuota(s.DB)
	}
	return i, nil
}

func (s *Server) registerIngestion(mux *http.ServeMux) {
	for _, path := range []string{"/api/v0/event", "/api/v0/events", "/api/v0/error"} {
		mux.HandleFunc("OPTIONS "+path, func(w http.ResponseWriter, r *http.Request) {
			ingestHeaders(w, r)
			if r.Header.Get("Origin") != "" && r.Header.Get("Access-Control-Request-Method") != "" {
				w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, App-Key")
				w.Header().Set("Access-Control-Max-Age", "3600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusOK)
		})
		mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
			ingestHeaders(w, r)
			if !s.limit(w, r, "EventIngestion") {
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxIngestBody)
			s.Ingest.handle(w, r, path == "/api/v0/error", path == "/api/v0/events")
		})
	}
}

func ingestHeaders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Header.Get("Origin") != "" {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}
}

func (i *Ingestion) handle(w http.ResponseWriter, r *http.Request, reportError, batch bool) {
	key := strings.ToUpper(r.Header.Get("App-Key"))
	if strings.TrimSpace(key) == "" {
		i.recordRejection("missing_key", r)
		ingestRejectKey(w, reportError, http.StatusBadRequest, "Missing App-Key header", "Missing App-Key header.")
		return
	}
	app, err := i.lookupApp(r.Context(), key)
	if err != nil {
		ingestUnavailable(w)
		return
	}
	if app.id == "" {
		i.recordRejection("unknown_key", r)
		ingestRejectKey(w, reportError, http.StatusUnauthorized, "Invalid App-Key", "Application not found with given app key: "+key+".")
		return
	}
	if app.locked {
		i.recordRejection("locked", r)
		ingestRejectKey(w, reportError, http.StatusBadRequest, "Owner account is locked", "Owner account is locked.")
		return
	}
	if r.ContentLength > maxIngestBody {
		ingestBadBody(w, &http.MaxBytesError{Limit: maxIngestBody}, "")
		return
	}
	if reportError {
		i.handleError(w, r, app.id)
		return
	}
	i.handleEvents(w, r, app.id, batch)
}

func (i *Ingestion) recordRejection(reason string, r *http.Request) {
	if i.rejections != nil {
		i.rejections.record(reason, r)
	}
}

func ingestRejectKey(w http.ResponseWriter, reportError bool, status int, message, eventMessage string) {
	if !reportError {
		status = http.StatusOK
		message = eventMessage + " Events are not being recorded."
	}
	jsonResponse(w, status, map[string]string{"error": message})
}

func ingestUnavailable(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "5")
	jsonResponse(w, http.StatusServiceUnavailable, map[string]string{"error": "Ingestion temporarily unavailable"})
}

func ingestBadBody(w http.ResponseWriter, err error, message string) {
	var size *http.MaxBytesError
	if errors.As(err, &size) {
		jsonResponse(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "Request payload is too large"})
		return
	}
	jsonResponse(w, http.StatusBadRequest, map[string]string{"error": message})
}

func (i *Ingestion) handleEvents(w http.ResponseWriter, r *http.Request, appID string, batch bool) {
	var payloads []json.RawMessage
	if batch {
		if err := readJSON(r, &payloads); err != nil || payloads == nil {
			ingestBadBody(w, err, "Invalid event batch")
			return
		}
		if len(payloads) > maxEventBatch {
			jsonResponse(w, http.StatusBadRequest, fmt.Sprintf("Too many events (%d) in a single request. Maximum is %d.", len(payloads), maxEventBatch))
			return
		}
	} else {
		var payload json.RawMessage
		if err := readJSON(r, &payload); err != nil {
			ingestBadBody(w, err, "Invalid event payload")
			return
		}
		payloads = []json.RawMessage{payload}
	}

	now := time.Now().UTC()
	ip, agent := clientIP(r), r.UserAgent()
	country, region := i.geo.location(r, ip)
	rows := make([]ingestRecord, 0, len(payloads))
	agents := make([]string, 0, len(payloads))
	for _, payload := range payloads {
		var event ingestEventBody
		message := "Invalid event payload"
		if json.Unmarshal(payload, &event) == nil {
			message = event.validate(now)
		}
		if message != "" {
			if !batch {
				jsonResponse(w, http.StatusBadRequest, message)
				return
			}
			continue
		}
		fingerprintAgent := agent
		// Only the single-event API enriched Web user agents in the original ingestion pipeline.
		if !batch {
			fingerprintAgent = event.enrichAgent(agent)
		}
		rows = append(rows, newIngestRecord(event.row(appID, country, region)))
		agents = append(agents, fingerprintAgent)
	}
	if len(rows) == 0 {
		jsonResponse(w, http.StatusOK, struct{}{})
		return
	}
	if !i.events.reserve(rows) {
		ingestUnavailable(w)
		return
	}
	for n, record := range rows {
		row := record.data
		userID, err := i.privacy.userID(r.Context(), row["timestamp"].(time.Time), appID, row["session_id"].(string), ip, agents[n])
		if err != nil {
			i.events.release(rows)
			ingestUnavailable(w)
			return
		}
		row["user_id"] = userID
	}
	i.events.publish(rows)
	jsonResponse(w, http.StatusOK, struct{}{})
}

func (i *Ingestion) handleError(w http.ResponseWriter, r *http.Request, appID string) {
	var body ingestErrorBody
	if err := readJSON(r, &body); err != nil || !body.valid() {
		ingestBadBody(w, err, "Invalid error payload")
		return
	}
	now := time.Now().UTC()
	row, err := body.row(appID, now)
	if err != nil {
		ingestUnavailable(w)
		return
	}
	rows := []ingestRecord{newIngestRecord(row)}
	if !i.errors.reserve(rows) {
		ingestUnavailable(w)
		return
	}
	if i.quota != nil {
		allowed, err := i.quota.consume(r.Context(), appID, now)
		if err != nil {
			i.errors.release(rows)
			ingestUnavailable(w)
			return
		}
		if !allowed {
			i.errors.release(rows)
			// SDKs treat 429 as retryable. This report will not succeed within its retry window.
			jsonResponse(w, http.StatusForbidden, map[string]string{"error": "Error quota exceeded"})
			return
		}
	}
	i.errors.publish(rows)
	w.WriteHeader(http.StatusAccepted)
}
