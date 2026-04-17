// ============================================================
// PulseAnalytics — MongoDB Index Setup
// Run via: mongosh pulse < migrations/mongo/indexes.js
// ============================================================

db = db.getSiblingDB('pulse');

// ─── raw_events collection ────────────────────────────────────────────────────
db.createCollection('raw_events', {
  validator: {
    $jsonSchema: {
      bsonType: 'object',
      required: ['event_id', 'app_id', 'event_name', 'event_time'],
      properties: {
        event_id:   { bsonType: 'string' },
        app_id:     { bsonType: 'string' },
        event_name: { bsonType: 'string' },
        event_time: { bsonType: 'long' }
      }
    }
  }
});

// Unique index on event_id for deduplication
db.raw_events.createIndex(
  { event_id: 1 },
  { unique: true, name: 'event_id_unique' }
);

// Compound index for app+time range queries
db.raw_events.createIndex(
  { app_id: 1, event_time: -1 },
  { name: 'app_event_time' }
);

// Compound index for app+event_name queries (event explorer)
db.raw_events.createIndex(
  { app_id: 1, event_name: 1, event_time: -1 },
  { name: 'app_event_name_time' }
);

// User-level event lookup
db.raw_events.createIndex(
  { app_id: 1, 'batch.user_id': 1, event_time: -1 },
  { name: 'app_user_time', sparse: true }
);

// TTL index: auto-expire raw events after 90 days
db.raw_events.createIndex(
  { created_at: 1 },
  { expireAfterSeconds: 90 * 24 * 3600, name: 'ttl_90_days' }
);

// ─── user_profiles collection ─────────────────────────────────────────────────
db.createCollection('user_profiles');

// Unique index on app_id + user_id
db.user_profiles.createIndex(
  { app_id: 1, user_id: 1 },
  { unique: true, name: 'app_user_unique' }
);

// Index for property-based segmentation
db.user_profiles.createIndex(
  { app_id: 1, 'properties.country': 1 },
  { name: 'app_country', sparse: true }
);

db.user_profiles.createIndex(
  { app_id: 1, 'properties.plan': 1 },
  { name: 'app_plan', sparse: true }
);

// ─── Atlas Search index (requires Atlas M10+) ─────────────────────────────────
// Enables full-text search across event properties
db.raw_events.createSearchIndex({
  name: 'event_search',
  definition: {
    mappings: {
      dynamic: false,
      fields: {
        app_id:     { type: 'string' },
        event_name: { type: 'string', analyzer: 'lucene.keyword' },
        'props':    { type: 'document', dynamic: true }
      }
    }
  }
});

print('MongoDB indexes created successfully');
