package main

import (
	"net/http/httptest"
	"testing"
)

func TestIngestGeoIPDatabase(t *testing.T) {
	g, err := newIngestGeo("../etc", "SH")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.close)
	r := httptest.NewRequest("POST", "/api/v0/event", nil)
	r.Header.Set("CF-IPCountry", "CA")
	if country, _ := g.location(r, "8.8.8.8, 10.0.0.1"); country != "US" {
		t.Fatalf("database lookup or forwarding chain failed: %q", country)
	}
	for _, ip := range []string{"127.0.0.1", "invalid"} {
		if country, region := g.location(r, ip); country != "" || region != "" {
			t.Fatalf("unknown IP %q unexpectedly yielded %q, %q", ip, country, region)
		}
	}
}

func TestIngestGeoCloudSubdivisions(t *testing.T) {
	g, err := newIngestGeo("../etc", "US")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/v0/event", nil)
	r.Header.Set("cdn-requestcountrycode", "us")
	r.Header.Set("cdn-requeststatecode", "ca")
	r.Header.Set("CF-IPCountry", "DE")
	if country, region := g.location(r, "127.0.0.1"); country != "US" || region != "California" {
		t.Fatalf("wrong Cloud GeoIP result: %q, %q", country, region)
	}
}
