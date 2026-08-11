-- 1. Tạo database otlp và bảng otel_logs trước để tránh lỗi phụ thuộc
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
    INDEX idx_res_attr_key mapKeys(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_res_attr_value mapValues(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_log_attr_key mapKeys(LogAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_log_attr_value mapValues(LogAttributes) TYPE bloom_filter(0.01) GRANULARITY 1
) ENGINE = MergeTree()
PARTITION BY toYYYYMM(TimestampTime)
ORDER BY (ServiceName, SeverityText, TimestampTime, Timestamp)
TTL TimestampTime + INTERVAL 7 DAY DELETE;

-- 2. Tạo database storage và bảng storage-owned metering projection. The
-- generic envelope identity/module/schema are retained for migration and audit;
-- this legacy table is not the future universal billing contract.
CREATE DATABASE IF NOT EXISTS storage;

CREATE TABLE IF NOT EXISTS storage.metering_logs (
    timestamp DateTime64(3, 'UTC'),
    access_key String,
    bucket_name String,
    bytes_received UInt64,
    bytes_sent UInt64,
    method LowCardinality(String),
    status UInt16,
    duration_ms UInt32,
    path String,
    event_id String,
    module LowCardinality(String),
    metering_schema LowCardinality(String)
) ENGINE = MergeTree()
PARTITION BY toYYYYMM(timestamp)
ORDER BY (access_key, bucket_name, timestamp);

-- The init hook runs only on a fresh Central volume. Keep the new envelope
-- columns re-runnable for an explicitly managed migration before this legacy
-- projection is retired.
ALTER TABLE storage.metering_logs
    ADD COLUMN IF NOT EXISTS event_id String,
    ADD COLUMN IF NOT EXISTS module LowCardinality(String),
    ADD COLUMN IF NOT EXISTS metering_schema LowCardinality(String);

-- 3. Tạo bảng aggregated gộp theo giờ
CREATE TABLE IF NOT EXISTS storage.hourly_metering_agg (
    hour DateTime,
    access_key String,
    bucket_name String,
    total_upload_bytes SimpleAggregateFunction(sum, UInt64),
    total_download_bytes SimpleAggregateFunction(sum, UInt64),
    request_count SimpleAggregateFunction(sum, UInt64)
) ENGINE = SummingMergeTree()
PRIMARY KEY (access_key, bucket_name, hour)
ORDER BY (access_key, bucket_name, hour);

-- 4. Materialized View gộp dữ liệu theo giờ
CREATE MATERIALIZED VIEW IF NOT EXISTS storage.mv_hourly_metering
TO storage.hourly_metering_agg AS
SELECT
    toStartOfHour(timestamp) AS hour,
    access_key,
    bucket_name,
    sum(bytes_received) AS total_upload_bytes,
    sum(bytes_sent) AS total_download_bytes,
    count() AS request_count
FROM storage.metering_logs
WHERE status >= 200 AND status < 300
GROUP BY hour, access_key, bucket_name;

-- 5. Materialized View parse the storage projection from the generic
-- metering envelope. The old log_type is accepted only during migration;
-- unknown modules never enter this storage-owned table.
DROP VIEW IF EXISTS storage.mv_otel_to_metering;
CREATE MATERIALIZED VIEW storage.mv_otel_to_metering
TO storage.metering_logs AS
SELECT
    Timestamp AS timestamp,
    LogAttributes['access_key'] AS access_key,
    LogAttributes['bucket_name'] AS bucket_name,
    toUInt64(coalesce(LogAttributes['bytes_received'], '0')) AS bytes_received,
    toUInt64(coalesce(LogAttributes['bytes_sent'], '0')) AS bytes_sent,
    LogAttributes['method'] AS method,
    toUInt16(coalesce(LogAttributes['status'], '200')) AS status,
    toUInt32(coalesce(LogAttributes['duration_ms'], '0')) AS duration_ms,
    LogAttributes['path'] AS path,
    LogAttributes['event_id'] AS event_id,
    if(LogAttributes['module'] != '', LogAttributes['module'], 'storage') AS module,
    if(LogAttributes['metering_schema'] != '', LogAttributes['metering_schema'], 'storage.access.completed.v1') AS metering_schema
FROM otlp.otel_logs
WHERE (
        LogAttributes['log_type'] = 'metering'
        AND LogAttributes['module'] = 'storage'
        AND LogAttributes['metering_schema'] = 'storage.access.completed.v1'
      )
   OR LogAttributes['log_type'] = 'envoy-storage-access';

-- 6. Tạo bảng bucket_size_history lưu trữ lịch sử dung lượng sử dụng của các buckets
CREATE TABLE IF NOT EXISTS storage.bucket_size_history (
    timestamp DateTime64(3, 'UTC'),
    bucket_name String,
    owner_id String,
    owner_type LowCardinality(String),
    used_bytes UInt64
) ENGINE = MergeTree()
PARTITION BY toYYYYMM(timestamp)
ORDER BY (owner_id, bucket_name, timestamp);
