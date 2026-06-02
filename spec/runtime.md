# FINAL SPEC: Controlplane Runtime Delta Engine

## 1. Mục tiêu

Thiết kế một engine đồng bộ L1 RAM runtime state giữa nhiều controlplane replicas, phục vụ tải cao mà không đưa Redis/DB vào hot path.

Mục tiêu chính:

```text
1. Hot path chỉ đọc từ RAM local.
2. Không Redis GET trên request path.
3. Không DB query trên request path.
4. Không để mọi controlplane node cùng reload DB sau mutation.
5. Dùng NATS JetStream để durable fanout event.
6. Dùng transactional outbox để DB update và event intent atomic.
7. Mọi propagation event đều là delta event.
8. Full snapshot cũng được biểu diễn như một delta replace operation.
9. Batch cũng được biểu diễn bằng operations[].
10. Consumer apply vào RAM bằng copy-on-write.
11. Version + prev_version dùng để chống duplicate, out-of-order, missing event.
12. Reconcile/recovery dùng để sửa stale hoặc gap.
```

Core principle:

```text
Build exact change once, fanout many, apply locally.
```

---

## 2. Final Architecture

```text
                         ┌──────────────────────────────┐
                         │           Database            │
                         │                              │
                         │  business tables              │
                         │  config_versions              │
                         │  config_events_outbox         │
                         └──────────────┬───────────────┘
                                        │
                                        │ DB transaction:
                                        │ - update business data
                                        │ - bump version
                                        │ - insert outbox event
                                        ▼
                         ┌──────────────────────────────┐
                         │       Outbox Publisher        │
                         │                              │
                         │ - claim pending events        │
                         │ - batch by domain/shard       │
                         │ - publish NATS event          │
                         │ - mark published/failed       │
                         └──────────────┬───────────────┘
                                        │
                                        │ publish ConfigChangedEvent
                                        ▼
                         ┌──────────────────────────────┐
                         │        NATS JetStream         │
                         │                              │
                         │ cp.config.<domain>           │
                         │ cp.config.<domain>.<shard>   │
                         └──────────────┬───────────────┘
                                        │
                  ┌─────────────────────┼─────────────────────┐
                  │                     │                     │
                  ▼                     ▼                     ▼
        ┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐
        │ Controlplane A   │  │ Controlplane B   │  │ Controlplane C   │
        │ Runtime Engine   │  │ Runtime Engine   │  │ Runtime Engine   │
        │ L1 RAM Snapshot  │  │ L1 RAM Snapshot  │  │ L1 RAM Snapshot  │
        └──────────────────┘  └──────────────────┘  └──────────────────┘
                  │                     │                     │
                  ▼                     ▼                     ▼
           Hot path read          Hot path read          Hot path read
           atomic RAM load        atomic RAM load        atomic RAM load
```

---

## 3. Design Decisions

### 3.1 Bỏ toàn bộ EventMode

Không dùng:

```go
ModeSnapshotFullInline
ModeSnapshotFullRef
ModeDeltaInline
ModeDeltaBatchInline
ModeInvalidate
```

Thay vào đó:

```text
Mọi event đều là ConfigChangedEvent.
Event chứa scope + operations[].
```

Full snapshot:

```text
scope = domain
operation = replace
```

Shard snapshot:

```text
scope = shard
operation = replace
```

Single-key delta:

```text
scope = key
operation = upsert/delete
```

Batch delta:

```text
operations[] có nhiều operation
```

---

## 4. Event Semantics

### 4.1 Scope

```go
type Scope string

const (
 ScopeKey    Scope = "key"
 ScopeShard  Scope = "shard"
 ScopeDomain Scope = "domain"
)
```

Meaning:

```text
ScopeKey:
  Event affects one or more specific keys.

ScopeShard:
  Event affects one shard.
  Can be used for batched operations or shard replace.

ScopeDomain:
  Event affects whole domain.
  Used for small bounded domains such as zone_catalog.
```

### 4.2 Operation

```go
type ChangeOp string

const (
 OpUpsert  ChangeOp = "upsert"
 OpDelete  ChangeOp = "delete"
 OpReplace ChangeOp = "replace"
)
```

Meaning:

```text
upsert:
  Insert or replace full state of one key.

delete:
  Delete one key.

replace:
  Replace whole target scope.
  If scope=domain, replace whole domain snapshot.
  If scope=shard, replace whole shard snapshot.
```

Important rule:

```text
Prefer upsert with full state of the key.
Avoid partial patch unless absolutely necessary.
```

---

