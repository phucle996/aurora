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

-- 2. Tạo database storage và bảng metering_logs
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
    path String
) ENGINE = MergeTree()
PARTITION BY toYYYYMM(timestamp)
ORDER BY (access_key, bucket_name, timestamp);

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
WHERE status = 200
GROUP BY hour, access_key, bucket_name;

-- 5. Materialized View parse logs từ otel_logs sang metering_logs phẳng
CREATE MATERIALIZED VIEW IF NOT EXISTS storage.mv_otel_to_metering
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
    LogAttributes['path'] AS path
FROM otlp.otel_logs
WHERE mapContains(LogAttributes, 'access_key')
  AND mapContains(LogAttributes, 'bytes_received')
  AND mapContains(LogAttributes, 'bytes_sent');
