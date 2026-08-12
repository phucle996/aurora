use std::collections::HashSet;
use std::sync::Arc;
use std::time::Duration;

use chrono::{DateTime, Utc};
use prost::Message;
use redis::Value;
use sha2::{Digest, Sha256};
use sqlx::{PgPool, Postgres};
use tokio::sync::watch;
use uuid::Uuid;

use crate::config::Config;
use crate::engine::storage_usage_report_proto::StorageUsageReportV1;
use crate::engine::{PricingRuntime, acquire_billing_lease, release_billing_lease};

const STORAGE_USAGE_STREAM: &str = "aurora:storage:usage:reports";
const STORAGE_USAGE_GROUP: &str = "cost-engine-storage-metering-v1";
const STORAGE_USAGE_LOCK: &str = "storage:report:settlement:lock";
const STORAGE_USAGE_FENCING_COUNTER: &str = "storage:report:settlement:fencing_counter";
const MAX_REPORT_BYTES: usize = 4 * 1024 * 1024;
const MAX_AGGREGATES: usize = 100_000;
const MAX_REPORT_WINDOW_MS: i64 = 86_400_000;
const MAX_CLOCK_SKEW_MS: i64 = 5 * 60 * 1_000;
const NETWORK_IN_CHARGE_KIND: &str = "storage.network_in.byte";
const NETWORK_OUT_CHARGE_KIND: &str = "storage.network_out.byte";
const STORAGE_CHARGE_KIND: &str = "storage.capacity.gb_hour";
const NETWORK_OUT_LINE_NAMESPACE: Uuid = Uuid::from_u128(0x1f8db4f2_1cf4_4d42_8e52_34b7f3f3f9a1);
const NETWORK_IN_LINE_NAMESPACE: Uuid = Uuid::from_u128(0x3d3ca119_2a2c_44b2_9fb5_31dba6d5e019);
const STORAGE_LINE_NAMESPACE: Uuid = Uuid::from_u128(0x8c9f7a46_3f03_4ef9_9d9a_1c5d4e8c2a70);

#[derive(Debug)]
struct StreamEntry {
    id: String,
    report_id: Option<String>,
    zone_id: Option<String>,
    report_sha256: Option<Vec<u8>>,
    payload: Option<Vec<u8>>,
}

enum EntryDisposition {
    Ack,
    Retry(String),
}

/// Report settlement is intentionally a workflow-local consumer. It owns the
/// Redis stream parsing, PostgreSQL inbox transition, wallet transaction and
/// ACK boundary in one module so those invariants cannot be accidentally
/// reused by unrelated billing workflows.
pub async fn run_storage_usage_report_settlement(
    config: Config,
    pg_pool: PgPool,
    mut redis_conn: redis::aio::MultiplexedConnection,
    pricing_runtime: Arc<PricingRuntime>,
    mut shutdown_rx: watch::Receiver<bool>,
) {
    let group_result: redis::RedisResult<()> = redis::cmd("XGROUP")
        .arg("CREATE")
        .arg(STORAGE_USAGE_STREAM)
        .arg(STORAGE_USAGE_GROUP)
        .arg("0-0")
        .arg("MKSTREAM")
        .query_async(&mut redis_conn)
        .await;
    if let Err(error) = group_result {
        let message = error.to_string();
        if !message.contains("BUSYGROUP") {
            eprintln!("Storage usage report consumer cannot create group: {message}");
            return;
        }
    }

    let consumer = format!(
        "cost-engine-{}-{}",
        std::env::var("HOSTNAME").unwrap_or_else(|_| "local".to_string()),
        std::process::id()
    );
    let mut claim_cursor = "0-0".to_string();
    eprintln!(
        "Storage usage report settlement enabled; stream={STORAGE_USAGE_STREAM} group={STORAGE_USAGE_GROUP} consumer={consumer}"
    );

    loop {
        if *shutdown_rx.borrow() {
            break;
        }

        let entries = match next_entries(
            &mut redis_conn,
            &consumer,
            &mut claim_cursor,
            &mut shutdown_rx,
        )
        .await
        {
            Ok(entries) => entries,
            Err(error) => {
                eprintln!("Storage usage report stream read failed: {error}");
                wait_or_shutdown(Duration::from_secs(2), &mut shutdown_rx).await;
                continue;
            }
        };

        for entry in entries {
            if *shutdown_rx.borrow() {
                break;
            }
            match settle_entry(&config, &pg_pool, &pricing_runtime, &mut redis_conn, entry).await {
                EntryDisposition::Ack => {}
                EntryDisposition::Retry(error) => {
                    eprintln!("Storage usage report remains pending: {error}");
                    wait_or_shutdown(Duration::from_secs(1), &mut shutdown_rx).await;
                    break;
                }
            }
        }
    }
}

