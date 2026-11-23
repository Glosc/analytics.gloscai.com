package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

func (s *Server) runBilling(ctx context.Context) {
	if !s.billingManagedCloud() && !s.Config.Development {
		return
	}
	mailer := s.billingMailer()
	var workers sync.WaitGroup
	for _, job := range []struct {
		name string
		next func(time.Time) time.Time
		run  func(context.Context, time.Time) error
	}{
		{"overuse notifications", billingNextOveruse, func(ctx context.Context, now time.Time) error {
			return s.billingNotifyOveruse(ctx, now, mailer)
		}},
		{"monthly overuse reset", billingNextReset, s.billingResetOveruse},
		{"trial expiration", billingNextDay, s.billingExpireTrials},
		{"trial reminders", billingNextDay, func(ctx context.Context, now time.Time) error {
			return s.billingNotifyTrials(ctx, now, mailer)
		}},
	} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for ctx.Err() == nil {
				timer := time.NewTimer(time.Until(job.next(time.Now().UTC())))
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case now := <-timer.C:
					if err := job.run(ctx, now.UTC()); err != nil && ctx.Err() == nil {
						slog.Error("billing job failed", "job", job.name, "error", err)
					}
				}
			}
		}()
	}
	workers.Wait()
}

func billingNextOveruse(now time.Time) time.Time {
	next := now.UTC().Truncate(time.Hour).Add(10 * time.Minute)
	if !next.After(now) {
		next = next.Add(time.Hour)
	}
	return next
}

func billingNextReset(now time.Time) time.Time {
	month := billingMonth(now)
	next := month.Add(5 * time.Minute)
	if !next.After(now) {
		next = month.AddDate(0, 1, 0).Add(5 * time.Minute)
	}
	return next
}

func billingNextDay(now time.Time) time.Time {
	return now.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
}

func (s *Server) billingResetOveruse(ctx context.Context, _ time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET lock_reason = NULL WHERE lock_reason = 'O'`)
	return err
}

func (s *Server) billingExpireTrials(ctx context.Context, now time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users u SET lock_reason = 'T'
		WHERE u.free_quota IS NULL AND u.free_trial_ends_at <= $1 AND u.lock_reason IS NULL
		AND NOT EXISTS (SELECT 1 FROM subscriptions s WHERE s.owner_id = u.id)`, now)
	return err
}

type billingContact struct{ id, name, email string }

