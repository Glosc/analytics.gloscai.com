package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
)

type appResponse struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	AppKey       string  `json:"appKey"`
	IconPath     string  `json:"iconPath"`
	HasEvents    bool    `json:"hasEvents"`
	HasOwnership bool    `json:"hasOwnership"`
	LockReason   *string `json:"lockReason"`
}

type appInput struct {
	Name string `json:"name"`
	Icon string `json:"icon"`
}

func (s *Server) registerApps(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/_apps", s.withAppsUser(s.listApps))
	mux.HandleFunc("POST /api/_apps", s.withAppsUser(s.createApp))
	mux.HandleFunc("GET /api/_apps/{appId}", s.withAppsUser(s.getApp))
	mux.HandleFunc("PUT /api/_apps/{appId}", s.withAppsUser(s.updateApp))
	mux.HandleFunc("DELETE /api/_apps/{appId}", s.withAppsUser(s.deleteApp))
	mux.HandleFunc("GET /api/_apps/{appId}/shares", s.withAppsUser(s.listAppShares))
	mux.HandleFunc("GET /api/_apps/{appId}/shares/info", s.withAppsUser(s.appShareInfo))
	mux.HandleFunc("PUT /api/_apps/{appId}/shares/{email}", s.withAppsUser(s.changeAppShare))
	mux.HandleFunc("DELETE /api/_apps/{appId}/shares/{email}", s.withAppsUser(s.changeAppShare))
	mux.HandleFunc("GET /api/_apps/{appId}/requests", s.withAppsUser(s.getAppRequest))
	mux.HandleFunc("POST /api/_apps/{appId}/requests", s.withAppsUser(s.createAppRequest))
	mux.HandleFunc("DELETE /api/_apps/{appId}/requests", s.withAppsUser(s.cancelAppRequest))
	mux.HandleFunc("GET /api/_app-requests", s.withAppsUser(s.listIncomingAppRequests))
	mux.HandleFunc("POST /api/_apps/{appId}/requests/accept", s.withAppsUser(s.acceptAppRequest))
	mux.HandleFunc("POST /api/_apps/{appId}/requests/reject", s.withAppsUser(s.rejectAppRequest))
	s.registerAppFlags(mux)
	mux.HandleFunc("GET /uploads/{path...}", s.readAppBlob)
}

func (s *Server) withAppsUser(handler func(http.ResponseWriter, *http.Request, *Identity) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		user := s.requireUser(w, r)
		if user == nil {
			return
		}
		if err := handler(w, r, user); err != nil {
			writeAppError(w, r, err)
		}
	}
}

type appHTTPError struct {
	status int
	body   any
}

func (e appHTTPError) Error() string { return http.StatusText(e.status) }

func appValidationError(field, message string) error {
	return appHTTPError{http.StatusBadRequest, map[string]any{
		"status": http.StatusBadRequest,
		"errors": map[string][]string{field: {message}},
	}}
}

func writeAppError(w http.ResponseWriter, r *http.Request, err error) {
	var response appHTTPError
	switch {
	case errors.As(err, &response):
		jsonResponse(w, response.status, response.body)
	case errors.Is(err, sql.ErrNoRows):
		w.WriteHeader(http.StatusNotFound)
	default:
		slog.ErrorContext(r.Context(), "app API request failed", "route", r.Pattern, "error", err)
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
	}
}

func readAppJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	if err := readJSON(r, dst); err != nil {
		return appValidationError("body", "Invalid request body.")
	}
	return nil
}

// ASP.NET's length validators count UTF-16 code units.
func validAppText(value string, minLen, maxLen int) bool {
	if strings.TrimSpace(value) == "" || strings.ContainsRune(value, 0) {
		return false
	}
	length := 0
	for _, r := range value {
		length++
		if r > 0xffff {
			length++
		}
	}
	return length >= minLen && length <= maxLen
}

const appColumns = `a.id, a.name, COALESCE(a.icon_path, ''), a.app_key, a.owner_id = $1, a.has_events, u.lock_reason`

// Email shares are for current accounts, not deleted or stale session identities.
const appViewer = `(a.owner_id = $1 OR ($2 <> '' AND EXISTS (
	SELECT 1 FROM app_shares sh WHERE sh.app_id = a.id AND sh.email = $2
) AND EXISTS (SELECT 1 FROM users viewer WHERE viewer.id = $1 AND lower(viewer.email) = $2)))`

func scanApp(row interface{ Scan(...any) error }) (appResponse, error) {
	var app appResponse
	err := row.Scan(&app.ID, &app.Name, &app.IconPath, &app.AppKey, &app.HasOwnership, &app.HasEvents, &app.LockReason)
	return app, err
}

