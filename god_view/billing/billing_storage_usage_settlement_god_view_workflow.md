# Storage Usage Billing Settlement — God View

This is the Central background workflow that receives one Zone-owned hourly
`StorageUsageReportV1` and settles its three possible usage kinds against
Billing PostgreSQL. It has no browser request and therefore no ACR phase. The
Zone supplies observations only; Central resolves owner, price and wallet
from durable Billing state.

Central ClickHouse is not part of this workflow. Zone ClickHouse is only the
local journal that produces the report. Billing PostgreSQL is the sole money
Source of Truth.

## Runtime contract

| Item | Contract |
| --- | --- |
| Source | Kafka `{prefix}.storage.usage.reports.v1` |
| Kafka consumer | Job Orchestrator `aurora-job-orchestrator-storage-usage-v1` |
| Central handoff | Shared Redis Stream `aurora:storage:usage:reports` |
| Settlement owner | Cost Engine storage report consumer group `cost-engine-storage-metering-v1` |
| Billable units | `NETWORK_IN/BYTE`, `NETWORK_OUT/BYTE`, `STORAGE/GB_HOUR_MICRO` |
| Money SoT | Billing PostgreSQL wallet, report/line inbox, ownership projection and immutable ledger |
| Invalid input | Sanitized Kafka DLQ record, then source offset commit |
| Transient failure | Keep Kafka offset or Redis entry pending; retry/reclaim |
| Duplicate | Deterministic report/line IDs and inbox status make replay idempotent |

## Phase 1 — Job Orchestrator validates and relays Kafka

JO owns the Kafka-to-Redis transport boundary. It validates payload size,
schema, UUIDs, hourly window shape, aggregate bounds, identity shape,
billable quantity and SHA-256. Storage capacity aggregates use a validated
`resource_name`; transfer aggregates use a non-nil `resource_id`. The original
payload is never copied into the dead-letter record.

```mermaid
sequenceDiagram
    participant K as Kafka storage.usage.reports.v1
    participant J as Job Orchestrator metering worker
    participant D as Kafka dead-letter topic
    participant R as Shared Redis stream

    K-->>J: StorageUsageReportV1 record
    J->>J: Decode protobuf and validate size schema window UUID identity quantities and checksum
    alt invalid report
        J->>D: Publish sanitized error code, source offset and empty payload
        D-->>J: Durable DLQ acknowledgement
        J->>K: Commit source offset
    else valid report
        J->>R: XADD report_id zone_id checksum payload
        R-->>J: Redis stream entry ID
        J->>K: Commit source offset only after XADD
    end
```

If `XADD` fails, JO returns an error and does not commit Kafka. A crash after
`XADD` but before commit causes a Kafka redelivery; the Cost Engine absorbs it
through the report ID and payload checksum.

## Phase 2 — Cost Engine validates, opens pricing runs and resolves ownership

The Cost Engine consumes the Redis stream with reclaimable pending entries. It
revalidates the transport metadata against the canonical protobuf, then starts
one fenced billing run for each service type in the report:
`NETWORK_IN`, `NETWORK_OUT` and `STORAGE`. If any run cannot be opened, all
already-open runs are marked retryable and no wallet transaction starts.

For transfer aggregates, the resource UUID is looked up in the Billing
ownership projection. For capacity aggregates, the report carries a bucket
name and the engine resolves the current ownership projection by
`resource_type=STORAGE_BUCKET`, Zone, and the report's half-open time window.
Missing or ambiguous ownership becomes durable `UNRATED`; no wallet is guessed.

```mermaid
sequenceDiagram
    participant R as Shared Redis stream
    participant E as Cost Engine report worker
    participant F as Redis billing fence
    participant B as Billing PostgreSQL pricing and ownership
    participant I as Report and line inbox

    R-->>E: XREADGROUP or reclaimed pending entry
    E->>E: Decode report and compare stream report_id zone_id checksum
    E->>F: Acquire settlement lease and fencing token
    E->>B: Begin NETWORK_IN billing run
    E->>B: Begin NETWORK_OUT billing run
    E->>B: Begin STORAGE billing run
    alt pricing run unavailable
        E->>B: Mark previously opened runs RETRYING
        E-->>R: Leave entry pending for reclaim
    else all runs opened
        E->>B: Insert/lock report inbox by report_id and checksum
        E->>I: Materialize deterministic line identity per resource and service type
        E->>B: Resolve owner at window boundary
        alt owner missing or ambiguous
            E->>I: Persist unrated line and reason
        else owner resolved
            E-->>E: Continue to atomic wallet settlement phase
        end
    end
```

The engine never consults Central ClickHouse, client credentials or an
untrusted owner field. The Zone report is an observation, not authorization.

