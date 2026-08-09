use redis::AsyncCommands;
use std::time::Duration;
use tokio::sync::watch;
use uuid::Uuid;

pub struct RedisBillingLease {
    pub key: &'static str,
    pub token: String,
    pub fencing_token: i64,
    pub stop_tx: watch::Sender<bool>,
    pub lost_rx: watch::Receiver<bool>,
    pub renew_handle: tokio::task::JoinHandle<()>,
}

pub async fn acquire_billing_lease(
    redis_conn: &mut redis::aio::MultiplexedConnection,
    key: &'static str,
    fencing_counter_key: &str,
    lock_ttl_secs: u64,
) -> Option<RedisBillingLease> {
    let fencing_token: i64 = redis_conn.incr(fencing_counter_key, 1).await.ok()?;
    let token = format!("{}:{}", fencing_token, Uuid::now_v7());
    let acquired: Option<String> = redis::cmd("SET")
        .arg(key)
        .arg(&token)
        .arg("NX")
        .arg("PX")
        .arg(lock_ttl_secs * 1000)
        .query_async(redis_conn)
        .await
        .ok()
        .flatten();
    acquired.as_ref()?;

    let (stop_tx, mut stop_rx) = watch::channel(false);
    let (lost_tx, lost_rx) = watch::channel(false);
    let mut renew_conn = redis_conn.clone();
    let renew_token = token.clone();
    let ttl_ms = lock_ttl_secs * 1000;
    let renew_every = Duration::from_secs((lock_ttl_secs / 3).max(1));
    let renew_handle = tokio::spawn(async move {
        let mut interval = tokio::time::interval(renew_every);
        loop {
            tokio::select! {
                _ = interval.tick() => {
                    let renewed: Result<i32, _> = redis::Script::new(
                        "if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('PEXPIRE', KEYS[1], ARGV[2]) else return 0 end"
                    ).key(key).arg(&renew_token).arg(ttl_ms).invoke_async(&mut renew_conn).await;
                    if !matches!(renewed, Ok(value) if value == 1) {
                        let _ = lost_tx.send(true);
                        break;
                    }
                }
                _ = stop_rx.changed() => {
                    if *stop_rx.borrow() { break; }
                }
            }
        }
    });
    Some(RedisBillingLease {
        key,
        token,
        fencing_token,
        stop_tx,
        lost_rx,
        renew_handle,
    })
}

pub async fn release_billing_lease(
    lease: RedisBillingLease,
    redis_conn: &mut redis::aio::MultiplexedConnection,
) {
    let _ = lease.stop_tx.send(true);
    let _ = lease.renew_handle.await;
    // [COMMENT]: Compare-and-delete không thể xóa nhầm lock của replica kế nhiệm sau TTL/failover.
    let _: Result<i32, _> = redis::Script::new(
        "if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) else return 0 end"
    ).key(lease.key).arg(lease.token).invoke_async(redis_conn).await;
}
