package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func authTestSSMEndpoint(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	endpoint := httptest.NewServer(handler)
	t.Cleanup(endpoint.Close)
	t.Setenv("AWS_ENDPOINT_URL_SSM", endpoint.URL)
	t.Setenv("AWS_REGION", "us-west-2")
	t.Setenv("AWS_ACCESS_KEY_ID", "local-mock-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "local-mock-secret-key")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_IGNORE_CONFIGURED_ENDPOINT_URLS", "false")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "no-aws-config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "no-aws-credentials"))
}

func authTestSSMRequest(t *testing.T, r *http.Request) string {
	t.Helper()
	if r.Method != http.MethodPost || r.Header.Get("X-Amz-Target") != "AmazonSSM.GetParametersByPath" ||
		!strings.Contains(r.Header.Get("Authorization"), "Credential=local-mock-access-key/") {
		t.Error("expected the standard SDK to sign a GetParametersByPath request")
	}
	var request struct {
		Path           string
		WithDecryption bool
		Recursive      bool
		NextToken      string
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Path != authLegacySSMPath || !request.WithDecryption || request.Recursive {
		t.Error("SSM request did not use the old provider's exact path and decryption behavior")
	}
	return request.NextToken
}

func authTestSSMParam(name, value string) map[string]string {
	return map[string]string{"Name": authLegacySSMPath + name, "Value": value, "Type": "SecureString"}
}

func TestAuthSSMPaginationAndRevocation(t *testing.T) {
	var requests atomic.Int32
	var malformed atomic.Bool
	const individual = "429a687c-897f-4dd1-b8dd-9003810e9af8"
	const beforeCutoff = "19306b6e-6a70-460a-9f45-792fc73d1951"
	const sameAsCutoff = "6ec94a42-9d8b-4423-80fe-cc7087c7976e"
	const cutoff = "2026-11-23T00:00:00Z"
	key := func(id, date string) map[string]string {
		return authTestSSMParam("key-"+id, fmt.Sprintf(`<key id=%q version="1"><creationDate>%s</creationDate></key>`, id, date))
	}
	authTestSSMEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		next := authTestSSMRequest(t, r)
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		if next == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"NextToken": "page-2", "Parameters": []map[string]string{
					key(individual, "2026-11-24T00:00:00Z"), key(beforeCutoff, "2026-11-22T23:59:59Z"), key(sameAsCutoff, cutoff),
				},
			})
			return
		}
		if next != "page-2" {
			t.Errorf("unexpected page token: %s", next)
		}
		mass := `<revocation version="1"><revocationDate>` + cutoff + `</revocationDate><key id="*" /></revocation>`
		if malformed.Load() {
			mass = `<revocation version="1"><key id="*" /></revocation>`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Parameters": []map[string]string{
			authTestSSMParam("revocation-"+individual, fmt.Sprintf(`<revocation version="1"><key id=%q /></revocation>`, strings.ToUpper(individual))),
			authTestSSMParam("revocation-20261123", mass),
		}})
	})
	load := newAuthLegacySSMLoader()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ring, err := load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ring.keys) != 3 || requests.Load() != 2 || !ring.revoked(*ring.keys[individual]) ||
		!ring.revoked(*ring.keys[beforeCutoff]) || ring.revoked(*ring.keys[sameAsCutoff]) {
		t.Fatal("SSM key ring pagination or revocation handling does not match ASP.NET")
	}
	malformed.Store(true)
	if ring, err := load(ctx); err == nil || ring != nil {
		t.Fatal("malformed revocation allowed a partial key ring")
	}
}