async fn settle_entry(
    config: &Config,
    pg_pool: &PgPool,
    pricing_runtime: &Arc<PricingRuntime>,
    redis_conn: &mut redis::aio::MultiplexedConnection,
    entry: StreamEntry,
) -> EntryDisposition {
    let payload = match entry.payload.as_deref() {
        Some(payload) => payload,
        None => {
            return EntryDisposition::Retry(format!("stream entry {} has no payload", entry.id));
        }
    };
    let report = match decode_report(payload) {
        Ok(report) => report,
        Err(error) => {
            eprintln!(
                "Storage usage report entry {} rejected before settlement: {error}",
                entry.id
            );
            return if let Some(report) = decode_report_without_checksum(payload) {
                match persist_dead_report(pg_pool, &report, payload, error).await {
                    Ok(()) => match acknowledge_entry(redis_conn, &entry.id).await {
                        Ok(()) => EntryDisposition::Ack,
                        Err(ack_error) => EntryDisposition::Retry(ack_error),
                    },
                    Err(persist_error) => EntryDisposition::Retry(persist_error),
                }
            } else {
                // JO is the durable quarantine owner for malformed protobuf.
                // Keep the Redis entry pending if the defensive parser cannot
                // even recover a report identity.
                EntryDisposition::Retry(error.to_string())
            };
        }
    };

    if entry.report_id.as_deref() != Some(report.report_id.as_str())
        || entry.zone_id.as_deref() != Some(report.zone_id.as_str())
        || entry.report_sha256.as_deref() != Some(report.report_sha256.as_slice())
    {
        let error = "STORAGE_USAGE_REPORT_STREAM_FIELDS_MISMATCH";
        return match persist_dead_report(pg_pool, &report, payload, error).await {
            Ok(()) => match acknowledge_entry(redis_conn, &entry.id).await {
                Ok(()) => EntryDisposition::Ack,
                Err(ack_error) => EntryDisposition::Retry(ack_error),
            },
            Err(persist_error) => EntryDisposition::Retry(persist_error),
        };
    }

    if report.correction {
        // The current wire contract has unsigned quantities, so negative
        // adjustments cannot be represented safely yet. Keep the correction
        // durable and unrated until the approved correction policy lands.
        let error = "STORAGE_USAGE_CORRECTION_POLICY_NOT_ENABLED";
        return match persist_dead_report(pg_pool, &report, payload, error).await {
            Ok(()) => match acknowledge_entry(redis_conn, &entry.id).await {
                Ok(()) => EntryDisposition::Ack,
                Err(ack_error) => EntryDisposition::Retry(ack_error),
            },
            Err(persist_error) => EntryDisposition::Retry(persist_error),
        };
    }

    let Some(lease) = acquire_billing_lease(
        redis_conn,
        STORAGE_USAGE_LOCK,
        STORAGE_USAGE_FENCING_COUNTER,
        config.lock_ttl_secs,
    )
    .await
    else {
        return EntryDisposition::Retry("another settlement worker owns the fence".to_string());
    };

    let result = settle_report(
        pg_pool,
        pricing_runtime,
        &report,
        payload,
        lease.fencing_token,
    )
    .await;
    let lost = *lease.lost_rx.borrow();
    let mut release_conn = redis_conn.clone();
    release_billing_lease(lease, &mut release_conn).await;

    match result {
        Ok(()) if !lost => match acknowledge_entry(redis_conn, &entry.id).await {
            Ok(()) => EntryDisposition::Ack,
            Err(ack_error) => EntryDisposition::Retry(ack_error),
        },
        Ok(()) => EntryDisposition::Retry("settlement fence was lost before ACK".to_string()),
        Err(error) => EntryDisposition::Retry(error),
    }
}

async fn settle_report(
    pg_pool: &PgPool,
    pricing_runtime: &Arc<PricingRuntime>,
    report: &StorageUsageReportV1,
    payload: &[u8],
    fencing_token: i64,
) -> Result<(), String> {
    let window_start = DateTime::<Utc>::from_timestamp_millis(report.window_start_unix_ms)
        .ok_or_else(|| "report window start is invalid".to_string())?;
    let window_end = DateTime::<Utc>::from_timestamp_millis(report.window_end_unix_ms)
        .ok_or_else(|| "report window end is invalid".to_string())?;
    let report_id =
        Uuid::parse_str(&report.report_id).map_err(|_| "invalid report UUID".to_string())?;
    let zone_id = Uuid::parse_str(&report.zone_id).map_err(|_| "invalid Zone UUID".to_string())?;
    let mut pricing_leases: Vec<(&str, crate::engine::BillingPricingLease)> = Vec::new();
    for (charge_kind, has_quantity) in [
        (
            NETWORK_IN_CHARGE_KIND,
            report
                .aggregates
                .iter()
                .any(|aggregate| aggregate.upload_bytes > 0),
        ),
        (
            NETWORK_OUT_CHARGE_KIND,
            report
                .aggregates
                .iter()
                .any(|aggregate| aggregate.download_bytes > 0),
        ),
        (
            STORAGE_CHARGE_KIND,
            report
                .aggregates
                .iter()
                .any(|aggregate| aggregate.storage_gb_hours_micros > 0),
        ),
    ] {
        if !has_quantity {
            continue;
        }
        match pricing_runtime
            .begin_billing_run(
                charge_kind,
                report_id,
                zone_id,
                window_start,
                window_end,
                fencing_token,
            )
            .await
        {
            Ok(lease) => pricing_leases.push((charge_kind, lease)),
            Err(error) => {
                for (_, lease) in &pricing_leases {
                    let _ = pricing_runtime
                        .mark_billing_run_retrying(lease.billing_run_id)
                        .await;
                }
                return Err(error.to_string());
            }
        }
    }
    let pricing_lease_refs: Vec<(&str, &crate::engine::BillingPricingLease)> = pricing_leases
        .iter()
        .map(|(charge_kind, lease)| (*charge_kind, lease))
        .collect();
    let result = settle_report_transaction(
        pg_pool,
        &pricing_lease_refs,
        report,
        payload,
        window_start,
        window_end,
    )
    .await;
    match result {
        Ok(()) => {
            for (_, lease) in &pricing_leases {
                pricing_runtime
                    .complete_billing_run(lease.billing_run_id, window_end)
                    .await
                    .map_err(|error| error.to_string())?;
            }
            Ok(())
        }
        Err(error) => {
            for (_, lease) in &pricing_leases {
                let _ = pricing_runtime
                    .mark_billing_run_retrying(lease.billing_run_id)
                    .await;
            }
            Err(error)
        }
    }
}

