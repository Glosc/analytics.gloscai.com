package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const billingTrialMonthlyQuota = 200_000_000

type billingPlan struct {
	Name            string     `json:"name"`
	MonthlyEvents   int64      `json:"monthlyEvents"`
	MonthlyPrice    int        `json:"monthlyPrice"`
	VariantID       int64      `json:"variantId"`
	FreeTrialEndsAt *time.Time `json:"freeTrialEndsAt"`
}

var billingPaidPlans = [...]struct {
	name          string
	monthlyEvents int64
	monthlyPrice  int
	productionID  int64
	devID         int64
}{
	{"200k Plan", 200_000, 10, 103474, 85183},
	{"1M Plan", 1_000_000, 20, 103475, 85184},
	{"2M Plan", 2_000_000, 40, 103476, 85185},
	{"5M Plan", 5_000_000, 75, 103477, 85187},
	{"10M Plan", 10_000_000, 140, 103478, 85188},
	{"20M Plan", 20_000_000, 240, 103479, 85190},
	{"30M Plan", 30_000_000, 300, 103480, 85192},
	{"50M Plan", 50_000_000, 450, 103481, 85194},
	{"100M Plan", 100_000_000, 750, 614418, 0},
	{"200M Plan", 200_000_000, 1200, 614417, 0},
	{"500M Plan", 500_000_000, 1500, 636430, 0},
}

func billingPlanForVariant(variant int64) (billingPlan, error) {
	for _, plan := range billingPaidPlans {
		if variant == plan.productionID || (variant != 0 && variant == plan.devID) {
			return billingPlan{
				Name: plan.name, MonthlyEvents: plan.monthlyEvents,
				MonthlyPrice: plan.monthlyPrice, VariantID: variant,
			}, nil
		}
	}
	return billingPlan{}, fmt.Errorf("unknown Lemon Squeezy variant %d", variant)
}

type billingSubscription struct {
	ID        int64      `json:"-"`
	VariantID int64      `json:"-"`
	Status    string     `json:"status"`
	EndsAt    *time.Time `json:"endsAt"`
}

type billingHistoricUsage struct {
	Date   time.Time `json:"date"`
	Events uint64    `json:"events"`
}

type billingHistoryRow struct {
	Date   analyticsTime
	Events analyticsCount
}

func (s *Server) billingManagedCloud() bool {
	return s.Config.Region == "EU" || s.Config.Region == "US"
}

func (s *Server) registerBilling(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/_billing", s.billingAuthenticated(s.billingState))
	mux.HandleFunc("GET /api/_billing/historical", s.billingAuthenticated(s.billingHistorical))
	mux.HandleFunc("POST /api/_billing/checkout", s.billingAuthenticated(s.billingCheckout))
	mux.HandleFunc("POST /api/_billing/portal", s.billingAuthenticated(s.billingPortal))
	mux.HandleFunc("POST /webhook/lemonsqueezy", s.billingWebhook)
}

func (s *Server) billingAuthenticated(handler func(http.ResponseWriter, *http.Request, *Identity)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, no-cache")
		if user := s.requireUser(w, r); user != nil {
			handler(w, r, user)
		}
	}
}

func (s *Server) billingSubscription(ctx context.Context, ownerID string) (*billingSubscription, error) {
	var sub billingSubscription
	err := s.DB.QueryRowContext(ctx, `SELECT id, variant_id, status, ends_at FROM subscriptions
		WHERE owner_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`, ownerID).
		Scan(&sub.ID, &sub.VariantID, &sub.Status, &sub.EndsAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

func (s *Server) billingFreePlan(ctx context.Context, ownerID string) (billingPlan, error) {
	var quota sql.NullInt64
	var endsAt sql.NullTime
	if err := s.DB.QueryRowContext(ctx, `SELECT free_quota, free_trial_ends_at FROM users WHERE id = $1`, ownerID).
		Scan(&quota, &endsAt); err != nil {
		return billingPlan{}, err
	}
	if quota.Valid {
		return billingPlan{Name: "Free Plan", MonthlyEvents: quota.Int64}, nil
	}
	if endsAt.Valid {
		return billingPlan{Name: "Free Trial", MonthlyEvents: billingTrialMonthlyQuota, FreeTrialEndsAt: &endsAt.Time}, nil
	}
	return billingPlan{}, errors.New("account has neither a free quota nor a trial")
}

func (s *Server) billingOwnedAppIDs(ctx context.Context, ownerID string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM apps WHERE owner_id = $1`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id, id+"_DEBUG")
	}
	return ids, rows.Err()
}

func (s *Server) billingState(w http.ResponseWriter, r *http.Request, user *Identity) {
	ctx := r.Context()
	sub, err := s.billingSubscription(ctx, user.ID)
	if err != nil {
		billingRequestError(w, "load subscription", err)
		return
	}
	var plan billingPlan
	if sub == nil || sub.Status == "expired" {
		plan, err = s.billingFreePlan(ctx, user.ID)
	} else {
		plan, err = billingPlanForVariant(sub.VariantID)
	}
	if err != nil {
		billingRequestError(w, "load plan", err)
		return
	}
	ids, err := s.billingOwnedAppIDs(ctx, user.ID)
	if err != nil {
		billingRequestError(w, "load owned apps", err)
		return
	}
	var usage uint64
	if len(ids) != 0 {
		row, err := analyticsFirst[struct{ Count analyticsCount }](ctx, s.Analytics, "get_billing_usage__v1",
			analyticsArgs{"app_ids": strings.Join(ids, ",")})
		if err != nil {
			billingRequestError(w, "load current usage", err)
			return
		}
		usage = uint64(row.Count)
	}
	state := "OK"
	if plan.MonthlyEvents <= 0 || usage >= uint64(plan.MonthlyEvents) {
		state = "OVERUSE"
	}
	now := time.Now().UTC()
	jsonResponse(w, http.StatusOK, map[string]any{
		"state": state, "usage": usage, "month": now.Month(), "year": now.Year(),
		"subscription": sub, "plan": plan,
	})
}

func (s *Server) billingHistorical(w http.ResponseWriter, r *http.Request, user *Identity) {
	ctx := r.Context()
	ids, err := s.billingOwnedAppIDs(ctx, user.ID)
	if err != nil {
		billingRequestError(w, "load owned apps", err)
		return
	}
	var usage []billingHistoryRow
	if len(ids) != 0 {
		usage, err = analyticsRows[billingHistoryRow](ctx, s.Analytics, "billing_historical_usage__v1",
			analyticsArgs{"app_ids": strings.Join(ids, ",")})
		if err != nil {
			billingRequestError(w, "load historical usage", err)
			return
		}
	}
	byMonth := make(map[time.Time]uint64, len(usage))
	for _, row := range usage {
		byMonth[billingMonth(row.Date.Time)] = uint64(row.Events)
	}
	current := billingMonth(time.Now())
	history := make([]billingHistoricUsage, 0, 7)
	for offset := -6; offset <= 0; offset++ {
		month := current.AddDate(0, offset, 0)
		history = append(history, billingHistoricUsage{Date: month, Events: byMonth[month]})
	}
	jsonResponse(w, http.StatusOK, history)
}

func billingMonth(at time.Time) time.Time {
	year, month, _ := at.UTC().Date()
	return time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
}

func billingRequestError(w http.ResponseWriter, operation string, err error) {
	slog.Error("billing request failed", "operation", operation, "error", err)
	jsonResponse(w, http.StatusInternalServerError, map[string]string{"message": "Unable to process billing request"})
}
