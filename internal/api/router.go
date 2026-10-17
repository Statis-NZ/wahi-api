package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/meilisearch/meilisearch-go"
	"github.com/statis/wahi-api/internal/config"
)

// Server holds the HTTP router, Huma API instance, and Meilisearch client.
type Server struct {
	Config      *config.Config
	Meili       meilisearch.ServiceManager
	Huma        huma.API
	Handler     http.Handler
	rateLimiter *ipRateLimiter
}

// NewServer initializes the Huma v2 OpenAPI router, registers all endpoints,
// and wraps the HTTP handler with CORS and rate-limiting middleware.
func NewServer(cfg *config.Config, meiliClient meilisearch.ServiceManager) *Server {
	mux := http.NewServeMux()

	humaCfg := huma.DefaultConfig("Wāhi API", "1.0.0")
	humaCfg.Info.Description = "Lightweight, self-hostable New Zealand Address Autosuggest & Geocoding REST API backed by LINZ open data and Meilisearch."

	api := humago.New(mux, humaCfg)

	s := &Server{
		Config:      cfg,
		Meili:       meiliClient,
		Huma:        api,
		rateLimiter: newIPRateLimiter(cfg.RateLimitRPS, cfg.RateLimitBurst),
	}

	s.registerRoutes()

	var handler http.Handler = mux
	handler = s.rateLimitMiddleware(handler)
	handler = s.corsMiddleware(handler)
	s.Handler = handler

	return s
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	allowedOrigins := s.Config.CORSOrigins
	allowAll := len(allowedOrigins) == 0
	for _, o := range allowedOrigins {
		if o == "*" {
			allowAll = true
			break
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowAll {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if origin != "" {
			for _, allowed := range allowedOrigins {
				if strings.EqualFold(origin, allowed) {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Vary", "Origin")
					break
				}
			}
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-Requested-With")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

type tokenBucket struct {
	tokens     float64
	lastRefill time.Time
}

type ipRateLimiter struct {
	mu      sync.Mutex
	rps     float64
	burst   float64
	clients map[string]*tokenBucket
}

func newIPRateLimiter(rps float64, burst int) *ipRateLimiter {
	if rps <= 0 {
		rps = 50
	}
	if burst <= 0 {
		burst = 100
	}
	return &ipRateLimiter{
		rps:     rps,
		burst:   float64(burst),
		clients: make(map[string]*tokenBucket),
	}
}

func (l *ipRateLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, exists := l.clients[ip]
	if !exists {
		if len(l.clients) > 10000 {
			// Evict stale entries to bound memory usage
			cutoff := now.Add(-5 * time.Minute)
			for k, v := range l.clients {
				if v.lastRefill.Before(cutoff) {
					delete(l.clients, k)
				}
			}
		}
		l.clients[ip] = &tokenBucket{
			tokens:     l.burst - 1,
			lastRefill: now,
		}
		return true
	}

	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * l.rps
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.lastRefill = now

	if b.tokens < 1.0 {
		return false
	}
	b.tokens -= 1.0
	return true
}

func (s *Server) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		ip := extractClientIP(r)
		if !s.rateLimiter.allow(ip) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"title":"Too Many Requests","status":429,"detail":"Rate limit exceeded. Please slow down."}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func extractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[0]); ip != "" {
			return ip
		}
	}
	if xrip := strings.TrimSpace(r.Header.Get("X-Real-IP")); xrip != "" {
		return xrip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}