## 5. Final Event Schema

```go
type ConfigChangedEvent struct {
 SchemaVersion int `json:"schema_version"`

 Domain Domain `json:"domain"`
 Scope  Scope  `json:"scope"`

 // Optional top-level key.
 // Useful when event affects one key.
 Key string `json:"key,omitempty"`

 // -1 for non-sharded/small domain.
 Shard int `json:"shard"`

 PrevVersion int64 `json:"prev_version"`
 Version     int64 `json:"version"`

 Operations []ConfigOperation `json:"operations"`

 SourceInstance string    `json:"source_instance"`
 TraceID         string    `json:"trace_id,omitempty"`
 PublishedAt     time.Time `json:"published_at"`
 Reason          string    `json:"reason,omitempty"`
}
```

```go
type ConfigOperation struct {
 Op      ChangeOp        `json:"op"`
 Key     string          `json:"key,omitempty"`
 Payload json.RawMessage `json:"payload,omitempty"`
}
```

```go
type Domain string

const (
 DomainZoneCatalog Domain = "zone_catalog"
 DomainRatePolicy  Domain = "rate_policy"
 DomainOtelPolicy  Domain = "otel_policy"
 DomainSecretRef   Domain = "secret_ref"
)
```

---

## 6. Example Events

### 6.1 ZoneCatalog full replace

`zone_catalog` là small bounded domain, nên replace toàn domain là hợp lý.

```json
{
  "schema_version": 1,
  "domain": "zone_catalog",
  "scope": "domain",
  "key": "cp:zone_catalog",
  "shard": -1,
  "prev_version": 43,
  "version": 44,
  "operations": [
    {
      "op": "replace",
      "key": "cp:zone_catalog",
      "payload": [
        { "id": "z1", "code": "VN", "name": "Vietnam" },
        { "id": "z2", "code": "SG", "name": "Singapore" }
      ]
    }
  ],
  "source_instance": "controlplane-0",
  "trace_id": "trace-abc",
  "published_at": "2026-06-02T10:00:00Z",
  "reason": "zone_update"
}
```

### 6.2 RatePolicy key upsert

```json
{
  "schema_version": 1,
  "domain": "rate_policy",
  "scope": "key",
  "key": "cp:rate_policy:tenant:123",
  "shard": 17,
  "prev_version": 981,
  "version": 982,
  "operations": [
    {
      "op": "upsert",
      "key": "cp:rate_policy:tenant:123",
      "payload": {
        "tenant_id": "123",
        "limit_value": 1000,
        "window_sec": 60,
        "enabled": true
      }
    }
  ],
  "source_instance": "controlplane-0",
  "published_at": "2026-06-02T10:00:00Z",
  "reason": "rate_policy_update"
}
```

### 6.3 RatePolicy key delete

```json
{
  "schema_version": 1,
  "domain": "rate_policy",
  "scope": "key",
  "key": "cp:rate_policy:tenant:123",
  "shard": 17,
  "prev_version": 982,
  "version": 983,
  "operations": [
    {
      "op": "delete",
      "key": "cp:rate_policy:tenant:123"
    }
  ],
  "source_instance": "controlplane-0",
  "published_at": "2026-06-02T10:00:00Z",
  "reason": "rate_policy_delete"
}
```

### 6.4 Batched shard operations

Không cần `ModeDeltaBatchInline`. Batch chỉ là `operations[]`.

```json
{
  "schema_version": 1,
  "domain": "rate_policy",
  "scope": "shard",
  "shard": 17,
  "prev_version": 981,
  "version": 985,
  "operations": [
    {
      "op": "upsert",
      "key": "cp:rate_policy:tenant:123",
      "payload": {
        "tenant_id": "123",
        "limit_value": 1000,
        "window_sec": 60
      }
    },
    {
      "op": "delete",
      "key": "cp:rate_policy:tenant:456"
    },
    {
      "op": "upsert",
      "key": "cp:rate_policy:tenant:789",
      "payload": {
        "tenant_id": "789",
        "limit_value": 500,
        "window_sec": 60
      }
    }
  ],
  "source_instance": "controlplane-0",
  "published_at": "2026-06-02T10:00:00Z",
  "reason": "rate_policy_batch"
}
```

### 6.5 Shard replace for recovery