async fn settle_report_transaction(
    pg_pool: &PgPool,
    pricing_leases: &[(&str, &crate::engine::BillingPricingLease)],
    report: &StorageUsageReportV1,
    payload: &[u8],
    window_start: DateTime<Utc>,
    window_end: DateTime<Utc>,
) -> Result<(), String> {
    let report_id =
        Uuid::parse_str(&report.report_id).map_err(|_| "invalid report UUID".to_string())?;
    let zone_id = Uuid::parse_str(&report.zone_id).map_err(|_| "invalid Zone UUID".to_string())?;
    let payload_sha256 = report.report_sha256.as_slice();
    let source_evidence_hash = report
        .report_sha256
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    let mut tx = pg_pool
        .begin()
        .await
        .map_err(|error| format!("begin storage report transaction: {error}"))?;

    sqlx::query(
        "INSERT INTO billing.storage_usage_report_inbox
         (report_id, zone_id, window_start, window_end, sequence, correction,
          correction_of_report_id, payload_sha256, payload, status)
         VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'PROCESSING')
         ON CONFLICT (report_id) DO NOTHING",
    )
    .bind(report_id)
    .bind(zone_id)
    .bind(window_start)
    .bind(window_end)
    .bind(i64::try_from(report.sequence).map_err(|_| "report sequence exceeds BIGINT".to_string())?)
    .bind(report.correction)
    .bind(if report.correction {
        Some(
            Uuid::parse_str(&report.correction_of_report_id)
                .map_err(|_| "invalid correction parent".to_string())?,
        )
    } else {
        None
    })
    .bind(payload_sha256)
    .bind(payload)
    .execute(&mut *tx)
    .await
    .map_err(|error| format!("insert storage report inbox: {error}"))?;

    let existing = sqlx::query_as::<Postgres, (Vec<u8>, String)>(
        "SELECT payload_sha256, status FROM billing.storage_usage_report_inbox WHERE report_id=$1 FOR UPDATE",
    )
    .bind(report_id)
    .fetch_one(&mut *tx)
    .await
    .map_err(|error| format!("read storage report inbox: {error}"))?;
    if existing.0 != payload_sha256 {
        let _ = tx.rollback().await;
        return Err("storage report ID was reused with a different checksum".to_string());
    }
    if existing.1 == "SETTLED" || existing.1 == "UNRATED" {
        tx.commit()
            .await
            .map_err(|error| format!("commit idempotent storage report: {error}"))?;
        return Ok(());
    }

    let mut has_unrated = false;
    for aggregate in &report.aggregates {
        let metrics = [
            (
                NETWORK_IN_CHARGE_KIND,
                aggregate.upload_bytes,
                "BYTE",
                "NETWORK_IN",
                &NETWORK_IN_LINE_NAMESPACE,
            ),
            (
                NETWORK_OUT_CHARGE_KIND,
                aggregate.download_bytes,
                "BYTE",
                "NETWORK_OUT",
                &NETWORK_OUT_LINE_NAMESPACE,
            ),
            (
                STORAGE_CHARGE_KIND,
                aggregate.storage_gb_hours_micros,
                "GB_HOUR_MICRO",
                "STORAGE",
                &STORAGE_LINE_NAMESPACE,
            ),
        ];
        for (charge_kind_code, raw_quantity, usage_unit, direction, line_namespace) in metrics {
            if raw_quantity == 0 {
                continue;
            }
            let resource_id = if !aggregate.resource_id.is_empty() {
                Uuid::parse_str(&aggregate.resource_id)
                    .map_err(|_| "invalid storage resource UUID".to_string())?
            } else {
                Uuid::new_v5(&STORAGE_LINE_NAMESPACE, aggregate.resource_name.as_bytes())
            };
            let durable_resource_name = if aggregate.resource_name.is_empty() {
                resource_id.to_string()
            } else {
                aggregate.resource_name.clone()
            };
            let resource_name = Some(durable_resource_name.as_str());
            let pricing_lease = pricing_leases
                .iter()
                .find(|(kind, _)| *kind == charge_kind_code)
                .map(|(_, lease)| *lease)
                .ok_or_else(|| format!("missing pricing lease for {charge_kind_code}"))?;
            let line_id = Uuid::new_v5(
                line_namespace,
                format!("{report_id}:{resource_id}:{charge_kind_code}").as_bytes(),
            );
            let quantity = i64::try_from(raw_quantity)
                .map_err(|_| "storage usage quantity exceeds BIGINT".to_string())?;
            let request_count = if charge_kind_code == STORAGE_CHARGE_KIND {
                0
            } else {
                i64::try_from(aggregate.request_count)
                    .map_err(|_| "request count exceeds BIGINT".to_string())?
            };
            sqlx::query(
                "INSERT INTO billing.storage_usage_line_inbox
                (line_id, report_id, zone_id, resource_id, resource_name, direction,
                  usage_quantity, usage_unit, request_count, module_code, charge_kind_code,
                  usage_settlement_run_id, pricing_schedule_version_id, pricing_checksum)
                 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'storage',$10,$11,$12,$13)
                 ON CONFLICT (report_id, resource_id, direction) DO NOTHING",
            )
            .bind(line_id)
            .bind(report_id)
            .bind(zone_id)
            .bind(resource_id)
            .bind(resource_name)
            .bind(direction)
            .bind(quantity)
            .bind(usage_unit)
            .bind(request_count)
            .bind(charge_kind_code)
            .bind(pricing_lease.billing_run_id)
            .bind(pricing_lease.snapshot.version_id)
            .bind(&pricing_lease.snapshot.checksum)
            .execute(&mut *tx)
            .await
            .map_err(|error| format!("insert storage usage line: {error}"))?;

            let line_status: String = sqlx::query_scalar(
                "SELECT status FROM billing.storage_usage_line_inbox WHERE line_id=$1 FOR UPDATE",
            )
            .bind(line_id)
            .fetch_one(&mut *tx)
            .await
            .map_err(|error| format!("read storage usage line: {error}"))?;
            if line_status == "SETTLED" || line_status == "UNRATED" {
                has_unrated |= line_status == "UNRATED";
                continue;
            }

            let owners = if aggregate.resource_id.is_empty() {
                sqlx::query_as::<Postgres, (Uuid, Uuid, String)>(
                    "SELECT resource_id, owner_id, owner_type::text
                     FROM billing.resource_ownership_projection
                     WHERE resource_type='STORAGE_BUCKET'
                       AND resource_name=$1
                       AND zone_id=$2
                       AND effective_from <= $3
                       AND (effective_to IS NULL OR $4 < effective_to)
                     ORDER BY ownership_version DESC
                     LIMIT 2",
                )
                .bind(aggregate.resource_name.as_str())
                .bind(zone_id)
                .bind(window_end)
                .bind(window_start)
                .fetch_all(&mut *tx)
                .await
            } else {
                sqlx::query_as::<Postgres, (Uuid, Uuid, String)>(
                    "SELECT resource_id, owner_id, owner_type::text
                     FROM billing.resource_ownership_projection
                     WHERE resource_type='STORAGE_BUCKET'
                       AND resource_id=$1
                       AND zone_id=$2
                       AND effective_from <= $3
                       AND (effective_to IS NULL OR $4 < effective_to)
                     ORDER BY ownership_version DESC
                     LIMIT 2",
                )
                .bind(resource_id)
                .bind(zone_id)
                .bind(window_end)
                .bind(window_start)
                .fetch_all(&mut *tx)
                .await
            }
            .map_err(|error| format!("resolve storage resource ownership: {error}"))?;

            if owners.len() != 1 {
                has_unrated = true;
                let reason = if owners.is_empty() {
                    "OWNER_PROJECTION_MISSING"
                } else {
                    "OWNER_PROJECTION_AMBIGUOUS"
                };
                persist_unrated_line(
                    &mut tx,
                    UnratedLine {
                        line_id,
                        source_report_id: report_id,
                        source_evidence_hash: &source_evidence_hash,
                        pricing_schedule_version_id: pricing_lease.snapshot.version_id,
                        resource_id,
                        metering_time: window_end,
                        quantity,
                        charge_kind_code,
                        usage_unit,
                        resource_name: durable_resource_name.as_str(),
                        owner_id: None,
                        owner_type: None,
                        reason,
                    },
                )
                .await?;
                continue;
            }

            let (owner_resource_id, owner_id, owner_type) = owners[0].clone();
            let cost = if charge_kind_code == STORAGE_CHARGE_KIND {
                pricing_lease
                    .snapshot
                    .charge_micro_units_for_storage_gb_hours_micros(raw_quantity)
                    .map_err(|error| error.to_string())?
            } else {
                pricing_lease
                    .snapshot
                    .charge_micro_units_for_bytes(raw_quantity)
                    .map_err(|error| error.to_string())?
            };
            if cost <= 0 {
                sqlx::query(
                    "UPDATE billing.storage_usage_line_inbox
                 SET amount_micro_units=0, owner_id=$1, owner_type=$2::billing.owner_type,
                     status='SETTLED', settled_at=NOW()
                 WHERE line_id=$3",
                )
                .bind(owner_id)
                .bind(&owner_type)
                .bind(line_id)
                .execute(&mut *tx)
                .await
                .map_err(|error| format!("mark zero-cost storage line: {error}"))?;
                continue;
            }

            let wallet = sqlx::query_as::<Postgres, (Uuid, i64, i64, i64, String, Option<String>)>(
                "SELECT id, cash_balance, promotional_balance, overdraft_limit, status::text,
                        restriction_reason
             FROM billing.wallets
             WHERE owner_id=$1 AND owner_type=$2::billing.owner_type AND currency='USD'
             FOR UPDATE",
            )
            .bind(owner_id)
            .bind(&owner_type)
            .fetch_optional(&mut *tx)
            .await
            .map_err(|error| format!("lock storage owner wallet: {error}"))?;
            let Some((
                wallet_id,
                cash_balance,
                promotional_balance,
                overdraft_limit,
                status,
                _restriction_reason,
            )) = wallet
            else {
                has_unrated = true;
                persist_unrated_line(
                    &mut tx,
                    UnratedLine {
                        line_id,
                        source_report_id: report_id,
                        source_evidence_hash: &source_evidence_hash,
                        pricing_schedule_version_id: pricing_lease.snapshot.version_id,
                        resource_id,
                        metering_time: window_end,
                        quantity,
                        charge_kind_code,
                        usage_unit,
                        resource_name: durable_resource_name.as_str(),
                        owner_id: Some(owner_id),
                        owner_type: Some(&owner_type),
                        reason: "WALLET_MISSING",
                    },
                )
                .await?;
                continue;
            };
            if status == "CLOSED" {
                has_unrated = true;
                persist_unrated_line(
                    &mut tx,
                    UnratedLine {
                        line_id,
                        source_report_id: report_id,
                        source_evidence_hash: &source_evidence_hash,
                        pricing_schedule_version_id: pricing_lease.snapshot.version_id,
                        resource_id,
                        metering_time: window_end,
                        quantity,
                        charge_kind_code,
                        usage_unit,
                        resource_name: durable_resource_name.as_str(),
                        owner_id: Some(owner_id),
                        owner_type: Some(&owner_type),
                        reason: "WALLET_CLOSED",
                    },
                )
                .await?;
                continue;
            }
            if status == "PENDING_ACTIVATION" {
                has_unrated = true;
                persist_unrated_line(
                    &mut tx,
                    UnratedLine {
                        line_id,
                        source_report_id: report_id,
                        source_evidence_hash: &source_evidence_hash,
                        pricing_schedule_version_id: pricing_lease.snapshot.version_id,
                        resource_id,
                        metering_time: window_end,
                        quantity,
                        charge_kind_code,
                        usage_unit,
                        resource_name: durable_resource_name.as_str(),
                        owner_id: Some(owner_id),
                        owner_type: Some(&owner_type),
                        reason: "WALLET_PENDING_ACTIVATION",
                    },
                )
                .await?;
                continue;
            }

            let promo_debit = promotional_balance.min(cost);
            let new_promotional_balance = promotional_balance - promo_debit;
            let cash_debit = cost - promo_debit;
            let new_cash_balance = cash_balance
                .checked_sub(cash_debit)
                .ok_or_else(|| "wallet cash balance exceeds BIGINT".to_string())?;
            let mut new_status = status.clone();
            if new_cash_balance.saturating_add(overdraft_limit) <= 0 && status == "ACTIVE" {
                new_status = "SUSPENDED".to_string();
            }

            // A crash can leave a durable ledger row visible while a future
            // retry is still looking at a pending inbox line. Reconcile that
            // identity before touching the wallet; never debit around a
            // duplicate-ledger conflict.
            let ledger_already_exists: bool = sqlx::query_scalar(
                "SELECT EXISTS (SELECT 1 FROM billing.wallet_ledger_entries WHERE id=$1)",
            )
            .bind(line_id)
            .fetch_one(&mut *tx)
            .await
            .map_err(|error| format!("check storage usage ledger identity: {error}"))?;
            if ledger_already_exists {
                sqlx::query(
                    "UPDATE billing.storage_usage_line_inbox
                 SET amount_micro_units=$1, owner_id=$2, owner_type=$3::billing.owner_type,
                     status='SETTLED', settled_at=NOW()
                 WHERE line_id=$4",
                )
                .bind(cost)
                .bind(owner_id)
                .bind(&owner_type)
                .bind(line_id)
                .execute(&mut *tx)
                .await
                .map_err(|error| format!("reconcile storage usage ledger identity: {error}"))?;
                continue;
            }

            let mut wallet_version = 0_i64;
            sqlx::query(
                "UPDATE billing.wallets
             SET cash_balance=$1, promotional_balance=$2,
                 status=$3::billing.wallet_lifecycle_status,
                 restriction_reason=CASE WHEN $3='SUSPENDED' AND $4='ACTIVE' THEN 'CREDIT_EXHAUSTED' WHEN $3='ACTIVE' THEN NULL ELSE restriction_reason END,
                 status_changed_at=CASE WHEN status::text IS DISTINCT FROM $3 THEN NOW() ELSE status_changed_at END,
                 version=version+1, updated_at=NOW()
             WHERE id=$5
             RETURNING version",
            )
            .bind(new_cash_balance)
            .bind(new_promotional_balance)
            .bind(&new_status)
            .bind(&status)
            .bind(wallet_id)
            .fetch_one(&mut *tx)
            .await
            .map(|row: sqlx::postgres::PgRow| {
                use sqlx::Row;
                wallet_version = row.get("version");
            })
            .map_err(|error| format!("update storage owner wallet: {error}"))?;

            if new_status != status {
                let admission_mode = if new_status == "ACTIVE" {
                    "ALLOW"
                } else {
                    "SUSPEND_BILLABLE"
                };
                let admission_reason = if new_status == "SUSPENDED" {
                    Some("CREDIT_EXHAUSTED")
                } else {
                    None
                };
                sqlx::query(
                    "INSERT INTO billing.wallet_admission_outbox
                     (event_id, wallet_id, owner_id, owner_type, wallet_version, admission_mode, restriction_reason, effective_at)
                     VALUES ($1,$2,$3,$4::billing.owner_type,$5,$6,$7,NOW())",
                )
                .bind(Uuid::now_v7())
                .bind(wallet_id)
                .bind(owner_id)
                .bind(&owner_type)
                .bind(wallet_version)
                .bind(admission_mode)
                .bind(admission_reason)
                .execute(&mut *tx)
                .await
                .map_err(|error| format!("write storage wallet admission outbox: {error}"))?;
            }

            let ledger_result = sqlx::query(
                "INSERT INTO billing.wallet_ledger_entries
             (id, wallet_id, owner_id, owner_type, amount_micro_units,
              cash_balance_after, promotional_balance_after, currency, entry_type,
              module_code, charge_kind_code, reference_id, description, usage_settlement_run_id,
              pricing_schedule_id, pricing_schedule_version_id, pricing_checksum,
              resource_id, resource_type, usage_quantity,
              usage_unit, occurred_at, source_evidence_hash)
             VALUES ($1,$2,$3,$4::billing.owner_type,$5,$6,$7,'USD','USAGE_CHARGE',
                     'storage',$8,$9,$10,$11,$12,$13,$14,$15,'STORAGE_BUCKET',
                     $16,$17,$18,$19)",
            )
            .bind(line_id)
            .bind(wallet_id)
            .bind(owner_id)
            .bind(&owner_type)
            .bind(-cost)
            .bind(new_cash_balance)
            .bind(new_promotional_balance)
            .bind(charge_kind_code)
            .bind(format!(
                "storage-report:{report_id}:{resource_id}:{charge_kind_code}"
            ))
            .bind(format!(
                "Storage {charge_kind_code} quantity {quantity} using report {report_id}"
            ))
            .bind(pricing_lease.billing_run_id)
            .bind(pricing_lease.snapshot.pricing_schedule_id)
            .bind(pricing_lease.snapshot.version_id)
            .bind(&pricing_lease.snapshot.checksum)
            .bind(owner_resource_id)
            .bind(quantity)
            .bind(usage_unit)
            .bind(window_end)
            .bind(&source_evidence_hash)
            .execute(&mut *tx)
            .await;
            if let Err(error) = ledger_result {
                if error
                    .as_database_error()
                    .and_then(|database| database.code())
                    .as_deref()
                    == Some("23505")
                {
                    return Err(
                        "storage usage ledger identity appeared concurrently; retry transaction"
                            .to_string(),
                    );
                }
                return Err(format!("insert storage usage ledger: {error}"));
            }

            sqlx::query(
                "UPDATE billing.storage_usage_line_inbox
             SET amount_micro_units=$1, owner_id=$2, owner_type=$3::billing.owner_type,
                 status='SETTLED', settled_at=NOW()
             WHERE line_id=$4",
            )
            .bind(cost)
            .bind(owner_id)
            .bind(&owner_type)
            .bind(line_id)
            .execute(&mut *tx)
            .await
            .map_err(|error| format!("mark storage usage line settled: {error}"))?;
        }
    }

    let (pending, unrated): (i64, i64) = sqlx::query_as(
        "SELECT COUNT(*) FILTER (WHERE status='PENDING'),
                COUNT(*) FILTER (WHERE status='UNRATED')
         FROM billing.storage_usage_line_inbox WHERE report_id=$1",
    )
    .bind(report_id)
    .fetch_one(&mut *tx)
    .await
    .map_err(|error| format!("read storage report line status: {error}"))?;
    if pending != 0 {
        return Err("storage report has pending lines after settlement".to_string());
    }
    let report_status = if has_unrated || unrated > 0 {
        "UNRATED"
    } else {
        "SETTLED"
    };
    sqlx::query(
        "UPDATE billing.storage_usage_report_inbox
         SET status=$1, settled_at=NOW(), last_error=NULL
         WHERE report_id=$2",
    )
    .bind(report_status)
    .bind(report_id)
    .execute(&mut *tx)
    .await
    .map_err(|error| format!("mark storage report settled: {error}"))?;
    tx.commit()
        .await
        .map_err(|error| format!("commit storage report settlement: {error}"))?;
    Ok(())
}

