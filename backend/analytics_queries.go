package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"
)

type analyticsArgs map[string]string

type analyticsQueries struct {
	mu     sync.Mutex
	byName map[string]*template.Template
}

var analyticsQueryName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func (a *Analytics) namedQuery(ctx context.Context, name string, args analyticsArgs) (io.ReadCloser, error) {
	if a == nil {
		return nil, fmt.Errorf("analytics is not configured")
	}
	if !analyticsQueryName.MatchString(name) {
		return nil, fmt.Errorf("invalid analytics query name %q", name)
	}
	if a.tinybird {
		params := make(url.Values, len(args))
		for key, value := range args {
			params.Set(key, value)
		}
		return a.request(ctx, http.MethodGet, "/v0/pipes/"+name+".json", params, nil, "")
	}
	tmpl, err := a.queries.load(a.etcDir, name)
	if err != nil {
		return nil, err
	}
	query, err := analyticsRenderQuery(tmpl, args)
	if err != nil {
		return nil, fmt.Errorf("render query %s: %w", name, err)
	}
	return a.stream(ctx, strings.TrimSuffix(strings.TrimSpace(query), ";")+"\nFORMAT JSON")
}

func analyticsRows[T any](ctx context.Context, a *Analytics, name string, args analyticsArgs) ([]T, error) {
	stream, err := a.namedQuery(ctx, name, args)
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", name, err)
	}
	defer stream.Close()
	return analyticsDecodeRows[T](stream)
}

func analyticsDecodeRows[T any](stream io.Reader) ([]T, error) {
	var result struct {
		Data      []T    `json:"data"`
		Error     string `json:"error"`
		Exception string `json:"exception"`
	}
	decoder := json.NewDecoder(stream)
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	if result.Error != "" || result.Exception != "" {
		return nil, fmt.Errorf("analytics query failed: %s%s", result.Error, result.Exception)
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing analytics response data")
	}
	if result.Data == nil {
		return []T{}, nil
	}
	return result.Data, nil
}

func analyticsFirst[T any](ctx context.Context, a *Analytics, name string, args analyticsArgs) (T, error) {
	rows, err := analyticsRows[T](ctx, a, name, args)
	if len(rows) > 0 {
		return rows[0], err
	}
	var zero T
	return zero, err
}

func (q *analyticsQueries) load(etcDir, name string) (*template.Template, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if tmpl := q.byName[name]; tmpl != nil {
		return tmpl, nil
	}
	data, err := os.ReadFile(filepath.Join(etcDir, "clickhouse", "queries", name+".liquid"))
	if err != nil {
		return nil, err
	}
	tmpl, err := analyticsCompileLiquid(name, string(data))
	if err != nil {
		return nil, fmt.Errorf("parse query %s: %w", name, err)
	}
	if q.byName == nil {
		q.byName = make(map[string]*template.Template)
	}
	q.byName[name] = tmpl
	return tmpl, nil
}

var (
	analyticsLiquidTag   = regexp.MustCompile(`(?s)\{\{.*?\}\}|\{%.*?%\}`)
	analyticsLiquidEqual = regexp.MustCompile(`^([a-z][a-z0-9_]*)\s*==\s*'([A-Za-z]+)'$`)
	analyticsDigits      = regexp.MustCompile(`^[0-9]+$`)
)

