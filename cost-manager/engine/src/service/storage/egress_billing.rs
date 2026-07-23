use std::sync::Arc;
use std::time::Duration;

use chrono::{DateTime, Utc};
use clickhouse::Client as ClickhouseClient;
use redis::AsyncCommands;
use serde::Deserialize;
use sqlx::Postgres;
use sqlx::postgres::PgPool;
use tokio::sync::watch;
use uuid::Uuid;

use crate::config::Config;
use crate::engine::{BillingPricingLease, BillingTask, PricingRuntime};

// [COMMENT]: Namespace UUID cố định giữ ledger idempotent khi run crash/retry bằng pinned pricing version.
const S3_BILLING_NAMESPACE: Uuid = Uuid::from_u128(0x5a18a5cb33a647d6be96cb57f70b13cf);
const BILLING_SERVICE_TYPE: &str = "NETWORK_OUT";

#[derive(Debug)]
struct BillableOwner {
    resource_id: Uuid,
    owner_id: Uuid,
    owner_type: String,
}

#[derive(Debug, Deserialize, clickhouse::Row)]
pub struct ClickhouseMeteringRow {
    pub hour: DateTime<Utc>,
    pub access_key: String,
    pub bucket_name: String,
    pub total_upload_bytes: u64,
    pub total_download_bytes: u64,
    pub request_count: u64,
}

pub struct StorageEgressBillingTask {
    pub config: Config,
    pub pg_pool: PgPool,
    pub ch_client: ClickhouseClient,
    pub redis_conn: redis::aio::MultiplexedConnection,
}

