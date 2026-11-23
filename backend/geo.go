package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/oschwald/maxminddb-golang/v2"
)

type ingestGeo struct {
	cloud   bool
	regions map[string]string
	mu      sync.RWMutex
	reader  *maxminddb.Reader
}

func newIngestGeo(etcDir, region string) (*ingestGeo, error) {
	if etcDir == "" {
		etcDir = "etc"
		if _, err := os.Stat(etcDir); os.IsNotExist(err) {
			etcDir = filepath.Join("..", "etc")
		}
	}
	dir := filepath.Join(etcDir, "geoip")
	geo := &ingestGeo{cloud: strings.EqualFold(region, "EU") || strings.EqualFold(region, "US")}
	if geo.cloud {
		data, err := os.ReadFile(filepath.Join(dir, "iso3166-2.json"))
		if err != nil {
			return nil, fmt.Errorf("read GeoIP region names: %w", err)
		}
		if err := json.Unmarshal(data, &geo.regions); err != nil {
			return nil, fmt.Errorf("decode GeoIP region names: %w", err)
		}
		return geo, nil
	}
	reader, err := maxminddb.Open(filepath.Join(dir, "GeoLite2-City.mmdb"))
	if err != nil {
		return nil, fmt.Errorf("open GeoIP database: %w", err)
	}
	geo.reader = reader
	return geo, nil
}

func (g *ingestGeo) close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.reader != nil {
		_ = g.reader.Close()
		g.reader = nil
	}
}

func (g *ingestGeo) location(r *http.Request, clientIP string) (country, region string) {
	if g.cloud {
		country = ingestFirstHeader(r, "cdn-requestcountrycode", "CloudFront-Viewer-Country", "CF-IPCountry")
		if len(country) != 2 || strings.Trim(country, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") != "" {
			return "", ""
		}
		code := ingestFirstHeader(r, "cdn-requeststatecode", "CloudFront-Viewer-Country-Region")
		return country, g.regions[country+"-"+code]
	}
	firstIP, _, _ := strings.Cut(clientIP, ",")
	address := strings.TrimSpace(firstIP)
	if strings.HasPrefix(address, "[") && strings.HasSuffix(address, "]") {
		address = address[1 : len(address)-1]
	}
	ip, err := netip.ParseAddr(address)
	if err != nil {
		return "", ""
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.reader == nil {
		return "", ""
	}
	var record struct {
		Country struct {
			ISOCode string `maxminddb:"iso_code"`
		} `maxminddb:"country"`
		Subdivisions []struct {
			Names struct {
				English string `maxminddb:"en"`
			} `maxminddb:"names"`
		} `maxminddb:"subdivisions"`
	}
	if result := g.reader.Lookup(ip.Unmap()); !result.Found() || result.Decode(&record) != nil {
		return "", ""
	}
	if n := len(record.Subdivisions); n != 0 {
		region = record.Subdivisions[n-1].Names.English
	}
	return strings.ToUpper(record.Country.ISOCode), region
}

func ingestFirstHeader(r *http.Request, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(r.Header.Get(key)); value != "" {
			return strings.ToUpper(value)
		}
	}
	return ""
}