func (s *Server) billingNotifyTrials(ctx context.Context, now time.Time, send billingMailer) error {
	start := now.Truncate(24*time.Hour).AddDate(0, 0, 5)
	rows, err := s.DB.QueryContext(ctx, `SELECT u.id, u.name, u.email FROM users u
		WHERE u.free_trial_ends_at >= $1 AND u.free_trial_ends_at < $2
		AND u.email IS NOT NULL AND u.email <> ''
		AND NOT EXISTS (SELECT 1 FROM subscriptions s WHERE s.owner_id = u.id)
		AND EXISTS (SELECT 1 FROM apps a WHERE a.owner_id = u.id AND a.has_events = true)`,
		start, start.AddDate(0, 0, 1))
	if err != nil {
		return err
	}
	defer rows.Close()
	var users []billingContact
	for rows.Next() {
		var user billingContact
		if err := rows.Scan(&user.id, &user.name, &user.email); err != nil {
			return err
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, user := range users {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := "TrialNotification." + user.id + "." + start.Format(time.DateOnly)
		err := s.billingNotifyOnce(ctx, key, func(*sql.Tx) error {
			return send(ctx, user.email, "Your Trial ends in 5 days", "TrialEndsSoon", map[string]string{
				"name": user.name, "url": s.Config.BaseURL,
			})
		})
		if err != nil {
			slog.Error("failed to send trial reminder", "user_id", user.id, "error", err)
		}
	}
	return nil
}

type billingUsageByApp struct {
	AppID string
	Count analyticsCount
}

type billingAccountUsage struct {
	billingContact
	freeQuota sql.NullInt64
	variantID sql.NullInt64
	usage     uint64
}

func (s *Server) billingUsageByAccount(ctx context.Context, usage []billingUsageByApp) (map[string]*billingAccountUsage, error) {
	ids := make([]string, 0, len(usage))
	byApp := make(map[string]uint64, len(usage))
	for _, row := range usage {
		if _, exists := byApp[row.AppID]; !exists {
			ids = append(ids, row.AppID)
		}
		byApp[row.AppID] += uint64(row.Count)
	}
	accounts := make(map[string]*billingAccountUsage)
	if len(ids) == 0 {
		return accounts, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT a.id, u.id, u.name, coalesce(u.email, ''), u.free_quota, s.variant_id
		FROM apps a JOIN users u ON u.id = a.owner_id
		LEFT JOIN LATERAL (
			SELECT variant_id FROM subscriptions WHERE owner_id = u.id AND status <> 'expired'
			ORDER BY created_at DESC, id DESC LIMIT 1
		) s ON true
		WHERE u.lock_reason IS NULL AND a.id = ANY($1::text[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var appID string
		var row billingAccountUsage
		if err := rows.Scan(&appID, &row.id, &row.name, &row.email, &row.freeQuota, &row.variantID); err != nil {
			return nil, err
		}
		account := accounts[row.id]
		if account == nil {
			account = &row
			accounts[row.id] = account
		}
		account.usage += byApp[appID]
	}
	return accounts, rows.Err()
}

func (s *Server) billingNotifyOveruse(ctx context.Context, now time.Time, send billingMailer) error {
	period := billingMonth(now)
	usage, err := analyticsRows[billingUsageByApp](ctx, s.Analytics, "billing_usage_per_app__v1",
		analyticsArgs{"period": period.Format(time.DateOnly)})
	if err != nil {
		return err
	}
	accounts, err := s.billingUsageByAccount(ctx, usage)
	if err != nil {
		return err
	}
	for _, account := range accounts {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.billingNotifyAccountUsage(ctx, period, account, send); err != nil {
			slog.Error("failed to process billing usage", "user_id", account.id, "error", err)
		}
	}
	return nil
}

func (s *Server) billingNotifyAccountUsage(ctx context.Context, period time.Time, account *billingAccountUsage, send billingMailer) error {
	quota := int64(billingTrialMonthlyQuota)
	if account.variantID.Valid {
		plan, err := billingPlanForVariant(account.variantID.Int64)
		if err != nil {
			return err
		}
		quota = plan.MonthlyEvents
	} else if account.freeQuota.Valid {
		quota = account.freeQuota.Int64
	}
	templateName, subject := billingUsageAlert(account.usage, quota)
	if templateName == "" {
		return nil
	}
	key := "OveruseNotification." + account.id + "." + templateName + "." + period.Format("2006-01")
	return s.billingNotifyOnce(ctx, key, func(tx *sql.Tx) error {
		if account.email != "" {
			name, _, _ := strings.Cut(account.name, " ")
			err := send(ctx, account.email, subject, templateName, map[string]string{
				"name": name, "quota": billingFormatQuota(quota), "url": s.Config.BaseURL,
			})
			if err != nil {
				return err
			}
		}
		if templateName == "UsageLevel100" {
			_, err := tx.ExecContext(ctx, `UPDATE users SET lock_reason = 'O' WHERE id = $1 AND lock_reason IS NULL`, account.id)
			return err
		}
		return nil
	})
}

func billingUsageAlert(usage uint64, quota int64) (templateName, subject string) {
	switch {
	case quota <= 0 || usage >= uint64(quota):
		return "UsageLevel100", "ACTION REQUIRED: Event ingestion paused"
	case float64(usage)/float64(quota) >= 0.9:
		return "UsageLevel90", "WARNING: You have used 90% of your monthly limit, incoming events will be dropped soon"
	case float64(usage)/float64(quota) >= 0.8:
		return "UsageLevel80", "You have used 80% of your monthly limit, incoming events will be dropped soon"
	default:
		return "", ""
	}
}

func billingFormatQuota(quota int64) string {
	value := strconv.FormatInt(quota, 10)
	sign := ""
	if strings.HasPrefix(value, "-") {
		sign, value = "-", value[1:]
	}
	var formatted strings.Builder
	formatted.WriteString(sign)
	for i := range len(value) {
		if i > 0 && (len(value)-i)%3 == 0 {
			formatted.WriteByte(',')
		}
		formatted.WriteByte(value[i])
	}
	return formatted.String()
}

func (s *Server) billingNotifyOnce(ctx context.Context, key string, action func(*sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Serialize matching notifications across replicas without holding a user row lock while sending email.
	var locked bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1, 0))`, key).Scan(&locked); err != nil || !locked {
		return err
	}
	var present string
	err = tx.QueryRowContext(ctx, `SELECT value FROM cache WHERE key = $1 AND expires_at > now()`, key).Scan(&present)
	if err == nil && present != "" {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := action(tx); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO cache (key, value, expires_at) VALUES ($1, $2, now() + interval '10 days')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, expires_at = EXCLUDED.expires_at`, key, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	return tx.Commit()
}
