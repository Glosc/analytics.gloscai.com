package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Snapshot of all 61 HTTP action attributes in src/Features/**/*Controller.cs
// at 53a6277, plus the two OAuthExtensions callback paths and Program.cs's
// MapHealthChecks("/healthz"). Keep it independent of the Go registration
// code so the test still works after the C# source is removed.
var legacyRouteContract = []string{
	// AppsController
	"GET /api/_apps",
	"POST /api/_apps",
	"GET /api/_apps/{appId}",
	"PUT /api/_apps/{appId}",
	"DELETE /api/_apps/{appId}",
	"GET /api/_apps/{appId}/shares",
	"GET /api/_apps/{appId}/shares/info",
	"PUT /api/_apps/{appId}/shares/{email}",
	"DELETE /api/_apps/{appId}/shares/{email}",
	"GET /api/_apps/{appId}/requests",
	"POST /api/_apps/{appId}/requests",
	"DELETE /api/_apps/{appId}/requests",
	"GET /api/_app-requests",
	"POST /api/_apps/{appId}/requests/accept",
	"POST /api/_apps/{appId}/requests/reject",
	// AuthController and OAuthExtensions
	"POST /api/_auth/register",
	"POST /api/_auth/login",
	"GET /api/_auth/github",
	"GET /api/_auth/google",
	"GET /api/_auth/providers",
	"GET /api/_auth/me",
	"POST /api/_auth/account/delete",
	"POST /api/_auth/signout",
	"GET /api/_auth/github/callback",
	"GET /api/_auth/google/callback",
	// BillingController and LemonSqueezyWebhookController
	"GET /api/_billing",
	"GET /api/_billing/historical",
	"POST /api/_billing/checkout",
	"POST /api/_billing/portal",
	"POST /webhook/lemonsqueezy",
	// UploadsController
	"GET /uploads/{**path}",
	// ErrorsController
	"POST /api/v0/error",
	"OPTIONS /api/v0/error",
	"GET /api/v0/apps/{appId}/errors",
	"GET /api/v0/apps/{appId}/errors/types",
	"GET /api/v0/apps/{appId}/errors/{errorId}",
	// ExportController
	"GET /api/_export/usage",
	"GET /api/_export/download",
	// FeatureFlagsController and FeatureFlagStateController
	"GET /api/_flags/{appId}",
	"POST /api/_flags",
	"PUT /api/_flags/{flagId}",
	"DELETE /api/_flags/{flagId}",
	"GET /api/v0/feature-flags/{featureFlagKey}",
	"GET /api/v0/feature-flags",
	// EventsController
	"POST /api/v0/event",
	"OPTIONS /api/v0/event",
	"POST /api/v0/events",
	"OPTIONS /api/v0/events",
	// StatsController
	"GET /api/_stats/top-countries",
	"GET /api/_stats/top-osversions",
	"GET /api/_stats/top-devices",
	"GET /api/_stats/top-operatingsystems",
	"GET /api/_stats/top-regions",
	"GET /api/_stats/top-events",
	"GET /api/_stats/top-appversions",
	"GET /api/_stats/top-appbuildnumbers",
	"GET /api/_stats/metrics",
	"GET /api/_stats/periodic",
	"GET /api/_stats/top-props",
	"GET /api/_stats/live-geo",
	"GET /api/_stats/live-sessions",
	"GET /api/_stats/live-session-details",
	"GET /api/_stats/historical-sessions",
	// Program.cs: MapHealthChecks is method-agnostic.
	"/healthz",
}

func TestLegacyRoutesRegistered(t *testing.T) {
	for _, mode := range []struct {
		name string
		dev  bool
	}{{"production", false}, {"development", true}} {
		t.Run(mode.name, func(t *testing.T) {
			routes := routeContractHandler(t, mode.dev)
			for _, entry := range legacyRouteContract {
				t.Run(entry, func(t *testing.T) {
					method, path, qualified := strings.Cut(entry, " ")
					if !qualified {
						testMethodlessHealthRoute(t, routes, entry)
						return
					}
					want := strings.ReplaceAll(entry, "{**path}", "{path...}")
					testRoutePattern(t, routes, method, routeContractExample(path), want)
				})
			}
		})
	}
}

func TestAdditionalRoutesRegistered(t *testing.T) {
	production := routeContractHandler(t, false)
	development := routeContractHandler(t, true)
	// A production SPA match must never count as an API registration.
	testRoutePattern(t, production, http.MethodGet, "/api/unknown-route-contract-path", "GET /")
	testRoutePattern(t, production, http.MethodPatch, "/api/_apps/contract-app", "")
	for _, health := range []string{"/health", "/alive"} {
		// The old Aspire helper defined these paths, but Program.cs never called it.
		testMethodlessHealthRoute(t, development, health)
		testRoutePattern(t, production, http.MethodGet, health, "GET /")
		testRoutePattern(t, production, http.MethodPost, health, "")
	}
	for _, auth := range []string{"/api/_auth/me", "/api/_auth/account/delete"} {
		testRoutePattern(t, production, http.MethodOptions, auth, "OPTIONS "+auth)
	}
}

func testMethodlessHealthRoute(t *testing.T, routes http.Handler, path string) {
	t.Helper()
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, "PROBE"} {
		testRoutePattern(t, routes, method, path, path)
	}
}

func routeContractHandler(t *testing.T, dev bool) http.Handler {
	t.Helper()
	t.Setenv("OAUTH_GITHUB_CLIENT_ID", "")
	t.Setenv("OAUTH_GOOGLE_CLIENT_ID", "")
	// Uploads can reach the DB even without credentials. A closed pool makes
	// dispatch observable without contacting a DB or requiring any service.
	db, err := sql.Open("pgx", "postgres://unused@127.0.0.1:1/unused")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s := &Server{Config: Config{Development: dev, WebDir: t.TempDir()}, DB: db, Ingest: &Ingestion{}}
	return s.routes()
}

func routeContractExample(path string) string {
	return strings.NewReplacer(
		"{appId}", "contract-app",
		"{email}", "reader%2Bteam%40example.test",
		"{errorId}", "contract-error",
		"{flagId}", "contract-flag-id",
		"{featureFlagKey}", "contract-flag-key",
		"{**path}", "icons/nested/contract-icon.png",
	).Replace(path)
}

func testRoutePattern(t *testing.T, routes http.Handler, method, path, want string) {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	routes.ServeHTTP(w, r)
	// Pattern distinguishes the selected route from the SPA fallback, even if
	// both return the same status or health is dispatched outside ServeMux.
	if r.Pattern != want {
		t.Errorf("%s %s matched %q (status %d), want %q", method, path, r.Pattern, w.Code, want)
	}
}
