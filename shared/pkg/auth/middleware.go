package auth

import (
	"context"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/ratelimit"
)

// APIKeyMiddleware authenticates requests via X-API-Key header.
func (s *Service) APIKeyMiddleware(rl *ratelimit.Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := r.Header.Get("X-API-Key")
			if apiKey == "" {
				http.Error(w, `{"error":"missing X-API-Key header"}`, http.StatusUnauthorized)
				return
			}

			app, err := s.ValidateAPIKey(r.Context(), apiKey)
			if err != nil {
				s.log.Warn("invalid api key",
					zap.String("ip", r.RemoteAddr),
					zap.Error(err),
				)
				http.Error(w, `{"error":"invalid api key"}`, http.StatusUnauthorized)
				return
			}

			// Per-app rate limiting
			if rl != nil {
				allowed, err := rl.Allow(r.Context(), app.ID, app.RPS, app.Burst)
				if err != nil {
					s.log.Error("rate limit check failed", zap.Error(err))
				} else if !allowed {
					w.Header().Set("Retry-After", "1")
					http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
					return
				}
			}

			ctx := WithApp(r.Context(), app)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// JWTMiddleware validates Bearer token for dashboard API.
func (s *Service) JWTMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, `{"error":"missing bearer token"}`, http.StatusUnauthorized)
			return
		}

		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		claims, err := s.ValidateToken(tokenStr)
		if err != nil {
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), claimsContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type claimsKey string

const claimsContextKey claimsKey = "claims"

// ClaimsFromContext retrieves JWT claims from context.
func ClaimsFromContext(r *http.Request) (*Claims, bool) {
	claims, ok := r.Context().Value(claimsContextKey).(*Claims)
	return claims, ok
}
