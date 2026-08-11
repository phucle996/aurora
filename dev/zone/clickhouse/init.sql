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

-- request_id is retained for the report publisher's explicit deduplication
-- query. Do not attach a SummingMergeTree directly to at-least-once OTel logs.
CREATE TABLE IF NOT EXISTS storage.access_event_journal (
    timestamp DateTime64(3, 'UTC'),
    zone_id UUID,
    request_id String,
    resource_id UUID,
    method LowCardinality(String),
    status UInt16,
    bytes_received UInt64,
    bytes_sent UInt64,
    duration_ms UInt32
) ENGINE = ReplacingMergeTree()
PARTITION BY toYYYYMM(timestamp)
ORDER BY (resource_id, request_id)
TTL timestamp + INTERVAL 30 DAY DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS storage.mv_otel_to_access_event_journal
TO storage.access_event_journal AS
SELECT
    Timestamp AS timestamp,
    toUUIDOrZero(LogAttributes['zone_id']) AS zone_id,
    LogAttributes['request_id'] AS request_id,
    toUUIDOrZero(LogAttributes['resource_id']) AS resource_id,
    LogAttributes['method'] AS method,
    toUInt16OrZero(LogAttributes['status']) AS status,
    toUInt64OrZero(LogAttributes['bytes_received']) AS bytes_received,
    toUInt64OrZero(LogAttributes['bytes_sent']) AS bytes_sent,
    toUInt32OrZero(LogAttributes['duration_ms']) AS duration_ms
FROM otlp.otel_logs
WHERE LogAttributes['log_type'] = 'zone-storage-access'
  AND LogAttributes['zone_id'] != ''
  AND LogAttributes['request_id'] != ''
  AND LogAttributes['resource_id'] != '';