```json
{
  "schema_version": 1,
  "domain": "rate_policy",
  "scope": "shard",
  "shard": 17,
  "prev_version": 981,
  "version": 990,
  "operations": [
    {
      "op": "replace",
      "payload": {
        "items": {
          "cp:rate_policy:tenant:123": {
            "tenant_id": "123",
            "limit_value": 1000
          },
          "cp:rate_policy:tenant:456": {
            "tenant_id": "456",
            "limit_value": 500
          }
        }
      }
    }
  ],
  "source_instance": "controlplane-0",
  "published_at": "2026-06-02T10:00:00Z",
  "reason": "shard_recovery"
}
```

---

## 7. Key Convention

Không đưa `tenant_id`, `service_id`, `workspace_id` thành field riêng trong generic event.

Mọi định danh domain-specific nằm trong `Key`.

Canonical format:

```text
cp:{domain}:{dimension}:{id}[:sub_dimension:{id}]
```

Examples:

```text
cp:zone_catalog
cp:rate_policy:tenant:123
cp:rate_policy:tenant:123:route:orders
cp:otel_policy:service:billing-api
cp:secret_ref:service:payment-worker
```

Helper package bắt buộc:

```go
package cachekey

func ZoneCatalog() string {
 return "cp:zone_catalog"
}

func RatePolicyTenant(tenantID string) string {
 return "cp:rate_policy:tenant:" + normalize(tenantID)
}

func OtelPolicyService(service string) string {
 return "cp:otel_policy:service:" + normalize(service)
}
```

Service layer không tự format key thủ công.

---

## 8. Database Schema

### 8.1 config_versions

```sql
CREATE TABLE config_versions (
    domain      VARCHAR(128) NOT NULL,
    shard       INT NOT NULL DEFAULT -1,
    version     BIGINT NOT NULL DEFAULT 0,
    updated_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(domain, shard)
);
```

Conventions:

```text
shard = -1:
  non-sharded domain/global version.

shard >= 0:
  shard version for large keyed domain.
```

Examples:

```text
domain          shard   version
zone_catalog    -1      44
rate_policy      0      120
rate_policy      1      201
rate_policy     17      982
otel_policy     -1      12
```

### 8.2 Optional bucket clock

Nếu nhiều domain/shard và reconcile nhiều, dùng bucket clock để tránh scan toàn bảng.

```sql
CREATE TABLE config_version_clocks (
    bucket      INT PRIMARY KEY,
    clock       BIGINT NOT NULL DEFAULT 0,
    updated_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

`config_versions` có thêm bucket:

```sql
ALTER TABLE config_versions
ADD COLUMN bucket INT NOT NULL DEFAULT 0;

CREATE INDEX ix_config_versions_bucket_updated
ON config_versions(bucket, updated_at);
```

Mutation bump thêm clock bucket:

```sql
UPDATE config_version_clocks
SET clock = clock + 1,
    updated_at = NOW()
WHERE bucket = $bucket;
```

Reconcile flow:

```text
1. Read bucket clocks.
2. If bucket clock unchanged, skip.
3. If changed, scan config_versions for that bucket.
```

---

## 9. Outbox Schema

```sql
CREATE TABLE config_events_outbox (
    id                BIGSERIAL PRIMARY KEY,

    domain            VARCHAR(128) NOT NULL,
    shard             INT NOT NULL DEFAULT -1,

    prev_version      BIGINT NOT NULL DEFAULT 0,
    version           BIGINT NOT NULL,

    scope             VARCHAR(32) NOT NULL,
    cache_key         VARCHAR(512),

    event_type        VARCHAR(64) NOT NULL,
    operation_hint    VARCHAR(32),

    operations        JSONB NOT NULL,

    reason            VARCHAR(128),
    trace_id          VARCHAR(128),
    source_instance   VARCHAR(128),

    status            VARCHAR(32) NOT NULL DEFAULT 'pending',
    attempts          INT NOT NULL DEFAULT 0,
    last_error        TEXT,
    next_attempt_at   TIMESTAMP NULL,

    created_at        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    published_at      TIMESTAMP NULL
);

CREATE UNIQUE INDEX ux_config_outbox_domain_shard_version
ON config_events_outbox(domain, shard, version);

CREATE INDEX ix_config_outbox_pending
ON config_events_outbox(status, next_attempt_at, created_at);

CREATE INDEX ix_config_outbox_domain_shard_status_version
ON config_events_outbox(domain, shard, status, version);
```

Status values:

```text
pending
processing
published
failed
superseded
```

---

## 10. Mutation Flow

### 10.1 Small domain: zone_catalog

```sql
BEGIN;

UPDATE zones
SET name = $1,
    updated_at = NOW()
WHERE id = $2;

