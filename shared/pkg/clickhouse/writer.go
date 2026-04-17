package clickhouse

import (
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/metrics"
	"github.com/pulse-analytics/shared/pkg/models"
)

const insertEventsQuery = `
INSERT INTO events (
	app_id, event_id, user_id, device_id, event_name,
	event_time, server_time, session_id,
	country_code, platform, app_version, os_family, browser,
	city, revenue, props, campaign_id, install_source
)`

// Writer performs bulk inserts into ClickHouse with adaptive backpressure.
//
// # Adaptive Backpressure
//
// The writer tracks the number of events currently buffered. When the buffer
// exceeds softLimit (80 % of bufSize), it signals the producer to slow down
// by returning ErrBackpressure from Write(). This prevents unbounded memory
// growth and gives ClickHouse time to absorb the load.
//
// At hardLimit (100 % of bufSize), Write() is fully blocked until the buffer
// drains below softLimit.
//
// # Write-Behind Pattern
//
// Events are never written synchronously to ClickHouse from the hot ingest
// path. Instead they are appended to an in-memory channel, and a single
// background goroutine flushes them in large batches. This decouples ingest
// latency from ClickHouse insert latency.
type Writer struct {
	client  *Client
	log     *zap.Logger
	m       *metrics.Registry
	ch      chan models.CHEvent // write-behind channel
	ticker  *time.Ticker
	done    chan struct{}
	bufSize int

	// pending is the number of events in ch (used for backpressure gauge).
	pending atomic.Int64

	// Stats
	droppedTotal atomic.Int64
}

// NewWriter creates a ClickHouse batch writer.
//
//	flushInterval — max time before flushing buffered rows.
//	bufSize       — channel capacity; flush when this many events are pending.
func NewWriter(client *Client, log *zap.Logger, m *metrics.Registry, flushInterval time.Duration, bufSize int) *Writer {
	w := &Writer{
		client:  client,
		log:     log,
		m:       m,
		ch:      make(chan models.CHEvent, bufSize),
		ticker:  time.NewTicker(flushInterval),
		done:    make(chan struct{}),
		bufSize: bufSize,
	}
	go w.loop()
	return w
}

// ErrBackpressure is returned by Write when the buffer is at soft-limit capacity.
var ErrBackpressure = fmt.Errorf("clickhouse writer: backpressure — buffer at soft limit")

// Write adds events to the write-behind buffer.
//
// Backpressure:
//   - Buffer < 80 %  → accepted, returns nil
//   - Buffer 80–99 % → accepted, returns ErrBackpressure (caller should slow down)
//   - Buffer = 100 % → event is dropped, DroppedTotal() is incremented
func (w *Writer) Write(events []models.CHEvent) error {
	softLimit := int(float64(w.bufSize) * 0.8)
	pending := int(w.pending.Load())

	var firstErr error
	for _, e := range events {
		select {
		case w.ch <- e:
			w.pending.Add(1)
		default:
			// Hard limit — drop the event rather than block the ingest path.
			w.droppedTotal.Add(1)
			w.m.CHInsertErrors.Add(1)
			continue
		}
	}

	if pending >= softLimit && firstErr == nil {
		firstErr = ErrBackpressure
	}
	return firstErr
}

// DroppedTotal returns the number of events dropped due to buffer overflow.
func (w *Writer) DroppedTotal() int64 { return w.droppedTotal.Load() }

// PendingCount returns the current number of buffered events.
func (w *Writer) PendingCount() int64 { return w.pending.Load() }

func (w *Writer) loop() {
	defer close(w.done)

	buf := make([]models.CHEvent, 0, w.bufSize)

	for {
		select {
		case e, ok := <-w.ch:
			if !ok {
				// Channel closed — drain and flush remainder.
				if len(buf) > 0 {
					w.doFlush(buf)
				}
				return
			}
			w.pending.Add(-1)
			buf = append(buf, e)

			// Drain all immediately available events in one batch (up to bufSize).
		drain:
			for len(buf) < w.bufSize {
				select {
				case e2, ok2 := <-w.ch:
					if !ok2 {
						break drain
					}
					w.pending.Add(-1)
					buf = append(buf, e2)
				default:
					break drain
				}
			}

			if len(buf) >= w.bufSize {
				w.doFlush(buf)
				buf = buf[:0]
			}

		case <-w.ticker.C:
			if len(buf) > 0 {
				w.doFlush(buf)
				buf = buf[:0]
			}
		}
	}
}

