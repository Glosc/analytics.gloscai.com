package main

import (
	"container/list"
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

type ingestCacheEntry[K comparable, V any] struct {
	key     K
	value   V
	expires time.Time
}

type ingestCache[K comparable, V any] struct {
	mu    sync.Mutex
	max   int
	items map[K]*list.Element
	order list.List
}

func newIngestCache[K comparable, V any](maxSize int) *ingestCache[K, V] {
	return &ingestCache[K, V]{max: maxSize, items: make(map[K]*list.Element)}
}

func (c *ingestCache[K, V]) remove(element *list.Element) {
	delete(c.items, element.Value.(ingestCacheEntry[K, V]).key)
	c.order.Remove(element)
}

func (c *ingestCache[K, V]) get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element := c.items[key]; element != nil {
		entry := element.Value.(ingestCacheEntry[K, V])
		if time.Now().Before(entry.expires) {
			c.order.MoveToFront(element)
			return entry.value, true
		}
		c.remove(element)
	}
	var zero V
	return zero, false
}

// remember preserves the first successful value if concurrent callers populated the same key.
func (c *ingestCache[K, V]) remember(key K, value V, expires time.Time) V {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element := c.items[key]; element != nil {
		entry := element.Value.(ingestCacheEntry[K, V])
		if time.Now().Before(entry.expires) {
			c.order.MoveToFront(element)
			return entry.value
		}
		c.remove(element)
	}
	if len(c.items) >= c.max {
		c.remove(c.order.Back())
	}
	c.items[key] = c.order.PushFront(ingestCacheEntry[K, V]{key, value, expires})
	return value
}

func (c *ingestCache[K, V]) prune(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for element := c.order.Back(); element != nil; {
		previous := element.Prev()
		if !now.Before(element.Value.(ingestCacheEntry[K, V]).expires) {
			c.remove(element)
		}
		element = previous
	}
}

type ingestApp struct {
	id     string
	locked bool
}

type ingestAppLookup struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	app     ingestApp
	err     error
}

func (i *Ingestion) lookupApp(ctx context.Context, key string) (ingestApp, error) {
	if ingestTextLen(key) > 20 {
		return ingestApp{}, nil
	}
	if app, ok := i.apps.get(key); ok {
		return app, nil
	}
	i.lookupMu.Lock()
	if app, ok := i.apps.get(key); ok {
		i.lookupMu.Unlock()
		return app, nil
	}
	lookup := i.lookups[key]
	if lookup == nil {
		if len(i.lookups) >= 512 {
			i.lookupMu.Unlock()
			return ingestApp{}, errors.New("ingestion app lookup capacity reached")
		}
		loadContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		lookup = &ingestAppLookup{done: make(chan struct{}), cancel: cancel}
		i.lookups[key] = lookup
		go i.loadApp(loadContext, key, lookup)
	}
	lookup.waiters++
	i.lookupMu.Unlock()

	select {
	case <-lookup.done:
		return lookup.app, lookup.err
	case <-ctx.Done():
		i.lookupMu.Lock()
		lookup.waiters--
		if lookup.waiters == 0 && i.lookups[key] == lookup {
			delete(i.lookups, key)
			lookup.cancel()
		}
		i.lookupMu.Unlock()
		return ingestApp{}, ctx.Err()
	}
}

func (i *Ingestion) loadApp(ctx context.Context, key string, lookup *ingestAppLookup) {
	defer lookup.cancel()
	select {
	case i.lookupGate <- struct{}{}:
		lookup.app, lookup.err = i.queryApp(ctx, key)
		<-i.lookupGate
	case <-ctx.Done():
		lookup.err = ctx.Err()
	}

	i.lookupMu.Lock()
	if i.lookups[key] == lookup {
		if lookup.err == nil {
			ttl := 30 * time.Minute
			if lookup.app.id == "" {
				ttl = 5 * time.Minute
			}
			i.apps.remember(key, lookup.app, time.Now().Add(ttl))
		}
		delete(i.lookups, key)
	}
	close(lookup.done)
	i.lookupMu.Unlock()
}

func (i *Ingestion) queryApp(ctx context.Context, key string) (ingestApp, error) {
	var app ingestApp
	var hasEvents bool
	err := i.db.QueryRowContext(ctx, `
		SELECT a.id, u.lock_reason IS NOT NULL, a.has_events
		FROM apps a JOIN users u ON u.id = a.owner_id
		WHERE a.app_key = $1 AND a.deleted_at IS NULL`, key).Scan(&app.id, &app.locked, &hasEvents)
	if errors.Is(err, sql.ErrNoRows) {
		return ingestApp{}, nil
	}
	if err != nil {
		return ingestApp{}, err
	}
	if !hasEvents {
		_, err = i.db.ExecContext(ctx, `UPDATE apps SET has_events = true WHERE id = $1 AND NOT has_events`, app.id)
	}
	return app, err
}