WITH bumped AS (
    UPDATE config_versions
    SET version = version + 1,
        updated_at = NOW()
    WHERE domain = 'zone_catalog'
      AND shard = -1
    RETURNING version
)
INSERT INTO config_events_outbox (
    domain,
    shard,
    prev_version,
    version,
    scope,
    cache_key,
    event_type,
    operation_hint,
    operations,
    reason,
    trace_id,
    source_instance,
    status
)
SELECT
    'zone_catalog',
    -1,
    version - 1,
    version,
    'domain',
    'cp:zone_catalog',
    'zone_update',
    'replace',
    jsonb_build_array(
        jsonb_build_object(
            'op', 'replace',
            'key', 'cp:zone_catalog',
            'payload', (
                SELECT jsonb_agg(
                    jsonb_build_object(
                        'id', z.id,
                        'code', z.code,
                        'name', z.name
                    )
                    ORDER BY z.code
                )
                FROM zones z
                WHERE z.deleted_at IS NULL
            )
        )
    ),
    'zone_mutation',
    $3,
    $4,
    'pending'
FROM bumped;

COMMIT;
```

Note:

```text
zone_catalog nhỏ nên có thể build replace payload trong transaction.
Nếu muốn transaction nhẹ hơn, outbox có thể chỉ ghi intent,
worker build domain replace payload sau.
```

### 10.2 Large keyed domain: rate_policy upsert

Payload phải lấy từ `RETURNING` để đúng với DB state sau update.

```sql
BEGIN;

WITH updated AS (
    UPDATE rate_policies
    SET limit_value = $1,
        window_sec = $2,
        enabled = $3,
        updated_at = NOW()
    WHERE tenant_id = $4
    RETURNING
        tenant_id,
        limit_value,
        window_sec,
        enabled,
        updated_at
),
bumped AS (
    UPDATE config_versions
    SET version = version + 1,
        updated_at = NOW()
    WHERE domain = 'rate_policy'
      AND shard = $5
    RETURNING version
)
INSERT INTO config_events_outbox (
    domain,
    shard,
    prev_version,
    version,
    scope,
    cache_key,
    event_type,
    operation_hint,
    operations,
    reason,
    trace_id,
    source_instance,
    status
)
SELECT
    'rate_policy',
    $5,
    bumped.version - 1,
    bumped.version,
    'key',
    $6,
    'rate_policy_update',
    'upsert',
    jsonb_build_array(
        jsonb_build_object(
            'op', 'upsert',
            'key', $6,
            'payload', to_jsonb(updated)
        )
    ),
    'rate_policy_mutation',
    $7,
    $8,
    'pending'
FROM updated, bumped;

COMMIT;
```

### 10.3 Large keyed domain: delete

```sql
BEGIN;

WITH deleted AS (
    DELETE FROM rate_policies
    WHERE tenant_id = $1
    RETURNING tenant_id
),
bumped AS (
    UPDATE config_versions
    SET version = version + 1,
        updated_at = NOW()
    WHERE domain = 'rate_policy'
      AND shard = $2
    RETURNING version
)
INSERT INTO config_events_outbox (
    domain,
    shard,
    prev_version,
    version,
    scope,
    cache_key,
    event_type,
    operation_hint,
    operations,
    reason,
    trace_id,
    source_instance,
    status
)
SELECT
    'rate_policy',
    $2,
    bumped.version - 1,
    bumped.version,
    'key',
    $3,
    'rate_policy_delete',
    'delete',
    jsonb_build_array(
        jsonb_build_object(
            'op', 'delete',
            'key', $3
        )
    ),
    'rate_policy_delete',
    $4,
    $5,
    'pending'
FROM deleted, bumped;

COMMIT;
```

---

## 11. Outbox Publisher

### 11.1 Claim pending events

```sql
WITH picked AS (
    SELECT id
    FROM config_events_outbox
    WHERE status = 'pending'
      AND (next_attempt_at IS NULL OR next_attempt_at <= NOW())
    ORDER BY created_at
    LIMIT 100
    FOR UPDATE SKIP LOCKED
)
UPDATE config_events_outbox o
SET status = 'processing',
    attempts = attempts + 1
