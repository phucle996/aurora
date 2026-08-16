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
use crate::engine::{
    BillingRunCommand, PricingRuntime, RateAdjustmentSnapshot, UsageChargeCommand,
    UsageChargeOutcome, acquire_billing_lease, release_billing_lease, settle_usage_charge,
};
use crate::service::storage::storage_usage_report_proto::StorageUsageReportV1;
use crate::service::storage::zone_adjustment_checksum;

const STORAGE_USAGE_STREAM: &str = "aurora:storage:usage:reports";
const STORAGE_USAGE_GROUP: &str = "cost-engine-storage-metering-v1";
const STORAGE_USAGE_DLQ: &str = "aurora:storage:usage:reports:dlq";
const STORAGE_USAGE_LOCK_PREFIX: &str = "storage:report:settlement:lock";
const STORAGE_USAGE_FENCING_COUNTER: &str = "storage:report:settlement:fencing_counter";
const MAX_REPORT_BYTES: usize = 4 * 1024 * 1024;
const MAX_AGGREGATES: usize = 100_000;
const MAX_CLOCK_SKEW_MS: i64 = 5 * 60 * 1_000;
const HOURLY_WINDOW_MS: i64 = 3_600_000;
const NETWORK_IN_CHARGE_KIND: &str = "storage.network_in.byte";
const NETWORK_OUT_CHARGE_KIND: &str = "storage.network_out.byte";
const STORAGE_CHARGE_KIND: &str = "storage.capacity.gb_hour";
const NETWORK_OUT_LINE_NAMESPACE: Uuid = Uuid::from_u128(0x1f8db4f2_1cf4_4d42_8e52_34b7f3f3f9a1);
const NETWORK_IN_LINE_NAMESPACE: Uuid = Uuid::from_u128(0x3d3ca119_2a2c_44b2_9fb5_31dba6d5e019);
const STORAGE_LINE_NAMESPACE: Uuid = Uuid::from_u128(0x8c9f7a46_3f03_4ef9_9d9a_1c5d4e8c2a70);
const REPORT_NAMESPACE: Uuid = Uuid::from_u128(0x5f0a_8e90_46e5_4fbb_8c01_7108_7f8c_1f22);

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

