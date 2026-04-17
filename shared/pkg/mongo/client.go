package mongo

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/config"
	"github.com/pulse-analytics/shared/pkg/models"
)

const (
	colRawEvents    = "raw_events"
	colUserProfiles = "user_profiles"
)

// Client wraps MongoDB driver with primary/replica read splitting.
//
// # Read/Write Splitting
//
// Two database handles are maintained over the same underlying connection:
//   - db     — uses the default read preference (primary).  All writes go here.
//   - readDB — uses the configured read preference (default: secondaryPreferred).
//     Read-only methods (GetRawEvents, GetUserProfile) use this handle so that
//     replica set secondaries absorb read traffic.
//
// When the deployment is a standalone node both handles point to primary and
// the driver ignores the secondary preference.
type Client struct {
	client *mongo.Client
	db     *mongo.Database // primary — writes
	readDB *mongo.Database // secondary preferred — reads
	log    *zap.Logger
}

// NewClient creates a MongoDB client with read/write splitting.
func NewClient(cfg *config.MongoConfig, log *zap.Logger) (*Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	rp, err := parseReadPreference(cfg.ReadPreference)
	if err != nil {
		return nil, fmt.Errorf("mongo read preference: %w", err)
	}

	serverAPI := options.ServerAPI(options.ServerAPIVersion1)
	clientOpts := options.Client().
		ApplyURI(cfg.URI).
		SetServerAPIOptions(serverAPI).
		SetConnectTimeout(cfg.Timeout).
		SetTimeout(cfg.Timeout).
		SetMaxPoolSize(100).
		SetMinPoolSize(5)

	if cfg.ReplicaSet != "" {
		clientOpts.SetReplicaSet(cfg.ReplicaSet)
	}

	mc, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}

	if err := mc.Ping(ctx, readpref.Primary()); err != nil {
		return nil, fmt.Errorf("mongo ping: %w", err)
	}

	db := mc.Database(cfg.Database)
	readDB := mc.Database(cfg.Database, options.Database().SetReadPreference(rp))

	log.Info("mongo connected",
		zap.String("db", cfg.Database),
		zap.String("read_preference", cfg.ReadPreference),
	)
	return &Client{client: mc, db: db, readDB: readDB, log: log}, nil
}

// parseReadPreference converts a config string to a mongo readpref.ReadPref.
func parseReadPreference(pref string) (*readpref.ReadPref, error) {
	switch pref {
	case "", "primary":
		return readpref.Primary(), nil
	case "primaryPreferred":
		return readpref.PrimaryPreferred(), nil
	case "secondary":
		return readpref.Secondary(), nil
	case "secondaryPreferred":
		return readpref.SecondaryPreferred(), nil
	case "nearest":
		return readpref.Nearest(), nil
	default:
		return nil, fmt.Errorf("unknown read preference %q", pref)
	}
}

// ─── Raw Event Storage ────────────────────────────────────────────────────────

// InsertRawBatch stores raw events in MongoDB — primary.
func (c *Client) InsertRawBatch(ctx context.Context, appID string, events []models.Event) error {
	if len(events) == 0 {
		return nil
	}

	col := c.db.Collection(colRawEvents)
	docs := make([]any, len(events))
	for i, e := range events {
		docs[i] = bson.M{
			"event_id":   e.EventID,
			"app_id":     appID,
			"event_name": e.EventName,
			"event_time": e.EventTime,
			"props":      e.Props,
			"device":     e.Device,
			"app":        e.App,
			"revenue":    e.Revenue,
			"created_at": time.Now(),
		}
	}

	opts := options.InsertMany().SetOrdered(false) // continue on error
	_, err := col.InsertMany(ctx, docs, opts)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil // tolerate duplicate event_ids
		}
		return fmt.Errorf("insert raw batch: %w", err)
	}
	return nil
}

// GetRawEvents fetches raw events for an app within a time range — read replica.
func (c *Client) GetRawEvents(ctx context.Context, appID string, fromMs, toMs int64, limit int) ([]bson.M, error) {
	col := c.readDB.Collection(colRawEvents)
	filter := bson.M{
		"app_id": appID,
		"event_time": bson.M{
			"$gte": fromMs,
			"$lte": toMs,
		},
	}
	opts := options.Find().
		SetLimit(int64(limit)).
		SetSort(bson.D{{Key: "event_time", Value: -1}})

	cursor, err := col.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var results []bson.M
	return results, cursor.All(ctx, &results)
}

// ─── User Profiles ────────────────────────────────────────────────────────────

// UpsertUserProfile upserts a user's property map — primary.
func (c *Client) UpsertUserProfile(ctx context.Context, appID, userID string, props map[string]any) error {
	col := c.db.Collection(colUserProfiles)
	filter := bson.M{"app_id": appID, "user_id": userID}

	update := bson.M{
		"$set":         props,
		"$setOnInsert": bson.M{"created_at": time.Now()},
		"$currentDate": bson.M{"updated_at": true},
	}

	opts := options.Update().SetUpsert(true)
	_, err := col.UpdateOne(ctx, filter, update, opts)
	return err
}

// GetUserProfile fetches a user's profile — read replica.
func (c *Client) GetUserProfile(ctx context.Context, appID, userID string) (bson.M, error) {
	col := c.readDB.Collection(colUserProfiles)
	filter := bson.M{"app_id": appID, "user_id": userID}

	var result bson.M
	if err := col.FindOne(ctx, filter).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

// ─── Index Setup ──────────────────────────────────────────────────────────────

// EnsureIndexes creates required indexes.
func (c *Client) EnsureIndexes(ctx context.Context) error {
	rawCol := c.db.Collection(colRawEvents)

	indexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "event_id", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("event_id_unique"),
		},
		{
			Keys:    bson.D{{Key: "app_id", Value: 1}, {Key: "event_time", Value: -1}},
			Options: options.Index().SetName("app_event_time"),
		},
		{
			Keys:    bson.D{{Key: "app_id", Value: 1}, {Key: "event_name", Value: 1}, {Key: "event_time", Value: -1}},
			Options: options.Index().SetName("app_event_name_time"),
		},
		{
			// TTL index: expire raw events after 90 days
			Keys:    bson.D{{Key: "created_at", Value: 1}},
			Options: options.Index().SetExpireAfterSeconds(90 * 24 * 3600).SetName("ttl_90d"),
		},
	}

	if _, err := rawCol.Indexes().CreateMany(ctx, indexes); err != nil {
		return fmt.Errorf("create raw_events indexes: %w", err)
	}

	userCol := c.db.Collection(colUserProfiles)
	userIndexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "app_id", Value: 1}, {Key: "user_id", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("app_user_unique"),
		},
	}
	if _, err := userCol.Indexes().CreateMany(ctx, userIndexes); err != nil {
		return fmt.Errorf("create user_profiles indexes: %w", err)
	}

	return nil
}

// InsertOne inserts a single document into the named collection — primary.
func (c *Client) InsertOne(ctx context.Context, collection string, doc any) error {
	_, err := c.db.Collection(collection).InsertOne(ctx, doc)
	return err
}

// Ping checks connectivity.
func (c *Client) Ping(ctx context.Context) error {
	return c.client.Ping(ctx, nil)
}

// Close closes the MongoDB connection.
func (c *Client) Close(ctx context.Context) error {
	return c.client.Disconnect(ctx)
}
