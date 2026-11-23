package main

import (
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
)

type gzipResponse struct {
	http.ResponseWriter
	enc    *gzip.Writer
	wrote  bool
	status int
}

func (w *gzipResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *gzipResponse) WriteHeader(n int) {
	if !w.wrote {
		w.status = n
		w.wrote = true
	}
}
func (w *gzipResponse) start(sample []byte) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	h := w.Header()
	ct := h.Get("Content-Type")
	if ct == "" && len(sample) > 0 {
		ct = http.DetectContentType(sample)
		h.Set("Content-Type", ct)
	}
	if len(sample) > 0 && h.Get("Content-Encoding") == "" && (strings.HasPrefix(ct, "text/") || strings.Contains(ct, "json") || strings.Contains(ct, "javascript") || strings.HasPrefix(ct, "image/svg")) {
		h.Del("Content-Length")
		h.Set("Content-Encoding", "gzip")
		w.enc, _ = gzip.NewWriterLevel(w.ResponseWriter, gzip.BestSpeed)
	}
	w.ResponseWriter.WriteHeader(w.status)
	w.wrote = true
	w.status = -1
}
func (w *gzipResponse) Write(p []byte) (int, error) {
	if w.status != -1 {
		w.start(p)
	}
	if w.enc != nil {
		return w.enc.Write(p)
	}
	return w.ResponseWriter.Write(p)
}
func (w *gzipResponse) Flush() {
	if w.status != -1 {
		w.start(nil)
	}
	if w.enc != nil {
		w.enc.Flush()
	}
	http.NewResponseController(w.ResponseWriter).Flush()
}
func compressResponse(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		if !acceptsGzip(r.Header.Get("Accept-Encoding")) || r.Header.Get("Range") != "" || r.Method == "HEAD" {
			next.ServeHTTP(w, r)
			return
		}
		sw := &gzipResponse{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status != -1 {
			sw.start(nil)
		}
		if sw.enc != nil {
			sw.enc.Close()
		}
	})
}

func acceptsGzip(header string) bool {
	wildcard := false
	for entry := range strings.SplitSeq(header, ",") {
		name, params, _ := strings.Cut(entry, ";")
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "gzip" && name != "*" {
			continue
		}
		allowed := true
		for parameter := range strings.SplitSeq(params, ";") {
			key, value, ok := strings.Cut(parameter, "=")
			if ok && strings.EqualFold(strings.TrimSpace(key), "q") {
				q, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
				allowed = err == nil && q > 0 && q <= 1
				break
			}
		}
		if name == "gzip" {
			return allowed
		}
		wildcard = allowed
	}
	return wildcard
}
