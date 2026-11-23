package main

import (
	"context"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

type rejectedSource struct{ reason, key, ip, agent string }
type rejectionStats struct {
	mu              sync.Mutex
	counts          map[rejectedSource]int
	total, overflow int
}

func (r *rejectionStats) record(reason string, req *http.Request) {
	cut := func(v string, n int) string {
		v = strings.TrimSpace(v)
		if len(v) > n {
			return v[:n]
		}
		return v
	}
	k := rejectedSource{reason, strings.ToUpper(cut(req.Header.Get("App-Key"), 20)), cut(clientIP(req), 100), cut(req.UserAgent(), 80)}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.total++
	if r.counts == nil {
		r.counts = make(map[rejectedSource]int)
	}
	if _, ok := r.counts[k]; !ok && len(r.counts) >= 500 {
		r.overflow++
		return
	}
	r.counts[k]++
}
func (r *rejectionStats) flush() {
	r.mu.Lock()
	counts, total, overflow := r.counts, r.total, r.overflow
	r.counts = nil
	r.total = 0
	r.overflow = 0
	r.mu.Unlock()
	if total == 0 {
		return
	}
	slog.Warn("rejected requests", "total", total, "sources", len(counts), "overflow", overflow)
	keys := slices.Collect(maps.Keys(counts))
	slices.SortFunc(keys, func(a, b rejectedSource) int { return counts[b] - counts[a] })
	for _, k := range keys[:min(10, len(keys))] {
		slog.Warn("rejected source", "count", counts[k], "reason", k.reason, "appKey", k.key, "ip", k.ip, "ua", k.agent)
	}
}
func (r *rejectionStats) run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	defer r.flush()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.flush()
		}
	}
}
