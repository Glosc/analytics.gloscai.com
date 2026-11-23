package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	appOwnershipPurpose = "AppOwnership"
	appSharePurpose     = "AppShare"
)

type appRequestResponse struct {
	TargetUserEmail string    `json:"targetUserEmail"`
	RequestedAt     time.Time `json:"requestedAt"`
	Status          string    `json:"status"`
	Purpose         string    `json:"purpose"`
}

type incomingAppRequest struct {
	AppID          string    `json:"appId"`
	AppName        string    `json:"appName"`
	RequesterEmail *string   `json:"requesterEmail"`
	RequestedAt    time.Time `json:"requestedAt"`
	Purpose        string    `json:"purpose"`
}

func appRequestPurpose(value string, optional bool) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		if optional {
			return "", nil
		}
		return appOwnershipPurpose, nil
	case "appownership", "0":
		return appOwnershipPurpose, nil
	case "appshare", "1":
		return appSharePurpose, nil
	default:
		return "", appValidationError("purpose", "Purpose must be AppOwnership or AppShare.")
	}
}

func (s *Server) getAppRequest(w http.ResponseWriter, r *http.Request, user *Identity) error {
	purpose, err := appRequestPurpose(r.URL.Query().Get("purpose"), false)
	if err != nil {
		return err
	}
	ctx, appID := r.Context(), r.PathValue("appId")
	if _, err := findOwnedApp(ctx, s.DB, user.ID, appID, false); err != nil {
		return err
	}
	var request appRequestResponse
	err = s.DB.QueryRowContext(ctx, `SELECT r.target_user_email, r.requested_at, r.status, r.purpose
		FROM app_requests r JOIN apps a ON a.id = r.app_id AND a.owner_id = r.current_owner_id
		WHERE a.id = $1 AND a.owner_id = $2 AND r.purpose = $3 AND r.status = 'pending' AND a.deleted_at IS NULL
		ORDER BY r.requested_at LIMIT 1`, appID, user.ID, purpose).
		Scan(&request.TargetUserEmail, &request.RequestedAt, &request.Status, &request.Purpose)
	if errors.Is(err, sql.ErrNoRows) {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	if err != nil {
		return err
	}
	request.RequestedAt = request.RequestedAt.UTC()
	jsonResponse(w, http.StatusOK, request)
	return nil
}

func (s *Server) createAppRequest(w http.ResponseWriter, r *http.Request, user *Identity) error {
	var input struct {
		TargetUserEmail string          `json:"targetUserEmail"`
		Purpose         json.RawMessage `json:"purpose"`
	}
	if err := readAppJSON(w, r, &input); err != nil {
		return err
	}
	var value string
	switch string(input.Purpose) {
	case "":
	case "0", "1":
		value = string(input.Purpose)
	default:
		if string(input.Purpose) == "null" || json.Unmarshal(input.Purpose, &value) != nil || strings.TrimSpace(value) == "" {
			return appValidationError("purpose", "Purpose must be AppOwnership or AppShare.")
		}
	}
	purpose, err := appRequestPurpose(value, false)
	if err != nil {
		return err
	}
	email := strings.ToLower(input.TargetUserEmail)
	if !validAppEmail(email) {
		return appValidationError("targetUserEmail", "Enter a valid email address.")
	}
	ctx := r.Context()
	err = s.withOwnedApp(ctx, user.ID, r.PathValue("appId"), func(tx *sql.Tx, app *appResponse) error {
		if email == strings.ToLower(user.Email) {
			return appValidationError("targetUserEmail", "Cannot create request to yourself")
		}
		var pending bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM app_requests
			WHERE app_id = $1 AND current_owner_id = $2 AND purpose = $3 AND status = 'pending')`, app.ID, user.ID, purpose).Scan(&pending); err != nil {
			return err
		}
		if pending {
			return appValidationError("targetUserEmail", "There is already a pending request for this app")
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO app_requests (app_id, current_owner_id, target_user_email, purpose, requested_at, status)
			VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP AT TIME ZONE 'UTC', 'pending')`, app.ID, user.ID, email, purpose)
		return err
	})
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, struct{}{})
	return nil
}

