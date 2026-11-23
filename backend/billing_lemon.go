package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const billingLemonAPI = "https://api.lemonsqueezy.com/v1"

var billingLemonHTTP = &http.Client{Timeout: 30 * time.Second}

type billingLemonAttributes struct {
	URL        string     `json:"url"`
	CustomerID int64      `json:"customer_id"`
	ProductID  int64      `json:"product_id"`
	VariantID  int64      `json:"variant_id"`
	Status     string     `json:"status"`
	EndsAt     *time.Time `json:"ends_at"`
	URLs       struct {
		CustomerPortal                   string  `json:"customer_portal"`
		CustomerPortalUpdateSubscription *string `json:"customer_portal_update_subscription"`
		UpdateCustomerPortal             string  `json:"update_customer_portal"`
	} `json:"urls"`
}

func (attr billingLemonAttributes) portalURL() string {
	updateURL := attr.URLs.UpdateCustomerPortal
	if attr.URLs.CustomerPortalUpdateSubscription != nil {
		updateURL = *attr.URLs.CustomerPortalUpdateSubscription
	}
	if updateURL != "" {
		return updateURL
	}
	return attr.URLs.CustomerPortal
}

type billingLemonResource struct {
	ID         string                  `json:"id"`
	Attributes *billingLemonAttributes `json:"attributes"`
}

func (s *Server) billingCheckout(w http.ResponseWriter, r *http.Request, user *Identity) {
	variantID := "103474"
	if s.Config.Development {
		variantID = "85183"
	}
	checkout := map[string]any{
		"custom": map[string]string{"user_id": user.ID, "region": s.Config.Region},
	}
	if user.Email != "" {
		checkout["email"] = user.Email
	}
	body := map[string]any{"data": map[string]any{
		"type": "checkouts",
		"attributes": map[string]any{
			"checkout_data":    checkout,
			"product_options":  map[string]string{"redirect_url": strings.TrimRight(s.Config.BaseURL, "/") + "/billing"},
			"checkout_options": map[string]bool{"embed": false, "dark": false},
		},
		"relationships": map[string]any{
			"store":   map[string]any{"data": map[string]string{"type": "stores", "id": "30508"}},
			"variant": map[string]any{"data": map[string]string{"type": "variants", "id": variantID}},
		},
	}}
	attr, err := billingLemonRequest(r.Context(), http.MethodPost, "/checkouts", body)
	if err != nil {
		billingLemonError(w, err)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]string{"url": attr.URL})
}

func (s *Server) billingPortal(w http.ResponseWriter, r *http.Request, user *Identity) {
	sub, err := s.billingSubscription(r.Context(), user.ID)
	if err != nil {
		billingRequestError(w, "load portal subscription", err)
		return
	}
	if sub == nil {
		jsonResponse(w, http.StatusNotFound, map[string]string{"message": "Subscription not found"})
		return
	}
	attr, err := billingLemonRequest(r.Context(), http.MethodGet, "/subscriptions/"+strconv.FormatInt(sub.ID, 10), nil)
	if err != nil {
		billingLemonError(w, err)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]string{"url": attr.portalURL()})
}

var errBillingLemonUnconfigured = errors.New("LEMONSQUEEZY_API_KEY is not configured")

func billingLemonRequest(ctx context.Context, method, path string, payload any) (billingLemonAttributes, error) {
	var attributes billingLemonAttributes
	key := os.Getenv("LEMONSQUEEZY_API_KEY")
	if key == "" {
		return attributes, errBillingLemonUnconfigured
	}
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return attributes, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, billingLemonAPI+path, body)
	if err != nil {
		return attributes, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/vnd.api+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/vnd.api+json")
	}
	resp, err := billingLemonHTTP.Do(req)
	if err != nil {
		return attributes, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return attributes, fmt.Errorf("Lemon Squeezy returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		Data billingLemonResource `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return attributes, err
	}
	if result.Data.Attributes != nil {
		attributes = *result.Data.Attributes
	}
	return attributes, nil
}

func billingLemonError(w http.ResponseWriter, err error) {
	if errors.Is(err, errBillingLemonUnconfigured) {
		jsonResponse(w, http.StatusServiceUnavailable, map[string]string{"message": "Billing is not configured"})
		return
	}
	slog.Error("Lemon Squeezy request failed", "error", err)
	jsonResponse(w, http.StatusBadGateway, map[string]string{"message": "Billing provider is unavailable"})
}