FROM picked
WHERE o.id = picked.id
RETURNING o.*;
```

### 11.2 Publish single event

Outbox row maps directly to `ConfigChangedEvent`.

```go
func BuildEventFromOutbox(row OutboxRow) ConfigChangedEvent {
 return ConfigChangedEvent{
  SchemaVersion: 1,
  Domain:        Domain(row.Domain),
  Scope:         Scope(row.Scope),
  Key:           row.CacheKey,
  Shard:         row.Shard,
  PrevVersion:   row.PrevVersion,
  Version:       row.Version,
  Operations:    row.Operations,
  SourceInstance: row.SourceInstance,
  TraceID:       row.TraceID,
  PublishedAt:   time.Now().UTC(),
  Reason:        row.Reason,
 }
}
```

### 11.3 Batch publish by domain/shard

Outbox publisher may combine continuous versions from same domain/shard into one event.

Input rows:

```text
rate_policy shard=17:
  v982 operations=[...]
  v983 operations=[...]
  v984 operations=[...]
```

Output event:

```text
prev_version = 981
version = 984
operations = concat(v982.operations, v983.operations, v984.operations)
```

Batch rule:

```text
1. Same domain.
2. Same shard.
3. Versions must be continuous.
4. Combined payload must not exceed MaxEventPayloadBytes.
5. Combined operations count must not exceed MaxOperationsPerEvent.
```

Config:

```go
const MaxEventPayloadBytes = 256 * 1024
const MaxOperationsPerEvent = 500
const OutboxBatchWindow = 20 * time.Millisecond
```

If limit exceeded:

```text
publish smaller batches
```

---

## 12. NATS Subject Design

Small domain:

```text
cp.config.zone_catalog
cp.config.otel_policy
```

Large sharded domain:

```text
cp.config.rate_policy.000
cp.config.rate_policy.001
...
cp.config.rate_policy.255
```

Generic format:

```text
cp.config.<domain>
cp.config.<domain>.<shard>
```

Recommendation:

```text
Small non-sharded:
  cp.config.<domain>

Large sharded:
  cp.config.<domain>.<shard>
```

---

## 13. NATS Consumer Model

Do not use one shared queue group for all controlplane replicas.

Wrong:

```text
queue group = controlplane-runtime
members = controlplane-0, controlplane-1, controlplane-2

Result:
  each message goes to only one member
```

Correct:

```text
each controlplane replica has its own durable consumer
```

Example:

```text
controlplane-0 -> cp-runtime-controlplane-0
controlplane-1 -> cp-runtime-controlplane-1
controlplane-2 -> cp-runtime-controlplane-2
```

If using StatefulSet, pod identity is stable.

If using Deployment:

```text
1. Use ephemeral consumer + reconcile fallback.
2. Or implement stable slot assignment.
3. Or clean up old durable consumers.
```

---

## 14. Runtime Snapshot Model

### 14.1 RuntimeSnapshot

```go
type RuntimeSnapshot struct {
 ZoneCatalog *ZoneCatalogSnapshot
 RatePolicy  *RatePolicySnapshot
 OtelPolicy  *OtelPolicySnapshot
 SecretRef   *SecretRefSnapshot

 CreatedAt time.Time
}
```

### 14.2 Small domain snapshot

```go
type ZoneCatalogSnapshot struct {
 Version int64
 List    []coreEntity.ZoneCatalog
 ByID    map[string]coreEntity.ZoneCatalog
 ByCode  map[string]coreEntity.ZoneCatalog
}
```

### 14.3 Large sharded snapshot

```go
const RatePolicyShardCount = 256

type RatePolicySnapshot struct {
 ShardCount int
 Shards     [RatePolicyShardCount]*RatePolicyShard
}

