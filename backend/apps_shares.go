package main

import (
	"database/sql"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"
)

type appShare struct {
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"createdAt"`
}

type appShareSummary struct {
	OwnerEmail            *string `json:"ownerEmail"`
	HasOwnership          bool    `json:"hasOwnership"`
	SharedWithCurrentUser bool    `json:"sharedWithCurrentUser"`
	NumberOfOtherShares   int     `json:"numberOfOtherShares"`
}

func (s *Server) listAppShares(w http.ResponseWriter, r *http.Request, user *Identity) error {
	ctx, appID := r.Context(), r.PathValue("appId")
	if _, err := findOwnedApp(ctx, s.DB, user.ID, appID, false); err != nil {
		return err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT sh.email, sh.created_at FROM app_shares sh JOIN apps a ON a.id = sh.app_id
		WHERE a.owner_id = $1 AND a.id = $2 AND a.deleted_at IS NULL ORDER BY sh.created_at, sh.email`, user.ID, appID)
	if err != nil {
		return err
	}
	defer rows.Close()
	shares := []appShare{}
	for rows.Next() {
		var share appShare
		if err := rows.Scan(&share.Email, &share.CreatedAt); err != nil {
			return err
		}
		share.CreatedAt = share.CreatedAt.UTC()
		shares = append(shares, share)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, shares)
	return nil
}

func (s *Server) appShareInfo(w http.ResponseWriter, r *http.Request, user *Identity) error {
	var info appShareSummary
	err := s.DB.QueryRowContext(r.Context(), `SELECT u.email, a.owner_id = $1,
		COUNT(*) FILTER (WHERE sh.email = $2) > 0, COUNT(*) FILTER (WHERE sh.email <> $2)
		FROM apps a JOIN users u ON u.id = a.owner_id
		LEFT JOIN app_shares sh ON sh.app_id = a.id AND sh.email <> COALESCE(u.email, '')
		WHERE a.id = $3 AND a.deleted_at IS NULL AND `+appViewer+`
		GROUP BY a.id, u.email`, user.ID, strings.ToLower(user.Email), r.PathValue("appId")).
		Scan(&info.OwnerEmail, &info.HasOwnership, &info.SharedWithCurrentUser, &info.NumberOfOtherShares)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	jsonResponse(w, http.StatusOK, info)
	return nil
}

func validAppEmail(email string) bool {
	if !validAppText(email, 3, 300) {
		return false
	}
	addr, err := mail.ParseAddress(email)
	return err == nil && addr.Address == email
}

func (s *Server) changeAppShare(w http.ResponseWriter, r *http.Request, user *Identity) error {
	email := strings.ToLower(r.PathValue("email"))
	ctx := r.Context()
	err := s.withOwnedApp(ctx, user.ID, r.PathValue("appId"), func(tx *sql.Tx, app *appResponse) error {
		if r.Method == http.MethodDelete {
			// Also allow removal of legacy rows written before address validation.
			_, err := tx.ExecContext(ctx, `DELETE FROM app_shares WHERE app_id = $1 AND email = $2`, app.ID, email)
			return err
		}
		if !validAppEmail(email) {
			return appValidationError("email", "Enter a valid email address.")
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO app_shares (app_id, email) VALUES ($1, $2) ON CONFLICT DO NOTHING`, app.ID, email)
		return err
	})
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, struct{}{})
	return nil
}
