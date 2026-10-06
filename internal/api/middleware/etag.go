package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

type etagResponseWriter struct {
	http.ResponseWriter
	buf        *bytes.Buffer
	statusCode int
	wroteHeader bool
}

func (w *etagResponseWriter) Write(b []byte) (int, error) {
	return w.buf.Write(b)
}

func (w *etagResponseWriter) WriteHeader(statusCode int) {
	if !w.wroteHeader {
		w.statusCode = statusCode
		w.wroteHeader = true
	}
}

// ETagMiddleware computes an SHA256 ETag on 200 OK GET responses.
// If the client supplies an If-None-Match header matching the ETag, it returns 304 Not Modified.
func ETagMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}

		// Skip streaming, metrics, and websockets
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") ||
			strings.EqualFold(r.Header.Get("Upgrade"), "websocket") ||
			r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		rec := &etagResponseWriter{
			ResponseWriter: w,
			buf:            &bytes.Buffer{},
			statusCode:     http.StatusOK,
		}

		next.ServeHTTP(rec, r)

		if rec.statusCode != http.StatusOK {
			w.WriteHeader(rec.statusCode)
			_, _ = w.Write(rec.buf.Bytes())
			return
		}

		bodyBytes := rec.buf.Bytes()
		hash := sha256.Sum256(bodyBytes)
		etag := `"` + hex.EncodeToString(hash[:16]) + `"`

		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "no-cache")

		if match := r.Header.Get("If-None-Match"); match != "" {
			if match == etag || match == "*" || strings.Contains(match, etag) {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bodyBytes)
	})
}
