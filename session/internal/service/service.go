// Package service implements session boundary detection logic.
// A new session starts when no event has been seen for a device in 30 minutes.
package service

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/metrics"
	"github.com/pulse-analytics/shared/pkg/models"
	redisclient "github.com/pulse-analytics/shared/pkg/redis"

	"github.com/pulse-analytics/session/internal/repo"
)

const sessionTimeout = 30 * time.Minute

// SessionService manages session state transitions.
type SessionService struct {
	repo    *repo.Repo
	metrics *metrics.Registry
	log     *zap.Logger
}

// New creates a SessionService.
func New(redis *redisclient.Client, m *metrics.Registry, log *zap.Logger) *SessionService {
	return &SessionService{
		repo:    repo.New(redis, log),
		metrics: m,
		log:     log,
	}
}

// ProcessResult holds the updated event and any synthetic session events generated.
type ProcessResult struct {
	UpdatedEvent models.EnrichedEvent
	SessionEvts  []models.SessionEvent
}

// Process assigns a session ID to an enriched event and emits synthetic session_start/end.
func (s *SessionService) Process(ctx context.Context, event models.EnrichedEvent) (*ProcessResult, error) {
	now := time.Now().UnixMilli()
	state, err := s.repo.GetSession(ctx, event.AppID, event.DeviceID)

	var sessionEvts []models.SessionEvent
	if err != nil || state == nil {
		// New session
		state = &models.SessionState{
			SessionID:   models.NewEventID(),
			AppID:       event.AppID,
			UserID:      event.UserID,
			DeviceID:    event.DeviceID,
			StartTimeMs: now,
			LastEventMs: now,
			EventCount:  1,
			EntryScreen: screenFromProps(event.Props),
			UpdatedAt:   time.Now(),
		}
		sessionEvts = append(sessionEvts, buildSessionStart(state))
	} else {
		// Check for session timeout
		if now-state.LastEventMs > sessionTimeout.Milliseconds() {
			// End old session
			exitScreen := screenFromProps(event.Props)
			sessionEvts = append(sessionEvts, buildSessionEnd(state, now, exitScreen, "timeout"))

			// Start new session
			state = &models.SessionState{
				SessionID:   models.NewEventID(),
				AppID:       event.AppID,
				UserID:      event.UserID,
				DeviceID:    event.DeviceID,
				StartTimeMs: now,
				LastEventMs: now,
				EventCount:  1,
				EntryScreen: screenFromProps(event.Props),
				UpdatedAt:   time.Now(),
			}
			sessionEvts = append(sessionEvts, buildSessionStart(state))
		} else {
			state.EventCount++
			state.LastEventMs = now
			state.UpdatedAt = time.Now()
			if screen := screenFromProps(event.Props); screen != "" {
				state.Screens = append(state.Screens, screen)
			}
		}
	}

	event.SessionID = state.SessionID
	if err := s.repo.SaveSession(ctx, state); err != nil {
		s.log.Warn("save session failed", zap.Error(err))
	}

	return &ProcessResult{UpdatedEvent: event, SessionEvts: sessionEvts}, nil
}

func buildSessionStart(s *models.SessionState) models.SessionEvent {
	return models.SessionEvent{
		SessionID:   s.SessionID,
		AppID:       s.AppID,
		UserID:      s.UserID,
		DeviceID:    s.DeviceID,
		StartTimeMs: s.StartTimeMs,
		EntryScreen: s.EntryScreen,
		Type:        "session_start",
	}
}

func buildSessionEnd(s *models.SessionState, endMs int64, exitScreen, reason string) models.SessionEvent {
	durS := (endMs - s.StartTimeMs) / 1000
	return models.SessionEvent{
		SessionID:   s.SessionID,
		AppID:       s.AppID,
		UserID:      s.UserID,
		DeviceID:    s.DeviceID,
		StartTimeMs: s.StartTimeMs,
		EndTimeMs:   endMs,
		DurationS:   durS,
		EventCount:  s.EventCount,
		EntryScreen: s.EntryScreen,
		ExitScreen:  exitScreen,
		ExitReason:  reason,
		Type:        "session_end",
	}
}

func screenFromProps(props map[string]any) string {
	if props == nil {
		return ""
	}
	if v, ok := props["screen_name"]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
