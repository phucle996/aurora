-- Zone-local metering journal. This database is diagnostic and aggregation
-- input owned by one Zone; it is not a billing ledger and is never queried by
-- Central Cost Engine directly.
CREATE DATABASE IF NOT EXISTS otlp;

CREATE TABLE IF NOT EXISTS otlp.otel_logs (
    Timestamp DateTime64(9, 'UTC') CODEC(DoubleDelta, ZSTD),
    TimestampTime DateTime DEFAULT toDateTime(Timestamp),
    TraceId String CODEC(ZSTD),
    SpanId String CODEC(ZSTD),
    TraceFlags UInt8 CODEC(ZSTD),
    SeverityText LowCardinality(String) CODEC(ZSTD),
    SeverityNumber Int32 CODEC(ZSTD),
    ServiceName String CODEC(ZSTD),
    Body String CODEC(ZSTD),
    ResourceAttributes Map(String, String) CODEC(ZSTD),
    LogAttributes Map(String, String) CODEC(ZSTD),
    INDEX idx_trace_id TraceId TYPE bloom_filter(0.001) GRANULARITY 1,
    INDEX idx_log_attr_key mapKeys(LogAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_log_attr_value mapValues(LogAttributes) TYPE bloom_filter(0.01) GRANULARITY 1
) ENGINE = MergeTree()
PARTITION BY toYYYYMM(TimestampTime)
ORDER BY (ServiceName, TimestampTime, Timestamp)
TTL TimestampTime + INTERVAL 7 DAY DELETE;

CREATE DATABASE IF NOT EXISTS storage;

-- event_id is the generic metering identity. request_id remains as a storage
-- projection alias during migration so the publisher's deduplication key and
-- existing retained rows stay compatible. Do not attach a SummingMergeTree
-- directly to at-least-once OTel logs.
CREATE TABLE IF NOT EXISTS storage.access_event_journal (
    timestamp DateTime64(3, 'UTC'),
    zone_id UUID,
    event_id String,
    request_id String,
    module LowCardinality(String),
    metering_schema LowCardinality(String),
    resource_id UUID,
    method LowCardinality(String),
    status UInt16,
    bytes_received UInt64,
    bytes_sent UInt64,
    duration_ms UInt32
) ENGINE = ReplacingMergeTree()
PARTITION BY toYYYYMM(timestamp)
ORDER BY (resource_id, request_id)
TTL toDateTime(timestamp) + INTERVAL 30 DAY DELETE;

-- Hourly capacity observations written by the sharded Zone Control scanner.
-- This is a local metering journal, not a billing ledger.  The billing report
-- publisher only consumes rows whose complete shard generation has a matching
-- completion marker below.
CREATE TABLE IF NOT EXISTS storage.bucket_capacity_journal (
    observed_at DateTime64(3, 'UTC'),
    billing_window_end DateTime64(3, 'UTC'),
    zone_id UUID,
    bucket_name String,
    used_bytes UInt64,
    scan_generation String,
    shard_id UInt16
) ENGINE = ReplacingMergeTree()
PARTITION BY toYYYYMM(observed_at)
ORDER BY (zone_id, billing_window_end, bucket_name, observed_at, scan_generation)
TTL toDateTime(observed_at) + INTERVAL 90 DAY DELETE;

CREATE TABLE IF NOT EXISTS storage.bucket_capacity_scan_completions (
    completed_at DateTime64(3, 'UTC'),
    billing_window_end DateTime64(3, 'UTC'),
    zone_id UUID,
    scan_generation String,
    shard_id UInt16
) ENGINE = ReplacingMergeTree()
PARTITION BY toYYYYMM(completed_at)
ORDER BY (zone_id, billing_window_end, shard_id, completed_at, scan_generation)
TTL toDateTime(completed_at) + INTERVAL 90 DAY DELETE;

-- Keep a manually re-runnable migration path for an existing Zone volume. The
-- Docker init hook runs only on a fresh volume, so operators must still apply
-- this file during an in-place upgrade before restarting the collector.
ALTER TABLE storage.access_event_journal
    ADD COLUMN IF NOT EXISTS event_id String AFTER zone_id,
    ADD COLUMN IF NOT EXISTS module LowCardinality(String) AFTER request_id,
    ADD COLUMN IF NOT EXISTS metering_schema LowCardinality(String) AFTER module;

DROP VIEW IF EXISTS storage.mv_otel_to_access_event_journal;
CREATE MATERIALIZED VIEW storage.mv_otel_to_access_event_journal
TO storage.access_event_journal AS
SELECT
    Timestamp AS timestamp,
    toUUIDOrZero(LogAttributes['zone_id']) AS zone_id,
    if(LogAttributes['event_id'] != '', LogAttributes['event_id'], LogAttributes['request_id']) AS event_id,
    if(LogAttributes['event_id'] != '', LogAttributes['event_id'], LogAttributes['request_id']) AS request_id,
    if(LogAttributes['module'] != '', LogAttributes['module'], 'storage') AS module,
    if(LogAttributes['metering_schema'] != '', LogAttributes['metering_schema'], 'storage.access.completed.v1') AS metering_schema,
    toUUIDOrZero(LogAttributes['resource_id']) AS resource_id,
    LogAttributes['method'] AS method,
    toUInt16OrZero(LogAttributes['status']) AS status,
    toUInt64OrZero(LogAttributes['bytes_received']) AS bytes_received,
    toUInt64OrZero(LogAttributes['bytes_sent']) AS bytes_sent,
    toUInt32OrZero(LogAttributes['duration_ms']) AS duration_ms
FROM otlp.otel_logs
WHERE (
        (
          LogAttributes['log_type'] = 'metering'
          AND LogAttributes['module'] = 'storage'
          AND LogAttributes['metering_schema'] = 'storage.access.completed.v1'
        )
        OR LogAttributes['log_type'] = 'zone-storage-access'
      )
  AND LogAttributes['zone_id'] != ''
  AND (LogAttributes['event_id'] != '' OR LogAttributes['request_id'] != '')
  AND LogAttributes['resource_id'] != '';