struct UnratedLine<'a> {
    line_id: Uuid,
    source_report_id: Uuid,
    source_evidence_hash: &'a str,
    pricing_schedule_version_id: Uuid,
    resource_id: Uuid,
    metering_time: DateTime<Utc>,
    quantity: i64,
    charge_kind_code: &'a str,
    usage_unit: &'a str,
    resource_name: &'a str,
    owner_id: Option<Uuid>,
    owner_type: Option<&'a str>,
    reason: &'a str,
}

async fn persist_unrated_line(
    tx: &mut sqlx::Transaction<'_, Postgres>,
    line: UnratedLine<'_>,
) -> Result<(), String> {
    sqlx::query(
        "INSERT INTO billing.unrated_usage
         (id, module_code, charge_kind_code, resource_type, resource_id, resource_name,
          metering_hour, usage_quantity, usage_unit, reason, source_report_id,
          source_evidence_hash, pricing_schedule_version_id)
         VALUES ($1,'storage',$2,'STORAGE_BUCKET',$3,$4,$5,$6,$7,$8,$9,$10,$11)
         ON CONFLICT (id) DO UPDATE
         SET retry_count=billing.unrated_usage.retry_count+1,
             reason=EXCLUDED.reason,
             source_report_id=EXCLUDED.source_report_id,
             source_evidence_hash=EXCLUDED.source_evidence_hash,
             pricing_schedule_version_id=EXCLUDED.pricing_schedule_version_id,
             updated_at=NOW()",
    )
    .bind(line.line_id)
    .bind(line.charge_kind_code)
    .bind(line.resource_id)
    .bind(line.resource_name)
    .bind(line.metering_time)
    .bind(line.quantity)
    .bind(line.usage_unit)
    .bind(line.reason)
    .bind(line.source_report_id)
    .bind(line.source_evidence_hash)
    .bind(line.pricing_schedule_version_id)
    .execute(&mut **tx)
    .await
    .map_err(|error| format!("persist unrated storage usage: {error}"))?;
    sqlx::query(
        "UPDATE billing.storage_usage_line_inbox
         SET status='UNRATED', reason=$1,
             owner_id=COALESCE($2, owner_id),
             owner_type=COALESCE($3::billing.owner_type, owner_type),
             settled_at=NOW()
         WHERE line_id=$4",
    )
    .bind(line.reason)
    .bind(line.owner_id)
    .bind(line.owner_type)
    .bind(line.line_id)
    .execute(&mut **tx)
    .await
    .map_err(|error| format!("mark storage line unrated: {error}"))?;
    Ok(())
}

