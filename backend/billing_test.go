package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBillingPlanVariants(t *testing.T) {
	for _, tt := range []struct {
		id, quota int64
		price     int
	}{{85183, 200_000, 10}, {103474, 200_000, 10}, {614417, 200_000_000, 1200}, {636430, 500_000_000, 1500}} {
		plan, err := billingPlanForVariant(tt.id)
		if err != nil || plan.VariantID != tt.id || plan.MonthlyEvents != tt.quota || plan.MonthlyPrice != tt.price {
			t.Errorf("variant %d: got %+v, %v", tt.id, plan, err)
		}
	}
	if _, err := billingPlanForVariant(0); err == nil {
		t.Error("free variant ID accepted as paid")
	}
}

func TestBillingSchedulesAndThresholds(t *testing.T) {
	utc := func(month time.Month, day, hour, minute int) time.Time {
		return time.Date(2026, month, day, hour, minute, 0, 0, time.UTC)
	}
	for _, tt := range []struct {
		name string
		next func(time.Time) time.Time
		from time.Time
		want time.Time
	}{
		{"hourly", billingNextOveruse, utc(12, 31, 23, 9), utc(12, 31, 23, 10)},
		{"hourly boundary", billingNextOveruse, utc(11, 23, 13, 10), utc(11, 23, 14, 10)},
		{"daily", billingNextDay, utc(11, 30, 23, 59), utc(12, 1, 0, 0)},
		{"monthly", billingNextReset, utc(12, 1, 0, 4), utc(12, 1, 0, 5)},
		{"next month", billingNextReset, utc(11, 1, 0, 5), utc(12, 1, 0, 5)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.next(tt.from.In(time.FixedZone("UTC+8", 8*60*60))); !got.Equal(tt.want) {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
	for _, tt := range []struct {
		usage uint64
		want  string
	}{{79, ""}, {80, "UsageLevel80"}, {89, "UsageLevel80"}, {90, "UsageLevel90"}, {99, "UsageLevel90"}, {100, "UsageLevel100"}, {150, "UsageLevel100"}} {
		if got, _ := billingUsageAlert(tt.usage, 100); got != tt.want {
			t.Errorf("usage %d%%: got %s, want %s", tt.usage, got, tt.want)
		}
	}
	if got := billingFormatQuota(200_000_000); got != "200,000,000" {
		t.Errorf("invalid formatted quota %q", got)
	}
}

func billingTestWebhookRequest(body, secret string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/webhook/lemonsqueezy", strings.NewReader(body))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	req.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
	return req
}

func TestBillingWebhookValidation(t *testing.T) {
	const secret = "test signing key"
	t.Setenv("LEMONSQUEEZY_SIGNING_SECRET", secret)
	s := &Server{Config: Config{Region: "US"}}
	mux := http.NewServeMux()
	s.registerBilling(mux)
	for _, tt := range []struct {
		name, body, secret string
		want               int
	}{
		{"invalid signature", `{}`, "wrong signing key", http.StatusUnauthorized},
		{"invalid JSON", `{`, secret, http.StatusBadRequest},
		{"missing region", `{"meta":{"event_name":"subscription_created"}}`, secret, http.StatusBadRequest},
		{"cross-region", `{"meta":{"custom_data":{"region":"EU"}},"data":"unused"}`, secret, http.StatusOK},
		{"unknown event", `{"meta":{"event_name":"unknown","custom_data":{"region":"US"}}}`, secret, http.StatusBadRequest},
		{"missing user", `{"meta":{"event_name":"subscription_created","custom_data":{"region":"US"}}}`, secret, http.StatusBadRequest},
		{"oversize", strings.Repeat(" ", (1<<20)+1), secret, http.StatusRequestEntityTooLarge},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, billingTestWebhookRequest(tt.body, tt.secret))
			if w.Code != tt.want {
				t.Errorf("got HTTP %d: %s, want %d", w.Code, w.Body, tt.want)
			}
		})
	}
	t.Setenv("LEMONSQUEEZY_SIGNING_SECRET", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, billingTestWebhookRequest(`{}`, ""))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("unconfigured webhook accepted HTTP %d", w.Code)
	}
}

type billingTestRoundTripper func(*http.Request) (*http.Response, error)

func (f billingTestRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBillingCheckoutProviderContract(t *testing.T) {
	t.Setenv("LEMONSQUEEZY_API_KEY", "test-key")
	previous := billingLemonHTTP
	t.Cleanup(func() { billingLemonHTTP = previous })
	for _, tt := range []struct {
		region, variant string
		development     bool
	}{{"DEV", "85183", true}, {"US", "103474", false}} {
		t.Run(tt.region, func(t *testing.T) {
			billingLemonHTTP = &http.Client{Transport: billingTestRoundTripper(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodPost || r.URL.String() != billingLemonAPI+"/checkouts" ||
					r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Content-Type") != "application/vnd.api+json" {
					t.Errorf("invalid checkout request: %s %s, %v", r.Method, r.URL, r.Header)
				}
				var body struct {
					Data struct {
						Attributes struct {
							CheckoutData struct {
								Email  string
								Custom map[string]string
							} `json:"checkout_data"`
							ProductOptions map[string]string `json:"product_options"`
						}
						Relationships map[string]struct{ Data map[string]string }
					}
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					return nil, err
				}
				checkout := body.Data.Attributes.CheckoutData
				if checkout.Email != "owner@example.test" || checkout.Custom["region"] != tt.region || checkout.Custom["user_id"] != "owner" ||
					body.Data.Attributes.ProductOptions["redirect_url"] != "https://example.test/billing" ||
					body.Data.Relationships["variant"].Data["id"] != tt.variant || body.Data.Relationships["store"].Data["id"] != "30508" {
					t.Errorf("invalid checkout body: %+v", body)
				}
				return &http.Response{StatusCode: 201, Body: io.NopCloser(strings.NewReader(`{"data":{"attributes":{"url":"https://example.test/checkout"}}}`))}, nil
			})}
			s := &Server{Config: Config{Region: tt.region, Development: tt.development, BaseURL: "https://example.test/"}}
			w := httptest.NewRecorder()
			s.billingCheckout(w, httptest.NewRequest(http.MethodPost, "/api/_billing/checkout", nil), &Identity{ID: "owner", Email: "owner@example.test"})
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"url":"https://example.test/checkout"`) {
				t.Errorf("HTTP %d: %s", w.Code, w.Body)
			}
		})
	}
}

func TestBillingPortalURLFallback(t *testing.T) {
	for _, tt := range []struct{ urls, want string }{
		{`"customer_portal_update_subscription":"preferred", "update_customer_portal":"alternate"`, "preferred"},
		{`"customer_portal_update_subscription":null, "update_customer_portal":"alternate"`, "alternate"},
		{`"customer_portal":"plain"`, "plain"},
	} {
		var attributes billingLemonAttributes
		if err := json.Unmarshal(fmt.Appendf(nil, `{"urls":{%s}}`, tt.urls), &attributes); err != nil {
			t.Fatal(err)
		}
		if got := attributes.portalURL(); got != tt.want {
			t.Errorf("got portal link %q, want %q", got, tt.want)
		}
	}
}

func TestBillingWorkerModeAndCancellation(t *testing.T) {
	(&Server{Config: Config{Region: "SH"}}).runBilling(context.Background())
	for _, config := range []Config{{Region: "DEV", Development: true}, {Region: "EU"}, {Region: "US"}} {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		(&Server{Config: config}).runBilling(ctx)
	}
}