impl BillingTask for StorageEgressBillingTask {
    fn name(&self) -> &'static str {
        "Storage Egress Billing Service"
    }

    fn service_type(&self) -> &'static str {
        BILLING_SERVICE_TYPE
    }

    fn lock_key(&self) -> &'static str {
        "storage:billing:lock"
    }

    fn fencing_counter_key(&self) -> &'static str {
        "storage:billing:fencing_counter"
    }

    fn checkpoint_key(&self) -> &'static str {
        "storage:billing:last_processed_time"
    }

    fn scan_interval(&self) -> Duration {
        self.config.scan_interval
    }

    fn lock_ttl_secs(&self) -> u64 {
        self.config.lock_ttl_secs
    }

    async fn execute(
        &self,
        pricing_lease: &BillingPricingLease,
        fencing_token: i64,
    ) -> Result<DateTime<Utc>, String> {
        let mut redis_conn = self.redis_conn.clone();
        let query = format!(
            "SELECT hour, access_key, bucket_name, total_upload_bytes, total_download_bytes, request_count \
             FROM hourly_metering_agg \
             WHERE hour > toDateTime('{}') AND hour <= toDateTime('{}') \
             ORDER BY hour ASC, access_key ASC, bucket_name ASC",
            pricing_lease.window_start.format("%Y-%m-%d %H:%M:%S"),
            pricing_lease.window_end.format("%Y-%m-%d %H:%M:%S"),
        );

        let mut max_hour = pricing_lease.window_start;
        let mut processed_records = 0_u64;
        let mut cursor = self
            .ch_client
            .query(&query)
            .fetch::<ClickhouseMeteringRow>()
            .map_err(|e| format!("ClickHouse query failed: {e:?}"))?;

        while let Some(row) = cursor
            .next()
            .await
            .map_err(|e| format!("ClickHouse cursor failed: {e:?}"))?
        {
            if row.hour > max_hour {
                max_hour = row.hour;
            }

            let cost = pricing_lease
                .snapshot
                .charge_micro_units_for_bytes(row.total_download_bytes)
                .map_err(|error| error.to_string())?;
            let usage_quantity = i64::try_from(row.total_download_bytes)
                .map_err(|_| "Usage quantity exceeds BIGINT lineage capacity".to_string())?;

            if cost <= 0 {
                processed_records += 1;
                continue;
            }

            let identifier = format!(
                "{}:{}:{}",
                row.access_key,
                row.bucket_name,
                row.hour.to_rfc3339()
            );
            let transaction_id = Uuid::new_v5(&S3_BILLING_NAMESPACE, identifier.as_bytes());

            // [COMMENT]: Resolve billable owner theo metering timestamp; bucket transfer không đổi payer của usage lịch sử.
            let owner = sqlx::query_as::<_, (Uuid, Uuid, String)>(
                "SELECT resource_id, owner_id, owner_type::text \
                 FROM billing.resource_ownership_projection \
                 WHERE resource_type='STORAGE_BUCKET' AND resource_name=$1 \
                   AND effective_from <= $2 AND (effective_to IS NULL OR $2 < effective_to) \
                 ORDER BY ownership_version DESC LIMIT 1",
            )
            .bind(&row.bucket_name)
            .bind(row.hour)
            .fetch_optional(&self.pg_pool)
            .await
            .map_err(|e| format!("Owner projection lookup failed: {e}"))?
            .map(|(resource_id, owner_id, owner_type)| BillableOwner {
                resource_id,
                owner_id,
                owner_type,
            });

            let Some(owner) = owner else {
                persist_unrated_usage(
                    &self.pg_pool,
                    transaction_id,
                    &row,
                    None,
                    usage_quantity,
                    "OWNER_PROJECTION_MISSING",
                    None,
                )
                .await?;
                processed_records += 1;
                continue;
            };

            let mut tx = self
                .pg_pool
                .begin()
                .await
                .map_err(|e| format!("PostgreSQL transaction begin failed: {e}"))?;

            // [COMMENT]: FOR SHARE fence ngăn replica lease cũ commit sau khi failover đã đổi fencing_token.
            let run_fence = sqlx::query_as::<_, (i64, String)>(
                "SELECT fencing_token, status FROM billing.billing_runs WHERE id=$1 FOR SHARE",
            )
            .bind(pricing_lease.billing_run_id)
            .fetch_optional(&mut *tx)
            .await
            .map_err(|e| format!("Fencing check failed: {e}"))?;

            match run_fence {
                Some((token, status))
                    if token == fencing_token && (status == "RUNNING" || status == "RETRYING") => {}
                _ => {
                    let _ = tx.rollback().await;
                    return Err("Fencing token mismatch or run not active".to_string());
                }
            }

            let wallet = sqlx::query_as::<Postgres, (Uuid, i64, i64, i64, String)>(
                "SELECT id, cash_balance, promotional_balance, overdraft_limit, status::text \
                 FROM billing.wallets \
                 WHERE owner_id=$1 AND owner_type=$2::billing.owner_type AND currency='USD' FOR UPDATE",
            )
            .bind(owner.owner_id)
            .bind(&owner.owner_type)
            .fetch_optional(&mut *tx)
            .await
            .map_err(|e| format!("Wallet lock failed: {e}"))?;

            let Some((wallet_id, cash_balance, promotional_balance, overdraft_limit, status)) =
                wallet
            else {
                let _ = tx.rollback().await;
                persist_unrated_usage(
                    &self.pg_pool,
                    transaction_id,
                    &row,
                    Some(owner.resource_id),
                    usage_quantity,
                    "WALLET_MISSING",
                    Some(format!("{}:{}", owner.owner_type, owner.owner_id)),
                )
                .await?;
                processed_records += 1;
                continue;
            };

            // [COMMENT]: Promotional credit được tiêu trước; phần còn lại mới debit cash/overdraft.
            let promo_debit = promotional_balance.min(cost);
            let new_promotional_balance = promotional_balance - promo_debit;
            let cash_debit = cost - promo_debit;
            let new_cash_balance = cash_balance
                .checked_sub(cash_debit)
                .ok_or_else(|| "Wallet cash balance exceeds BIGINT capacity".to_string())?;
            let mut new_status = status.clone();
            if new_cash_balance.saturating_add(overdraft_limit) <= 0 && status == "ACTIVE" {
                new_status = "SUSPENDED".to_string();
                let block_key = format!("storage:blocked_keys:{}", row.access_key);
                let _: Result<(), redis::RedisError> = redis_conn
                    .set_ex(block_key, "true", self.config.block_key_ttl_secs)
                    .await;
            }

            sqlx::query(
                "UPDATE billing.wallets \
                 SET cash_balance=$1, promotional_balance=$2, status=$3::billing.wallet_status, \
                     version=version+1, updated_at=NOW() WHERE id=$4",
            )
            .bind(new_cash_balance)
            .bind(new_promotional_balance)
            .bind(&new_status)
            .bind(wallet_id)
            .execute(&mut *tx)
            .await
            .map_err(|e| format!("Wallet update failed: {e}"))?;

            // [COMMENT]: Ledger pin run/version/resource/owner và exact micro-unit amount.
            let description = format!(
                "S3 egress {} bytes using pricing version {}",
                row.total_download_bytes, pricing_lease.snapshot.version_number,
            );
            let ledger_result = sqlx::query(
                "INSERT INTO billing.wallet_ledger_entries \
                 (id, wallet_id, owner_id, owner_type, amount_micro_units, cash_balance_after, \
                  promotional_balance_after, currency, entry_type, service_type, reference_id, description, \
                  billing_run_id, tier_version_id, resource_id, resource_type, usage_quantity, usage_unit, occurred_at) \
                 VALUES ($1,$2,$3,$4::billing.owner_type,$5,$6,$7,'USD','USAGE_CHARGE', \
                         $8::billing.service_type,$9,$10,$11,$12,$13,'STORAGE_BUCKET',$14,'BYTE',$15)",
            )
            .bind(transaction_id)
            .bind(wallet_id)
            .bind(owner.owner_id)
            .bind(&owner.owner_type)
            .bind(-cost)
            .bind(new_cash_balance)
            .bind(new_promotional_balance)
            .bind(BILLING_SERVICE_TYPE)
            .bind("s3-egress")
            .bind(description)
            .bind(pricing_lease.billing_run_id)
            .bind(pricing_lease.snapshot.tier_version_id)
            .bind(owner.resource_id)
            .bind(usage_quantity)
            .bind(row.hour)
            .execute(&mut *tx)
            .await;

            match ledger_result {
                Ok(_) => {
                    tx.commit()
                        .await
                        .map_err(|e| format!("Ledger commit failed: {e}"))?;
                    processed_records += 1;
                }
                Err(error)
                    if error
                        .as_database_error()
                        .and_then(|db| db.code())
                        .as_deref()
                        == Some("23505") =>
                {
                    // [COMMENT]: Duplicate ledger ID rollback cả wallet mutation và được tính là idempotent success.
                    let _ = tx.rollback().await;
                    processed_records += 1;
                }
                Err(error) => {
                    let _ = tx.rollback().await;
                    return Err(format!("Ledger insert failed: {error}"));
                }
            }
        }

        println!(
            "Storage Egress Billing Service: Run {} hoàn tất {} records; pending pricing có thể activate.",
            pricing_lease.billing_run_id, processed_records,
        );

        Ok(max_hour)
    }
}

