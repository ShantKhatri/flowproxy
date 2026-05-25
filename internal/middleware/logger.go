package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/shivalaya/flowproxy/internal/storage"
)

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func Logger(logWriter *storage.LogWriter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)
			latency := time.Since(start)

			slog.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rw.status,
				"latency_ms", latency.Milliseconds(),
			)

			if logWriter == nil {
				return
			}

			clientID := r.Header.Get("X-Client-ID")
			if clientID == "" {
				clientID, _, _ = net.SplitHostPort(r.RemoteAddr)
			}

			entry := storage.LogEntry{
				ClientID:   clientID,
				Route:      r.URL.Path,
				Method:     r.Method,
				StatusCode: rw.status,
				LatencyMs:  latency.Milliseconds(),
				Timestamp:  start,
			}

			select {
			case logWriter.Chan() <- entry:
			default:
				logWriter.RecordDrop()
			}
		})
	}
}
