package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
)

func (s *Server) registerExport(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/_export/usage", s.exportUsage)
	mux.HandleFunc("GET /api/_export/download", s.exportDownload)
}

type exportMonth struct {
	Year   analyticsCount `json:"year"`
	Month  analyticsCount `json:"month"`
	Events analyticsCount `json:"events"`
}

func (s *Server) exportUsage(w http.ResponseWriter, r *http.Request) {
	q := statsQueryValues(r, false)
	appID, buildMode := q.Get("appid"), q.Get("buildmode")
	if !s.analyticsCanRead(w, r, appID, false) {
		return
	}
	if strings.TrimSpace(buildMode) == "" {
		statsBadQuery(w, "Invalid build mode.")
		return
	}
	rows, err := analyticsRows[exportMonth](r.Context(), s.Analytics, "monthly_usage__v1", analyticsArgs{"app_id": analyticsBuildAppID(appID, buildMode)})
	if strings.EqualFold(buildMode, "debug") && len(rows) > 5 {
		rows = rows[:5]
	}
	statsRespond(w, r, rows, err)
}

var exportUnsafeName = regexp.MustCompile(`[^a-zA-Z0-9]`)
var exportUnsafeMode = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

const exportPageSize = 100_000

func (s *Server) exportDownload(w http.ResponseWriter, r *http.Request) {
	q := statsQueryValues(r, true)
	appID, buildMode := q.Get("appid"), q.Get("buildmode")
	if !s.analyticsCanRead(w, r, appID, false) {
		return
	}
	start, end, err := statsDateRange(q)
	if err != nil || start.IsZero() || end.IsZero() {
		statsBadQuery(w, "Invalid start or end date.")
		return
	}
	where := fmt.Sprintf("FROM events WHERE app_id = '%s' AND timestamp BETWEEN '%s' AND '%s'",
		analyticsSQLEscape(analyticsBuildAppID(appID, buildMode)), start.Format(analyticsDateTime), end.Format(analyticsDateTime))
	ctx := r.Context()
	count, err := s.Analytics.exportEventCount(ctx, where)
	if err != nil {
		statsRespond(w, r, nil, err)
		return
	}
	parquet := strings.EqualFold(q.Get("format"), "parquet")
	contentType, extension := "text/csv", "csv"
	if parquet {
		contentType, extension = "application/octet-stream", "parquet"
	}
	name := strings.ToLower(exportUnsafeName.ReplaceAllString(q.Get("appname"), ""))
	mode := strings.ToLower(exportUnsafeMode.ReplaceAllString(buildMode, ""))
	filename := fmt.Sprintf("%s-%s-%s.%s", name, mode, start.Format("2006-01-02"), extension)
	selectQuery := `SELECT timestamp, user_id, session_id, event_name,
		replace(replace(string_props, '\u0022', '\''), '\u0027', '\'') AS string_props,
		numeric_props, os_name, os_version, locale, app_version, app_build_number,
		engine_name, engine_version, country_code, ` + exportCountryName + `, region_name ` + where + ` ORDER BY timestamp DESC`
	if count == 0 && !parquet {
		exportSetHeaders(w, contentType, filename)
		w.WriteHeader(http.StatusOK)
		return
	}
	for offset := uint64(0); ; {
		query := selectQuery + " FORMAT Parquet"
		if !parquet {
			// Preserve the CSVWithNames output of each legacy page.
			query = fmt.Sprintf("%s LIMIT %d OFFSET %d FORMAT CSVWithNames", selectQuery, exportPageSize, offset)
		}
		stream, err := s.Analytics.stream(ctx, query)
		if err != nil {
			if offset == 0 {
				statsRespond(w, r, nil, err)
			} else {
				slog.ErrorContext(ctx, "open export page", "offset", offset, "error", err)
				panic(http.ErrAbortHandler)
			}
			return
		}
		if offset == 0 {
			exportSetHeaders(w, contentType, filename)
		}
		_, err = io.Copy(w, stream)
		stream.Close()
		if err != nil {
			slog.ErrorContext(ctx, "stream export page", "offset", offset, "error", err)
			panic(http.ErrAbortHandler)
		}
		if parquet || uint64(count)-offset <= exportPageSize {
			return
		}
		offset += exportPageSize
	}
}

func exportSetHeaders(w http.ResponseWriter, contentType, filename string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
}

func (a *Analytics) exportEventCount(ctx context.Context, where string) (analyticsCount, error) {
	stream, err := a.stream(ctx, "SELECT COUNT(*) AS event_count "+where+" FORMAT JSON")
	if err != nil {
		return 0, err
	}
	defer stream.Close()
	rows, err := analyticsDecodeRows[struct {
		EventCount analyticsCount `json:"event_count"`
	}](stream)
	if len(rows) == 0 {
		return 0, err
	}
	return rows[0].EventCount, err
}
