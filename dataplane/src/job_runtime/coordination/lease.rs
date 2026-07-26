use std::time::Duration;

use sha2::{Digest, Sha256};

use crate::infra::zone_kv::{ZoneKvStore, ZoneLease};

pub const JOB_EXECUTION_LEASE_TTL_SECS: u64 = 30;
const JOB_EXECUTION_LEASE_IO_TIMEOUT: Duration = Duration::from_secs(5);

pub async fn acquire_execution_lease(
    zone_kv: &ZoneKvStore,
    resource_execution_identity: &str,
) -> Result<Option<ZoneLease>, String> {
    let job_key_digest = Sha256::digest(resource_execution_identity.as_bytes());
    let lock_key = format!("lease.job.{job_key_digest:x}");
    let owner_id = format!(
        "{}-{}",
        std::env::var("HOSTNAME").unwrap_or_else(|_| std::process::id().to_string()),
        uuid::Uuid::new_v4()
    );
    tokio::time::timeout(
        JOB_EXECUTION_LEASE_IO_TIMEOUT,
        zone_kv.acquire_lease(
            &lock_key,
            &owner_id,
            Duration::from_secs(JOB_EXECUTION_LEASE_TTL_SECS),
        ),
    )
    .await
    .map_err(|_| "Zone KV execution lease acquisition timed out".to_string())?
}

pub async fn release_execution_lease(
    zone_kv: &ZoneKvStore,
    lease: &ZoneLease,
) -> Result<bool, String> {
    tokio::time::timeout(JOB_EXECUTION_LEASE_IO_TIMEOUT, zone_kv.release_lease(lease))
        .await
        .map_err(|_| "Zone KV execution lease release timed out".to_string())?
}
