package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Region, BaseURL, AuthSecret, EtcDir, WebDir            string
	DatabaseURL, ClickHouseURL, TinybirdURL, TinybirdToken string
	Listen                                                 string
	Development                                            bool
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
func defaultDir(candidates ...string) string {
	for _, v := range candidates {
		if st, e := os.Stat(v); e == nil && st.IsDir() {
			return v
		}
	}
	return candidates[0]
}
func loadConfig(dev bool) (Config, error) {
	c := Config{
		Development:   dev || strings.EqualFold(envOr("APP_ENV", os.Getenv("ASPNETCORE_ENVIRONMENT")), "development"),
		Region:        strings.ToUpper(envOr("REGION", "SH")),
		BaseURL:       strings.TrimRight(strings.TrimSpace(os.Getenv("BASE_URL")), "/"),
		AuthSecret:    strings.TrimSpace(os.Getenv("AUTH_SECRET")),
		DatabaseURL:   envOr("ConnectionStrings__postgresdb", os.Getenv("DATABASE_URL")),
		ClickHouseURL: envOr("ConnectionStrings__clickhousedb", os.Getenv("CLICKHOUSE_URL")),
		TinybirdURL:   os.Getenv("TINYBIRD_BASE_URL"),
		TinybirdToken: os.Getenv("TINYBIRD_TOKEN"),
		EtcDir:        envOr("ETC_DIR", defaultDir("etc", "../etc")),
		WebDir:        envOr("WEB_DIR", defaultDir("wwwroot", "../src/wwwroot", "src/wwwroot")),
		Listen:        envOr("LISTEN_ADDR", ":"+envOr("PORT", envOr("ASPNETCORE_HTTP_PORTS", "8080"))),
	}
	if dev {
		c.Listen = envOr("LISTEN_ADDR", "127.0.0.1:5251")
		if c.BaseURL == "" {
			c.BaseURL = "http://localhost:3000"
		}
		if c.DatabaseURL == "" {
			c.DatabaseURL = "Host=localhost;Port=" + envOr("DEV_POSTGRES_PORT", "5433") + ";Username=aptabase;Password=aptabase_pw;Database=aptabase"
		}
		if c.ClickHouseURL == "" && c.TinybirdURL == "" {
			c.ClickHouseURL = "Host=localhost;Port=8123;Username=aptabase;Password=aptabase_pw"
		}
		if c.AuthSecret == "" {
			c.AuthSecret = "E9F5C811CD74B9A3921A5DB502C0EDBE64B746000663AF04D91C249687FA969B"
		}
	}
	if c.Development {
		c.Region = "DEV"
	}
	if c.BaseURL == "" || c.DatabaseURL == "" {
		return c, errors.New("BASE_URL and DATABASE_URL are required")
	}
	if !strings.Contains(c.BaseURL, "://") {
		c.BaseURL = "https://" + c.BaseURL
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return c, errors.New("invalid BASE_URL")
	}
	if len(c.AuthSecret) < 32 || (!c.Development && (c.AuthSecret == "c4rI4x8kz5DgKJ1is5Eiu9bNncSQ6ROD" || c.AuthSecret == "E9F5C811CD74B9A3921A5DB502C0EDBE64B746000663AF04D91C249687FA969B")) {
		return c, errors.New("set AUTH_SECRET to a private secret of at least 32 characters")
	}
	c.EtcDir, err = filepath.Abs(c.EtcDir)
	if err != nil {
		return c, err
	}
	c.DatabaseURL, err = postgresDSN(c.DatabaseURL)
	return c, err
}

// Decode the Npgsql connection strings used by existing Compose deployments, including quoted values.
func connectionFields(s string) (map[string]string, error) {
	fields := map[string]string{}
	s = strings.TrimSpace(s)
	for s != "" {
		if s[0] == ';' {
			s = strings.TrimSpace(s[1:])
			continue
		}
		k, tail, ok := strings.Cut(s, "=")
		if !ok || strings.Contains(k, ";") {
			return nil, errors.New("invalid connection string field")
		}
		tail = strings.TrimSpace(tail)
		var value string
		if len(tail) > 0 && (tail[0] == '\'' || tail[0] == '"') {
			quote := tail[0]
			tail = tail[1:]
			var b strings.Builder
			for {
				i := strings.IndexByte(tail, quote)
				if i < 0 {
					return nil, errors.New("unterminated connection string quote")
				}
				b.WriteString(tail[:i])
				tail = tail[i+1:]
				if len(tail) == 0 || tail[0] != quote {
					break
				}
				b.WriteByte(quote)
				tail = tail[1:]
			}
			value = b.String()
			tail = strings.TrimSpace(tail)
			if len(tail) > 0 && tail[0] != ';' {
				return nil, errors.New("invalid connection string separator")
			}
			s = tail
		} else {
			value, s, _ = strings.Cut(tail, ";")
			value = strings.TrimSpace(value)
		}
		fields[strings.ToLower(strings.TrimSpace(k))] = value
		s = strings.TrimSpace(s)
	}
	return fields, nil
}
func postgresDSN(raw string) (string, error) {
	if strings.HasPrefix(raw, "postgres://") || strings.HasPrefix(raw, "postgresql://") {
		return raw, nil
	}
	if !strings.Contains(raw, ";") && (strings.HasPrefix(raw, "host=") || strings.Contains(raw, " host=")) {
		return raw, nil
	}
	fields, err := connectionFields(raw)
	if err != nil {
		return "", err
	}
	host := fields["host"]
	if host == "" {
		host = fields["server"]
	}
	if host == "" {
		return "", fmt.Errorf("DATABASE_URL has no host")
	}
	if p := fields["port"]; p != "" {
		host = net.JoinHostPort(strings.Trim(host, "[]"), p)
	}
	u := url.URL{Scheme: "postgres", Host: host, Path: "/" + fields["database"]}
	for _, k := range []string{"username", "user id", "userid", "user"} {
		if v := fields[k]; v != "" {
			u.User = url.UserPassword(v, fields["password"])
			break
		}
	}
	q := u.Query()
	for _, k := range []string{"ssl mode", "sslmode"} {
		if mode := fields[k]; mode != "" {
			mode = strings.ToLower(mode)
			mode = strings.ReplaceAll(mode, "verifyfull", "verify-full")
			mode = strings.ReplaceAll(mode, "verifyca", "verify-ca")
			q.Set("sslmode", mode)
		}
	}
	for k, param := range map[string]string{
		"root certificate": "sslrootcert", "ssl certificate": "sslcert", "ssl key": "sslkey",
		"search path": "search_path", "application name": "application_name", "timeout": "connect_timeout",
	} {
		if v := fields[k]; v != "" {
			q.Set(param, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
