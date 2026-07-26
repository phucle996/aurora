// ======================================================================================================
// 📂 gateway/ratelimit.rs — Edge Rate Limiter & Fast-Bypass Block Cache (Moka L1)
// ======================================================================================================

use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use std::sync::Arc;
use std::time::Duration;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RouteGroup {
    SreCritical,
    SreGeneral,
    UserCritical,
    UserPersonal,
    UserMe,
    UserTenant,
    Billing,
    PaymentWebhook,
    AuthPublic,
    General,
}

impl RouteGroup {
    pub fn as_str(&self) -> &'static str {
        match self {
            RouteGroup::SreCritical => "sre_critical",
            RouteGroup::SreGeneral => "sre_general",
            RouteGroup::UserCritical => "user_critical",
            RouteGroup::UserPersonal => "user_personal",
            RouteGroup::UserMe => "user_me",
            RouteGroup::UserTenant => "user_tenant",
            RouteGroup::Billing => "billing",
            RouteGroup::PaymentWebhook => "payment_webhook",
            RouteGroup::AuthPublic => "auth_public",
            RouteGroup::General => "general",
        }
    }
}

/// [COMMENT]: Tự động phân loại Route Group dựa trên đường dẫn path
pub fn detect_route_group(path: &str) -> RouteGroup {
    if path.starts_with("/admin/critical") {
        RouteGroup::SreCritical
    } else if path.starts_with("/admin") {
        RouteGroup::SreGeneral
    } else if path.starts_with("/api/v1/critical/") {
        RouteGroup::UserCritical
    } else if path.starts_with("/api/v1/auth/") {
        RouteGroup::AuthPublic
    } else if path.split('?').next().is_some_and(|value| {
        value == "/api/v1/billing/webhooks/personal/payment-settled"
            || value == "/api/v1/billing/webhooks/tenant/payment-settled"
    }) {
        RouteGroup::PaymentWebhook
    } else if path.starts_with("/api/v1/billing") {
        RouteGroup::Billing
    } else if path.contains("/personal") {
        RouteGroup::UserPersonal
    } else if path.contains("/me") {
        RouteGroup::UserMe
    } else if path.contains("/tenant") {
        RouteGroup::UserTenant
    } else {
        RouteGroup::General
    }
}

/// [COMMENT]: RateLimiter tại Edge — tích hợp L1 Moka Cache chặn nhanh token/IP bị block trong 30s.
pub struct RateLimiter {
    session_mgr: Arc<SessionManager>,
    block_cache: moka::future::Cache<String, ()>,
}

impl RateLimiter {
    pub fn new(session_mgr: Arc<SessionManager>) -> Self {
        // [COMMENT]: Khởi tạo L1 Moka cache lưu trữ danh sách các key bị chặn tạm thời
        let block_cache = moka::future::Cache::builder()
            .max_capacity(10_000)
            .time_to_live(Duration::from_secs(30))
            .build();

        Self {
            session_mgr,
            block_cache,
        }
    }

    /// [COMMENT]: Fast-Bypass Check: Kiểm tra key trong L1 Block Cache (0ms latency).
    pub async fn is_blocked(&self, key: &str) -> bool {
        self.block_cache.contains_key(key)
    }

    /// [COMMENT]: Đánh dấu key (token hash / IP / User) bị block trong 30s
    pub async fn block_key(&self, key: &str) {
        self.block_cache.insert(key.to_string(), ()).await;
        Logger::sys_warn(
            "ratelimit",
            &format!("Key '{}' added to L1 block cache for 30s", key),
            "",
        );
    }

    /// [COMMENT]: Lấy cấu hình giới hạn Pre-Auth cho từng RouteGroup (nâng cao giới hạn cho IP)
    fn get_pre_auth_limit(group: RouteGroup) -> (u32, u64, u32, u64) {
        // [COMMENT]: Trả về (max_ip_reqs, window_ip_secs, max_device_reqs, window_device_secs)
        match group {
            RouteGroup::SreCritical => (50, 1, 5, 1),
            RouteGroup::SreGeneral => (200, 1, 15, 1),
            RouteGroup::UserCritical => (20, 60, 6, 60),
            RouteGroup::Billing => (300, 1, 30, 1),
            // Provider traffic may share a small set of egress IPs. HMAC and the
            // 64 KiB body cap remain the authentication/CPU boundary.
            RouteGroup::PaymentWebhook => (1_000, 1, 1_000, 1),
            // [COMMENT]: Argon2/register/login là CPU-expensive; limit thấp theo IP và device trước khi vào handler.
            RouteGroup::AuthPublic => (30, 60, 8, 60),
            RouteGroup::UserPersonal => (300, 1, 20, 1),
            RouteGroup::UserMe => (500, 1, 30, 1),
            RouteGroup::UserTenant => (200, 1, 15, 1),
            RouteGroup::General => (1000, 1, 50, 1),
        }
    }

