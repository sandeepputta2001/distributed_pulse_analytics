package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"

	"github.com/pulse-analytics/internal/config"
	"github.com/pulse-analytics/internal/models"
	"github.com/pulse-analytics/internal/postgres"
	"github.com/pulse-analytics/internal/redis"
)

var (
	ErrInvalidAPIKey = errors.New("invalid or inactive API key")
	ErrRateLimited   = errors.New("rate limit exceeded")
	ErrUnauthorized  = errors.New("unauthorized")
)

type contextKey string

const appContextKey contextKey = "app"

// Service handles API key auth with Redis caching.
type Service struct {
	pg    *postgres.Client
	redis *redis.Client
	cfg   *config.AuthConfig
	log   *zap.Logger
}

// Claims holds JWT payload.
type Claims struct {
	OrgID string `json:"org_id"`
	AppID string `json:"app_id"`
	Role  string `json:"role"`
	jwt.RegisteredClaims
}

func NewService(pg *postgres.Client, redis *redis.Client, cfg *config.AuthConfig, log *zap.Logger) *Service {
	return &Service{pg: pg, redis: redis, cfg: cfg, log: log}
}

// ValidateAPIKey validates an API key, using Redis as an L1 cache.
// Cache hit: ~1µs. Cache miss: ~1ms (Postgres lookup).
func (s *Service) ValidateAPIKey(ctx context.Context, apiKey string) (*models.App, error) {
	cacheKey := "auth:key:" + apiKey

	// L1 cache lookup
	var app models.App
	if err := s.redis.GetJSON(ctx, cacheKey, &app); err == nil {
		if !app.Active {
			return nil, ErrInvalidAPIKey
		}
		return &app, nil
	}

	// Cache miss: fetch from Postgres
	fetched, err := s.pg.GetAppByAPIKey(ctx, apiKey)
	if err != nil {
		s.log.Warn("api key not found", zap.String("key", maskKey(apiKey)))
		return nil, ErrInvalidAPIKey
	}

	// Cache for 5 minutes
	if err := s.redis.SetJSON(ctx, cacheKey, fetched, s.cfg.APIKeyCacheTTL); err != nil {
		s.log.Warn("failed to cache api key", zap.Error(err))
	}

	return fetched, nil
}

// GenerateToken creates a signed JWT for dashboard auth.
func (s *Service) GenerateToken(orgID, appID, role string) (string, error) {
	claims := Claims{
		OrgID: orgID,
		AppID: appID,
		Role:  role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(s.cfg.JWTExpiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "pulse-analytics",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.cfg.JWTSecret))
}

// ValidateToken parses and validates a JWT.
func (s *Service) ValidateToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(s.cfg.JWTSecret), nil
	})
	if err != nil {
		return nil, ErrUnauthorized
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrUnauthorized
	}

	return claims, nil
}

// WithApp stores app in context for handler access.
func WithApp(ctx context.Context, app *models.App) context.Context {
	return context.WithValue(ctx, appContextKey, app)
}

// AppFromContext retrieves app from context.
func AppFromContext(ctx context.Context) (*models.App, bool) {
	app, ok := ctx.Value(appContextKey).(*models.App)
	return app, ok
}

func maskKey(key string) string {
	if len(key) > 8 {
		return key[:4] + "****" + key[len(key)-4:]
	}
	return "****"
}
