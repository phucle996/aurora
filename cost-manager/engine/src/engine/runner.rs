use chrono::{DateTime, Utc};
use redis::AsyncCommands;
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::watch;
use tokio::time::sleep;

use crate::engine::{
    BillingPricingLease, PricingRuntime, acquire_billing_lease, release_billing_lease,
};

pub trait BillingTask: Send + Sync {
    // Tên task dùng để ghi log
    fn name(&self) -> &'static str;

    // Loại dịch vụ tương ứng (NETWORK_OUT, STORAGE, VM...)
    fn service_type(&self) -> &'static str;

    // Các cấu hình Redis Lock & Fencing
    fn lock_key(&self) -> &'static str;
    fn fencing_counter_key(&self) -> &'static str;

    // Checkpoint key để lưu mốc thời gian đã xử lý trên Redis
    fn checkpoint_key(&self) -> &'static str;

    // Cấu hình thời gian chạy
    fn scan_interval(&self) -> Duration;
    fn lock_ttl_secs(&self) -> u64;

    // Logic tính cước chính
    async fn execute(
        &self,
        pricing_lease: &BillingPricingLease,
        fencing_token: i64,
    ) -> Result<DateTime<Utc>, String>;
}

pub async fn run_billing_task<T: BillingTask + 'static>(
    runtime: Arc<PricingRuntime>,
    mut redis_conn: redis::aio::MultiplexedConnection,
    task: T,
    mut shutdown_rx: watch::Receiver<bool>,
) {
    println!("{}: Khởi động vòng lặp tính cước framework.", task.name());

    loop {
        if *shutdown_rx.borrow() {
            break;
        }

        // 1. Acquire Lock phân tán & Fencing
        let Some(redis_lease) = acquire_billing_lease(
            &mut redis_conn,
            task.lock_key(),
            task.fencing_counter_key(),
            task.lock_ttl_secs(),
        )
        .await
        else {
            wait_for_next_cycle(task.scan_interval(), &mut shutdown_rx).await;
            continue;
        };

        // 2. Đọc checkpoint xử lý gần nhất từ Redis
        let requested_start = match redis_conn
            .get::<_, Option<String>>(task.checkpoint_key())
            .await
        {
            Ok(Some(value)) => chrono::DateTime::parse_from_rfc3339(&value)
                .map(|val| val.with_timezone(&Utc))
                .unwrap_or_else(|_| Utc::now() - Duration::from_secs(3600)),
            Ok(None) => Utc::now() - Duration::from_secs(3600),
            Err(error) => {
                eprintln!("{}: Không đọc được checkpoint: {error}", task.name());
                release_billing_lease(redis_lease, &mut redis_conn).await;
                wait_for_next_cycle(task.scan_interval(), &mut shutdown_rx).await;
                continue;
            }
        };
        let requested_end = Utc::now();

        // 3. Begin billing run và Pin version cước
        let pricing_lease = match runtime
            .begin_billing_run(
                task.service_type(),
                requested_start,
                requested_end,
                redis_lease.fencing_token,
            )
            .await
        {
            Ok(lease) => lease,
            Err(error) => {
                eprintln!("{}: Không pin được pricing version: {error}", task.name());
                release_billing_lease(redis_lease, &mut redis_conn).await;
                wait_for_next_cycle(task.scan_interval(), &mut shutdown_rx).await;
                continue;
            }
        };

        // 4. Thực thi logic nghiệp vụ của Task
        let mut run_failed = false;
        let mut max_hour = pricing_lease.window_start;

        match task
            .execute(&pricing_lease, redis_lease.fencing_token)
            .await
        {
            Ok(processed_max_hour) => {
                max_hour = processed_max_hour;
            }
            Err(error) => {
                eprintln!("{}: Thực thi nghiệp vụ thất bại: {error}", task.name());
                run_failed = true;
            }
        }

        // Never advance the checkpoint after lock renewal was lost. Durable
        // fencing makes any partial ledger work safe to retry under a new lease.
        if *redis_lease.lost_rx.borrow() {
            eprintln!("{}: Distributed billing lease was lost", task.name());
            run_failed = true;
        }

        // 5. Hoàn tất lượt chạy hoặc đánh dấu Retry
        if !run_failed {
            // Lưu checkpoint mới lên Redis
            let checkpoint_result: Result<(), redis::RedisError> = redis_conn
                .set(task.checkpoint_key(), max_hour.to_rfc3339())
                .await;

            if let Err(error) = checkpoint_result {
                eprintln!("{}: Ghi checkpoint thất bại: {error}", task.name());
                run_failed = true;
            } else if let Err(error) = runtime
                .complete_billing_run(pricing_lease.billing_run_id, max_hour)
                .await
            {
                eprintln!("{}: Durable run completion thất bại: {error}", task.name());
                run_failed = true;
            }
        }

        if run_failed {
            let _ = runtime
                .mark_billing_run_retrying(pricing_lease.billing_run_id)
                .await;
        }

        // 6. Release Lock & Chờ chu kỳ tiếp theo
        release_billing_lease(redis_lease, &mut redis_conn).await;
        wait_for_next_cycle(task.scan_interval(), &mut shutdown_rx).await;
    }
}

pub async fn wait_for_next_cycle(interval: Duration, shutdown_rx: &mut watch::Receiver<bool>) {
    tokio::select! {
        _ = sleep(interval) => {}
        _ = shutdown_rx.changed() => {}
    }
}
