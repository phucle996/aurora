# Zone health watchdog — God View

Handles missing report evidence after a Dataplane failure. It is a timer-driven recovery workflow, separate from live Zone report processing.

## Trigger and authority

| Trigger | Leader election | Write authority | Prohibited writes |
|---|---|---|---|
| watchdog timer observes stale durable report timestamp | short Shared Redis lease | timestamp-fenced service `actual_state` | Zone desired state and SRE lifecycle intent |

## Phase 1 — singleton watchdog

### Internal input and output

| Part | Contract |
|---|---|
| Input | watchdog timer and durable Zone service observation timestamps |
| Coordination output | a single current JO replica owns the short Shared Redis watchdog lease |
| Durable output | only stale service `actual_state` is lowered through newer timestamp fence |
| Failure/retry | lease loss performs no write; PostgreSQL failure leaves no claim that recovery succeeded and next timer retries |

### Key contract

| Key / store | Operation | Owner / invariant |
|---|---|---|
| watchdog leader lease | Shared Redis | acquire/renew by one JO replica | stale owner cannot coordinate concurrent recovery |
| `zone_services.actual_observed_at` | PostgreSQL | timestamp-fenced update | report or watchdog with older evidence cannot win |

```mermaid
sequenceDiagram
    participant W as JO watchdog
    participant R as Shared Redis
    participant PG as PostgreSQL
    W->>R: acquire or renew watchdog lease
    alt not lease owner
        W->>W: wait and retry
    else leader
        W->>PG: read durable Zone observation timestamps
        W->>PG: mark stale observed service health down with newer timestamp fence
    end
```

The Redis lease prevents concurrent replicas from issuing the same recovery writes. The database timestamp fence rejects a stale owner after failover.

## Phase 2 — recovery boundary

### Internal input and output

| Part | Contract |
|---|---|
| Trigger | next valid Zone report with newer timestamp/fencing evidence |
| Output | report worker may advance observed health through its own fenced write-back |
| Prohibited output | no `zone_services.desired_state` mutation and no `zones.status` mutation |

```mermaid
sequenceDiagram
    participant W as JO watchdog
    participant PG as PostgreSQL
    participant K as Kafka Zone reports
    participant J as JO report worker
    W->>PG: timestamp-fenced stale health downgrade
    K-->>J: later valid report
    J->>PG: validate then write only newer observed health
    Note over W,J: neither component changes desired state or SRE lifecycle intent
```

Absence of a heartbeat is health evidence, not SRE authorization.
