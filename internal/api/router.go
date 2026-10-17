package api

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	huma "github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	meilisearch "github.com/meilisearch/meilisearch-go"
	"github.com/statis/wahi-api/internal/config"
)

// Server holds the HTTP router, Huma API instance, and Meilisearch client.
type Server struct {
	Config      *config.Config
	Meili       meilisearch.ServiceManager
	Huma        huma.API
	Handler     http.Handler
	rateLimiter *ipRateLimiter

	tokensMu     sync.RWMutex
	accessTokens []string
}

// NewServer initializes the Huma v2 OpenAPI router, registers all endpoints,
// and wraps the HTTP handler with CORS, access token authentication, and rate-limiting middleware.
func NewServer(cfg *config.Config, meiliClient meilisearch.ServiceManager) *Server {
	mux := http.NewServeMux()

	humaCfg := huma.DefaultConfig("Wāhi API", "1.0.0")
	humaCfg.Info.Description = "Lightweight, self-hostable New Zealand Address Autosuggest & Geocoding REST API backed by LINZ open data and Meilisearch."

	if humaCfg.Components.SecuritySchemes == nil {
		humaCfg.Components.SecuritySchemes = make(map[string]*huma.SecurityScheme)
	}
	humaCfg.Components.SecuritySchemes["BearerAuth"] = &huma.SecurityScheme{
		Type:        "http",
		Scheme:      "bearer",
		Description: "Access token passed via Authorization: Bearer <token>",
	}
	humaCfg.Components.SecuritySchemes["ApiKeyHeader"] = &huma.SecurityScheme{
		Type:        "apiKey",
		In:          "header",
		Name:        "X-API-Key",
		Description: "Access token passed via X-API-Key header",
	}
	humaCfg.Components.SecuritySchemes["ApiKeyQuery"] = &huma.SecurityScheme{
		Type:        "apiKey",
		In:          "query",
		Name:        "api_key",
		Description: "Access token passed via ?api_key=<token> query parameter",
	}

	api := humago.New(mux, humaCfg)

	s := &Server{
		Config:       cfg,
		Meili:        meiliClient,
		Huma:         api,
		rateLimiter:  newIPRateLimiter(cfg.RateLimitRPS, cfg.RateLimitBurst),
		accessTokens: append([]string(nil), cfg.APIAccessTokens...),
	}

	s.registerRoutes()

	var handler http.Handler = mux
	handler = s.rateLimitMiddleware(handler)
	handler = s.authMiddleware(handler)
	handler = s.corsMiddleware(handler)
	s.Handler = handler

	return s
}

// UpdateAccessTokens atomically updates the active access token list in memory,
// enabling zero-downtime token rotation (e.g. on SIGHUP).
func (s *Server) UpdateAccessTokens(tokens []string) {
	s.tokensMu.Lock()
	defer s.tokensMu.Unlock()
	s.accessTokens = append([]string(nil), tokens...)
}

func (s *Server) getAccessTokens() []string {
	s.tokensMu.RLock()
	defer s.tokensMu.RUnlock()
	return s.accessTokens
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
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		originMatched := false

		if allowAll {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			originMatched = true
		} else if origin != "" {
			if matchCORSOrigin(origin, allowedOrigins) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				originMatched = true
			}
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-API-Key, X-Requested-With")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			if !allowAll && s.Config.CORSStrict && origin != "" && !originMatched {
				writeProblemJSON(w, http.StatusForbidden, "Forbidden", "Origin is not permitted by CORS policy.")
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if !allowAll && s.Config.CORSStrict && origin != "" && !originMatched {
			writeProblemJSON(w, http.StatusForbidden, "Forbidden", "Origin is not permitted by CORS policy.")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// matchCORSOrigin checks whether an Origin header matches any configured pattern,
// supporting exact matches ("https://example.com") and wildcard subdomains ("https://*.example.com").
func matchCORSOrigin(origin string, allowedPatterns []string) bool {
	originLower := strings.ToLower(strings.TrimSpace(origin))
	for _, pattern := range allowedPatterns {
		patLower := strings.ToLower(strings.TrimSpace(pattern))
		if patLower == "*" || patLower == originLower {
			return true
		}
		// Support wildcard subdomains such as "https://*.example.com"
		if idx := strings.Index(patLower, "*."); idx != -1 {
			prefix := patLower[:idx]   // e.g. "https://"
			suffix := patLower[idx+1:] // e.g. ".example.com"
			if strings.HasPrefix(originLower, prefix) && strings.HasSuffix(originLower, suffix) {
				sub := originLower[len(prefix) : len(originLower)-len(suffix)]
				if len(sub) > 0 && !strings.Contains(sub, "/") {
					return true
				}
			}
		}
	}
	return false
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens := s.getAccessTokens()
		if len(tokens) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		// Allow health checks and OpenAPI documentation UI to load without token headers
		switch r.URL.Path {
		case "/healthz", "/docs", "/openapi.json", "/openapi.yaml":
			next.ServeHTTP(w, r)
			return
		}

		provided := extractAccessToken(r)
		if provided == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="wahi-api"`)
			writeProblemJSON(w, http.StatusUnauthorized, "Unauthorized", "Missing access token. Provide Authorization: Bearer <token>, X-API-Key header, or ?api_key query parameter.")
			return
		}

		if !matchAccessToken(provided, tokens) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="wahi-api", error="invalid_token"`)
			writeProblemJSON(w, http.StatusUnauthorized, "Unauthorized", "Invalid or expired access token.")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func extractAccessToken(r *http.Request) string {
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); auth != "" {
		if len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
			return strings.TrimSpace(auth[7:])
		}
	}
	if apiKey := strings.TrimSpace(r.Header.Get("X-API-Key")); apiKey != "" {
		return apiKey
	}
	if qKey := strings.TrimSpace(r.URL.Query().Get("api_key")); qKey != "" {
		return qKey
	}
	return ""
}

func matchAccessToken(provided string, validTokens []string) bool {
	providedBytes := []byte(provided)
	matched := false
	for _, valid := range validTokens {
		validBytes := []byte(valid)
		if subtle.ConstantTimeCompare(providedBytes, validBytes) == 1 {
			matched = true
		}
	}
	return matched
}

func writeProblemJSON(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"title":` + quoteJSON(title) + `,"status":` + itoaStatus(status) + `,"detail":` + quoteJSON(detail) + `}`))
}

func quoteJSON(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

func itoaStatus(code int) string {
	switch code {
	case http.StatusUnauthorized:
		return "401"
	case http.StatusForbidden:
		return "403"
	case http.StatusTooManyRequests:
		return "429"
	default:
		return "400"
	}
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
			writeProblemJSON(w, http.StatusTooManyRequests, "Too Many Requests", "Rate limit exceeded. Please slow down.")
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
