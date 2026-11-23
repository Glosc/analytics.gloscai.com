package main

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"time"
)

type ingestQuotaKey struct{ appID, period string }

type ingestQuota struct {
	db        *sql.DB
	exhausted *ingestCache[ingestQuotaKey, struct{}]
	period    atomic.Pointer[string]
	gate      chan struct{}
}

func newIngestQuota(db *sql.DB) *ingestQuota {
	return &ingestQuota{db: db, exhausted: newIngestCache[ingestQuotaKey, struct{}](16384), gate: make(chan struct{}, 1)}
}

func (q *ingestQuota) consume(ctx context.Context, appID string, now time.Time) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	period := ingestQuotaPeriod(now)
	if err := q.ensurePeriod(ctx, period); err != nil {
		return false, err
	}
	key := ingestQuotaKey{appID, period}
	if _, found := q.exhausted.get(key); found {
		return false, nil
	}
	var id string
	err := q.db.QueryRowContext(ctx, `
		UPDATE apps SET error_count = error_count + 1
		WHERE id = $1 AND error_count < error_quota
		RETURNING id`, appID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		q.exhausted.remember(key, struct{}{}, time.Now().Add(5*time.Minute))
		return false, nil
	}
	return err == nil, err
}

func ingestQuotaPeriod(now time.Time) string {
	return now.UTC().Add(-5 * time.Minute).Format("2006-01")
}

func (q *ingestQuota) ensurePeriod(ctx context.Context, period string) error {
	if current := q.period.Load(); current != nil && *current >= period {
		return nil
	}
	select {
	case q.gate <- struct{}{}:
		defer func() { <-q.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if current := q.period.Load(); current != nil && *current >= period {
		return nil
	}
	if err := q.advancePeriod(ctx, period); err != nil {
		return err
	}
	q.period.Store(&period)
	return nil
}

func (q *ingestQuota) advancePeriod(ctx context.Context, period string) error {
	const marker = "ingestion:error-quota-period"
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// A new deployment should preserve any consumption from the current month. Only an
	// advance of the shared marker resets counters, once even if multiple instances race.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO cache (key, value, expires_at) VALUES ($1, $2, 'infinity')
		ON CONFLICT (key) DO NOTHING`, marker, period); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE cache SET value = $2 WHERE key = $1 AND value < $2`, marker, period)
	if err != nil {
		return err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE apps SET error_count = 0 WHERE error_count > 0`); err != nil {
			return err
		}
	}
	return tx.Commit()
}