async fn persist_dead_report(
    pg_pool: &PgPool,
    report: &StorageUsageReportV1,
    payload: &[u8],
    error: &'static str,
) -> Result<(), String> {
    let report_id =
        Uuid::parse_str(&report.report_id).map_err(|_| "invalid report UUID".to_string())?;
    let zone_id = Uuid::parse_str(&report.zone_id).map_err(|_| "invalid Zone UUID".to_string())?;
    let window_start = DateTime::<Utc>::from_timestamp_millis(report.window_start_unix_ms)
        .ok_or_else(|| "report window start is invalid".to_string())?;
    let window_end = DateTime::<Utc>::from_timestamp_millis(report.window_end_unix_ms)
        .ok_or_else(|| "report window end is invalid".to_string())?;
    sqlx::query(
        "INSERT INTO billing.storage_usage_report_inbox
         (report_id, zone_id, window_start, window_end, sequence, correction,
          correction_of_report_id, payload_sha256, payload, status, last_error)
         VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'DEAD',$10)
         ON CONFLICT (report_id) DO UPDATE
         SET status='DEAD', last_error=EXCLUDED.last_error, retry_count=billing.storage_usage_report_inbox.retry_count+1
         WHERE billing.storage_usage_report_inbox.status NOT IN ('SETTLED','UNRATED')",
    )
    .bind(report_id)
    .bind(zone_id)
    .bind(window_start)
    .bind(window_end)
    .bind(i64::try_from(report.sequence).map_err(|_| "report sequence exceeds BIGINT".to_string())?)
    .bind(report.correction)
    .bind(if report.correction {
        Some(Uuid::parse_str(&report.correction_of_report_id).map_err(|_| "invalid correction parent".to_string())?)
    } else {
        None
    })
    .bind(report.report_sha256.as_slice())
    .bind(payload)
    .bind(error)
    .execute(pg_pool)
    .await
    .map_err(|error| format!("persist rejected storage report: {error}"))?;
    Ok(())
}

