package main

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"
)

const (
	ingestFlushRows  = 1000
	ingestQueueBytes = 32 << 20
)

type ingestRecord struct {
	data map[string]any
	size int
}

func newIngestRecord(data map[string]any) ingestRecord {
	size := 1024 // Approximate map allocation and non-string fields.
	for _, value := range data {
		if s, ok := value.(string); ok {
			size += len(s)
		}
	}
	return ingestRecord{data, size}
}

type ingestQueue struct {
	resource string
	capacity int
	ready    chan struct{}
	mu       sync.Mutex
	closed   bool
	inUse    int
	bytes    int
	pending  []ingestRecord
}

func newIngestQueue(resource string, capacity int) *ingestQueue {
	return &ingestQueue{resource: resource, capacity: capacity, ready: make(chan struct{}, 1)}
}

func ingestRecordsSize(rows []ingestRecord) int {
	size := 0
	for _, row := range rows {
		size += row.size
	}
	return size
}

// Reservations count queued rows, in-flight writes, and admissions awaiting Postgres.
func (q *ingestQueue) reserve(rows []ingestRecord) bool {
	size := ingestRecordsSize(rows)
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || q.inUse+len(rows) > q.capacity || q.bytes+size > ingestQueueBytes {
		return false
	}
	q.inUse += len(rows)
	q.bytes += size
	return true
}

func (q *ingestQueue) publish(rows []ingestRecord) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending = append(q.pending, rows...)
	if q.closed || len(q.pending) >= ingestFlushRows {
		q.wake()
	}
}

func (q *ingestQueue) release(rows []ingestRecord) {
	size := ingestRecordsSize(rows)
	q.mu.Lock()
	defer q.mu.Unlock()
	q.inUse -= len(rows)
	q.bytes -= size
	if q.closed {
		q.wake()
	}
}

func (q *ingestQueue) take() ([]ingestRecord, int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := min(len(q.pending), ingestFlushRows)
	rows := slices.Clone(q.pending[:n])
	clear(q.pending[:n])
	q.pending = q.pending[n:]
	if len(q.pending) == 0 {
		q.pending = nil
	}
	return rows, q.inUse
}

func (q *ingestQueue) wake() {
	select {
	case q.ready <- struct{}{}:
	default:
	}
}

func (i *Ingestion) Run(ctx context.Context) {
	defer i.geo.close()
	var workers sync.WaitGroup
	workers.Go(func() { i.runQueue(ctx, i.events, 10*time.Second) })
	workers.Go(func() { i.runQueue(ctx, i.errors, 5*time.Second) })
	i.maintain(ctx)
	workers.Wait()
}

func (i *Ingestion) maintain(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	var lastDay, lastSweep string
	for ctx.Err() == nil {
		now := time.Now().UTC()
		if hour := now.Format("2006-01-02T15"); hour != lastSweep {
			i.apps.prune(now)
			i.privacy.sessions.prune(now)
			if i.quota != nil {
				i.quota.exhausted.prune(now)
			}
			lastSweep = hour
		}
		if day := now.Format(time.DateOnly); day != lastDay {
			i.privacy.salts.prune(now)
			purgeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := i.privacy.purge(purgeCtx, now)
			cancel()
			if err == nil {
				lastDay = day
			} else if ctx.Err() == nil {
				slog.Warn("could not purge old ingestion salts")
			}
		}
		if i.quota != nil {
			resetCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := i.quota.ensurePeriod(resetCtx, ingestQuotaPeriod(now))
			cancel()
			if err != nil && ctx.Err() == nil {
				slog.Warn("could not initialize or reset error quota period")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (i *Ingestion) runQueue(ctx context.Context, q *ingestQueue, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var batch []ingestRecord
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case <-ticker.C:
		case <-q.ready:
		}
		for ctx.Err() == nil {
			batch, _ = q.take()
			if len(batch) == 0 || !i.writeIngestBatch(ctx, q.resource, batch) {
				break
			}
			q.release(batch)
			batch = nil
		}
	}

	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	drain, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for drain.Err() == nil {
		if len(batch) != 0 {
			if !i.writeIngestBatch(drain, q.resource, batch) {
				break
			}
			q.release(batch)
		}
		var inUse int
		batch, inUse = q.take()
		if len(batch) != 0 {
			continue
		}
		if inUse == 0 {
			return
		}
		select {
		case <-drain.Done():
		case <-q.ready:
		}
	}
	q.mu.Lock()
	unflushed := q.inUse
	q.pending = nil
	q.mu.Unlock()
	if unflushed != 0 {
		slog.Warn("ingestion shutdown left rows unwritten", "resource", q.resource, "rows", unflushed)
	}
}

func (i *Ingestion) writeIngestBatch(ctx context.Context, resource string, batch []ingestRecord) bool {
	rows := make([]map[string]any, len(batch))
	for n, record := range batch {
		rows[n] = record.data
	}
	for _, delay := range [...]time.Duration{0, time.Second, 3 * time.Second, 5 * time.Second} {
		if ctx.Err() != nil {
			return false
		}
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return false
			}
		}
		attempt, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := i.write(attempt, resource, rows)
		cancel()
		if err == nil {
			return true
		}
		// A confirmed partial write must not be retried: it would duplicate committed rows.
		var permanent interface{ Permanent() bool }
		if errors.As(err, &permanent) && permanent.Permanent() {
			break
		}
		if ctx.Err() != nil {
			return false
		}
	}
	slog.Warn("ingestion could not write all rows in a batch", "resource", resource, "rows", len(rows))
	return true
}
