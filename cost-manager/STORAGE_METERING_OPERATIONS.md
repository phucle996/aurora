# Storage Metering Operations

This runbook covers the report-driven storage metering path:

```text
Zone Public Edge -> Zone OTel -> Zone ClickHouse
  -> Zone Control JetStream outbox -> Kafka
  -> Job Orchestrator -> Shared Redis Stream
  -> Cost Engine -> Billing PostgreSQL
```

Billing PostgreSQL is the financial source of truth. Zone ClickHouse is a
bounded journal and aggregation store; Central does not need ClickHouse for
storage billing.

## Health signals and SLOs

Use bounded labels (`zone`, `workflow`, `reason`) only. Do not label metrics by
user, bucket name, object key, ticket, or access key.

| Signal | Target | Operator action |
| --- | --- | --- |
| Closed-window report freshness | p95 <= 20 minutes after the five-minute late grace | Inspect Zone Control assignment, ClickHouse completion markers, and outbox depth |
| Kafka/JO/Redis relay backlog | No sustained growth for 15 minutes | Check Kafka consumer lag, JO relay logs, and Shared Redis stream pending entries |
| `UNRATED` storage lines | 0 new lines after ownership/wallet projection lag clears | Repair the missing durable projection or wallet, then replay the report |
| `DEAD` reports | 0 unexpected reports | Review the stable error code; corrections remain quarantined until signed adjustment policy exists |
| Reconciliation delta | 0 for a closed window | Compare Zone report totals with accepted, relayed, inbox, settled, and unrated totals |

Important events include `ZONE_STORAGE_REPORT_OUTBOXED`,
`ZONE_STORAGE_REPORT_KAFKA_PUBLISHED`, `ZONE_STORAGE_METERING_AGGREGATION_FAILED`,
`ZONE_STORAGE_SCAN_ASSIGNMENT_LOST`, and the JO/Cost storage-metering relay
events. Assignment epoch and report ID are safe correlation fields.

## Reconciliation procedure

For one `(zone_id, window_start, window_end)`:

1. In Zone ClickHouse, record the closed-window transfer and capacity totals
   before the local journal retention expires.
2. Confirm the report has one `report_id`, a matching SHA-256, and the expected
   aggregate count.
3. Confirm Kafka accepted the report and JO committed the source offset only
   after `XADD` into `aurora:storage:usage:reports`.
4. In Billing PostgreSQL, compare `storage_usage_report_inbox`,
   `storage_usage_line_inbox`, `wallet_ledger_entries`, and `unrated_usage`.
   The report is complete only when every line is `SETTLED` or explicitly
   `UNRATED`; `PENDING` is an operational failure.
5. A repeated report ID or line identity must not create another ledger debit.
   Investigate a payload checksum collision as a data-integrity incident.

Do not use telemetry totals as a financial oracle. OTel and Victoria are
diagnostic; the Zone journal and Billing PostgreSQL inbox/ledger are the
workflow authorities at their respective boundaries.

## Replay and quarantine

- A transient Kafka, Redis, or Billing PostgreSQL failure leaves the source
  unsettled or the Redis entry pending. Restore the dependency and allow the
  consumer group to reclaim the record.
- A malformed or cross-Zone report is sanitized into the JO DLQ before its
  Kafka offset is committed. The DLQ contains a bounded error code and payload
  fingerprint, never the original report bytes.
- A missing/ambiguous owner or wallet is persisted as `UNRATED`. Repair the
  durable projection, then replay the report through the normal consumer path.
- A correction report is currently persisted as `DEAD` with its parent lineage.
  Do not edit or delete a settled line. A signed adjustment contract must be
  approved before any correction can produce a ledger entry.

## Safe rollback

1. Stop or scale the Cost Engine storage-report consumer to zero.
2. Do not delete Kafka records, Redis entries, report inbox rows, or ledger
   entries.
3. Keep Zone report publication and durable outbox state intact while deciding
   the next authoritative consumer.
4. Never re-enable a legacy Central ClickHouse debit path while report-driven
   settlement can charge the same usage. Recovery uses replay or an immutable
   adjustment, not in-place mutation.

## Retention defaults

The current Zone defaults are: OTel raw logs 7 days, access-event journal 30
days, capacity journal/completions 90 days, and JetStream report/DLQ streams 30
days. The development Kafka storage-report topic retains 7 days. Billing
PostgreSQL inbox and ledger retention follows the financial archive policy and
must not be shortened to match telemetry retention.