async fn acknowledge_entry(
    redis_conn: &mut redis::aio::MultiplexedConnection,
    entry_id: &str,
) -> Result<(), String> {
    redis::Script::new(
        "local acked=redis.call('XACK',KEYS[1],ARGV[1],ARGV[2]); \
         if acked == 1 then return redis.call('XDEL',KEYS[1],ARGV[2]) end; \
         return 0",
    )
    .key(STORAGE_USAGE_STREAM)
    .arg(STORAGE_USAGE_GROUP)
    .arg(entry_id)
    .invoke_async::<_, i32>(redis_conn)
    .await
    .map(|_| ())
    .map_err(|error| format!("acknowledge storage usage report entry {entry_id}: {error}"))
}

async fn next_entries(
    redis_conn: &mut redis::aio::MultiplexedConnection,
    consumer: &str,
    claim_cursor: &mut String,
    shutdown_rx: &mut watch::Receiver<bool>,
) -> Result<Vec<StreamEntry>, String> {
    let claimed: Value = redis::cmd("XAUTOCLAIM")
        .arg(STORAGE_USAGE_STREAM)
        .arg(STORAGE_USAGE_GROUP)
        .arg(consumer)
        .arg(5_000)
        .arg(&*claim_cursor)
        .arg("COUNT")
        .arg(50)
        .query_async(redis_conn)
        .await
        .map_err(|error| format!("XAUTOCLAIM: {error}"))?;
    if let Value::Bulk(parts) = &claimed {
        if let Some(Value::Data(next)) = parts.first() {
            *claim_cursor = String::from_utf8_lossy(next).into_owned();
        }
        if let Some(Value::Bulk(entries)) = parts.get(1) {
            let parsed = parse_entries(entries);
            if !parsed.is_empty() {
                return Ok(parsed);
            }
        }
    }
    if *shutdown_rx.borrow() {
        return Ok(Vec::new());
    }
    let reply: Value = redis::cmd("XREADGROUP")
        .arg("GROUP")
        .arg(STORAGE_USAGE_GROUP)
        .arg(consumer)
        .arg("BLOCK")
        .arg(2_000)
        .arg("COUNT")
        .arg(50)
        .arg("STREAMS")
        .arg(STORAGE_USAGE_STREAM)
        .arg(">")
        .query_async(redis_conn)
        .await
        .map_err(|error| format!("XREADGROUP: {error}"))?;
    let Value::Bulk(streams) = reply else {
        return Ok(Vec::new());
    };
    let Some(Value::Bulk(stream_data)) = streams.first() else {
        return Ok(Vec::new());
    };
    let Some(Value::Bulk(entries)) = stream_data.get(1) else {
        return Ok(Vec::new());
    };
    Ok(parse_entries(entries))
}

