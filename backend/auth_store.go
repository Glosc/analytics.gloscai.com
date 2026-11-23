package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const authAccountColumns = `id, name, username, coalesce(email, ''), role, lock_reason`

type authQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func authScanAccount(row *sql.Row, extra ...any) (*authAccount, error) {
	var user authAccount
	args := append([]any{&user.ID, &user.Name, &user.Username, &user.Email, &user.Role, &user.LockReason}, extra...)
	err := row.Scan(args...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	user.AvatarURL = authAvatar(user.Email, user.Username)
	return &user, nil
}

func (s *Server) authAccountByID(ctx context.Context, id string) (*authAccount, error) {
	return authScanAccount(s.DB.QueryRowContext(ctx, `SELECT `+authAccountColumns+` FROM users WHERE id = $1`, id))
}

func (s *Server) authPasswordAccount(ctx context.Context, username string) (*authAccount, string, error) {
	var hash string
	user, err := authScanAccount(s.DB.QueryRowContext(ctx, `SELECT `+authAccountColumns+`, coalesce(password_hash, '')
		FROM users WHERE username = $1`, username), &hash)
	return user, hash, err
}

func authInsertAccount(ctx context.Context, db authQueryer, name, username, email string, passwordHash *string) (*authAccount, error) {
	const query = `INSERT INTO users (id, name, username, email, password_hash, role, free_quota)
		VALUES ($1, $2, $3, nullif($4, ''), $5,
			CASE WHEN $6::boolean OR EXISTS (SELECT 1 FROM users WHERE role = 'admin') THEN 'user' ELSE 'admin' END,
			200000000)
		ON CONFLICT DO NOTHING RETURNING ` + authAccountColumns
	id := newID()
	user, err := authScanAccount(db.QueryRowContext(ctx, query, id, name, username, email, passwordHash, false))
	if user != nil || err != nil {
		return user, err
	}
	// The partial unique index decides concurrent attempts to take the admin slot.
	return authScanAccount(db.QueryRowContext(ctx, query, id, name, username, email, passwordHash, true))
}

func (s *Server) authOAuthAccount(ctx context.Context, provider, subject, name, email string) (*authAccount, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Repeated callbacks for a provider identity must not create unlinked users.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "aptabase/auth/"+provider+"/"+subject); err != nil {
		return nil, err
	}
	user, err := authScanAccount(tx.QueryRowContext(ctx, `SELECT u.id, u.name, u.username, coalesce(u.email, ''), u.role, u.lock_reason
		FROM user_providers p JOIN users u ON u.id = p.user_id WHERE p.provider_name = $1 AND p.provider_uid = $2`, provider, subject))
	if user != nil || err != nil {
		return user, err
	}

	email = strings.ToLower(email)
	byEmail := func() (*authAccount, error) {
		return authScanAccount(tx.QueryRowContext(ctx, `SELECT `+authAccountColumns+` FROM users WHERE email = $1`, email))
	}
	user, err = byEmail()
	if err != nil {
		return nil, err
	}
	if user == nil {
		baseUsername := authUsernameFromEmail(email)
		for attempt := 0; attempt < 5; attempt++ {
			username := baseUsername
			if attempt > 0 {
				id := newID()
				username += "_" + strings.ToLower(id[:min(6, len(id))])
			}
			user, err = authInsertAccount(ctx, tx, name, username, email, nil)
			if err != nil {
				return nil, err
			}
			if user != nil {
				break
			}
			// A different provider may have just inserted the account for this email.
			user, err = byEmail()
			if err != nil {
				return nil, err
			}
			if user != nil {
				break
			}
		}
	}
	if user == nil {
		return nil, errors.New("could not allocate an OAuth username")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_providers (provider_name, provider_uid, user_id)
		VALUES ($1, $2, $3)`, provider, subject, user.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return user, nil
}

func authUsernameFromEmail(email string) string {
	local, _, _ := strings.Cut(email, "@")
	var username strings.Builder
	for i := 0; i < len(local) && username.Len() < 30; i++ {
		c := local[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-' {
			username.WriteByte(c)
		}
	}
	if username.Len() < 3 {
		return "user"
	}
	return username.String()
}

func (s *Server) authDeleteAccount(ctx context.Context, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, query := range []string{
		`DELETE FROM app_requests WHERE current_owner_id = $1 OR app_id IN (SELECT id FROM apps WHERE owner_id = $1)`,
		`DELETE FROM feature_flags WHERE app_id IN (SELECT id FROM apps WHERE owner_id = $1)`,
		`DELETE FROM app_shares WHERE app_id IN (SELECT id FROM apps WHERE owner_id = $1)`,
		`DELETE FROM app_salts WHERE app_id IN (SELECT id FROM apps WHERE owner_id = $1)`,
		`DELETE FROM apps WHERE owner_id = $1`,
		`DELETE FROM user_providers WHERE user_id = $1`,
		`DELETE FROM subscriptions WHERE owner_id = $1`,
		`DELETE FROM users WHERE id = $1`,
	} {
		if _, err := tx.ExecContext(ctx, query, id); err != nil {
			return fmt.Errorf("delete account: %w", err)
		}
	}
	return tx.Commit()
}
