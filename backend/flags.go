package main

import (
	"context"
	"database/sql"
	"net/http"
)

type appFlagResponse struct {
	ID          *string `json:"id"`
	AppID       *string `json:"appId"`
	Key         string  `json:"key"`
	Value       string  `json:"value"`
	Environment string  `json:"environment"`
}

type appFlagInput struct {
	AppID       string `json:"appId"`
	Key         string `json:"key"`
	Value       string `json:"value"`
	Environment string `json:"environment"`
}

func (s *Server) registerAppFlags(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/_flags/{appId}", s.withAppsUser(s.listAppFlags))
	mux.HandleFunc("POST /api/_flags", s.withAppsUser(s.createAppFlag))
	mux.HandleFunc("PUT /api/_flags/{flagId}", s.withAppsUser(s.updateAppFlag))
	mux.HandleFunc("DELETE /api/_flags/{flagId}", s.withAppsUser(s.deleteAppFlag))

	mux.HandleFunc("GET /api/v0/feature-flags", s.publicAppFlags)
	mux.HandleFunc("GET /api/v0/feature-flags/{featureFlagKey}", s.publicAppFlags)
}

func scanAppFlag(row interface{ Scan(...any) error }) (appFlagResponse, error) {
	var flag appFlagResponse
	err := row.Scan(&flag.ID, &flag.AppID, &flag.Key, &flag.Value, &flag.Environment)
	return flag, err
}

func readAppFlagInput(w http.ResponseWriter, r *http.Request) (appFlagInput, error) {
	var input appFlagInput
	if err := readAppJSON(w, r, &input); err != nil {
		return input, err
	}
	for _, field := range []struct {
		name, value, message string
		minLen, maxLen       int
	}{
		{"key", input.Key, "Key must be between 2 and 255 characters.", 2, 255},
		{"value", input.Value, "Value must be between 1 and 4000 characters.", 1, 4000},
		{"environment", input.Environment, "Environment must be between 2 and 50 characters.", 2, 50},
	} {
		if !validAppText(field.value, field.minLen, field.maxLen) {
			return input, appValidationError(field.name, field.message)
		}
	}
	return input, nil
}

func (s *Server) listAppFlags(w http.ResponseWriter, r *http.Request, user *Identity) error {
	ctx, appID := r.Context(), r.PathValue("appId")
	if _, err := findOwnedApp(ctx, s.DB, user.ID, appID, false); err != nil {
		return err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT f.id, f.app_id, f.key, f.value, f.environment
		FROM feature_flags f JOIN apps a ON a.id = f.app_id
		WHERE a.owner_id = $1 AND a.id = $2 AND a.deleted_at IS NULL
		ORDER BY f.key, f.environment, f.id LIMIT 50`, user.ID, appID)
	if err != nil {
		return err
	}
	defer rows.Close()
	flags := []appFlagResponse{}
	for rows.Next() {
		flag, err := scanAppFlag(rows)
		if err != nil {
			return err
		}
		flags = append(flags, flag)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, flags)
	return nil
}

func (s *Server) createAppFlag(w http.ResponseWriter, r *http.Request, user *Identity) error {
	input, err := readAppFlagInput(w, r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var owned appResponse
	err = s.withOwnedApp(ctx, user.ID, input.AppID, func(tx *sql.Tx, app *appResponse) error {
		owned = *app
		_, err := tx.ExecContext(ctx, `INSERT INTO feature_flags (id, app_id, key, value, environment, conditions)
			VALUES ($1, $2, $3, $4, $5, '')`, newID(), app.ID, input.Key, input.Value, input.Environment)
		return err
	})
	if err != nil {
		return err
	}
	// The create endpoint historically returns the parent app, not the flag.
	jsonResponse(w, http.StatusOK, owned)
	return nil
}

func (s *Server) withOwnedAppFlag(ctx context.Context, ownerID, flagID string, change func(*sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var appID string
	err = tx.QueryRowContext(ctx, `SELECT a.id FROM apps a JOIN feature_flags f ON f.app_id = a.id
		JOIN users u ON u.id = a.owner_id
		WHERE f.id = $1 AND a.owner_id = $2 AND a.deleted_at IS NULL FOR UPDATE OF a`, flagID, ownerID).Scan(&appID)
	if err != nil {
		return err
	}
	if err := change(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Server) updateAppFlag(w http.ResponseWriter, r *http.Request, user *Identity) error {
	input, err := readAppFlagInput(w, r)
	if err != nil {
		return err
	}
	ctx, flagID := r.Context(), r.PathValue("flagId")
	var flag appFlagResponse
	err = s.withOwnedAppFlag(ctx, user.ID, flagID, func(tx *sql.Tx) error {
		var err error
		flag, err = scanAppFlag(tx.QueryRowContext(ctx, `UPDATE feature_flags
			SET key = $1, value = $2, environment = $3, modified_at = now() WHERE id = $4
			RETURNING id, app_id, key, value, environment`, input.Key, input.Value, input.Environment, flagID))
		return err
	})
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, flag)
	return nil
}

func (s *Server) deleteAppFlag(w http.ResponseWriter, r *http.Request, user *Identity) error {
	ctx, flagID := r.Context(), r.PathValue("flagId")
	err := s.withOwnedAppFlag(ctx, user.ID, flagID, func(tx *sql.Tx) error {
		var id string
		return tx.QueryRowContext(ctx, `DELETE FROM feature_flags WHERE id = $1 RETURNING id`, flagID).Scan(&id)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