type RatePolicyShard struct {
 Version int64
 Items   map[string]RatePolicy
}
```

### 14.4 RuntimeEngine

```go
type RuntimeEngine struct {
 current atomic.Pointer[RuntimeSnapshot]

 store    RuntimeStore
 eventBus ConfigEventBus

 instanceID string
 ready      atomic.Bool

 sfg singleflight.Group

 applySchedulers map[Domain]*DomainApplyScheduler
}
```

---

## 15. Hot Path Read

No Redis. No DB. No lock.

```go
func (e *RuntimeEngine) ZoneCatalog() []coreEntity.ZoneCatalog {
 snap := e.current.Load()
 if snap == nil || snap.ZoneCatalog == nil {
  return nil
 }
 return snap.ZoneCatalog.List
}
```

```go
func (e *RuntimeEngine) RatePolicy(key string) (RatePolicy, bool) {
 snap := e.current.Load()
 if snap == nil || snap.RatePolicy == nil {
  return RatePolicy{}, false
 }

 shardID := HashKeyToShard(key, RatePolicyShardCount)
 shard := snap.RatePolicy.Shards[shardID]
 if shard == nil {
  return RatePolicy{}, false
 }

 p, ok := shard.Items[key]
 return p, ok
}
```

---

## 16. Apply Logic

### 16.1 Version rules

Consumer may apply event only if:

```go
localVersion == event.PrevVersion
```

Duplicate/old:

```go
localVersion >= event.Version
```

Gap:

```go
localVersion != event.PrevVersion
```

then recover.

### 16.2 General handler

```go
func (e *RuntimeEngine) HandleEvent(ctx context.Context, ev ConfigChangedEvent) error {
 local := e.current.Load()
 localVersion := e.localVersion(local, ev.Domain, ev.Shard)

 if localVersion >= ev.Version {
  return nil
 }

 if localVersion != ev.PrevVersion {
  return e.Recover(ctx, ev.Domain, ev.Shard, ev.Version)
 }

 switch ev.Scope {
 case ScopeDomain:
  return e.applyDomainEvent(ctx, ev)
 case ScopeShard:
  return e.applyShardEvent(ctx, ev)
 case ScopeKey:
  return e.applyKeyEvent(ctx, ev)
 default:
  return fmt.Errorf("unknown scope: %s", ev.Scope)
 }
}
```

### 16.3 Apply domain replace

```go
func (e *RuntimeEngine) applyZoneCatalogReplace(ctx context.Context, ev ConfigChangedEvent) error {
 if len(ev.Operations) != 1 || ev.Operations[0].Op != OpReplace {
  return fmt.Errorf("zone_catalog requires one replace operation")
 }

 var items []coreEntity.ZoneCatalog
 if err := json.Unmarshal(ev.Operations[0].Payload, &items); err != nil {
  return err
 }

 zoneSnap := BuildZoneCatalogSnapshot(items, ev.Version)

 old := e.current.Load()
 next := old.CloneShallow()
 next.ZoneCatalog = zoneSnap
 next.CreatedAt = time.Now().UTC()

 e.current.Store(next)
 return nil
}
```

### 16.4 Apply shard/key event with copy-on-write

```go
func (e *RuntimeEngine) applyRatePolicyEvent(ctx context.Context, ev ConfigChangedEvent) error {
 old := e.current.Load()
 if old == nil || old.RatePolicy == nil {
  return e.Recover(ctx, ev.Domain, ev.Shard, ev.Version)
 }

 oldRate := old.RatePolicy
 oldShard := oldRate.Shards[ev.Shard]
 if oldShard == nil {
  return e.Recover(ctx, ev.Domain, ev.Shard, ev.Version)
 }

 if oldShard.Version >= ev.Version {
  return nil
 }

 if oldShard.Version != ev.PrevVersion {
  return e.Recover(ctx, ev.Domain, ev.Shard, ev.Version)
 }

 newItems := make(map[string]RatePolicy, len(oldShard.Items)+len(ev.Operations))

 for k, v := range oldShard.Items {
  newItems[k] = v
 }

 for _, op := range ev.Operations {
  switch op.Op {
  case OpUpsert:
   var policy RatePolicy
   if err := json.Unmarshal(op.Payload, &policy); err != nil {
    return err
   }
   newItems[op.Key] = policy

  case OpDelete:
   delete(newItems, op.Key)

  case OpReplace:
   var replacement map[string]RatePolicy
   if err := json.Unmarshal(op.Payload, &replacement); err != nil {
    return err
   }
   newItems = replacement

  default:
   return fmt.Errorf("unsupported operation: %s", op.Op)
  }
 }

 newShard := &RatePolicyShard{
  Version: ev.Version,
  Items:   newItems,
 }

 newRate := oldRate.CloneShallow()
 newRate.Shards[ev.Shard] = newShard

 next := old.CloneShallow()
 next.RatePolicy = newRate
 next.CreatedAt = time.Now().UTC()

 e.current.Store(next)
 return nil
}
```

---

## 17. Apply Scheduler and Micro-batching

Nếu NATS events đến liên tục, không apply từng event ngay.

Use per domain/shard apply queue:

```text
applyQueue[domain][shard]
```

Scheduler behavior:

```text
1. Enqueue event by domain/shard.
2. Wait micro-batch window, e.g. 10-50ms.
3. Sort events by version.
4. Merge continuous events.
5. Copy affected shard once.
6. Apply all operations.
7. Atomic swap once.
```

Example:

```text
Events:
  v982, v983, v984

Instead of:
  copy shard 3 times

Do:
  copy shard once
  apply v982 -> v983 -> v984
  swap once to v984
