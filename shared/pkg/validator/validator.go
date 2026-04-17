// Package validator provides request validation helpers for HTTP handlers.
package validator

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var appIDRegexp = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,64}$`)

// ValidationError aggregates one or more field-level validation failures.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	msgs := make([]string, 0, len(e.Fields))
	for field, msg := range e.Fields {
		msgs = append(msgs, fmt.Sprintf("%s: %s", field, msg))
	}
	return strings.Join(msgs, "; ")
}

// validator accumulates field errors.
type validator struct {
	errs map[string]string
}

func newValidator() *validator {
	return &validator{errs: make(map[string]string)}
}

func (v *validator) required(field, value string) {
	if strings.TrimSpace(value) == "" {
		v.errs[field] = "required"
	}
}

func (v *validator) appID(value string) {
	if !appIDRegexp.MatchString(value) {
		v.errs["app_id"] = "must be 3–64 alphanumeric, underscore or hyphen characters"
	}
}

func (v *validator) timeRange(fromMs, toMs int64) {
	if fromMs > 0 && toMs > 0 && fromMs >= toMs {
		v.errs["time_range"] = "from_ms must be before to_ms"
	}
	maxFuture := time.Now().Add(5 * time.Minute).UnixMilli()
	if toMs > maxFuture {
		v.errs["to_ms"] = "to_ms cannot be more than 5 minutes in the future"
	}
}

func (v *validator) granularity(value string) {
	allowed := map[string]bool{"minute": true, "hour": true, "day": true, "week": true, "month": true}
	if value != "" && !allowed[value] {
		v.errs["granularity"] = "must be one of: minute, hour, day, week, month"
	}
}

func (v *validator) minLen(field string, slice []string, min int) {
	if len(slice) < min {
		v.errs[field] = fmt.Sprintf("must have at least %d item(s)", min)
	}
}

func (v *validator) maxLen(field string, slice []string, max int) {
	if len(slice) > max {
		v.errs[field] = fmt.Sprintf("must have at most %d item(s)", max)
	}
}

func (v *validator) err() error {
	if len(v.errs) == 0 {
		return nil
	}
	return &ValidationError{Fields: v.errs}
}

// ─── Public validation functions ─────────────────────────────────────────────

// EventCountParams validates parameters for the event count endpoint.
type EventCountParams struct {
	AppID       string
	FromMs      int64
	ToMs        int64
	Granularity string
}

func ValidateEventCount(p EventCountParams) error {
	v := newValidator()
	v.required("app_id", p.AppID)
	if p.AppID != "" {
		v.appID(p.AppID)
	}
	v.timeRange(p.FromMs, p.ToMs)
	v.granularity(p.Granularity)
	return v.err()
}

// FunnelParams validates parameters for the funnel query endpoint.
type FunnelParams struct {
	AppID         string
	Steps         []string
	WindowSeconds int64
	FromMs        int64
	ToMs          int64
}

func ValidateFunnel(p FunnelParams) error {
	v := newValidator()
	v.required("app_id", p.AppID)
	if p.AppID != "" {
		v.appID(p.AppID)
	}
	v.minLen("steps", p.Steps, 2)
	v.maxLen("steps", p.Steps, 10)
	if p.WindowSeconds < 0 {
		v.errs["window_seconds"] = "must be non-negative"
	}
	v.timeRange(p.FromMs, p.ToMs)
	return v.err()
}

// RetentionParams validates parameters for the retention endpoint.
type RetentionParams struct {
	AppID  string
	DayNs  []int32
	FromMs int64
	ToMs   int64
}

func ValidateRetention(p RetentionParams) error {
	v := newValidator()
	v.required("app_id", p.AppID)
	if p.AppID != "" {
		v.appID(p.AppID)
	}
	for _, d := range p.DayNs {
		if d < 1 || d > 365 {
			v.errs["day_ns"] = "each value must be between 1 and 365"
			break
		}
	}
	v.timeRange(p.FromMs, p.ToMs)
	return v.err()
}

// IsValidationError returns true if err is a *ValidationError.
func IsValidationError(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}
