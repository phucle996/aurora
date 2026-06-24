// ======================================================================================================
// 📂 MODULE: acl/src/service/ratelimit.rs
//            Bộ giới hạn tần suất (Rate Limiter) tích hợp L1 Block Cache & Redis Token Bucket tại Edge
// ======================================================================================================

use crate::core::session::SessionManager;
use crate::core::token::Claims;
use crate::observability::logger::Logger;
use moka::future::Cache;
use std::sync::Arc;
use std::time::Duration;
use tonic::Status;

// LUA Script thực hiện giải thuật Token Bucket chính xác trên Redis
const LUA_TOKEN_BUCKET: &str = r#"
local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local refill_amount = tonumber(ARGV[2])
local refill_period_ms = tonumber(ARGV[3])
local now_ms = tonumber(ARGV[4])
local requested = tonumber(ARGV[5])

local data = redis.call('HMGET', key, 'tokens', 'last_updated_ms')
local tokens = tonumber(data[1])
local last_updated_ms = tonumber(data[2])

if not tokens then
    tokens = capacity
    last_updated_ms = now_ms
else
    local elapsed = now_ms - last_updated_ms
    if elapsed > 0 then
        local refill = (elapsed / refill_period_ms) * refill_amount
        tokens = math.min(capacity, tokens + refill)
        last_updated_ms = now_ms
    end
end

if tokens >= requested then
    tokens = tokens - requested
    redis.call('HMSET', key, 'tokens', tokens, 'last_updated_ms', last_updated_ms)
    redis.call('EXPIRE', key, 86400) -- Hạn dùng 1 ngày cho cache bucket
    return {1, math.floor(tokens)}
else
    redis.call('HMSET', key, 'tokens', tokens, 'last_updated_ms', last_updated_ms)
    redis.call('EXPIRE', key, 86400)
    return {0, math.floor(tokens)}
end
"#;

pub struct RateLimiter {
    session_mgr: Arc<SessionManager>,
    // L1 Block Cache: Lưu các token (bằng sha256_hash) đã bị block tạm thời.
    // Nếu token bị block, các request sau trỏ vào sẽ bị từ chối cực nhanh (Fast-Bypass)
    // mà không cần phải kết nối Redis hay giải mã chữ ký Ed25519/Vault.
    blocked_tokens_cache: Cache<String, ()>,
}

impl RateLimiter {
    pub fn new(session_mgr: Arc<SessionManager>) -> Self {
        // Đặt dung lượng cache tối đa là 50,000 bản ghi, TTL mặc định cho mỗi block là 60 giây
        let blocked_tokens_cache = Cache::builder()
            .max_capacity(50_000)
            .time_to_live(Duration::from_secs(60))
            .build();

        Self {
            session_mgr,
            blocked_tokens_cache,
        }
    }

    /// Kiểm tra xem mã băm SHA-256 của token hiện tại có đang nằm trong danh sách chặn nhanh (L1 Block) hay không.
    pub async fn is_blocked(&self, token_hash: &str) -> bool {
        self.blocked_tokens_cache.contains_key(token_hash)
    }

    /// Đánh giá giới hạn Rate Limit của request (Post-Auth) dựa trên cấu hình phân quyền và độ nhạy cảm của Endpoint.
    pub async fn check_rate_limit(
        &self,
        claims: &Claims,
        token_hash: &str,
        is_critical_admin: bool,
        path: &str,
    ) -> Result<(), Status> {
        // 1. Định nghĩa cấu hình giới hạn (Capacity, Refill Amount, Refill Period in MS)
        // [COMMENT]: Ngưỡng phân tầng bảo vệ:
        //   - SRE Critical (API thay đổi hạ tầng, status): 5 reqs/phút -> Capacity=5, Refill=1 token / 12 giây
        //   - SRE Non-Critical (các api admin đọc xem thông tin): 60 reqs/phút -> Capacity=60, Refill=1 token / 1 giây
        //   - User API bình thường: 100 reqs/phút -> Capacity=100, Refill=1 token / 0.6 giây (600ms)
        let (capacity, refill_amount, refill_period_ms) = if claims.is_admin() {
            if is_critical_admin {
                (5, 1, 12000)
            } else {
                (60, 1, 1000)
            }
        } else {
            (100, 1, 600)
        };

        // 2. Lấy kết nối tới Redis
        let mut conn = self.session_mgr.get_connection().await.map_err(|e| {
            Logger::sys_error(
                "ratelimit",
                "Failed to get Redis connection for rate limiting",
                &e.to_string(),
            );
            Status::internal("Rate limit service temporarily unavailable")
        })?;

        // 3. Xây dựng Key định danh bucket trên Redis
        // [COMMENT]: Format key: rl:{path}:{access_key}
        let limit_key = format!("rl:{}:{}", path, claims.access_key);
        let now_ms = chrono::Utc::now().timestamp_millis();

        // 4. Chạy Lua script trên Redis một cách nguyên tử (atomic)
        let script = redis::Script::new(LUA_TOKEN_BUCKET);
        let result: Vec<i64> = script
            .key(&limit_key)
            .arg(capacity)
            .arg(refill_amount)
            .arg(refill_period_ms)
            .arg(now_ms)
            .arg(1) // requested tokens = 1
            .invoke_async(&mut conn)
            .await
            .map_err(|e| {
                Logger::sys_error(
                    "ratelimit",
                    "Redis error while executing Token Bucket Lua script",
                    &e.to_string(),
                );
                Status::internal("Rate limit service temporarily unavailable")
            })?;

        if result.len() < 2 {
            return Err(Status::internal("Invalid rate limit response from Redis"));
        }

        let allowed = result[0] == 1;
        let remaining_tokens = result[1];

        // 5. Nếu hết quota (allowed == false), ghi nhận token_hash vào L1 Block Cache để bypass các request sau
        if !allowed {
            Logger::sys_warn(
                "ratelimit",
                &format!(
                    "Rate limit exceeded for client path: {}. Blocking token_hash: {}",
                    path, token_hash
                ),
                &claims.access_key,
            );

            // Đưa token_hash vào cache chặn nhanh
            self.blocked_tokens_cache
                .insert(token_hash.to_string(), ())
                .await;

            return Err(Status::resource_exhausted(
                "Too Many Requests - Rate limit exceeded",
            ));
        }

        Logger::sys_debug(
            "ratelimit",
            &format!(
                "Rate limit check passed. Key: {}, Remaining: {}",
                limit_key, remaining_tokens
            ),
        );

        Ok(())
    }
}
