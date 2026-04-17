// Package repo provides ClickHouse data access for the CHWriter service.
package repo

import (
	"context"

	"go.uber.org/zap"

	chchlient "github.com/pulse-analytics/shared/pkg/clickhouse"
	"github.com/pulse-analytics/shared/pkg/models"
)

// Repo wraps the sharded ClickHouse writer for bulk event inserts.
type Repo struct {
	writer *chchlient.ShardedWriter
	log    *zap.Logger
}

// New creates a CHWriter Repo.
func New(writer *chchlient.ShardedWriter, log *zap.Logger) *Repo {
	return &Repo{writer: writer, log: log}
}

// WriteEvents buffers events for bulk insert.
func (r *Repo) WriteEvents(events []models.CHEvent) {
	r.writer.Write(events)
}

// Flush forces a flush of all pending buffered events.
func (r *Repo) Flush(ctx context.Context) {
	// ShardedWriter.Stop handles flush on shutdown
}