// [COMMENT]: Durable unrated row cho phép checkpoint tiến mà không mất usage trong lúc projection/wallet lag.
async fn persist_unrated_usage(
    pool: &PgPool,
    id: Uuid,
    row: &ClickhouseMeteringRow,
    resource_id: Option<Uuid>,
    usage_quantity: i64,
    reason: &str,
    last_error: Option<String>,
) -> Result<(), String> {
    sqlx::query(
        "INSERT INTO billing.unrated_usage \
         (id, service_type, resource_type, resource_id, resource_name, access_key, metering_hour, \
          usage_quantity, usage_unit, reason, last_error) \
         VALUES ($1,$2::billing.service_type,'STORAGE_BUCKET',$3,$4,$5,$6,$7,'BYTE',$8,$9) \
         ON CONFLICT (id) DO UPDATE SET retry_count=billing.unrated_usage.retry_count+1, \
             last_error=EXCLUDED.last_error, updated_at=NOW()",
    )
    .bind(id)
    .bind(BILLING_SERVICE_TYPE)
    .bind(resource_id)
    .bind(&row.bucket_name)
    .bind(&row.access_key)
    .bind(row.hour)
    .bind(usage_quantity)
    .bind(reason)
    .bind(last_error)
    .execute(pool)
    .await
    .map_err(|error| format!("Persist unrated usage failed: {error}"))?;
    Ok(())
}

pub async fn run_storage_egress_billing(
    config: Config,
    pg_pool: PgPool,
    ch_client: ClickhouseClient,
    redis_conn: redis::aio::MultiplexedConnection,
    pricing_runtime: Arc<PricingRuntime>,
    shutdown_rx: watch::Receiver<bool>,
) {
    let task = StorageEgressBillingTask {
        config,
        pg_pool,
        ch_client,
        redis_conn: redis_conn.clone(),
    };
    crate::engine::run_billing_task(pricing_runtime, redis_conn, task, shutdown_rx).await;
}