## Phase 3 — Atomic wallet, line inbox and ledger settlement

All billable lines are settled in one PostgreSQL transaction per report. The
transaction locks the report and line inbox identities, then the owner wallet.
`storage.network_in.byte` and `storage.network_out.byte` use byte quantity and
the corresponding pinned Pricing Schedule snapshot. `storage.capacity.gb_hour`
uses the fixed-point GB-hour quantity and its Storage Pricing Schedule. A storage line keeps the original bucket name while its
deterministic synthetic UUID satisfies the existing UUID-based ledger/index
contract.

```mermaid
sequenceDiagram
    participant E as Cost Engine settlement worker
    participant P as Billing PostgreSQL transaction
    participant O as Ownership projection
    participant W as Owner wallet row
    participant L as Immutable wallet ledger
    participant R as Shared Redis stream

    E->>P: BEGIN and SELECT report inbox FOR UPDATE
    P->>P: Insert/lock NETWORK_IN, NETWORK_OUT and STORAGE line identities
    P->>O: Resolve resource UUID or bucket name at report window
    alt unresolved owner or wallet not active
        P->>P: Insert billing.unrated_usage and mark line UNRATED
    else owner and wallet resolved
        P->>W: SELECT wallet FOR UPDATE
        P->>P: Compute schedule charge in fixed micro-units
        P->>W: Update cash/promotional balances and lifecycle status
        P->>L: INSERT deterministic USAGE_CHARGE ledger ID
        P->>P: Mark line SETTLED and report SETTLED
    end
    P-->>E: COMMIT or rollback all wallet/ledger/inbox mutations
    E->>R: XACK then XDEL only after commit and intact fencing lease
```

If a ledger identity already exists after a crash, the engine reconciles the
line as settled without debiting again. Any PostgreSQL or fence error rolls
back the transaction; Redis reclaim retries the same report. A report with
unrated lines is durably marked `UNRATED`, allowing a later reconciliation
workflow without speculative charging.

Corrections remain append-only. The current unsigned wire contract cannot
express a safe negative adjustment, so a correction is persisted as `DEAD`
with `STORAGE_USAGE_CORRECTION_POLICY_NOT_ENABLED` and does not mutate a
wallet or settled history.

## Phase 4 — Pending-activation historical reconciliation

`PENDING_ACTIVATION` is not a free allowance. When a valid closed report has a
resolved owner but its wallet is still pending, the settlement transaction keeps
the line, pinned schedule version, run identity and report evidence, marks the
line `UNRATED` with `WALLET_PENDING_ACTIVATION`, and leaves wallet admission
suspended. A verified top-up credits the wallet and creates the Storage-owned
`billing.storage_pending_activation_reconcile` request in the same transaction;
it never emits `ALLOW` directly.

```mermaid
sequenceDiagram
    participant P as Payment settlement transaction
    participant B as Billing PostgreSQL
    participant W as Storage pending-activation worker
    participant C as PricingRuntime version cache
    participant L as Wallet ledger
    participant O as Wallet admission outbox

    P->>B: Credit pending wallet and keep PENDING_ACTIVATION/NOT_ACTIVATED
    P->>B: Insert storage_pending_activation_reconcile and SUSPEND_BILLABLE outbox
    W->>B: Claim request and lock wallet plus pending Storage lines
    W->>B: Recheck owner projection at historical metering boundary
    W->>C: Load pinned schedule version/checksum from the line
    alt owner, price or evidence unavailable
        W->>B: Keep line UNRATED and request BLOCKED with bounded reason
    else historical line is rateable
        W->>B: Debit wallet and append deterministic USAGE_CHARGE ledger line
        W->>B: Mark Storage line SETTLED and unrated evidence RESOLVED
        alt all pending lines terminal and credit remains positive
            W->>B: PENDING_ACTIVATION -> ACTIVE and increment wallet version
            W->>O: Append ALLOW with the new wallet version
        else credit exhausted
            W->>B: Keep/transition SUSPENDED(CREDIT_EXHAUSTED)
            W->>O: Append SUSPEND_BILLABLE with the new wallet version
        end
    end
```

The worker never rates at top-up time and never calls a Zone or the browser. It
uses deterministic line/ledger identity, so replay after a crash cannot debit
the same historical fact twice. Missing or ambiguous ownership, checksum
mismatch, unsupported currency or a missing pinned run blocks activation and
remains bounded operational evidence.

## Contract and key table

