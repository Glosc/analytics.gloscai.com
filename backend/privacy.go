package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math/bits"
	"strings"
	"time"
)

type ingestSessionKey struct{ appID, sessionID string }
type ingestSaltKey struct{ appID, date string }

type ingestPrivacy struct {
	db          *sql.DB
	sessions    *ingestCache[ingestSessionKey, string]
	salts       *ingestCache[ingestSaltKey, [16]byte]
	saltGate    chan struct{}
	retrySaltAt time.Time // protected by saltGate
}

func newIngestPrivacy(db *sql.DB) *ingestPrivacy {
	return &ingestPrivacy{
		db: db, sessions: newIngestCache[ingestSessionKey, string](131072),
		salts: newIngestCache[ingestSaltKey, [16]byte](16384), saltGate: make(chan struct{}, 1),
	}
}

func (p *ingestPrivacy) userID(ctx context.Context, timestamp time.Time, appID, sessionID, ip, agent string) (string, error) {
	key := ingestSessionKey{appID, sessionID}
	if id, ok := p.sessions.get(key); ok {
		return id, nil
	}
	day := timestamp.UTC().Truncate(24 * time.Hour)
	salt, err := p.dailySalt(ctx, appID, day)
	if err != nil {
		return "", err
	}
	return p.sessions.remember(key, ingestUserHash(salt, ip, agent), time.Now().Add(48*time.Hour)), nil
}

func (p *ingestPrivacy) dailySalt(ctx context.Context, appID string, day time.Time) ([16]byte, error) {
	key := ingestSaltKey{appID, day.Format(time.DateOnly)}
	if salt, ok := p.salts.get(key); ok {
		return salt, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case p.saltGate <- struct{}{}:
		defer func() { <-p.saltGate }()
	case <-ctx.Done():
		return [16]byte{}, ctx.Err()
	}
	if salt, ok := p.salts.get(key); ok {
		return salt, nil
	}
	if time.Now().Before(p.retrySaltAt) {
		return [16]byte{}, errors.New("daily salt temporarily unavailable")
	}
	var candidate, salt [16]byte
	if _, err := rand.Read(candidate[:]); err != nil {
		return salt, err
	}
	var persisted []byte
	err := p.db.QueryRowContext(ctx, `
		INSERT INTO app_salts (app_id, date, salt) VALUES ($1, $2, $3)
		ON CONFLICT (app_id, date) DO UPDATE SET salt = app_salts.salt
		RETURNING salt`, appID, key.date, candidate[:]).Scan(&persisted)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			p.retrySaltAt = time.Now().Add(5 * time.Second)
		}
		return salt, err
	}
	if len(persisted) != len(salt) {
		return salt, errors.New("invalid stored daily salt")
	}
	copy(salt[:], persisted)
	return p.salts.remember(key, salt, day.Add(48*time.Hour)), nil
}

func (p *ingestPrivacy) purge(ctx context.Context, now time.Time) error {
	cutoff := now.UTC().AddDate(0, 0, -2).Format(time.DateOnly)
	_, err := p.db.ExecContext(ctx, `DELETE FROM app_salts WHERE date <= $1`, cutoff)
	return err
}

func ingestUserHash(salt [16]byte, ip, agent string) string {
	// The legacy FastHashes caller used the first eight salt bytes for both SipHash keys.
	// Preserve that derivation and the exact input and output encoding across deployment.
	key := binary.LittleEndian.Uint64(salt[:8])
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], ingestSipHash(key, key, ip+"|$"+agent))
	return strings.ToUpper(hex.EncodeToString(encoded[:]))
}

func ingestSipHash(k0, k1 uint64, text string) uint64 {
	v0, v1 := k0^0x736f6d6570736575, k1^0x646f72616e646f6d
	v2, v3 := k0^0x6c7967656e657261, k1^0x7465646279746573
	round := func() {
		v0 += v1
		v2 += v3
		v1 = bits.RotateLeft64(v1, 13) ^ v0
		v3 = bits.RotateLeft64(v3, 16) ^ v2
		v0 = bits.RotateLeft64(v0, 32)
		v2 += v1
		v0 += v3
		v1 = bits.RotateLeft64(v1, 17) ^ v2
		v3 = bits.RotateLeft64(v3, 21) ^ v0
		v2 = bits.RotateLeft64(v2, 32)
	}
	block := func(m uint64) {
		v3 ^= m
		round()
		round()
		v0 ^= m
	}
	tail := uint64(len(text)&255) << 56
	for len(text) >= 8 {
		block(binary.LittleEndian.Uint64([]byte(text[:8])))
		text = text[8:]
	}
	for i := range len(text) {
		tail |= uint64(text[i]) << (8 * i)
	}
	block(tail)
	v2 ^= 0xff
	for range 4 {
		round()
	}
	return v0 ^ v1 ^ v2 ^ v3
}