fn parse_entries(entries: &[Value]) -> Vec<StreamEntry> {
    entries.iter().filter_map(parse_entry).collect()
}

fn parse_entry(value: &Value) -> Option<StreamEntry> {
    let Value::Bulk(parts) = value else {
        return None;
    };
    let Value::Data(id) = parts.first()? else {
        return None;
    };
    let Value::Bulk(fields) = parts.get(1)? else {
        return None;
    };
    let mut entry = StreamEntry {
        id: String::from_utf8_lossy(id).into_owned(),
        report_id: None,
        zone_id: None,
        report_sha256: None,
        payload: None,
    };
    for field in fields.chunks(2) {
        if field.len() != 2 {
            continue;
        }
        let Value::Data(name) = &field[0] else {
            continue;
        };
        let Value::Data(value) = &field[1] else {
            continue;
        };
        match name.as_slice() {
            b"report_id" => entry.report_id = Some(String::from_utf8_lossy(value).into_owned()),
            b"zone_id" => entry.zone_id = Some(String::from_utf8_lossy(value).into_owned()),
            b"report_sha256" => entry.report_sha256 = Some(value.clone()),
            b"payload" => entry.payload = Some(value.clone()),
            _ => {}
        }
    }
    Some(entry)
}

fn decode_report(payload: &[u8]) -> Result<StorageUsageReportV1, &'static str> {
    if payload.is_empty() || payload.len() > MAX_REPORT_BYTES {
        return Err("STORAGE_USAGE_REPORT_SIZE_INVALID");
    }
    let report =
        StorageUsageReportV1::decode(payload).map_err(|_| "STORAGE_USAGE_REPORT_PROTO_INVALID")?;
    validate_report_shape(&report)?;
    let mut canonical = report.clone();
    canonical.report_sha256.clear();
    let digest = Sha256::digest(canonical.encode_to_vec());
    if report.report_sha256.as_slice() != digest.as_slice() {
        return Err("STORAGE_USAGE_REPORT_CHECKSUM_INVALID");
    }
    Ok(report)
}

