package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const analyticsDateTime = "2006-01-02 15:04:05"

type analyticsCoordinates struct {
	Latitude  float64 `json:"lat"`
	Longitude float64 `json:"lng"`
}

type Analytics struct {
	client      *http.Client
	endpoint    *url.URL
	tinybird    bool
	token       string
	etcDir      string
	queries     analyticsQueries
	coordinates map[string]map[string]analyticsCoordinates
}

func newAnalytics(c Config) (*Analytics, error) {
	a := &Analytics{
		client: &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		etcDir: c.EtcDir,
	}
	if a.etcDir == "" {
		a.etcDir = "etc"
	}
	var err error
	if c.ClickHouseURL != "" {
		a.endpoint, err = analyticsClickHouseURL(c.ClickHouseURL)
	} else {
		a.tinybird, a.token = true, c.TinybirdToken
		a.endpoint, err = analyticsHTTPURL(c.TinybirdURL)
	}
	if err != nil {
		return nil, err
	}
	coordinates, err := os.ReadFile(filepath.Join(a.etcDir, "geoip", "coordinates.json"))
	if err == nil {
		if err := json.Unmarshal(coordinates, &a.coordinates); err != nil {
			return nil, fmt.Errorf("decode analytics coordinates: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read analytics coordinates: %w", err)
	}
	return a, nil
}

func analyticsHTTPURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Fragment != "" {
		return nil, errors.New("analytics endpoint must be a valid HTTP(S) URL")
	}
	return u, nil
}

func analyticsClickHouseURL(value string) (*url.URL, error) {
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return analyticsHTTPURL(value)
	}
	// Accept the deployment's existing ClickHouse.Client connection strings.
	parts, err := connectionFields(value)
	if err != nil {
		return nil, fmt.Errorf("invalid ClickHouse connection string: %w", err)
	}
	host := strings.Trim(parts["host"], "[]")
	if host == "" {
		return nil, errors.New("ClickHouse connection string has no host")
	}
	protocol := strings.ToLower(parts["protocol"])
	if protocol == "" {
		protocol = "http"
	}
	if protocol != "http" && protocol != "https" {
		return nil, errors.New("ClickHouse protocol must be HTTP(S)")
	}
	port := parts["port"]
	if port == "" {
		port = "8123"
		if protocol == "https" {
			port = "8443"
		}
	}
	u := &url.URL{Scheme: protocol, Host: net.JoinHostPort(host, port)}
	var username string
	for _, key := range []string{"username", "user", "user id", "userid"} {
		if username = parts[key]; username != "" {
			break
		}
	}
	if username != "" || parts["password"] != "" {
		if username == "" {
			username = "default"
		}
		u.User = url.UserPassword(username, parts["password"])
	}
	if database := parts["database"]; database != "" {
		u.RawQuery = url.Values{"database": {database}}.Encode()
	}
	if _, err := analyticsHTTPURL(u.String()); err != nil {
		return nil, err
	}
	return u, nil
}

func (a *Analytics) Migrate(ctx context.Context) error {
	if a == nil || a.tinybird {
		return nil
	}
	dir := filepath.Join(a.etcDir, "clickhouse")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read ClickHouse migrations: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return fmt.Errorf("read ClickHouse migration %s: %w", entry.Name(), err)
		}
		stream, err := a.stream(ctx, string(content))
		if err != nil {
			return fmt.Errorf("run ClickHouse migration %s: %w", entry.Name(), err)
		}
		_, err = io.Copy(io.Discard, stream)
		stream.Close()
		if err != nil {
			return fmt.Errorf("read ClickHouse migration %s result: %w", entry.Name(), err)
		}
	}
	return nil
}

