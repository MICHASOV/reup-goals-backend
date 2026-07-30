package security

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

const (
	defaultRequestLimit = 2 << 20
	audioRequestLimit   = 27 << 20
	fileRequestLimit    = 82 << 20
)

type limiterEntry struct {
	count   int
	resetAt time.Time
}

type Limiter struct {
	mu      sync.Mutex
	entries map[string]limiterEntry
	limit   int
	window  time.Duration
	now     func() time.Time
}

func NewLimiter(limit int, window time.Duration) *Limiter {
	if limit <= 0 {
		limit = 300
	}
	if window <= 0 {
		window = time.Minute
	}
	return &Limiter{entries: make(map[string]limiterEntry), limit: limit, window: window, now: time.Now}
}

func (l *Limiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if !l.allow(clientIP(r)) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "rate_limit_exceeded"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *Limiter) allow(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.entries[key]
	if !ok || !now.Before(entry.resetAt) {
		l.entries[key] = limiterEntry{count: 1, resetAt: now.Add(l.window)}
		l.prune(now)
		return true
	}
	if entry.count >= l.limit {
		return false
	}
	entry.count++
	l.entries[key] = entry
	return true
}

func (l *Limiter) prune(now time.Time) {
	if len(l.entries) < 4096 {
		return
	}
	for key, entry := range l.entries {
		if !now.Before(entry.resetAt) {
			delete(l.entries, key)
		}
	}
}

func Harden(next http.Handler) http.Handler {
	return securityHeaders(recoverPanics(limitRequestBodies(next)))
}

func RequireTrustedOrigin(allowedOrigins []string, next http.Handler) http.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		if value := strings.TrimRight(strings.TrimSpace(origin), "/"); value != "" {
			allowed[value] = true
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions || strings.HasPrefix(strings.TrimSpace(r.Header.Get("Authorization")), "Bearer ") {
			next.ServeHTTP(w, r)
			return
		}
		if _, err := r.Cookie("reupgoals_session"); err != nil {
			next.ServeHTTP(w, r)
			return
		}
		origin := strings.TrimRight(strings.TrimSpace(r.Header.Get("Origin")), "/")
		if origin == "" || !allowed[origin] {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "untrusted_origin"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-site")
		w.Header().Set("Permissions-Policy", "camera=(), geolocation=(), payment=(), usb=()")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func limitRequestBodies(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := int64(defaultRequestLimit)
		switch r.URL.Path {
		case "/api/v2/strategic-director/files", "/api/v2/strategy-facilitator/files":
			limit = fileRequestLimit
		case "/api/v2/audio/transcriptions", "/api/v2/tactics-facilitator/files":
			limit = audioRequestLimit
		}
		if r.ContentLength > limit {
			writePayloadTooLarge(w)
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				// #nosec G706 -- request-derived values are quoted before logging.
				log.Printf("[ERROR] panic method=%q path=%q value=%q\n%s", r.Method, r.URL.Path, fmt.Sprint(recovered), debug.Stack())
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "internal_error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func writePayloadTooLarge(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusRequestEntityTooLarge)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "payload_too_large"})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	parsed := net.ParseIP(host)
	if parsed != nil && parsed.IsLoopback() {
		if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); net.ParseIP(forwarded) != nil {
			return forwarded
		}
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(realIP) != nil {
			return realIP
		}
	}
	if host == "" {
		return "unknown"
	}
	return host
}
