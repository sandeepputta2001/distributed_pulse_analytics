// Package repo provides Redis state access for the Session service.
package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/models"
	redisclient "github.com/pulse-analytics/shared/pkg/redis"
)

const sessionTTL = 35 * time.Minute // 30min inactivity + 5min buffer

// Repo manages session state in Redis.
type Repo struct {
	redis *redisclient.Client
	log   *zap.Logger
}

// New creates a session Repo.
func New(redis *redisclient.Client, log *zap.Logger) *Repo {
	return &Repo{redis: redis, log: log}
}

// GetSession fetches the current session state for a device.
func (r *Repo) GetSession(ctx context.Context, appID, deviceID string) (*models.SessionState, error) {
	key := sessionKey(appID, deviceID)
	var state models.SessionState
	if err := r.redis.GetJSON(ctx, key, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// SaveSession persists session state with TTL.
func (r *Repo) SaveSession(ctx context.Context, state *models.SessionState) error {
	key := sessionKey(state.AppID, state.DeviceID)
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return r.redis.Set(ctx, key, data, sessionTTL)
}

// DeleteSession removes session state (called on session_end).
func (r *Repo) DeleteSession(ctx context.Context, appID, deviceID string) error {
	return r.redis.Del(ctx, sessionKey(appID, deviceID))
}

func sessionKey(appID, deviceID string) string {
	return fmt.Sprintf("session:%s:%s", appID, deviceID)
}