func TestAuthSSMCacheUnknownKeysAndFailure(t *testing.T) {
	t.Setenv("ASPNET_DATA_PROTECTION_KEY_DIR", "")
	var loads atomic.Int32
	var fail atomic.Bool
	started, release := make(chan struct{}), make(chan struct{})
	cached := authLegacySSMCache{load: func(ctx context.Context) (*authLegacyKeyRing, error) {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > authLegacySSMTimeout {
			t.Error("remote key loading must have a timeout")
		}
		if loads.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if fail.Load() {
			return nil, errors.New("simulated SSM outage")
		}
		return &authLegacyKeyRing{keys: make(map[string]*authLegacyKey)}, nil
	}}
	unknown := func(idx byte) string {
		// Structurally plausible envelope, with a fake key ID and no authentic MAC.
		var raw [100]byte
		copy(raw[:], authLegacyMagic)
		raw[4] = idx
		return base64.RawURLEncoding.EncodeToString(raw[:])
	}
	ctx := context.Background()
	for _, region := range []string{"SH", "DEV"} {
		if ticket, err := readLegacyAuthSession(ctx, unknown(0), region, &cached); ticket != nil || err != nil || loads.Load() != 0 {
			t.Fatalf("self-hosted region %s did remote work", region)
		}
	}
	if _, err := readLegacyAuthSession(ctx, "CfDJ8corrupt", "EU", &cached); err != nil || loads.Load() != 0 {
		t.Fatal("invalid envelope did remote work")
	}
	first, cancel := context.WithCancel(ctx)
	firstDone := make(chan error, 1)
	go func() {
		_, err := readLegacyAuthSession(first, unknown(1), "EU", &cached)
		firstDone <- err
	}()
	defer cancel()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("did not start SSM refresh")
	}
	cancel()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Errorf("canceled request got %v", err)
	}
	var group sync.WaitGroup
	for i := range 32 {
		group.Go(func() {
			if ticket, err := readLegacyAuthSession(ctx, unknown(byte(i)), "US", &cached); ticket != nil || err != nil {
				t.Errorf("unknown key yielded a session or remote error: %v", err)
			}
		})
	}
	close(release)
	group.Wait()
	if loads.Load() != 1 {
		t.Fatalf("unknown key IDs caused %d loads; want 1", loads.Load())
	}
	fail.Store(true)
	authTestExpireSSM(&cached)
	for range 3 {
		if ring, err := cached.get(ctx); err == nil || ring != nil {
			t.Fatal("stale keys were used after a failed refresh")
		}
	}
	if loads.Load() != 2 {
		t.Fatalf("failed refresh bypassed error backoff: loads=%d", loads.Load())
	}
}

func authTestExpireSSM(cache *authLegacySSMCache) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.expiresAt = time.Time{}
}

func testAuthSSMRealCookie(t *testing.T, cookie *http.Cookie) {
	// Use the genuine key only from the explicitly configured isolated reference service.
	payload, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil || len(payload) < 20 {
		t.Fatal("reference did not return an ASP.NET key identifier")
	}
	id := authLegacyGUID(payload[4:20])
	file := "key-" + id
	data, err := os.ReadFile(filepath.Join(os.Getenv("ASPNET_DATA_PROTECTION_KEY_DIR"), file+".xml"))
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	var revoked atomic.Bool
	authTestSSMEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_ = authTestSSMRequest(t, r)
		params := []map[string]string{authTestSSMParam(file, string(data))}
		if revoked.Load() {
			params = append(params, authTestSSMParam("revocation-"+id, `<revocation><key id="`+id+`" /></revocation>`))
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		_ = json.NewEncoder(w).Encode(map[string]any{"Parameters": params})
	})
	cache := authLegacySSMCache{load: newAuthLegacySSMLoader()}
	ctx := context.Background()
	local, err := readLegacyAuthSession(ctx, cookie.Value, "EU", &cache)
	if err != nil || local == nil || requests.Load() != 0 {
		t.Fatalf("local key ring did not take precedence: err=%v requests=%d", err, requests.Load())
	}
	t.Setenv("ASPNET_DATA_PROTECTION_KEY_DIR", t.TempDir())
	managed, err := readLegacyAuthSession(ctx, cookie.Value, "US", &cache)
	if err != nil || managed == nil || managed.Identity != local.Identity || requests.Load() != 1 {
		t.Fatalf("SSM did not authenticate the genuine .NET cookie: err=%v requests=%d", err, requests.Load())
	}
	revoked.Store(true)
	authTestExpireSSM(&cache)
	if ticket, err := readLegacyAuthSession(ctx, cookie.Value, "EU", &cache); err != nil || ticket != nil || requests.Load() != 2 {
		t.Fatalf("revoked SSM key was accepted after refresh: err=%v requests=%d", err, requests.Load())
	}
}
