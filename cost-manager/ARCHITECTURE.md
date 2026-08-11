# Cost Manager Architecture

Cost Manager owns Billing PostgreSQL, payment-intent settlement, wallet and
ledger mutations, Tier pricing, and the storage-usage billing engine. IAM owns
identity, permissions and the Cloud session; ACR is the only HTTP security
boundary for Cost Manager.

```text
Cloud / Cost Console
        |
 Envoy -> ACR -- verified identity, owner rewrite, proof --> Cost API
                    |                                      |
                    |                                      +--> Billing PostgreSQL
                    |                                      |     wallets, intents,
                    |                                      |     immutable ledger,
                    |                                      |     tier catalog/outbox
                    v                                      |
             Auth-State Redis                              v
             Trinity / Billing Alias                 Shared Redis
                                                     streams, PubSub, locks
                                                        |             |
                     IAM wallet-provision outbox -------+             |
                                                                      v
                                                        Cost Engine -> ClickHouse
                                                            -> wallet/ledger
```

## Ownership boundaries

- **ACR** verifies Cloud Trinity or host-bound Billing Alias, selects the
  verified personal/tenant owner branch, strips client identity headers and
  validates one-time critical proof. It never reads Billing tables.
- **Cost API** owns HTTP validation, exact Billing authorization, payment
  intent/referral/wallet/Tier transactions and provider-webhook verification.
- **Billing PostgreSQL** is durable SoT for money, Tier versions, inboxes and
  resource ownership projections. Redis never replaces a money transaction.
- **Cost Engine** pins immutable Tier versions for each run, resolves ownership
  at the metering timestamp, and debits wallet plus immutable ledger atomically.
- **Shared Redis** carries at-least-once wallet-provision and ownership events;
  PubSub pricing messages are latency hints only. Durable inbox/outbox and
  periodic reconciliation provide recovery.

## Runtime pipelines

```text
IAM activation/tenant creation -> owner-specific outbox -> Shared Redis Stream
  -> Cost provision consumer -> inbox + PENDING_ACTIVATION wallet transaction

Storage job result -> Controlplane storage outbox -> Job Orchestrator
  -> Shared Redis ownership stream -> Cost ownership projection transaction
  -> Cost Engine ClickHouse usage read -> wallet + ledger transaction

Tier version transaction + pricing outbox -> relay -> Redis PubSub
  -> Engine checksum-validated preload -> safe-boundary COW activation
```

## Storage metering transition

The current engine still reads the Central ClickHouse `hourly_metering_agg`
projection for its charge-producing storage egress loop. The replacement path
is intentionally staged and is not charge-producing yet:

```text
Zone Public Edge -> Zone OTel -> Zone ClickHouse request journal
  -> Zone Control closed-window report outbox
  -> Kafka storage.usage.reports.v1
  -> Job Orchestrator validation -> Shared Redis stream
  -> Cost Engine report consumer -> Billing PostgreSQL wallet/ledger
```

The canonical input is
[`StorageUsageReportV1`](../proto/cost-manager/engine/storage_usage_report.proto).
Zone ClickHouse is local journal/aggregation state and does not choose a payer.
The Job Orchestrator relay is shadow-mode infrastructure; it validates size,
window, UUID, correction lineage and SHA-256 before a report reaches the Redis
handoff. Wallet mutation remains on the existing ClickHouse path until the
settlement, replay and reconciliation gates in the God Plan are explicitly
closed. The two charge-producing paths must never run at the same time.

Each public or ACR-local HTTP workflow is documented independently in
[`god_view/billing/README.md`](../god_view/billing/README.md). Runtime contracts
are included in the terminal phase of the workflow that triggers them; they are
not standalone generic God Views.
