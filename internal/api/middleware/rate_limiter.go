package middleware

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type ipBucket struct {
	tokens     float64
	lastRefill time.Time
}

type RateLimiter struct {
	mu           sync.Mutex
	buckets      map[string]*ipBucket
	authBuckets  map[string]*ipBucket
	globalLimit  float64       // max tokens (e.g. 120)
	globalRefill float64       // tokens per sec (e.g. 2.0)
	authLimit    float64       // max tokens (e.g. 10)
	authRefill   float64       // tokens per sec (e.g. 0.166 => ~10/min)
	window       time.Duration
}

func NewRateLimiter(globalLimit int, window time.Duration) *RateLimiter {
	if globalLimit <= 0 {
		globalLimit = 120
	}
	if window <= 0 {
		window = 1 * time.Minute
	}

	rl := &RateLimiter{
		buckets:      make(map[string]*ipBucket),
		authBuckets:  make(map[string]*ipBucket),
		globalLimit:  float64(globalLimit),
		globalRefill: float64(globalLimit) / window.Seconds(),
		authLimit:    10.0,
		authRefill:   10.0 / window.Seconds(),
		window:       window,
	}

	// Periodic background cleanup of idle buckets
	go rl.cleanupLoop()

	return rl
}

func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for ip, b := range rl.buckets {
			if now.Sub(b.lastRefill) > 10*time.Minute {
				delete(rl.buckets, ip)
			}
		}
		for ip, b := range rl.authBuckets {
			if now.Sub(b.lastRefill) > 10*time.Minute {
				delete(rl.authBuckets, ip)
			}
		}
		rl.mu.Unlock()
	}
}

func extractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return strings.TrimSpace(xrip)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractClientIP(r)
		path := strings.ToLower(r.URL.Path)

		// Check if path is a high-risk authentication / password / ticket endpoint
		isAuthPath := strings.Contains(path, "login") ||
			strings.Contains(path, "forgot-password") ||
			strings.Contains(path, "reset-password") ||
			strings.Contains(path, "ws/ticket")

		rl.mu.Lock()
		now := time.Now()

		var allowed bool
		var limit, remaining float64

		if isAuthPath {
			b, exists := rl.authBuckets[ip]
			if !exists {
				b = &ipBucket{tokens: rl.authLimit, lastRefill: now}
				rl.authBuckets[ip] = b
			} else {
				elapsed := now.Sub(b.lastRefill).Seconds()
				b.tokens = minFloat(rl.authLimit, b.tokens+elapsed*rl.authRefill)
				b.lastRefill = now
			}

			limit = rl.authLimit
			if b.tokens >= 1.0 {
				b.tokens -= 1.0
				allowed = true
			}
			remaining = b.tokens
		} else {
			b, exists := rl.buckets[ip]
			if !exists {
				b = &ipBucket{tokens: rl.globalLimit, lastRefill: now}
				rl.buckets[ip] = b
			} else {
				elapsed := now.Sub(b.lastRefill).Seconds()
				b.tokens = minFloat(rl.globalLimit, b.tokens+elapsed*rl.globalRefill)
				b.lastRefill = now
			}

			limit = rl.globalLimit
			if b.tokens >= 1.0 {
				b.tokens -= 1.0
				allowed = true
			}
			remaining = b.tokens
		}

		rl.mu.Unlock()

		w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%.0f", limit))
		w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%.0f", maxFloat(0, remaining)))

		if !allowed {
			w.Header().Set("Retry-After", "10")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error":   "Rate limit exceeded (DDoS protection active)",
				"message": "Too many requests. Please wait a moment before trying again.",
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