fn decode_report_without_checksum(payload: &[u8]) -> Option<StorageUsageReportV1> {
    let report = StorageUsageReportV1::decode(payload).ok()?;
    validate_report_shape(&report).ok()?;
    Some(report)
}

fn validate_report_shape(report: &StorageUsageReportV1) -> Result<(), &'static str> {
    let report_id =
        Uuid::parse_str(&report.report_id).map_err(|_| "STORAGE_USAGE_REPORT_ID_INVALID")?;
    let zone_id =
        Uuid::parse_str(&report.zone_id).map_err(|_| "STORAGE_USAGE_REPORT_ZONE_INVALID")?;
    if report.schema_version != 1
        || report_id.is_nil()
        || zone_id.is_nil()
        || report.window_end_unix_ms <= report.window_start_unix_ms
        || report.window_end_unix_ms - report.window_start_unix_ms > MAX_REPORT_WINDOW_MS
        || report.aggregates.is_empty()
        || report.aggregates.len() > MAX_AGGREGATES
        || report.report_sha256.len() != 32
    {
        return Err("STORAGE_USAGE_REPORT_CONTRACT_INVALID");
    }
    let now = Utc::now().timestamp_millis();
    if report.window_end_unix_ms > now.saturating_add(MAX_CLOCK_SKEW_MS)
        || report.window_start_unix_ms < now.saturating_sub(7 * 86_400_000)
    {
        return Err("STORAGE_USAGE_REPORT_TIME_INVALID");
    }
    let mut resources = HashSet::with_capacity(report.aggregates.len());
    for aggregate in &report.aggregates {
        let identity = if !aggregate.resource_id.is_empty() {
            let resource_id = Uuid::parse_str(&aggregate.resource_id)
                .map_err(|_| "STORAGE_USAGE_REPORT_RESOURCE_INVALID")?;
            if resource_id.is_nil() {
                return Err("STORAGE_USAGE_REPORT_RESOURCE_INVALID");
            }
            format!("id:{resource_id}")
        } else if !aggregate.resource_name.is_empty() {
            if aggregate.resource_name.len() > 255
                || (!aggregate.resource_name.starts_with("ws-")
                    && !aggregate.resource_name.starts_with("tn-"))
            {
                return Err("STORAGE_USAGE_REPORT_RESOURCE_NAME_INVALID");
            }
            format!("name:{}", aggregate.resource_name)
        } else {
            return Err("STORAGE_USAGE_REPORT_RESOURCE_INVALID");
        };
        if !resources.insert(identity) {
            return Err("STORAGE_USAGE_REPORT_RESOURCE_DUPLICATE");
        }
        if i64::try_from(aggregate.download_bytes).is_err()
            || i64::try_from(aggregate.upload_bytes).is_err()
            || i64::try_from(aggregate.request_count).is_err()
            || i64::try_from(aggregate.storage_bytes).is_err()
            || i64::try_from(aggregate.storage_gb_hours_micros).is_err()
        {
            return Err("STORAGE_USAGE_REPORT_NUMERIC_INVALID");
        }
        if aggregate.storage_gb_hours_micros > 0 && aggregate.resource_name.is_empty() {
            return Err("STORAGE_USAGE_REPORT_RESOURCE_NAME_INVALID");
        }
        if aggregate.upload_bytes == 0
            && aggregate.download_bytes == 0
            && aggregate.storage_gb_hours_micros == 0
        {
            return Err("STORAGE_USAGE_REPORT_QUANTITY_INVALID");
        }
    }
    if report.correction {
        let parent = Uuid::parse_str(&report.correction_of_report_id)
            .map_err(|_| "STORAGE_USAGE_REPORT_CORRECTION_INVALID")?;
        if parent.is_nil() || parent == report_id {
            return Err("STORAGE_USAGE_REPORT_CORRECTION_INVALID");
        }
    } else if !report.correction_of_report_id.is_empty() {
        return Err("STORAGE_USAGE_REPORT_CORRECTION_INVALID");
    }
    Ok(())
}

async fn wait_or_shutdown(duration: Duration, shutdown_rx: &mut watch::Receiver<bool>) {
    tokio::select! {
        _ = tokio::time::sleep(duration) => {}
        _ = shutdown_rx.changed() => {}
    }
}

#[cfg(test)]
#[path = "../../../tests/unit/storage_usage_report.rs"]
mod tests;