```

Config:

```go
const ApplyBatchWindow = 20 * time.Millisecond
const MaxApplyBatchEvents = 1000
const MaxApplyBatchOperations = 5000
```

If queue lag too high:

```text
trigger shard recovery instead of replaying unbounded deltas
```

---

## 18. Recovery

Gap detected:

```text
local version = 980
event prev_version = 981
event version = 982
```

Do not apply event.

Recovery order:

```text
1. Try shard/domain replace event if available.
2. Coordinated DB load for affected shard/domain only.
3. Build local replacement snapshot.
4. Atomic swap.
5. Ack/ignore old events whose version <= local version.
```

Recovery should be protected by singleflight:

```go
func (e *RuntimeEngine) Recover(ctx context.Context, domain Domain, shard int, targetVersion int64) error {
 key := fmt.Sprintf("recover:%s:%d:%d", domain, shard, targetVersion)

 _, err, _ := e.sfg.Do(key, func() (any, error) {
  if shard >= 0 {
   return nil, e.recoverShard(ctx, domain, shard, targetVersion)
  }
  return nil, e.recoverDomain(ctx, domain, targetVersion)
 })

 return err
}
```

For large domain:

```text
Recover only affected shard.
Do not reload entire domain.
```

---

## 19. Reconcile Optimization

Reconcile must not scan everything too frequently.

### 19.1 Basic reconcile

```sql
SELECT domain, shard, version
FROM config_versions
WHERE updated_at > $1
ORDER BY updated_at
LIMIT 1000;
```

Node keeps:

```go
lastVersionScanAt time.Time
```

### 19.2 Better reconcile with bucket clocks

Flow:

```text
1. Read config_version_clocks.
2. Compare with local bucket clocks.
3. Only scan changed buckets.
4. Recover stale shard/domain.
```

Query:

```sql
SELECT bucket, clock, updated_at
FROM config_version_clocks;
```

Then:

```sql
SELECT domain, shard, version, updated_at
FROM config_versions
WHERE bucket = $1
  AND updated_at > $2
ORDER BY updated_at;
```

### 19.3 Reconcile interval

Use jitter:

```text
base = 15s
jitter = 15s
```

So nodes do not hit DB at the same time.

---

## 20. Correctness Rules

These are mandatory.

```text
1. Outbox event must be inserted in same transaction as business update.
2. Version bump must happen in same transaction.
3. Payload should come from UPDATE/INSERT/DELETE ... RETURNING when DB generates fields.
4. Event must include prev_version and version.
5. Consumer applies only if local_version == prev_version.
6. If local_version >= version, event is duplicate/old.
7. If local_version != prev_version, gap detected; recover.
8. Upsert payload should be full state of the key.
9. Avoid partial patch.
10. Apply by copy-on-write.
11. Never mutate currently published RAM map/slice.
12. NATS ack only after successful apply or safe ignore.
```

---

## 21. Why Upsert Should Contain Full Key State

Good:

```json
{
  "op": "upsert",
  "key": "cp:rate_policy:tenant:123",
  "payload": {
    "tenant_id": "123",
    "limit_value": 1000,
    "window_sec": 60,
    "enabled": true
  }
}
```

Bad unless strictly necessary:

```json
{
  "op": "patch",
  "payload": {
    "limit_value": 1000
  }
}
```

Reason:

```text
Partial patch depends on previous local state.
If node missed previous event, patch can produce wrong state.
Full upsert is safer and deterministic.
```

---

## 22. Payload Limits

No external ref in normal propagation.

Event payload must be inline.

Config:

```go
const MaxEventPayloadBytes = 256 * 1024
const MaxOperationsPerEvent = 500
```

If payload exceeds limit:

```text
1. Split batch.
2. Split shard.
3. Split key/domain model.
4. Use recovery DB load path if necessary.
```

Do not introduce `delta_ref`.

Design rule:

```text
Delta must stay small.
If delta is large, fix the data model.
```

---

## 23. Failure Handling

### 23.1 DB commit succeeds but service crashes

Handled by outbox.

```text
Outbox row exists.
Publisher retries later.
```

### 23.2 NATS publish fails

Outbox remains pending/failed.

```text
Retry with backoff.
```

### 23.3 Consumer crashes before ack

NATS redelivers.

```text
Version check makes apply idempotent.
```

### 23.4 Duplicate event

```text
local_version >= event.version
=> ack and ignore
```

### 23.5 Out-of-order event

```text
local_version != event.prev_version
=> recover
```

### 23.6 Consumer lag too high

```text
Trigger shard/domain recovery.
Skip replaying huge backlog if local recovered version >= event.version.
```

### 23.7 Startup with no snapshot

```text
Pod must not become ready.
```

No request-path DB fallback.

---

## 24. Startup Flow

```text
1. Load required versions from config_versions.
2. Load small domain snapshots from DB.
3. Load large domain shards from DB or lazy warm critical shards.
4. Build RuntimeSnapshot.
5. Store atomic pointer.
6. Start NATS consumers.
7. Start apply schedulers.
8. Start reconcile loop.
9. Mark ready.
```

Readiness:

```go
func (e *RuntimeEngine) Ready() bool {
 return e.ready.Load() && e.current.Load() != nil
}
```

---

## 25. Metrics

Required:

```text
runtime_cache_ready
runtime_snapshot_version{domain,shard}
runtime_snapshot_age_seconds{domain,shard}