func (s *Server) cancelAppRequest(w http.ResponseWriter, r *http.Request, user *Identity) error {
	purpose, err := appRequestPurpose(r.URL.Query().Get("purpose"), false)
	if err != nil {
		return err
	}
	ctx := r.Context()
	err = s.withOwnedApp(ctx, user.ID, r.PathValue("appId"), func(tx *sql.Tx, app *appResponse) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM app_requests WHERE app_id = $1 AND purpose = $2 AND status = 'pending'`, app.ID, purpose)
		return err
	})
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, struct{}{})
	return nil
}

func (s *Server) listIncomingAppRequests(w http.ResponseWriter, r *http.Request, user *Identity) error {
	purpose, err := appRequestPurpose(r.URL.Query().Get("purpose"), true)
	if err != nil {
		return err
	}
	requests := []incomingAppRequest{}
	email := strings.ToLower(user.Email)
	if email == "" {
		jsonResponse(w, http.StatusOK, requests)
		return nil
	}
	rows, err := s.DB.QueryContext(r.Context(), `SELECT r.app_id, a.name, u.email, r.requested_at, r.purpose
		FROM app_requests r JOIN apps a ON a.id = r.app_id AND a.owner_id = r.current_owner_id
		JOIN users u ON u.id = r.current_owner_id
		JOIN users recipient ON recipient.id = $3 AND lower(recipient.email) = r.target_user_email
		WHERE r.target_user_email = $1 AND r.status = 'pending' AND a.deleted_at IS NULL AND ($2 = '' OR r.purpose = $2)
		ORDER BY r.requested_at DESC`, email, purpose, user.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var request incomingAppRequest
		if err := rows.Scan(&request.AppID, &request.AppName, &request.RequesterEmail, &request.RequestedAt, &request.Purpose); err != nil {
			return err
		}
		request.RequestedAt = request.RequestedAt.UTC()
		requests = append(requests, request)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, requests)
	return nil
}

func (s *Server) acceptAppRequest(w http.ResponseWriter, r *http.Request, user *Identity) error {
	return s.resolveAppRequest(w, r, user, true)
}

func (s *Server) rejectAppRequest(w http.ResponseWriter, r *http.Request, user *Identity) error {
	return s.resolveAppRequest(w, r, user, false)
}

func (s *Server) resolveAppRequest(w http.ResponseWriter, r *http.Request, user *Identity, accept bool) error {
	purpose, err := appRequestPurpose(r.URL.Query().Get("purpose"), false)
	if err != nil {
		return err
	}
	notFound := appHTTPError{http.StatusNotFound, "No pending app request found"}
	email := strings.ToLower(user.Email)
	if email == "" {
		return notFound
	}
	ctx, appID := r.Context(), r.PathValue("appId")
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Use the same app lock as owner mutations before resolving a pending request.
	var ownerID string
	var ownerEmail *string
	err = tx.QueryRowContext(ctx, `SELECT a.owner_id, u.email FROM apps a JOIN users u ON u.id = a.owner_id
		JOIN users recipient ON recipient.id = $2 AND lower(recipient.email) = $3
		WHERE a.id = $1 AND a.owner_id <> $2 AND a.deleted_at IS NULL AND EXISTS (
			SELECT 1 FROM app_requests r WHERE r.app_id = a.id AND r.current_owner_id = a.owner_id
			AND r.target_user_email = $3 AND r.purpose = $4 AND r.status = 'pending'
		) FOR UPDATE OF a FOR KEY SHARE OF recipient`, appID, user.ID, email, purpose).Scan(&ownerID, &ownerEmail)
	if errors.Is(err, sql.ErrNoRows) {
		return notFound
	}
	if err != nil {
		return err
	}
	status := "rejected"
	if accept {
		status = "accepted"
	}
	result, err := tx.ExecContext(ctx, `UPDATE app_requests SET status = $1, completed_at = CURRENT_TIMESTAMP AT TIME ZONE 'UTC'
		WHERE app_id = $2 AND current_owner_id = $3 AND target_user_email = $4 AND purpose = $5 AND status = 'pending'`,
		status, appID, ownerID, email, purpose)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return notFound
	}
	if accept {
		shareEmail := email
		if purpose == appOwnershipPurpose {
			if _, err := tx.ExecContext(ctx, `UPDATE apps SET owner_id = $1 WHERE id = $2`, user.ID, appID); err != nil {
				return err
			}
			// Requests issued by the former owner can no longer grant access.
			if _, err := tx.ExecContext(ctx, `DELETE FROM app_requests WHERE app_id = $1 AND current_owner_id = $2 AND status = 'pending'`, appID, ownerID); err != nil {
				return err
			}
			shareEmail = ""
			if ownerEmail != nil {
				shareEmail = strings.ToLower(*ownerEmail)
			}
		}
		if shareEmail != "" {
			if _, err := tx.ExecContext(ctx, `INSERT INTO app_shares (app_id, email) VALUES ($1, $2) ON CONFLICT DO NOTHING`, appID, shareEmail); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, struct{}{})
	return nil
}
