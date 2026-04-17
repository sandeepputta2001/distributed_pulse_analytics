// Package session implements the session engine.
// A session is a contiguous group of events from the same device
// with no more than 30 minutes of inactivity between events.
// The engine maintains per-device session state in Redis and
// emits session_start / session_end synthetic events.
package session

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/metrics"
	"github.com/pulse-analytics/shared/pkg/models"
	redisclient "github.com/pulse-analytics/shared/pkg/redis"
)

const (
	// SessionTimeout is the inactivity window that closes a session.
	SessionTimeout = 30 * time.Minute

	// sessionKeyPrefix is the Redis key prefix for session state.
	sessionKeyPrefix = "session:state:"
)

// Engine manages session state for enriched events.
type Engine struct {
	redis   *redisclient.Client
	m       *metrics.Registry
	log     *zap.Logger
	timeout time.Duration
}

// NewEngine creates a Session Engine.
func NewEngine(redis *redisclient.Client, m *metrics.Registry, log *zap.Logger) *Engine {
	return &Engine{
		redis:   redis,
		m:       m,
		log:     log,
		timeout: SessionTimeout,
	}
}

// Process assigns a session ID to an enriched event and manages
// session open/close lifecycle. Returns the updated event plus any
// synthetic session events to emit.
func (e *Engine) Process(ctx context.Context, event models.EnrichedEvent) (
	updated models.EnrichedEvent, sessionEvts []models.SessionEvent, err error,
) {
	// Session state key: per device per app
	key := sessionKeyPrefix + event.AppID + ":" + event.DeviceID

	// Load current session state from Redis
	state, isNew, err := e.loadSession(ctx, key)
	if err != nil {
		return event, nil, fmt.Errorf("load session: %w", err)
	}

	now := event.ServerTime
	var emitEnd bool

	if isNew {
		// Start a new session
		state = &models.SessionState{
			SessionID:   fmt.Sprintf("%s-%s-%d", event.AppID, event.DeviceID, now),
			AppID:       event.AppID,
			UserID:      event.UserID,
			DeviceID:    event.DeviceID,
			StartTimeMs: now,
			LastEventMs: now,
			EventCount:  0,
			Screens:     []string{},
		}
		e.m.SessionsOpened.Inc()
	} else {
		// Check if session expired (inactivity gap > 30 min)
		inactiveDuration := time.Duration(now-state.LastEventMs) * time.Millisecond
		if inactiveDuration > e.timeout {
			// Close old session
			sessionEvts = append(sessionEvts, e.buildSessionEndEvent(state, now-1))
			emitEnd = true
			e.m.SessionsClosed.Inc()
			e.m.SessionDuration.Observe(float64(now-state.StartTimeMs) / 1000)

			// Open new session
			state = &models.SessionState{
				SessionID:   fmt.Sprintf("%s-%s-%d", event.AppID, event.DeviceID, now),
				AppID:       event.AppID,
				UserID:      event.UserID,
				DeviceID:    event.DeviceID,
				StartTimeMs: now,
				LastEventMs: now,
				EventCount:  0,
				Screens:     []string{},
			}
			e.m.SessionsOpened.Inc()
		}
	}

	// Emit session_start on new session
	if isNew || emitEnd {
		sessionEvts = append(sessionEvts, models.SessionEvent{
			SessionID:   state.SessionID,
			AppID:       state.AppID,
			UserID:      state.UserID,
			DeviceID:    state.DeviceID,
			StartTimeMs: state.StartTimeMs,
			Type:        "session_start",
		})
	}

	// Update session state
	state.LastEventMs = now
	state.EventCount++
	if state.UserID == "" && event.UserID != "" {
		state.UserID = event.UserID // late identity resolution
	}

	// Track screen views
	if event.EventName == "screen_view" || event.EventName == "page_view" {
		if screenName, ok := event.Props["screen_name"].(string); ok && screenName != "" {
			state.Screens = append(state.Screens, screenName)
			if state.EntryScreen == "" {
				state.EntryScreen = screenName
			}
		}
	}

	// Attach session_id to the event
	event.SessionID = state.SessionID

	// Persist updated session state with sliding TTL
	if err := e.saveSession(ctx, key, state, e.timeout+5*time.Minute); err != nil {
		e.log.Warn("save session failed", zap.Error(err))
	}

	return event, sessionEvts, nil
}

func (e *Engine) buildSessionEndEvent(state *models.SessionState, endTimeMs int64) models.SessionEvent {
	exitScreen := ""
	if len(state.Screens) > 0 {
		exitScreen = state.Screens[len(state.Screens)-1]
	}
	return models.SessionEvent{
		SessionID:   state.SessionID,
		AppID:       state.AppID,
		UserID:      state.UserID,
		DeviceID:    state.DeviceID,
		StartTimeMs: state.StartTimeMs,
		EndTimeMs:   endTimeMs,
		DurationS:   (endTimeMs - state.StartTimeMs) / 1000,
		EventCount:  state.EventCount,
		Screens:     state.Screens,
		EntryScreen: state.EntryScreen,
		ExitScreen:  exitScreen,
		ExitReason:  "timeout",
		Type:        "session_end",
	}
}

func (e *Engine) loadSession(ctx context.Context, key string) (*models.SessionState, bool, error) {
	var state models.SessionState
	if err := e.redis.GetJSON(ctx, key, &state); err != nil {
		if redisclient.IsNotFound(err) {
			return nil, true, nil // new session
		}
		return nil, true, nil // treat errors as new session (fail open)
	}
	return &state, false, nil
}

func (e *Engine) saveSession(ctx context.Context, key string, state *models.SessionState, ttl time.Duration) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return e.redis.Set(ctx, key, data, ttl)
}
