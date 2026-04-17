// Package pulse provides a Go client SDK for the PulseAnalytics ingest gateway.
//
// Usage:
//
//	client := pulse.New("https://gateway.pulse-analytics.io", "your-api-key")
//	client.Track(ctx, pulse.Event{EventName: "purchase_completed", Props: map[string]any{"price": 29.99}})
package pulse

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Client is a thread-safe PulseAnalytics ingest client.
type Client struct {
	baseURL    string
	apiKey     string
	appID      string
	deviceID   string
	sdkVersion string
	httpClient *http.Client

	// Batching
	mu            sync.Mutex
	queue         []Event
	maxBatch      int
	flushInterval time.Duration
	stopCh        chan struct{}
	wg            sync.WaitGroup
}

// Event represents a single analytics event.
type Event struct {
	EventID   string         `json:"event_id,omitempty"`
	EventName string         `json:"event_name"`
	EventTime int64          `json:"event_time,omitempty"` // epoch ms; defaults to now
	Props     map[string]any `json:"props,omitempty"`
	Revenue   float64        `json:"revenue,omitempty"`
}

// IdentifyPayload is used for the /v1/identify endpoint.
type IdentifyPayload struct {
	UserID string         `json:"user_id"`
	Traits map[string]any `json:"traits,omitempty"`
}

// Option configures the Client.
type Option func(*Client)

// WithHTTPClient sets a custom http.Client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

// WithMaxBatch sets the maximum number of events per batch (max 500, default 100).
func WithMaxBatch(n int) Option {
	return func(c *Client) {
		if n > 500 {
			n = 500
		}
		c.maxBatch = n
	}
}

// WithFlushInterval sets how often buffered events are flushed (default 2s).
func WithFlushInterval(d time.Duration) Option {
	return func(c *Client) { c.flushInterval = d }
}

// WithSDKVersion overrides the reported sdk_version string.
func WithSDKVersion(v string) Option {
	return func(c *Client) { c.sdkVersion = v }
}

// New creates and starts a PulseAnalytics client.
func New(baseURL, apiKey, appID, deviceID string, opts ...Option) *Client {
	c := &Client{
		baseURL:       baseURL,
		apiKey:        apiKey,
		appID:         appID,
		deviceID:      deviceID,
		sdkVersion:    "1.0.0",
		httpClient:    &http.Client{Timeout: 10 * time.Second},
		maxBatch:      100,
		flushInterval: 2 * time.Second,
		stopCh:        make(chan struct{}),
	}
	for _, o := range opts {
		o(c)
	}
	c.wg.Add(1)
	go c.flushLoop()
	return c
}

// Track enqueues a single event. It is flushed automatically in the background.
// Call Flush or Close to ensure delivery before process exit.
func (c *Client) Track(ctx context.Context, e Event) {
	if e.EventTime == 0 {
		e.EventTime = time.Now().UnixMilli()
	}
	c.mu.Lock()
	c.queue = append(c.queue, e)
	full := len(c.queue) >= c.maxBatch
	c.mu.Unlock()

	if full {
		c.Flush(ctx)
	}
}

// TrackNow sends a single event synchronously (bypasses the batch queue).
func (c *Client) TrackNow(ctx context.Context, e Event) error {
	if e.EventTime == 0 {
		e.EventTime = time.Now().UnixMilli()
	}
	return c.sendBatch(ctx, []Event{e})
}

// Identify updates user profile traits.
func (c *Client) Identify(ctx context.Context, userID string, traits map[string]any) error {
	payload := IdentifyPayload{UserID: userID, Traits: traits}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/identify", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("identify: HTTP %d", resp.StatusCode)
	}
	return nil
}

// Flush sends all buffered events immediately.
func (c *Client) Flush(ctx context.Context) {
	c.mu.Lock()
	if len(c.queue) == 0 {
		c.mu.Unlock()
		return
	}
	batch := c.queue
	c.queue = nil
	c.mu.Unlock()

	_ = c.sendBatch(ctx, batch)
}

// Close flushes remaining events and stops the background goroutine.
func (c *Client) Close(ctx context.Context) {
	close(c.stopCh)
	c.wg.Wait()
	c.Flush(ctx)
}

func (c *Client) flushLoop() {
	defer c.wg.Done()
	ticker := time.NewTicker(c.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.Flush(context.Background())
		}
	}
}

func (c *Client) sendBatch(ctx context.Context, events []Event) error {
	payload := map[string]any{
		"app_id":      c.appID,
		"device_id":   c.deviceID,
		"sdk_version": c.sdkVersion,
		"sent_at_ms":  time.Now().UnixMilli(),
		"events":      events,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	// gzip compress
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err = gz.Write(data); err != nil {
		return err
	}
	if err = gz.Close(); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/events", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("ingest: HTTP %d", resp.StatusCode)
	}
	return nil
}
