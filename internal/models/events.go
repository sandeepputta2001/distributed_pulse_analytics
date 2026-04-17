package models

import (
	"time"

	"github.com/google/uuid"
)

// ─── Core Event Models ────────────────────────────────────────────────────────

// Event represents a single analytics event.
type Event struct {
	EventID   string                 `json:"event_id" bson:"event_id"`
	EventName string                 `json:"event_name" bson:"event_name"`
	EventTime int64                  `json:"event_time" bson:"event_time"` // client epoch ms
	Props     map[string]interface{} `json:"props,omitempty" bson:"props,omitempty"`
	Device    *DeviceContext         `json:"device,omitempty" bson:"device,omitempty"`
	App       *AppContext            `json:"app,omitempty" bson:"app,omitempty"`
	Revenue   float64                `json:"revenue,omitempty" bson:"revenue,omitempty"`
}

// EventBatch is the ingest payload from SDKs.
type EventBatch struct {
	AppID      string  `json:"app_id"`
	DeviceID   string  `json:"device_id"`
	UserID     string  `json:"user_id,omitempty"`
	SDKVersion string  `json:"sdk_version"`
	SentAtMs   int64   `json:"sent_at_ms"`
	Events     []Event `json:"events"`
}

// DeviceContext holds device metadata.
type DeviceContext struct {
	DeviceModel  string `json:"device_model,omitempty" bson:"device_model,omitempty"`
	OS           string `json:"os,omitempty" bson:"os,omitempty"`
	OSVersion    string `json:"os_version,omitempty" bson:"os_version,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty" bson:"manufacturer,omitempty"`
	ScreenSize   string `json:"screen_size,omitempty" bson:"screen_size,omitempty"`
	NetworkType  string `json:"network_type,omitempty" bson:"network_type,omitempty"`
	Language     string `json:"language,omitempty" bson:"language,omitempty"`
	Timezone     string `json:"timezone,omitempty" bson:"timezone,omitempty"`
	Carrier      string `json:"carrier,omitempty" bson:"carrier,omitempty"`
	IsTablet     bool   `json:"is_tablet,omitempty" bson:"is_tablet,omitempty"`
	UserAgent    string `json:"user_agent,omitempty" bson:"user_agent,omitempty"`
}

// AppContext holds application metadata.
type AppContext struct {
	Version     string `json:"version,omitempty" bson:"version,omitempty"`
	Build       string `json:"build,omitempty" bson:"build,omitempty"`
	Platform    string `json:"platform,omitempty" bson:"platform,omitempty"` // android|ios|web|react_native|flutter|server
	Environment string `json:"environment,omitempty" bson:"environment,omitempty"`
}

// EnrichedEvent is an event after the enrichment pipeline.
type EnrichedEvent struct {
	// Original event fields
	EventID   string                 `json:"event_id"`
	EventName string                 `json:"event_name"`
	EventTime int64                  `json:"event_time"` // client epoch ms
	Props     map[string]interface{} `json:"props,omitempty"`
	Device    *DeviceContext         `json:"device,omitempty"`
	App       *AppContext            `json:"app,omitempty"`
	Revenue   float64                `json:"revenue,omitempty"`

	// Identity
	AppID    string `json:"app_id"`
	UserID   string `json:"user_id"`
	DeviceID string `json:"device_id"`

	// Server-side
	SessionID       string `json:"session_id,omitempty"`
	ServerTime      int64  `json:"server_time"`      // server epoch ms
	ClientOffsetMs  int64  `json:"client_offset_ms"` // server_time - client event_time

	// GeoIP
	CountryCode string  `json:"country_code,omitempty"`
	CountryName string  `json:"country_name,omitempty"`
	Region      string  `json:"region,omitempty"`
	City        string  `json:"city,omitempty"`
	Latitude    float64 `json:"latitude,omitempty"`
	Longitude   float64 `json:"longitude,omitempty"`

	// UA Parsing
	Browser        string `json:"browser,omitempty"`
	BrowserVersion string `json:"browser_version,omitempty"`
	OSFamily       string `json:"os_family,omitempty"`

	// Attribution
	InstallSource string `json:"install_source,omitempty"`
	CampaignID    string `json:"campaign_id,omitempty"`
	AdsetID       string `json:"adset_id,omitempty"`
}

// ─── Session Models ───────────────────────────────────────────────────────────

type SessionState struct {
	SessionID    string    `json:"session_id"`
	AppID        string    `json:"app_id"`
	UserID       string    `json:"user_id"`
	DeviceID     string    `json:"device_id"`
	StartTimeMs  int64     `json:"start_time_ms"`
	LastEventMs  int64     `json:"last_event_ms"`
	EventCount   int       `json:"event_count"`
	Screens      []string  `json:"screens"`
	EntryScreen  string    `json:"entry_screen"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type SessionEvent struct {
	SessionID   string   `json:"session_id"`
	AppID       string   `json:"app_id"`
	UserID      string   `json:"user_id"`
	DeviceID    string   `json:"device_id"`
	StartTimeMs int64    `json:"start_time_ms"`
	EndTimeMs   int64    `json:"end_time_ms"`
	DurationS   int64    `json:"duration_s"`
	EventCount  int      `json:"event_count"`
	Screens     []string `json:"screens"`
	EntryScreen string   `json:"entry_screen"`
	ExitScreen  string   `json:"exit_screen"`
	ExitReason  string   `json:"exit_reason"` // background | crash | timeout
	Type        string   `json:"type"`        // session_start | session_end
}

// ─── Funnel Models ────────────────────────────────────────────────────────────

type FunnelDefinition struct {
	FunnelID      string   `json:"funnel_id" db:"funnel_id"`
	AppID         string   `json:"app_id" db:"app_id"`
	Name          string   `json:"name" db:"name"`
	Steps         []string `json:"steps" db:"steps"`
	WindowSeconds int64    `json:"window_seconds" db:"window_seconds"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
}