func billingValidSignature(secret, signature string, body []byte) bool {
	if secret == "" || len(signature) != hex.EncodedLen(sha256.Size) {
		return false
	}
	decoded, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), decoded)
}

func (s *Server) billingWebhook(w http.ResponseWriter, r *http.Request) {
	secret := os.Getenv("LEMONSQUEEZY_SIGNING_SECRET")
	signature := r.Header.Get("X-Signature")
	if secret == "" || len(signature) != hex.EncodedLen(sha256.Size) {
		jsonResponse(w, http.StatusUnauthorized, map[string]string{"message": "Invalid LemonSqueezy Signature"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		var tooLarge *http.MaxBytesError
		status := http.StatusBadRequest
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		jsonResponse(w, status, map[string]string{"message": "Invalid request body"})
		return
	}
	if !billingValidSignature(secret, signature, body) {
		jsonResponse(w, http.StatusUnauthorized, map[string]string{"message": "Invalid LemonSqueezy Signature"})
		return
	}
	var event struct {
		Meta struct {
			EventName  string            `json:"event_name"`
			CustomData map[string]string `json:"custom_data"`
		} `json:"meta"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"message": "Invalid JSON Body"})
		return
	}
	region, ok := event.Meta.CustomData["region"]
	if !ok || region == "" {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"message": "Missing 'region' on meta.custom_data"})
		return
	}
	if region != s.Config.Region {
		jsonResponse(w, http.StatusOK, map[string]string{"message": "Ignoring event from different region"})
		return
	}
	if event.Meta.EventName != "subscription_created" && event.Meta.EventName != "subscription_updated" {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"message": "Unknown event"})
		return
	}
	ownerID := event.Meta.CustomData["user_id"]
	if strings.TrimSpace(ownerID) == "" {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"message": "Missing 'user_id' on meta.custom_data"})
		return
	}
	var resource billingLemonResource
	if err := json.Unmarshal(event.Data, &resource); err != nil || resource.Attributes == nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"message": "Invalid subscription event body"})
		return
	}
	subID, err := strconv.ParseInt(resource.ID, 10, 64)
	attr := *resource.Attributes
	if err != nil || subID <= 0 || attr.Status == "" ||
		(attr.Status != "expired" && (attr.VariantID <= 0 || attr.ProductID <= 0 || attr.CustomerID <= 0)) {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"message": "Invalid subscription event body"})
		return
	}
	if err := s.billingApplySubscription(r.Context(), ownerID, subID, attr); err != nil {
		if errors.Is(err, errBillingSubscriptionOwner) {
			jsonResponse(w, http.StatusBadRequest, map[string]string{"message": "Invalid subscription owner"})
			return
		}
		billingRequestError(w, "apply Lemon Squeezy subscription", err)
		return
	}
	jsonResponse(w, http.StatusOK, struct{}{})
}

var errBillingSubscriptionOwner = errors.New("invalid subscription owner")

func (s *Server) billingApplySubscription(ctx context.Context, ownerID string, id int64, attr billingLemonAttributes) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE users SET lock_reason = NULL WHERE id = $1`, ownerID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return errBillingSubscriptionOwner
	}
	if attr.Status == "expired" {
		var existingOwner string
		err := tx.QueryRowContext(ctx, `DELETE FROM subscriptions WHERE id = $1 RETURNING owner_id`, id).Scan(&existingOwner)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && existingOwner != ownerID {
			return errBillingSubscriptionOwner
		}
	} else {
		result, err := tx.ExecContext(ctx, `INSERT INTO subscriptions
			(id, owner_id, customer_id, product_id, variant_id, status, ends_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (id) DO UPDATE SET product_id = EXCLUDED.product_id, variant_id = EXCLUDED.variant_id,
				status = EXCLUDED.status, ends_at = EXCLUDED.ends_at, modified_at = now()
			WHERE subscriptions.owner_id = EXCLUDED.owner_id`, id, ownerID, attr.CustomerID,
			attr.ProductID, attr.VariantID, attr.Status, attr.EndsAt)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			return errBillingSubscriptionOwner
		}
	}
	return tx.Commit()
}