func (w *Writer) doFlush(batch []models.CHEvent) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := w.insertBatch(ctx, batch); err != nil {
		w.log.Error("clickhouse insert failed",
			zap.Int("count", len(batch)),
			zap.Error(err),
		)
		w.m.CHInsertErrors.Add(float64(len(batch)))
		return
	}

	elapsed := time.Since(start)
	w.log.Debug("clickhouse flush",
		zap.Int("rows", len(batch)),
		zap.Duration("elapsed", elapsed),
	)
	w.m.CHInserted.Add(float64(len(batch)))
	w.m.CHInsertLatency.Observe(elapsed.Seconds())
}

func (w *Writer) insertBatch(ctx context.Context, events []models.CHEvent) error {
	batch, err := w.client.PrepareBatch(ctx, insertEventsQuery)
	if err != nil {
		return fmt.Errorf("prepare batch: %w", err)
	}

	for _, e := range events {
		props := make(map[string]string, len(e.Props))
		for k, v := range e.Props {
			props[k] = v
		}

		if err := batch.Append(
			e.AppID,
			e.EventID,
			e.UserID,
			e.DeviceID,
			e.EventName,
			e.EventTime,
			e.ServerTime,
			e.SessionID,
			e.CountryCode,
			e.Platform,
			e.AppVersion,
			e.OSFamily,
			e.Browser,
			e.City,
			e.Revenue,
			props,
			e.CampaignID,
			e.InstallSrc,
		); err != nil {
			return fmt.Errorf("batch append: %w", err)
		}
	}

	return batch.Send()
}

// Stop gracefully flushes pending rows and stops the writer.
func (w *Writer) Stop() {
	w.ticker.Stop()
	close(w.ch)
	// Wait for the loop to drain and flush.
	<-w.done
}

// ─── Sharded Writer ───────────────────────────────────────────────────────────

// ShardedWriter multiplexes writes across one Writer per ClickHouse shard.
//
// Each incoming batch is split by app_id: events for the same app always go
// to the same shard (FNV-1a(app_id) % numShards), preserving per-app ordering
// within ClickHouse while distributing load across the shard pool.
//
// When only one shard is configured, ShardedWriter is a zero-overhead pass-
// through to that single Writer.
type ShardedWriter struct {
	writers []*Writer
}

// NewShardedWriter creates one Writer per shard in pool.
// bufSizePerShard is the write-behind channel capacity for each shard writer.
func NewShardedWriter(pool *Pool, log *zap.Logger, m *metrics.Registry, flushInterval time.Duration, bufSizePerShard int) *ShardedWriter {
	writers := make([]*Writer, len(pool.shards))
	for i, shard := range pool.shards {
		writers[i] = NewWriter(shard, log, m, flushInterval, bufSizePerShard)
	}
	return &ShardedWriter{writers: writers}
}

// Write routes each event to its target shard Writer.
func (sw *ShardedWriter) Write(events []models.CHEvent) error {
	if len(sw.writers) == 1 {
		return sw.writers[0].Write(events)
	}

	// Group events by shard index.
	n := uint64(len(sw.writers))
	buckets := make(map[uint64][]models.CHEvent, n)
	for _, e := range events {
		h := fnv.New64a()
		_, _ = io.WriteString(h, e.AppID)
		idx := h.Sum64() % n
		buckets[idx] = append(buckets[idx], e)
	}

	var firstErr error
	for idx, batch := range buckets {
		if err := sw.writers[idx].Write(batch); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// DroppedTotal returns the sum of dropped events across all shard writers.
func (sw *ShardedWriter) DroppedTotal() int64 {
	var total int64
	for _, w := range sw.writers {
		total += w.DroppedTotal()
	}
	return total
}

// Stop gracefully stops all shard writers (drains and flushes each).
func (sw *ShardedWriter) Stop() {
	for _, w := range sw.writers {
		w.Stop()
	}
}