type FunnelState struct {
	FunnelID       string  `json:"funnel_id"`
	UserID         string  `json:"user_id"`
	CompletedSteps int     `json:"completed_steps"`
	StepTimestamps []int64 `json:"step_timestamps"`
	Converted      bool    `json:"converted"`
}

// ─── App / Tenant Models ──────────────────────────────────────────────────────

type App struct {
	ID        string    `json:"id" db:"id"`
	OrgID     string    `json:"org_id" db:"org_id"`
	Name      string    `json:"name" db:"name"`
	APIKey    string    `json:"api_key" db:"api_key"`
	RPS       float64   `json:"rps" db:"rps"`         // rate limit events/sec
	Burst     int       `json:"burst" db:"burst"`
	Active    bool      `json:"active" db:"active"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

type Org struct {
	ID        string    `json:"id" db:"id"`
	Name      string    `json:"name" db:"name"`
	Plan      string    `json:"plan" db:"plan"` // free | growth | enterprise
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// ─── ClickHouse Row Models ────────────────────────────────────────────────────

// CHEvent is the flat row written to ClickHouse events table.
type CHEvent struct {
	AppID       string
	EventID     string
	UserID      string
	DeviceID    string
	EventName   string
	EventTime   time.Time
	ServerTime  time.Time
	SessionID   string
	CountryCode string
	Platform    string
	AppVersion  string
	OSFamily    string
	Browser     string
	City        string
	Revenue     float64
	Props       map[string]string
	CampaignID  string
	InstallSrc  string
}

// ─── Alert Models ─────────────────────────────────────────────────────────────

type AlertRule struct {
	ID          string    `json:"id" db:"id"`
	AppID       string    `json:"app_id" db:"app_id"`
	Name        string    `json:"name" db:"name"`
	MetricName  string    `json:"metric_name" db:"metric_name"`
	Condition   string    `json:"condition" db:"condition"` // gt | lt | eq
	Threshold   float64   `json:"threshold" db:"threshold"`
	WindowMins  int       `json:"window_mins" db:"window_mins"`
	Channels    []string  `json:"channels" db:"channels"` // email | webhook | pagerduty
	WebhookURL  string    `json:"webhook_url" db:"webhook_url"`
	EmailTo     []string  `json:"email_to" db:"email_to"`
	Active      bool      `json:"active" db:"active"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}

// ─── Cohort Models ────────────────────────────────────────────────────────────

type CohortDefinition struct {
	ID          string    `json:"id" db:"id"`
	AppID       string    `json:"app_id" db:"app_id"`
	Name        string    `json:"name" db:"name"`
	Description string    `json:"description,omitempty" db:"description"`
	FilterSQL   string    `json:"filter_sql,omitempty" db:"filter_sql"`
	UserCount   int64     `json:"user_count" db:"user_count"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}

// ─── Experiment Models ────────────────────────────────────────────────────────

type Experiment struct {
	ID          string    `json:"id" db:"id"`
	AppID       string    `json:"app_id" db:"app_id"`
	Name        string    `json:"name" db:"name"`
	Description string    `json:"description,omitempty" db:"description"`
	Status      string    `json:"status" db:"status"` // draft | running | paused | concluded
	GoalEvent   string    `json:"goal_event,omitempty" db:"goal_event"`
	Variants    []byte    `json:"variants,omitempty" db:"variants"` // JSONB [{name, traffic_pct}]
	StartAt     *time.Time `json:"start_at,omitempty" db:"start_at"`
	EndAt       *time.Time `json:"end_at,omitempty" db:"end_at"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}

// ─── Campaign Models ──────────────────────────────────────────────────────────

type Campaign struct {
	ID          string            `json:"id" db:"id"`
	AppID       string            `json:"app_id" db:"app_id"`
	Name        string            `json:"name" db:"name"`
	TriggerType string            `json:"trigger_type" db:"trigger_type"`
	TriggerConf map[string]string `json:"trigger_conf" db:"trigger_conf"`
	Channel     string            `json:"channel" db:"channel"`
	ChannelConf map[string]string `json:"channel_conf" db:"channel_conf"`
	Active      bool              `json:"active" db:"active"`
	CreatedAt   time.Time         `json:"created_at" db:"created_at"`
}

type CampaignStats struct {
	CampaignID string  `json:"campaign_id"`
	Sent       int64   `json:"sent"`
	Delivered  int64   `json:"delivered"`
	Failed     int64   `json:"failed"`
	OpenRate   float64 `json:"open_rate_pct"`
	ClickRate  float64 `json:"click_rate_pct"`
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func NewEventID() string {
	return uuid.New().String()
}

func NowMs() int64 {
	return time.Now().UnixMilli()
}
