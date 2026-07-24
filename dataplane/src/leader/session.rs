//! Fenced leader session shared by all leader-only duties.

use std::sync::Arc;
use std::time::Duration;

use tokio_util::sync::CancellationToken;

use crate::infra::zone_kv::{ZoneKvStore, ZoneLease};
use crate::observability::logger::{LogFields, Logger};

#[derive(Clone)]
pub(crate) struct ZoneLeaderSession {
    zone_kv: Arc<ZoneKvStore>,
    lease: ZoneLease,
    cancelled: CancellationToken,
}

impl ZoneLeaderSession {
    pub(crate) fn new(
        zone_kv: Arc<ZoneKvStore>,
        lease: ZoneLease,
        cancelled: CancellationToken,
    ) -> Self {
        Self {
            zone_kv,
            lease,
            cancelled,
        }
    }

    pub(crate) fn owner_id(&self) -> &str {
        &self.lease.owner_id
    }

    pub(crate) fn fencing_token(&self) -> u64 {
        self.lease.fencing_token
    }

    pub(crate) fn cancellation_token(&self) -> CancellationToken {
        self.cancelled.clone()
    }

    pub(crate) fn is_cancelled(&self) -> bool {
        self.cancelled.is_cancelled()
    }

    /// [COMMENT]: Đây là security/failure boundary trước mọi probe/publish của leader.
    /// KV read lỗi cũng fail-closed để pod bị partition không tiếp tục làm stale leader.
    pub(crate) async fn permits_external_side_effect(&self) -> bool {
        if self.cancelled.is_cancelled() {
            return false;
        }
        match self.zone_kv.lease_is_current(&self.lease).await {
            Ok(true) => true,
            Ok(false) => {
                Logger::sys_warn_with_fields(
                    "leader.side_effect_guard",
                    "ZONE_LEADER_FENCED",
                    "Leader side effect was denied because the lease is no longer current",
                    "",
                    LogFields {
                        operation_id: Some(&self.lease.owner_id),
                        leader_fencing_token: Some(self.lease.fencing_token),
                        outcome: Some("denied"),
                        ..LogFields::default()
                    },
                );
                false
            }
            Err(error) => {
                Logger::sys_warn_with_fields(
                    "leader.side_effect_guard",
                    "ZONE_LEADER_LEASE_READ_FAILED",
                    "Leader side effect was denied because current lease could not be verified",
                    &error,
                    LogFields {
                        operation_id: Some(&self.lease.owner_id),
                        leader_fencing_token: Some(self.lease.fencing_token),
                        retryable: Some(true),
                        outcome: Some("denied"),
                        ..LogFields::default()
                    },
                );
                false
            }
        }
    }

    pub(crate) async fn wait(&self, duration: Duration) -> bool {
        tokio::select! {
            _ = self.cancelled.cancelled() => false,
            _ = tokio::time::sleep(duration) => true,
        }
    }
}
