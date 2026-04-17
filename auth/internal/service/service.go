// Package service implements auth business logic: JWT issuance, API key validation,
// org/app registration, and API key rotation.
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/auth"
	"github.com/pulse-analytics/shared/pkg/config"
	"github.com/pulse-analytics/shared/pkg/models"
	redisclient "github.com/pulse-analytics/shared/pkg/redis"

	"github.com/pulse-analytics/auth/internal/repo"
)

// AuthService orchestrates all authentication and registration workflows.
type AuthService struct {
	repo    *repo.Repo
	jwt     *auth.Service      // shared JWT/API-key auth
	redis   *redisclient.Client
	cfg     *config.AuthConfig
	log     *zap.Logger
}

// New creates an AuthService.
func New(r *repo.Repo, jwtSvc *auth.Service, redis *redisclient.Client, cfg *config.AuthConfig, log *zap.Logger) *AuthService {
	return &AuthService{repo: r, jwt: jwtSvc, redis: redis, cfg: cfg, log: log}
}

// RegisterOrgApp creates a new org + app, returns orgID, appID, apiKey, JWT.
func (s *AuthService) RegisterOrgApp(ctx context.Context, orgName, appName, email string) (orgID, appID, apiKey, token string, err error) {
	orgID, appID, apiKey, err = s.repo.CreateOrgAndApp(ctx, orgName, appName, email)
	if err != nil {
		return
	}
	token, err = s.jwt.GenerateToken(orgID, appID, "admin")
	if err != nil {
		return
	}
	s.log.Info("org registered", zap.String("org_id", orgID), zap.String("app_id", appID))
	return
}

// ExchangeAPIKey validates an API key and issues a JWT.
func (s *AuthService) ExchangeAPIKey(ctx context.Context, apiKey string) (token string, app *models.App, err error) {
	app, err = s.jwt.ValidateAPIKey(ctx, apiKey)
	if err != nil {
		return
	}
	token, err = s.jwt.GenerateToken(app.OrgID, app.ID, "admin")
	return
}

// RefreshToken validates an existing JWT and issues a new one.
func (s *AuthService) RefreshToken(tokenStr string) (newToken string, claims *auth.Claims, err error) {
	claims, err = s.jwt.ValidateToken(tokenStr)
	if err != nil {
		return
	}
	newToken, err = s.jwt.GenerateToken(claims.OrgID, claims.AppID, claims.Role)
	return
}

// ValidateToken parses and validates a JWT, returning its claims.
func (s *AuthService) ValidateToken(tokenStr string) (*auth.Claims, error) {
	return s.jwt.ValidateToken(tokenStr)
}

// RotateAPIKey generates a new API key and updates Postgres.
func (s *AuthService) RotateAPIKey(ctx context.Context, appID string) (string, error) {
	newKey, err := generateAPIKey()
	if err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	if err := s.repo.RotateAPIKey(ctx, appID, newKey); err != nil {
		return "", fmt.Errorf("rotate key: %w", err)
	}
	// Invalidate Redis cache for old key
	_ = s.redis.Del(ctx, "auth:key:"+newKey)
	s.log.Info("api key rotated", zap.String("app_id", appID))
	return newKey, nil
}

// JWTExpiry returns the configured JWT expiry duration.
func (s *AuthService) JWTExpiry() time.Duration {
	return s.cfg.JWTExpiry
}

func generateAPIKey() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "pk_live_" + hex.EncodeToString(b), nil
}
