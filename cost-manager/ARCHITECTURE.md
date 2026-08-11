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
                                                        Cost Engine <- Zone report relay
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

Zone hourly usage report -> Job Orchestrator Kafka relay -> Shared Redis
  -> Cost Engine fenced settlement -> wallet + ledger transaction

Tier version transaction + pricing outbox -> relay -> Redis PubSub
  -> Engine checksum-validated preload -> safe-boundary COW activation
```

## Storage metering runtime

The Engine no longer reads a Central ClickHouse projection. Zone-local
metering journals are aggregated into a versioned report and relayed through
Kafka and Shared Redis:

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
JO validates size, window, identity, correction lineage and SHA-256 before a
report reaches Redis. The Engine validates again, opens one pricing run for
each of `NETWORK_IN`, `NETWORK_OUT` and `STORAGE`, resolves ownership in
Billing PostgreSQL, and atomically mutates wallet plus immutable ledger.
Replays are idempotent and ACK/XDEL occurs only after commit and an intact
billing fence. Corrections are quarantined as `DEAD` until the signed
adjustment policy is approved.

Each public or ACR-local HTTP workflow is documented independently in
[`god_view/billing/`](../god_view/billing/). Runtime contracts
are included in the terminal phase of the workflow that triggers them; they are
not standalone generic God Views.