// Translate only the Liquid constructs used by etc/clickhouse. The standard
// template engine validates the control flow and executes the cached result.
func analyticsCompileLiquid(name, source string) (*template.Template, error) {
	var out strings.Builder
	pos := 0
	for _, bounds := range analyticsLiquidTag.FindAllStringIndex(source, -1) {
		literal := source[pos:bounds[0]]
		if strings.Contains(literal, "{{") || strings.Contains(literal, "{%") {
			return nil, fmt.Errorf("unterminated Liquid tag")
		}
		out.WriteString(literal)
		tag := source[bounds[0]:bounds[1]]
		pos = bounds[1]
		text := strings.TrimSpace(tag[2 : len(tag)-2])
		if strings.HasPrefix(tag, "{{") && analyticsQueryName.MatchString(text) {
			fmt.Fprintf(&out, `{{index . %q}}`, text)
			continue
		}
		if strings.HasPrefix(tag, "{%") {
			switch text {
			case "else":
				out.WriteString("{{else}}")
				continue
			case "endif":
				out.WriteString("{{end}}")
				continue
			}
			kind, expr, found := strings.Cut(text, " ")
			if found && (kind == "if" || kind == "elsif") {
				condition, err := analyticsLiquidCondition(strings.TrimSpace(expr))
				if err != nil {
					return nil, err
				}
				if kind == "elsif" {
					kind = "else if"
				}
				fmt.Fprintf(&out, "{{%s %s}}", kind, condition)
				continue
			}
		}
		return nil, fmt.Errorf("unsupported Liquid tag %q", tag)
	}
	literal := source[pos:]
	if strings.Contains(literal, "{{") || strings.Contains(literal, "{%") {
		return nil, fmt.Errorf("unterminated Liquid tag")
	}
	out.WriteString(literal)
	return template.New(name).Parse(out.String())
}

func analyticsLiquidCondition(expr string) (string, error) {
	if match := analyticsLiquidEqual.FindStringSubmatch(expr); len(match) != 0 {
		return fmt.Sprintf(`eq (index . %q) %q`, match[1], match[2]), nil
	}
	if analyticsQueryName.MatchString(expr) {
		return fmt.Sprintf(`index . %q`, expr), nil
	}
	if left, right, ok := strings.Cut(expr, " and "); ok && analyticsQueryName.MatchString(left) && analyticsQueryName.MatchString(right) {
		return fmt.Sprintf(`and (index . %q) (index . %q)`, left, right), nil
	}
	return "", fmt.Errorf("unsupported Liquid condition %q", expr)
}

func analyticsRenderQuery(tmpl *template.Template, args analyticsArgs) (string, error) {
	safe := make(analyticsArgs, len(args))
	for key, value := range args {
		switch key {
		case "app_ids":
			parts := strings.Split(value, ",")
			for i := range parts {
				parts[i] = analyticsSQLEscape(parts[i])
			}
			safe[key] = strings.Join(parts, "','")
		case "name_column":
			if !analyticsQueryName.MatchString(value) {
				return "", fmt.Errorf("invalid query column")
			}
			safe[key] = value
		case "limit", "offset":
			if !analyticsDigits.MatchString(value) {
				return "", fmt.Errorf("invalid query %s", key)
			}
			safe[key] = value
		default:
			safe[key] = analyticsSQLEscape(value)
		}
	}
	var query strings.Builder
	if err := tmpl.Execute(&query, safe); err != nil {
		return "", err
	}
	return query.String(), nil
}

var analyticsSQLReplacer = strings.NewReplacer(`\`, `\\`, `'`, `\'`)

func analyticsSQLEscape(value string) string {
	return analyticsSQLReplacer.Replace(value)
}

type analyticsCount uint64

func (n *analyticsCount) UnmarshalJSON(data []byte) error {
	var value json.Number
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseUint(string(value), 10, 64)
	if err != nil {
		return err
	}
	*n = analyticsCount(parsed)
	return nil
}

type analyticsFloat float64

func (n *analyticsFloat) UnmarshalJSON(data []byte) error {
	var value json.Number
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value == "" {
		return nil
	}
	parsed, err := value.Float64()
	if err != nil {
		return err
	}
	*n = analyticsFloat(parsed)
	return nil
}

func analyticsParseDate(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, analyticsDateTime, "2006-01-02T15:04:05", "2006-01-02"} {
		if date, err := time.Parse(layout, value); err == nil {
			return date.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date")
}

type analyticsTime struct{ time.Time }

func (t *analyticsTime) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		return nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	date, err := analyticsParseDate(value)
	if err != nil {
		return err
	}
	t.Time = date
	return nil
}