runtime_event_received_total{domain,shard,scope}
runtime_event_apply_total{domain,shard,scope,status}
runtime_event_lag_ms{domain,shard}
runtime_event_payload_bytes{domain,shard}
runtime_event_operations_total{domain,shard}

runtime_apply_batch_size{domain,shard}
runtime_apply_batch_duration_ms{domain,shard}
runtime_delta_gap_detected_total{domain,shard}

runtime_recover_total{domain,shard,status}
runtime_recover_duration_ms{domain,shard}
runtime_reconcile_total{status}
runtime_reconcile_stale_detected_total{domain,shard}

outbox_pending_total{domain,shard}
outbox_processing_total{domain,shard}
outbox_published_total{domain,shard}
outbox_failed_total{domain,shard}
outbox_publish_duration_ms{domain,shard}
outbox_attempts_total{domain,shard}

nats_publish_total{subject,status}
nats_redelivery_total{consumer,subject}
nats_consumer_lag{consumer,subject}
```

Avoid expensive metrics on hot path.

---

## 26. Rollout Plan

### Phase 1: Foundation

```text
1. Add config_versions with domain/shard.
2. Add config_events_outbox with operations JSONB.
3. Add NATS ConfigEventBus.
4. Add RuntimeEngine with atomic snapshot.
5. Add basic reconcile.
```

### Phase 2: Migrate zone_catalog

```text
1. Use scope=domain, op=replace.
2. GetZoneCatalog reads RAM only.
3. Mutation writes outbox event.
4. Outbox publishes NATS event.
5. Consumers replace ZoneCatalogSnapshot.
```

### Phase 3: Add first large delta domain

```text
1. Pick rate_policy or tenant_config.
2. Use shard version.
3. Use scope=key, op=upsert/delete.
4. Add sharded copy-on-write.
5. Add gap detection and shard recovery.
```

### Phase 4: Add batching

```text
1. Outbox batch continuous versions per shard.
2. Runtime apply scheduler micro-batches per shard.
3. Add backpressure and lag-based recovery.
```

### Phase 5: Reconcile optimization

```text
1. Add bucket clocks if needed.
2. Add jittered reconcile.
3. Recover only stale shards.
```

---

## 27. Final Decisions

Chosen:

```text
1. No EventMode.
2. Every propagation message is ConfigChangedEvent.
3. Full snapshot = scope=domain + op=replace.
4. Shard snapshot = scope=shard + op=replace.
5. Delta = scope=key/shard + op=upsert/delete.
6. Batch = operations[].
7. No delta_ref.
8. No snapshot_ref in normal propagation.
9. NATS JetStream for fanout.
10. DB + config_versions + outbox as source of truth.
11. Sharded copy-on-write for large domains.
12. Micro-batch apply to avoid copy storm.
13. Bucketed/jittered reconcile to avoid DB check storm.
```

Rejected:

```text
1. Redis GET on hot path.
2. DB query on hot path.
3. Redis Pub/Sub for config propagation.
4. EventMode enum explosion.
5. delta_ref.
6. Full snapshot of large table on every update.
7. Every node reloads DB after invalidation.
8. Shared NATS queue group for all controlplane replicas.
9. Mutating RAM maps in place.
10. Continuous full DB version scanning.
```

Core invariant:

```text
For each domain/shard:

business data update,
config_versions bump,
and config_events_outbox insert
must happen in one DB transaction.

A consumer may apply an event only when:

  local_version == event.prev_version

If not, it must recover before applying.
```

Final principle:

```text
Everything is an operation list.

Small domain:
  replace domain.

Large domain:
  upsert/delete keys inside shard.

High update rate:
  batch operations.

Gap:
  recover shard/domain.

Hot path:
  RAM only.
```
