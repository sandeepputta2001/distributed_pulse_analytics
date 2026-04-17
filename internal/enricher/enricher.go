package enricher

import (
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/pulse-analytics/internal/geo"
	"github.com/pulse-analytics/internal/models"
)

// Service enriches raw events with GeoIP, UA parsing,
// server-time correction, and attribution data.
type Service struct {
	geo *geo.Resolver
	log *zap.Logger
}

// IngestMessage is what the gateway publishes to raw-events.
type IngestMessage struct {
	Batch    models.EventBatch `json:"batch"`
	ClientIP string            `json:"client_ip"`
	ServerTS int64             `json:"server_ts"` // server epoch ms
}

// NewService creates an enrichment service.
func NewService(geoResolver *geo.Resolver, log *zap.Logger) *Service {
	return &Service{geo: geoResolver, log: log}
}

// Enrich converts a raw ingest message into enriched events.
func (s *Service) Enrich(msg IngestMessage) []models.EnrichedEvent {
	batch := msg.Batch
	clientIP := msg.ClientIP
	serverTS := msg.ServerTS

	// GeoIP lookup (in-memory MaxMind DB, ~1µs)
	var loc *geo.Location
	if s.geo != nil {
		loc, _ = s.geo.Lookup(clientIP)
	}
	if loc == nil {
		loc = &geo.Location{}
	}

	// UA parsing from device context
	browser, browserVersion, osFamily := parseUA(batch)

	enriched := make([]models.EnrichedEvent, 0, len(batch.Events))
	for _, evt := range batch.Events {
		// Server-side timestamp correction:
		// client_offset = server_time - client_time
		// If client clock drifts, downstream uses server_time for aggregations
		clientOffsetMs := serverTS - evt.EventTime

		e := models.EnrichedEvent{
			// Original event fields
			EventID:   evt.EventID,
			EventName: evt.EventName,
			EventTime: evt.EventTime,
			Props:     evt.Props,
			Device:    evt.Device,
			App:       evt.App,
			Revenue:   evt.Revenue,

			// Identity
			AppID:    batch.AppID,
			UserID:   batch.UserID,
			DeviceID: batch.DeviceID,

			// Server-side
			ServerTime:     serverTS,
			ClientOffsetMs: clientOffsetMs,

			// GeoIP enrichment
			CountryCode: loc.CountryCode,
			CountryName: loc.CountryName,
			Region:      loc.Region,
			City:        loc.City,
			Latitude:    loc.Latitude,
			Longitude:   loc.Longitude,

			// UA enrichment
			Browser:        browser,
			BrowserVersion: browserVersion,
			OSFamily:       osFamily,
		}

		// Extract attribution attributes from event properties
		if props := evt.Props; props != nil {
			if v, ok := props["install_source"].(string); ok {
				e.InstallSource = v
			}
			if v, ok := props["campaign_id"].(string); ok {
				e.CampaignID = v
			}
			if v, ok := props["adset_id"].(string); ok {
				e.AdsetID = v
			}
		}

		enriched = append(enriched, e)
	}

	return enriched
}

// ToCHEvent converts an EnrichedEvent to a flat ClickHouse row.
func ToCHEvent(e models.EnrichedEvent) models.CHEvent {
	platform := "unknown"
	appVersion := ""
	if e.App != nil {
		platform = normalizePlatform(e.App.Platform)
		appVersion = e.App.Version
	}

	// Flatten props to map[string]string for ClickHouse Map(String,String)
	flatProps := make(map[string]string, len(e.Props))
	for k, v := range e.Props {
		flatProps[k] = flattenValue(v)
	}

	return models.CHEvent{
		AppID:       e.AppID,
		EventID:     e.EventID,
		UserID:      e.UserID,
		DeviceID:    e.DeviceID,
		EventName:   e.EventName,
		EventTime:   time.UnixMilli(e.EventTime).UTC(),
		ServerTime:  time.UnixMilli(e.ServerTime).UTC(),
		SessionID:   e.SessionID,
		CountryCode: e.CountryCode,
		Platform:    platform,
		AppVersion:  appVersion,
		OSFamily:    e.OSFamily,
		Browser:     e.Browser,
		City:        e.City,
		Revenue:     e.Revenue,
		Props:       flatProps,
		CampaignID:  e.CampaignID,
		InstallSrc:  e.InstallSource,
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// parseUA extracts browser/OS info from the User-Agent string in device context.
// We do simple string matching here; swap with ua-parser library if desired.
func parseUA(batch models.EventBatch) (browser, browserVersion, osFamily string) {
	if len(batch.Events) == 0 || batch.Events[0].Device == nil {
		return
	}
	ua := batch.Events[0].Device.UserAgent
	if ua == "" {
		return
	}

	ua = strings.ToLower(ua)
	switch {
	case strings.Contains(ua, "chrome"):
		browser = "Chrome"
	case strings.Contains(ua, "firefox"):
		browser = "Firefox"
	case strings.Contains(ua, "safari"):
		browser = "Safari"
	case strings.Contains(ua, "edge"):
		browser = "Edge"
	default:
		browser = "Other"
	}

	switch {
	case strings.Contains(ua, "android"):
		osFamily = "Android"
	case strings.Contains(ua, "iphone") || strings.Contains(ua, "ipad"):
		osFamily = "iOS"
	case strings.Contains(ua, "windows"):
		osFamily = "Windows"
	case strings.Contains(ua, "mac"):
		osFamily = "macOS"
	case strings.Contains(ua, "linux"):
		osFamily = "Linux"
	}

	return browser, browserVersion, osFamily
}

func normalizePlatform(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "android":
		return "android"
	case "ios", "iphone", "ipad":
		return "ios"
	case "web", "browser":
		return "web"
	case "react_native", "reactnative":
		return "react_native"
	case "flutter":
		return "flutter"
	case "server", "backend":
		return "server"
	default:
		return "unknown"
	}
}

func flattenValue(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return fmt.Sprintf("%g", val)
	case int:
		return fmt.Sprintf("%d", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", val)
	}
}