func (a *Analytics) Write(ctx context.Context, resource string, rows []map[string]any) error {
	if resource != "events" && resource != "error_events" {
		return fmt.Errorf("unsupported analytics resource %q", resource)
	}
	if len(rows) == 0 {
		return nil
	}
	if a == nil {
		return errors.New("analytics is not configured")
	}
	var body bytes.Buffer
	path, contentType := "", "application/x-ndjson"
	params := make(url.Values)
	if a.tinybird {
		path = "/v0/events"
		contentType = "text/plain; charset=utf-8"
		params.Set("name", resource)
		params.Set("wait", "true")
	} else {
		params.Set("query", "INSERT INTO "+resource+" FORMAT JSONEachRow")
		params.Set("date_time_input_format", "best_effort")
		params.Set("wait_end_of_query", "1")
	}
	encoder := json.NewEncoder(&body)
	for _, row := range rows {
		fields := make(map[string]any, len(row))
		for key, value := range row {
			if a.tinybird {
				key = analyticsCamelField(key)
			} else if timestamp, ok := value.(time.Time); ok {
				value = timestamp.UTC().Format(analyticsDateTime)
			}
			fields[key] = value
		}
		if err := encoder.Encode(fields); err != nil {
			return fmt.Errorf("encode analytics row: %w", err)
		}
	}
	stream, err := a.request(ctx, http.MethodPost, path, params, &body, contentType)
	if err != nil {
		return fmt.Errorf("write %s: %w", resource, err)
	}
	defer stream.Close()
	if !a.tinybird {
		_, err := io.Copy(io.Discard, stream)
		return err
	}
	var result struct {
		Successful  int `json:"successful_rows"`
		Quarantined int `json:"quarantined_rows"`
	}
	if err := json.NewDecoder(stream).Decode(&result); err != nil {
		return fmt.Errorf("read Tinybird write result: %w", err)
	}
	if result.Successful != len(rows) || result.Quarantined != 0 {
		return analyticsPartialWrite{resource: resource, successful: result.Successful, total: len(rows), quarantined: result.Quarantined}
	}
	return nil
}

type analyticsPartialWrite struct {
	resource                       string
	successful, total, quarantined int
}

func (e analyticsPartialWrite) Error() string {
	return fmt.Sprintf("Tinybird wrote %d of %d %s rows (%d quarantined)", e.successful, e.total, e.resource, e.quarantined)
}

// Replaying an acknowledged batch can duplicate already committed events.
func (e analyticsPartialWrite) Permanent() bool { return true }

func analyticsCamelField(key string) string {
	parts := strings.Split(key, "_")
	for i := 1; i < len(parts); i++ {
		if part := parts[i]; part != "" {
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, "")
}

func (a *Analytics) stream(ctx context.Context, query string) (io.ReadCloser, error) {
	if a == nil {
		return nil, errors.New("analytics is not configured")
	}
	if a.tinybird {
		return a.request(ctx, http.MethodPost, "/v0/sql", nil, strings.NewReader(query), "text/plain; charset=utf-8")
	}
	return a.request(ctx, http.MethodPost, "", url.Values{"wait_end_of_query": {"1"}}, strings.NewReader(query), "text/plain; charset=utf-8")
}

func (a *Analytics) request(ctx context.Context, method, path string, args url.Values, body io.Reader, contentType string) (io.ReadCloser, error) {
	u := *a.endpoint
	if path != "" {
		u.Path = strings.TrimRight(u.Path, "/") + path
		u.RawPath = ""
	}
	query := u.Query()
	for key, values := range args {
		query[key] = values
	}
	u.RawQuery = query.Encode()
	credentials := u.User
	u.User = nil
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if a.tinybird {
		req.Header.Set("Authorization", "Bearer "+a.token)
	} else if credentials != nil {
		password, _ := credentials.Password()
		req.SetBasicAuth(credentials.Username(), password)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		// The request URL can contain filters; avoid including it in logs.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, err
	}
	if exception := resp.Header.Get("X-ClickHouse-Exception-Code"); resp.StatusCode < 200 || resp.StatusCode >= 300 || (exception != "" && exception != "0") {
		defer resp.Body.Close()
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("analytics service returned %s: %s", resp.Status, strings.TrimSpace(string(message)))
	}
	return resp.Body, nil
}
