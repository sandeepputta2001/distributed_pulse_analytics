// Package service converts enriched/session events to ClickHouse rows.
package service

import (
	"strconv"
	"time"

	"github.com/pulse-analytics/shared/pkg/models"
)

// EnrichedToCHEvent maps an EnrichedEvent to a ClickHouse row.
func EnrichedToCHEvent(e models.EnrichedEvent) models.CHEvent {
	props := make(map[string]string, len(e.Props))
	for k, v := range e.Props {
		props[k] = asString(v)
	}

	platform := ""
	appVersion := ""
	if e.App != nil {
		platform = e.App.Platform
		appVersion = e.App.Version
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
		Props:       props,
		CampaignID:  e.CampaignID,
		InstallSrc:  e.InstallSource,
	}
}

// SessionToCHEvent maps a synthetic SessionEvent to a ClickHouse row.
func SessionToCHEvent(se models.SessionEvent) models.CHEvent {
	return models.CHEvent{
		AppID:      se.AppID,
		EventID:    se.SessionID + "-" + se.Type,
		UserID:     se.UserID,
		DeviceID:   se.DeviceID,
		EventName:  se.Type,
		EventTime:  time.UnixMilli(se.StartTimeMs).UTC(),
		ServerTime: time.UnixMilli(se.StartTimeMs).UTC(),
		SessionID:  se.SessionID,
		Props: map[string]string{
			"session_duration_s": strconv.FormatInt(se.DurationS, 10),
			"event_count":        strconv.Itoa(se.EventCount),
			"entry_screen":       se.EntryScreen,
			"exit_screen":        se.ExitScreen,
			"exit_reason":        se.ExitReason,
		},
	}
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return ""
	}
}