    /// [COMMENT]: Lấy cấu hình giới hạn Post-Auth cho từng RouteGroup (theo User ID và Device ID)
    fn get_post_auth_limit(group: RouteGroup) -> (u32, u64, u32, u64) {
        // [COMMENT]: Trả về (max_user_reqs, window_user_secs, max_device_reqs, window_device_secs)
        match group {
            RouteGroup::SreCritical => (10, 1, 10, 1),
            RouteGroup::SreGeneral => (30, 1, 30, 1),
            RouteGroup::UserCritical => (30, 60, 20, 60),
            RouteGroup::Billing => (80, 1, 80, 1),
            RouteGroup::PaymentWebhook => (1_000, 1, 1_000, 1),
            RouteGroup::AuthPublic => (20, 60, 20, 60),
            RouteGroup::UserPersonal => (60, 1, 60, 1),
            RouteGroup::UserMe => (100, 1, 100, 1),
            RouteGroup::UserTenant => (40, 1, 40, 1),
            RouteGroup::General => (200, 1, 200, 1),
        }
    }

    /// [COMMENT]: Kiểm tra giới hạn rate limit ở Phase 1: Trước xác thực (Pre-Auth)
    pub async fn check_pre_auth(
        &self,
        client_ip: &str,
        device_id: Option<&str>,
        group: RouteGroup,
    ) -> bool {
        let (ip_max, ip_window, dev_max, dev_window) = Self::get_pre_auth_limit(group);

        // [COMMENT]: Kiểm tra rate limit theo IP
        let ip_key = format!("pre:ip:{}:{}", client_ip, group.as_str());
        if !self.check_redis_limit(&ip_key, ip_max, ip_window).await {
            return false;
        }

        // [COMMENT]: Kiểm tra rate limit theo Device ID nếu có
        if let Some(dev) = device_id {
            let dev_key = format!("pre:device:{}:{}", dev, group.as_str());
            if !self.check_redis_limit(&dev_key, dev_max, dev_window).await {
                return false;
            }
        }

        true
    }

    /// [COMMENT]: Kiểm tra giới hạn rate limit ở Phase 2: Sau xác thực (Post-Auth)
    pub async fn check_post_auth(
        &self,
        user_id: &str,
        device_id: Option<&str>,
        group: RouteGroup,
    ) -> bool {
        let (user_max, user_window, dev_max, dev_window) = Self::get_post_auth_limit(group);

        // [COMMENT]: Kiểm tra rate limit theo User ID
        let user_key = format!("post:user:{}:{}", user_id, group.as_str());
        if !self
            .check_redis_limit(&user_key, user_max, user_window)
            .await
        {
            return false;
        }

        // [COMMENT]: Kiểm tra rate limit theo Device ID nếu có
        if let Some(dev) = device_id {
            let dev_key = format!("post:device:{}:{}", dev, group.as_str());
            if !self.check_redis_limit(&dev_key, dev_max, dev_window).await {
                return false;
            }
        }

        true
    }

    /// [COMMENT]: Thực hiện đếm và kiểm tra giới hạn qua Redis (L2) kèm theo cache chặn Moka (L1)
    async fn check_redis_limit(&self, key: &str, max_requests: u32, window_secs: u64) -> bool {
        // [COMMENT]: Kiểm tra xem key có đang bị block tạm thời trong bộ nhớ L1 không
        if self.is_blocked(key).await {
            return false;
        }

        // [COMMENT]: Kết nối tới Redis L2
        let mut conn = match self.session_mgr.get_connection().await {
            Ok(c) => c,
            Err(e) => {
                Logger::sys_error(
                    "ratelimit",
                    "Redis connection failed, failing open",
                    &e.to_string(),
                );
                return true; // Fail open if Redis is down
            }
        };

        let redis_key = format!("ratelimit:{}", key);

        // [COMMENT]: Thực hiện tăng số đếm trong Redis
        let current: Result<u32, _> = redis::cmd("INCR")
            .arg(&redis_key)
            .query_async(&mut conn)
            .await;

        match current {
            Ok(val) => {
                // [COMMENT]: Nếu là request đầu tiên trong window, thiết lập TTL cho key
                if val == 1 {
                    let _: Result<(), _> = redis::cmd("EXPIRE")
                        .arg(&redis_key)
                        .arg(window_secs)
                        .query_async(&mut conn)
                        .await;
                }

                // [COMMENT]: Nếu vượt quá ngưỡng giới hạn, ghi vào L1 Block Cache và trả về false
                if val > max_requests {
                    self.block_key(key).await;
                    false
                } else {
                    true
                }
            }
            Err(e) => {
                Logger::sys_error(
                    "ratelimit",
                    "Redis command execution failed, failing open",
                    &e.to_string(),
                );
                true // Fail open
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::{detect_route_group, RouteGroup};

    #[test]
    fn payment_webhook_uses_bounded_public_rate_group() {
        assert_eq!(
            detect_route_group("/api/v1/billing/webhooks/personal/payment-settled"),
            RouteGroup::PaymentWebhook
        );
        assert_eq!(
            detect_route_group("/api/v1/billing/webhooks/tenant/payment-settled?attempt=2"),
            RouteGroup::PaymentWebhook
        );
        assert_eq!(
            detect_route_group("/api/v1/billing/webhooks/other"),
            RouteGroup::Billing
        );
    }
}
