package main

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

const (
	authLegacySSMPath    = "/aptabase/production/aspnet-dataprotection/"
	authLegacySSMTimeout = 5 * time.Second
	authLegacySSMTTL     = time.Minute
	authLegacySSMBackoff = 15 * time.Second
)

type authLegacyKeyRing struct {
	keys        map[string]*authLegacyKey
	revocations []authLegacyRevocation
}

func (r *authLegacyKeyRing) revoked(key authLegacyKey) bool {
	for _, revoke := range r.revocations {
		if revoke.matches(key) {
			return true
		}
	}
	return false
}

type authLegacySSMCache struct {
	load      func(context.Context) (*authLegacyKeyRing, error)
	mu        sync.Mutex
	ring      *authLegacyKeyRing
	err       error
	expiresAt time.Time
	pending   chan struct{}
}

var authManagedLegacyKeys = authLegacySSMCache{load: newAuthLegacySSMLoader()}

func (c *authLegacySSMCache) get(ctx context.Context) (*authLegacyKeyRing, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c.mu.Lock()
		if time.Now().Before(c.expiresAt) {
			ring, err := c.ring, c.err
			c.mu.Unlock()
			return ring, err
		}
		if c.pending == nil {
			c.pending = make(chan struct{})
			go c.refresh()
		}
		pending := c.pending
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-pending:
		}
	}
}

func (c *authLegacySSMCache) refresh() {
	// One bounded fetch serves all requests, regardless of which caller disconnects.
	ctx, cancel := context.WithTimeout(context.Background(), authLegacySSMTimeout)
	defer cancel()
	ring, err := c.load(ctx)
	ttl := authLegacySSMTTL
	if err != nil {
		// Do not trust stale keys after losing the ability to check revocations.
		ring, ttl = nil, authLegacySSMBackoff
	}
	c.mu.Lock()
	c.ring, c.err, c.expiresAt = ring, err, time.Now().Add(ttl)
	close(c.pending)
	c.pending = nil
	c.mu.Unlock()
}

func newAuthLegacySSMLoader() func(context.Context) (*authLegacyKeyRing, error) {
	// The cache serializes refreshes, so the SDK client and its credential cache can be reused.
	var client *ssm.Client
	return func(ctx context.Context) (*authLegacyKeyRing, error) {
		if client == nil {
			cfg, err := config.LoadDefaultConfig(ctx, config.WithRetryMaxAttempts(2))
			if err != nil {
				return nil, fmt.Errorf("configure AWS for legacy Data Protection keys: %w", err)
			}
			if cfg.Region == "" {
				return nil, errors.New("AWS region is required to read the legacy Data Protection key ring")
			}
			client = ssm.NewFromConfig(cfg)
		}
		return authLegacySSMList(ctx, client)
	}
}

type authLegacySSMClient interface {
	GetParametersByPath(context.Context, *ssm.GetParametersByPathInput, ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error)
}

func authLegacySSMList(ctx context.Context, client authLegacySSMClient) (*authLegacyKeyRing, error) {
	input := &ssm.GetParametersByPathInput{Path: aws.String(authLegacySSMPath), WithDecryption: aws.Bool(true)}
	ring := &authLegacyKeyRing{keys: make(map[string]*authLegacyKey)}
	seen := make(map[string]struct{})
	for {
		page, err := client.GetParametersByPath(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("read legacy Data Protection key ring from SSM: %w", err)
		}
		for _, parameter := range page.Parameters {
			if err := ring.addXML(aws.ToString(parameter.Name), aws.ToString(parameter.Value)); err != nil {
				return nil, err
			}
		}
		next := aws.ToString(page.NextToken)
		if next == "" {
			return ring, nil
		}
		if _, duplicate := seen[next]; duplicate {
			return nil, errors.New("SSM repeated a legacy Data Protection pagination token")
		}
		seen[next] = struct{}{}
		input.NextToken = page.NextToken
	}
}

func (r *authLegacyKeyRing) addXML(name, value string) error {
	var element struct{ XMLName xml.Name }
	data := []byte(value)
	err := xml.Unmarshal(data, &element)
	if strings.HasPrefix(path.Base(name), "revocation-") || element.XMLName.Local == "revocation" {
		// A broken revocation record must not turn into a usable session.
		var revoke authLegacyRevocation
		if err != nil || xml.Unmarshal(data, &revoke) != nil || !revoke.valid() {
			return errors.New("invalid legacy Data Protection revocation in SSM")
		}
		r.revocations = append(r.revocations, revoke)
	} else if err == nil && element.XMLName.Local == "key" {
		var key authLegacyKey
		if err := xml.Unmarshal(data, &key); err == nil && key.ID != "" {
			id := strings.ToLower(key.ID)
			if _, duplicate := r.keys[id]; duplicate {
				return errors.New("duplicate legacy Data Protection key in SSM")
			}
			r.keys[id] = &key
		}
	}
	return nil
}
