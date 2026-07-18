# Wallet and ledger God View

## 1. Money model

All monetary values are signed `BIGINT` USD micro-units. One USD equals `1_000_000` micro-units. Tier
prices, wallet balances, promotional grants and ledger entries therefore share one exact integer unit.

```mermaid
flowchart LR
    USAGE[Rated usage] --> TX[PostgreSQL transaction]
    TX --> LOCK[SELECT wallet FOR UPDATE]
    LOCK --> BAL[Update cash and promo balance]
    BAL --> LEDGER[Insert immutable ledger entry]
    LEDGER --> COMMIT[Commit]
```

## 2. Invariants

- A wallet is unique by `(owner_id, owner_type, currency)`.
- `wallet_ledger_entries` is append-only. Usage charges are negative; top-ups and grants are positive.
- Wallet mutation and ledger insert commit in the same transaction.
- Usage charge id is deterministic from service/resource/hour so crash or retry cannot double-debit.
- Duplicate ledger id rolls back the attempted balance mutation and is treated as idempotent success.
- Promotional and cash balances are separate. Promotional credit is spent first and cannot become cash.
- Missing owner/wallet produces an `unrated_usage` row with deterministic id rather than a skipped charge.
- Each ledger usage row pins billing run, tier version, resource and ownership lineage.

## 3. HA/race controls

| Race | Control |
|---|---|
| Two engine replicas debit the same wallet | Redis lease + durable fencing + wallet row lock |
| Lease expires during a row transaction | Billing-run fencing token checked under row lock |
| Same ClickHouse aggregate replayed | Deterministic ledger primary key |
| Wallet provisioned after usage | `unrated_usage` durable retry queue |
| Concurrent top-up and usage charge | Same wallet `FOR UPDATE` serialization |

## 4. Source map

| Concern | Source |
|---|---|
| Wallet/ledger schema | `cost-manager/api/migrations/000002_tables.up.sql` |
| Wallet/index constraints | `cost-manager/api/migrations/000003_indexes.up.sql` |
| Egress debit transaction | `cost-manager/engine/src/service/storage/egress_billing.rs` |
| Billing run/version pin | `cost-manager/engine/src/engine/runtime.rs` |