| Key or contract | Value |
| --- | --- |
| Protobuf | `aurora.storage.metering.v1.StorageUsageReportV1` |
| Usage units | `BYTE` for network; `GB_HOUR_MICRO` for storage |
| Kafka consumer group | `aurora-job-orchestrator-storage-usage-v1` |
| Redis stream/group | `aurora:storage:usage:reports` / `cost-engine-storage-metering-v1` |
| Pricing runs | One fenced run per nonzero charge kind: `storage.network_in.byte`, `storage.network_out.byte`, `storage.capacity.gb_hour` |
| Line identity | UUIDv5 of report, resource identity and charge kind |
| Storage identity | Synthetic UUIDv5 plus durable `resource_name` bucket reference |
| Durable state | `billing.storage_usage_report_inbox`, `storage_usage_line_inbox`, `unrated_usage` |
| Money state | `billing.wallets` and `billing.wallet_ledger_entries` |
| Fence | `storage:report:settlement:lock` plus monotonic fencing counter |
| Pending activation | `billing.storage_pending_activation_reconcile` keyed by wallet |

## Phase 5 — Wallet admission fan-out and Zone enforcement

Every wallet transition is committed with a `wallet_admission_outbox` row. The
Cost relay claims only committed rows, resolves active `STORAGE_BUCKET`
ownership targets from Billing PostgreSQL, waits for the Shared Redis
durability fence, and only then marks the outbox row published. It never reads
a request or a live Controlplane database.

```mermaid
sequenceDiagram
    participant B as Billing PostgreSQL wallet transaction
    participant R as Cost wallet admission relay
    participant S as Shared Redis stream
    participant C as Controlplane Storage projection
    participant K as Kafka Zone admission topic
    participant Z as Zone Control admission consumer
    participant V as Zone admission KV
    participant E as Zone Control/Public authorizer

    B->>B: Commit wallet status, version, reason and wallet_admission_outbox
    R->>B: Claim unpublished outbox row with claim token
    R->>B: Resolve active STORAGE_BUCKET targets by owner
    R->>S: XADD versioned WalletAdmissionChangedV1
    R->>S: WAITAOF durability fence
    R->>B: Mark the claimed row published
    S-->>C: Consumer group delivers event
    C->>C: Apply owner projection and monotonic resource target versions
    C->>K: Publish one scoped event per target Zone after local commit
    K-->>Z: Zone Control consumer receives target event
    Z->>V: CAS resource admission by wallet_version
    E->>V: Read admission before billable ticket issue/transfer
    alt missing, stale or SUSPEND_BILLABLE
        E-->>E: Deny billable operation; revoke/delete remains allowed
    else ALLOW and non-expired
        E-->>E: Continue assertion/ticket path
    end
```

The Central projection and Zone KV are rebuildable read models. A lower or
duplicate wallet version is a no-op; a missing or expired `ALLOW` fails closed.
The browser, SDK and Zone authorizers never call Billing synchronously.

## Failure and security rules

| Failure | Behavior |
| --- | --- |
| Malformed/checksum-invalid report | Sanitized DLQ, Kafka commit, no Redis relay or wallet mutation |
| Redis outage | Kafka offset remains pending |
| JO crash after XADD | Kafka redelivery is safe through report checksum/idempotency |
| Pricing run failure | Already-open runs become retryable; report remains pending |
| Billing PostgreSQL outage | Redis entry remains pending for reclaim |
| Missing/ambiguous owner or wallet | Durable `UNRATED`; never debit a guessed owner |
| Wallet/ledger SQL failure | Entire transaction rolls back |
| Fence loss before ACK | Do not ACK; reclaim the report |
| Duplicate report/line | Inbox and deterministic ledger identity prevent a second debit |
| Correction | Durable `DEAD` quarantine until signed adjustment policy exists |
| Pending wallet | Historical lines remain `UNRATED` until Storage reconciliation settles them; top-up alone never opens admission |
| Central ClickHouse | Absent from the charge path; only Zone-local metering journals exist |

## Code map

- `job-orchestrator/src/storage_metering.rs`
- `proto/cost-manager/engine/storage_usage_report.proto`
- `cost-manager/engine/src/service/storage/usage_report_settlement.rs`
- `cost-manager/engine/src/service/storage/pending_activation_reconcile.rs`
- `cost-manager/engine/src/engine/snapshot.rs`
- `cost-manager/api/migrations/000008_storage_usage_contract.up.sql` (current
  storage settlement contract)
- `cost-manager/api/migrations/000009_metering_pricing_contract.up.sql` (decimal
  GB_HOUR catalog thresholds and storage indexes)
- `cost-manager/api/migrations/000010_storage_pricing_checksum.up.sql` (pricing
  snapshot checksum synchronized with the new thresholds; historical
  migrations remain checksum-immutable)
- `cost-manager/api/migrations/000011_payg_pricing_schedule_cutover.up.sql`
- `cost-manager/tmp/zone-local-storage-metering-refactor-plan.md`