async fn resolve_storage_zone_adjustment(
    pg_pool: &PgPool,
    zone_id: Uuid,
    at: DateTime<Utc>,
) -> Result<Option<RateAdjustmentSnapshot>, String> {
    let row = sqlx::query_as::<Postgres, (Uuid, i32, DateTime<Utc>, i64, i64, String)>(
        "SELECT id, version_number, effective_from, multiplier_numerator,
                multiplier_denominator, checksum
         FROM billing.storage_zone_price_adjustment_versions
         WHERE zone_id=$1 AND status <> 'CANCELLED'
           AND effective_from <= $2 AND (effective_to IS NULL OR $2 < effective_to)
         ORDER BY version_number DESC
         LIMIT 1",
    )
    .bind(zone_id)
    .bind(at)
    .fetch_optional(pg_pool)
    .await
    .map_err(|error| format!("resolve Storage Zone price adjustment: {error}"))?;
    let Some((id, version_number, effective_from, numerator, denominator, checksum)) = row else {
        return Ok(None);
    };
    let computed = zone_adjustment_checksum(
        zone_id,
        version_number,
        effective_from,
        numerator,
        denominator,
    );
    if checksum != computed {
        return Err(format!(
            "Storage Zone price adjustment {id} checksum mismatch"
        ));
    }
    Ok(Some(RateAdjustmentSnapshot {
        id,
        version_number,
        checksum,
        numerator,
        denominator,
    }))
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
            return match quarantine_entry(
                redis_conn,
                &entry.id,
                "STORAGE_USAGE_REPORT_PAYLOAD_MISSING",
            )
            .await
            {
                Ok(()) => EntryDisposition::Ack,
                Err(error) => EntryDisposition::Retry(error),
            };
        }
    };
    let report = match decode_report(payload) {
        Ok(report) => report,
        Err(error) => {
            eprintln!(
                "Storage usage report entry {} rejected before settlement: {error}",
                entry.id
            );
            return if let Some(report) = recover_dead_report(payload) {
                match persist_dead_report(pg_pool, &report, payload, error).await {
                    Ok(()) => match acknowledge_entry(redis_conn, &entry.id).await {
                        Ok(()) => EntryDisposition::Ack,
                        Err(ack_error) => EntryDisposition::Retry(ack_error),
                    },
                    Err(persist_error) => EntryDisposition::Retry(persist_error),
                }
            } else {
                match quarantine_entry(redis_conn, &entry.id, error).await {
                    Ok(()) => EntryDisposition::Ack,
                    Err(quarantine_error) => EntryDisposition::Retry(quarantine_error),
                }
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

    let lock_key = format!("{STORAGE_USAGE_LOCK_PREFIX}:{}", report.report_id);
    let Some(lease) = acquire_billing_lease(
        redis_conn,
        &lock_key,
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
    // Zone is trusted Storage report context. Storage owns selection of its
    // modifier; the PAYG runtime receives only an opaque immutable snapshot.
    let adjustment = resolve_storage_zone_adjustment(pg_pool, zone_id, window_end).await?;
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
                .any(|aggregate| aggregate.storage_byte_hours > 0),
        ),
    ] {
        if !has_quantity {
            continue;
        }
        match pricing_runtime
            .begin_billing_run(BillingRunCommand {
                source_module: "storage",
                charge_kind_code: charge_kind,
                source_report_id: report_id,
                requested_start: window_start,
                requested_end: window_end,
                adjustment: adjustment.clone(),
                fencing_token,
            })
            .await
        {
            Ok(lease) => pricing_leases.push((charge_kind, lease)),
            Err(error) => {
                for (_, lease) in &pricing_leases {
                    let _ = pricing_runtime
                        .mark_billing_run_retrying(lease.billing_run_id, fencing_token)
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
                    .complete_billing_run(lease.billing_run_id, window_end, fencing_token)
                    .await
                    .map_err(|error| error.to_string())?;
            }
            Ok(())
        }
        Err(error) => {
            for (_, lease) in &pricing_leases {
                let _ = pricing_runtime
                    .mark_billing_run_retrying(lease.billing_run_id, fencing_token)
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
    if existing.1 == "SETTLED" {
        tx.commit()
            .await
            .map_err(|error| format!("commit idempotent storage report: {error}"))?;
        return Ok(());
    }
    if existing.1 == "UNRATED" {
        sqlx::query(
            "UPDATE billing.storage_usage_report_inbox
             SET status='PROCESSING', retry_count=retry_count+1, last_error=NULL
             WHERE report_id=$1",
        )
        .bind(report_id)
        .execute(&mut *tx)
        .await
        .map_err(|error| format!("mark unrated Storage report for replay: {error}"))?;
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
                aggregate.storage_byte_hours,
                "BYTE_HOUR",
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
            if pricing_lease.snapshot.module_code != "storage"
                || pricing_lease.snapshot.charge_kind_code != charge_kind_code
                || pricing_lease.snapshot.raw_input_unit != usage_unit
            {
                return Err(format!(
                    "Storage adapter received incompatible pricing snapshot for {charge_kind_code}"
                ));
            }
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
            if line_status == "SETTLED" {
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
            let cost = pricing_lease
                .snapshot
                .charge_micro_units(raw_quantity, pricing_lease.adjustment.as_ref())
                .map_err(|error| error.to_string())?;
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

            let reference_id =
                format!("storage-report:{report_id}:{resource_id}:{charge_kind_code}");
            let description =
                format!("Storage {charge_kind_code} quantity {quantity} using report {report_id}");
            match settle_usage_charge(
                &mut tx,
                UsageChargeCommand {
                    ledger_entry_id: line_id,
                    owner_id,
                    owner_type: &owner_type,
                    amount_micro_units: cost,
                    module_code: "storage",
                    charge_kind_code,
                    reference_id: &reference_id,
                    description: &description,
                    usage_settlement_run_id: pricing_lease.billing_run_id,
                    pricing_schedule_id: pricing_lease.snapshot.pricing_schedule_id,
                    pricing_schedule_version_id: pricing_lease.snapshot.version_id,
                    pricing_checksum: &pricing_lease.snapshot.checksum,
                    resource_id: owner_resource_id,
                    resource_type: "STORAGE_BUCKET",
                    usage_quantity: quantity,
                    usage_unit,
                    occurred_at: window_end,
                    source_evidence_hash: &source_evidence_hash,
                },
            )
            .await?
            {
                UsageChargeOutcome::Settled => {}
                UsageChargeOutcome::Unrated(reason) => {
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
                            reason,
                        },
                    )
                    .await?;
                    continue;
                }
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

    sqlx::query(
        "UPDATE billing.unrated_usage u
         SET status='RESOLVED', last_error=NULL, updated_at=NOW()
         FROM billing.storage_usage_line_inbox l
         WHERE u.id=l.line_id AND l.report_id=$1 AND l.status='SETTLED'
           AND u.status <> 'RESOLVED'",
    )
    .bind(report_id)
    .execute(&mut *tx)
    .await
    .map_err(|error| format!("resolve replayed unrated Storage evidence: {error}"))?;

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
    if report_status == "UNRATED" {
        return Err("storage report retains unrated lines; replay required".to_string());
    }
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
             status='PENDING',
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

async fn quarantine_entry(
    redis_conn: &mut redis::aio::MultiplexedConnection,
    entry_id: &str,
    reason: &str,
) -> Result<(), String> {
    let reason = reason.chars().take(128).collect::<String>();
    redis::Script::new(
        "redis.call('XADD',KEYS[2],'MAXLEN','~',10000,'*','source_entry_id',ARGV[2],'reason',ARGV[3]); \
         local acked=redis.call('XACK',KEYS[1],ARGV[1],ARGV[2]); \
         if acked == 1 then redis.call('XDEL',KEYS[1],ARGV[2]) end; \
         return 1",
    )
    .key(STORAGE_USAGE_STREAM)
    .key(STORAGE_USAGE_DLQ)
    .arg(STORAGE_USAGE_GROUP)
    .arg(entry_id)
    .arg(reason)
    .invoke_async::<_, i32>(redis_conn)
    .await
    .map(|_| ())
    .map_err(|error| format!("quarantine storage usage report entry {entry_id}: {error}"))
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

fn recover_dead_report(payload: &[u8]) -> Option<StorageUsageReportV1> {
    if payload.is_empty() || payload.len() > MAX_REPORT_BYTES {
        return None;
    }
    let report = StorageUsageReportV1::decode(payload).ok()?;
    let report_id = Uuid::parse_str(&report.report_id).ok()?;
    let zone_id = Uuid::parse_str(&report.zone_id).ok()?;
    let window_start = DateTime::<Utc>::from_timestamp_millis(report.window_start_unix_ms)?;
    let window_end = DateTime::<Utc>::from_timestamp_millis(report.window_end_unix_ms)?;
    if report_id.is_nil()
        || zone_id.is_nil()
        || window_end <= window_start
        || i64::try_from(report.sequence).is_err()
        || report.report_sha256.len() != 32
        || (report.correction
            && Uuid::parse_str(&report.correction_of_report_id)
                .ok()
                .is_none_or(|parent| parent.is_nil() || parent == report_id))
    {
        return None;
    }
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
        || report.window_end_unix_ms - report.window_start_unix_ms != HOURLY_WINDOW_MS
        || report.window_start_unix_ms.rem_euclid(HOURLY_WINDOW_MS) != 0
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
    let expected_sequence = u64::try_from(report.window_end_unix_ms.div_euclid(HOURLY_WINDOW_MS))
        .map_err(|_| "STORAGE_USAGE_REPORT_SEQUENCE_INVALID")?;
    if !report.correction {
        let expected_report_id = Uuid::new_v5(
            &REPORT_NAMESPACE,
            format!(
                "{}:{}:{}:{}",
                zone_id, report.window_start_unix_ms, report.window_end_unix_ms, expected_sequence
            )
            .as_bytes(),
        );
        if report.sequence != expected_sequence || report_id != expected_report_id {
            return Err("STORAGE_USAGE_REPORT_SEQUENCE_INVALID");
        }
    }
    let mut resources = HashSet::with_capacity(report.aggregates.len());
    let mut last_identity: Option<String> = None;
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
        if !resources.insert(identity.clone()) {
            return Err("STORAGE_USAGE_REPORT_RESOURCE_DUPLICATE");
        }
        if last_identity
            .as_ref()
            .is_some_and(|previous| identity.as_str() <= previous.as_str())
        {
            return Err("STORAGE_USAGE_REPORT_RESOURCE_ORDER_INVALID");
        }
        last_identity = Some(identity);
        if i64::try_from(aggregate.download_bytes).is_err()
            || i64::try_from(aggregate.upload_bytes).is_err()
            || i64::try_from(aggregate.request_count).is_err()
            || i64::try_from(aggregate.storage_bytes).is_err()
            || i64::try_from(aggregate.storage_byte_hours).is_err()
        {
            return Err("STORAGE_USAGE_REPORT_NUMERIC_INVALID");
        }
        let capacity = aggregate.storage_byte_hours > 0;
        let transfer = aggregate.upload_bytes > 0 || aggregate.download_bytes > 0;
        if capacity
            && (aggregate.resource_name.is_empty()
                || !aggregate.resource_id.is_empty()
                || transfer
                || aggregate.request_count != 0
                || aggregate.storage_bytes != aggregate.storage_byte_hours)
        {
            return Err("STORAGE_USAGE_REPORT_CAPACITY_INVALID");
        }
        if transfer
            && (aggregate.resource_id.is_empty()
                || !aggregate.resource_name.is_empty()
                || aggregate.request_count == 0
                || aggregate.storage_bytes != 0
                || aggregate.storage_byte_hours != 0)
        {
            return Err("STORAGE_USAGE_REPORT_TRANSFER_INVALID");
        }
        if !capacity && !transfer {
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
