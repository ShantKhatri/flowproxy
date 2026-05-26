package middleware

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/shivalaya/flowproxy/internal/ratelimit"
)

func RateLimit(rl *ratelimit.RateLimiter, limitFn func(path string) (int, int)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clientID := r.Header.Get("X-Client-ID")
			if clientID == "" {
				clientID, _, _ = net.SplitHostPort(r.RemoteAddr)
			}

			limit, windowSec := limitFn(r.URL.Path)
			allowed, info, err := rl.Allow(r.Context(), clientID, r.URL.Path, limit, windowSec)

			if err != nil {
				// fail_closed: Redis down, block the request.
				http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
				return
			}

			w.Header().Set("X-RateLimit-Limit", strconv.FormatInt(info.Limit, 10))

			if !allowed {
				resetSec := info.ResetAtMs / 1000
				retryAfter := (info.ResetAtMs - time.Now().UnixMilli()) / 1000
				if retryAfter < 1 {
					retryAfter = 1
				}
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetSec, 10))
				w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
				http.Error(w, fmt.Sprintf("Rate limit exceeded. Retry after %ds", retryAfter), http.StatusTooManyRequests)
				return
			}

			w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(info.Remaining, 10))
			next.ServeHTTP(w, r)
		})
	}
}