func (s *Server) canReadApp(ctx context.Context, user *Identity, appID string) (bool, error) {
	if user == nil || user.ID == "" || appID == "" {
		return false, nil
	}
	var allowed bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM apps a JOIN users u ON u.id = a.owner_id
		WHERE a.id = $3 AND a.deleted_at IS NULL AND u.lock_reason IS NULL AND `+appViewer+`
	)`, user.ID, strings.ToLower(user.Email), appID).Scan(&allowed)
	return allowed, err
}

func (s *Server) listApps(w http.ResponseWriter, r *http.Request, user *Identity) error {
	rows, err := s.DB.QueryContext(r.Context(), `SELECT `+appColumns+`
		FROM apps a JOIN users u ON u.id = a.owner_id
		WHERE a.deleted_at IS NULL AND `+appViewer+` ORDER BY a.name`, user.ID, strings.ToLower(user.Email))
	if err != nil {
		return err
	}
	defer rows.Close()
	apps := []appResponse{}
	for rows.Next() {
		app, err := scanApp(rows)
		if err != nil {
			return err
		}
		apps = append(apps, app)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, apps)
	return nil
}

func (s *Server) getApp(w http.ResponseWriter, r *http.Request, user *Identity) error {
	app, err := scanApp(s.DB.QueryRowContext(r.Context(), `SELECT `+appColumns+`
		FROM apps a JOIN users u ON u.id = a.owner_id
		WHERE a.id = $3 AND a.deleted_at IS NULL AND `+appViewer,
		user.ID, strings.ToLower(user.Email), r.PathValue("appId")))
	if errors.Is(err, sql.ErrNoRows) {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, app)
	return nil
}

func (s *Server) createApp(w http.ResponseWriter, r *http.Request, user *Identity) error {
	var input appInput
	if err := readAppJSON(w, r, &input); err != nil {
		return err
	}
	if !validAppText(input.Name, 2, 40) {
		return appValidationError("name", "App name must be between 2 and 40 characters.")
	}
	app := appResponse{ID: newID(), Name: input.Name}
	for range 5 {
		digits, err := rand.Int(rand.Reader, big.NewInt(10000000000))
		if err != nil {
			return err
		}
		app.AppKey = fmt.Sprintf("A-%s-%010d", strings.ToUpper(s.Config.Region), digits.Int64())
		var ownerExists, created bool
		err = s.DB.QueryRowContext(r.Context(), `WITH owner AS (SELECT id FROM users WHERE id = $2), created AS (
			INSERT INTO apps (id, owner_id, name, app_key, has_events)
			SELECT $1, owner.id, $3, $4, false FROM owner ON CONFLICT (app_key) DO NOTHING RETURNING id
		) SELECT EXISTS (SELECT 1 FROM owner), EXISTS (SELECT 1 FROM created)`, app.ID, user.ID, app.Name, app.AppKey).
			Scan(&ownerExists, &created)
		if err != nil {
			return err
		}
		if !ownerExists {
			w.WriteHeader(http.StatusUnauthorized)
			return nil
		}
		if created {
			jsonResponse(w, http.StatusOK, app)
			return nil
		}
	}
	return errors.New("could not allocate a unique app key")
}

type appRowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func findOwnedApp(ctx context.Context, db appRowQueryer, ownerID, appID string, lock bool) (appResponse, error) {
	query := `SELECT ` + appColumns + ` FROM apps a JOIN users u ON u.id = a.owner_id
		WHERE a.owner_id = $1 AND a.id = $2 AND a.deleted_at IS NULL`
	if lock {
		query += ` FOR UPDATE OF a`
	}
	return scanApp(db.QueryRowContext(ctx, query, ownerID, appID))
}

func (s *Server) withOwnedApp(ctx context.Context, ownerID, appID string, change func(*sql.Tx, *appResponse) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	app, err := findOwnedApp(ctx, tx, ownerID, appID, true)
	if err != nil {
		return err
	}
	if err := change(tx, &app); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Server) updateApp(w http.ResponseWriter, r *http.Request, user *Identity) error {
	var input appInput
	if err := readAppJSON(w, r, &input); err != nil {
		return err
	}
	if !validAppText(input.Name, 2, 40) {
		return appValidationError("name", "App name must be between 2 and 40 characters.")
	}
	ctx := r.Context()
	var updated appResponse
	err := s.withOwnedApp(ctx, user.ID, r.PathValue("appId"), func(tx *sql.Tx, app *appResponse) error {
		if input.Icon != "" {
			path, err := storeAppIcon(ctx, tx, input.Icon)
			if err != nil {
				return err
			}
			app.IconPath = path
		}
		app.Name = input.Name
		updated = *app
		_, err := tx.ExecContext(ctx, `UPDATE apps SET name = $1, icon_path = $2 WHERE id = $3`, app.Name, app.IconPath, app.ID)
		return err
	})
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, updated)
	return nil
}

func (s *Server) deleteApp(w http.ResponseWriter, r *http.Request, user *Identity) error {
	ctx := r.Context()
	err := s.withOwnedApp(ctx, user.ID, r.PathValue("appId"), func(tx *sql.Tx, app *appResponse) error {
		_, err := tx.ExecContext(ctx, `UPDATE apps SET deleted_at = now() WHERE id = $1`, app.ID)
		return err
	})
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, struct{}{})
	return nil
}
