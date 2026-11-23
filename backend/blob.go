package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"image/png"
	"net/http"
	"strconv"
)

const maxAppIconSize = 50 << 10

func storeAppIcon(ctx context.Context, tx *sql.Tx, icon string) (string, error) {
	content, err := base64.StdEncoding.DecodeString(icon)
	if err != nil || len(content) > maxAppIconSize {
		return "", appValidationError("icon", "Icon must be a base64-encoded PNG of at most 50 KB.")
	}
	if _, err := png.DecodeConfig(bytes.NewReader(content)); err != nil {
		return "", appValidationError("icon", "Icon must be a PNG file.")
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	path := fmt.Sprintf("icons/%x-%x-%x-%x-%x.png", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
	_, err = tx.ExecContext(ctx, `INSERT INTO blobs (path, content, content_type) VALUES ($1, $2, 'image/png')`, path, content)
	return path, err
}

func (s *Server) readAppBlob(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	if path == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var content []byte
	var contentType string
	if err := s.DB.QueryRowContext(r.Context(), `SELECT content, content_type FROM blobs WHERE path = $1`, path).
		Scan(&content, &contentType); err != nil {
		writeAppError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000")
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(content)
	}
}
