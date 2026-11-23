package main

import (
	"net/http"
	"strings"
)

func (s *Server) publicAppFlags(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.limit(w, r, "FeatureFlags") {
		return
	}
	if err := s.readPublicAppFlags(w, r); err != nil {
		writeAppError(w, r, err)
	}
}

func (s *Server) readPublicAppFlags(w http.ResponseWriter, r *http.Request) error {
	appKey := strings.ToUpper(r.Header.Get("App-Key"))
	notFound := appHTTPError{http.StatusNotFound, "Appplication not found with given app key: " + appKey}
	if strings.TrimSpace(appKey) == "" || len(appKey) > 80 {
		return notFound
	}
	flagKey := r.PathValue("featureFlagKey")
	limit := 50
	if flagKey != "" {
		limit = 1
	}
	// Legacy App-Key lookup marks the app as onboarded on first SDK use.
	rows, err := s.DB.QueryContext(r.Context(), `WITH target AS (
			SELECT a.id, u.lock_reason IS NOT NULL AS locked FROM apps a JOIN users u ON u.id = a.owner_id
			WHERE a.app_key = $1 AND a.deleted_at IS NULL
		), mark_onboarded AS (
			UPDATE apps a SET has_events = true FROM target t
			WHERE a.id = t.id AND NOT a.has_events AND a.deleted_at IS NULL
		)
		SELECT t.locked, f.key, f.value FROM target t
		LEFT JOIN LATERAL (
			SELECT f.key, f.value FROM feature_flags f
			WHERE f.app_id = t.id AND ($2 = '' OR f.key = $2) AND NOT t.locked
			ORDER BY f.key, f.environment, f.id LIMIT $3
		) f ON true`, appKey, flagKey, limit)
	if err != nil {
		return err
	}
	defer rows.Close()
	flags := []appFlagResponse{}
	foundApp := false
	for rows.Next() {
		var locked bool
		var key, value *string
		if err := rows.Scan(&locked, &key, &value); err != nil {
			return err
		}
		foundApp = true
		if locked {
			s.rejections.record("locked", r)
			return appHTTPError{http.StatusBadRequest, "Owner account is locked."}
		}
		if key != nil && value != nil {
			flags = append(flags, appFlagResponse{Key: *key, Value: *value})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !foundApp {
		return notFound
	}
	if flagKey == "" {
		jsonResponse(w, http.StatusOK, flags)
	} else if len(flags) == 0 {
		w.WriteHeader(http.StatusNoContent)
	} else {
		jsonResponse(w, http.StatusOK, flags[0])
	}
	return nil
}
