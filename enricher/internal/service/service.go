// Package service implements the enrichment business logic.
// Adds GeoIP, User-Agent parsing, timestamp correction, and identity fields
// to each raw event.
package service

import (
	"time"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/geo"
	"github.com/pulse-analytics/shared/pkg/models"
)

// IngestMessage is the raw Kafka message from the gateway.
type IngestMessage struct {
	Batch    models.EventBatch `json:"batch"`
	ClientIP string            `json:"client_ip"`
	ServerTs int64             `json:"server_ts"`
}

// EnricherService enriches raw events with geo, UA, and timing data.
type EnricherService struct {
	geo *geo.Resolver
	log *zap.Logger
}

// New creates an EnricherService.
func New(geoResolver *geo.Resolver, log *zap.Logger) *EnricherService {
	return &EnricherService{geo: geoResolver, log: log}
}

// Enrich converts a raw IngestMessage into a slice of EnrichedEvents.
func (s *EnricherService) Enrich(msg IngestMessage) []models.EnrichedEvent {
	batch := msg.Batch
	serverTs := msg.ServerTs
	if serverTs == 0 {
		serverTs = time.Now().UnixMilli()
	}

	enriched := make([]models.EnrichedEvent, 0, len(batch.Events))
	for _, evt := range batch.Events {
		e := models.EnrichedEvent{
			EventID:        evt.EventID,
			EventName:      evt.EventName,
			EventTime:      evt.EventTime,
			Props:          evt.Props,
			Device:         evt.Device,
			App:            evt.App,
			Revenue:        evt.Revenue,
			AppID:          batch.AppID,
			UserID:         batch.UserID,
			DeviceID:       batch.DeviceID,
			ServerTime:     serverTs,
			ClientOffsetMs: serverTs - evt.EventTime,
		}

		// GeoIP enrichment
		if s.geo != nil && msg.ClientIP != "" {
			if loc, err := s.geo.Lookup(msg.ClientIP); err == nil {
				e.CountryCode = loc.CountryCode
				e.CountryName = loc.CountryName
				e.Region      = loc.Region
				e.City        = loc.City
				e.Latitude    = loc.Latitude
				e.Longitude   = loc.Longitude
			}
		}

		// UA enrichment
		if evt.Device != nil && evt.Device.UserAgent != "" {
			ua := parseUA(evt.Device.UserAgent)
			e.Browser        = ua.Browser
			e.BrowserVersion = ua.Version
			e.OSFamily        = ua.OS
		}

		enriched = append(enriched, e)
	}
	return enriched
}

type uaInfo struct {
	Browser string
	Version string
	OS      string
}

func parseUA(ua string) uaInfo {
	// Lightweight heuristic UA parser (production would use a proper library)
	info := uaInfo{}
	switch {
	case contains(ua, "Chrome"):
		info.Browser = "Chrome"
	case contains(ua, "Firefox"):
		info.Browser = "Firefox"
	case contains(ua, "Safari"):
		info.Browser = "Safari"
	case contains(ua, "Edge"):
		info.Browser = "Edge"
	}
	switch {
	case contains(ua, "Windows"):
		info.OS = "Windows"
	case contains(ua, "Mac OS"):
		info.OS = "macOS"
	case contains(ua, "Linux"):
		info.OS = "Linux"
	case contains(ua, "Android"):
		info.OS = "Android"
	case contains(ua, "iPhone") || contains(ua, "iPad"):
		info.OS = "iOS"
	}
	return info
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
