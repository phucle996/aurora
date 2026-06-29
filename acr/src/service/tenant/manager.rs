// ======================================================================================================
// 📂 MODULE: acr/src/service/tenant/manager.rs
//            Quản Lý Tenant Cache - L1 in-memory → Redis L2 (shared) → gRPC CP fallback
//
// 🔄 LUỒNG RESOLUTION (domain → tenant_id):
//   1. L1 Cache (HashMap<domain, CacheEntry<tenant_id>>, TTL 10 min, riêng từng node)
//   2. Redis L2 Cache (shared toàn cluster, key: tenant:domain:{domain})
//      → Sau khi 1 node gRPC + ghi L2, node khác tự đọc L2 không cần gRPC lại
//   3. gRPC ResolveTenant sang Controlplane (Single Flight trong node)
//   4. Negative Cache (tombstone 3 phút nếu domain không tồn tại)
//
// 🔄 LUỒNG MEMBERSHIP (tenant_id + user_id → is_member):
//   1. L1 Cache (HashMap<(tenant_id,user_id), CacheEntry<bool+role>>, TTL 5 min)
//      → TTL ngắn để membership revoke có hiệu lực trong 5 phút
//   2. gRPC CheckMembership sang Controlplane (không cache ở Redis L2 vì security-sensitive)
//
// 🔄 LUỒNG WARMUP (bootstrap):
//   Gọi WarmupTenants theo chunk, ghi domain→id vào Redis L2 cho cluster dùng chung
// ======================================================================================================

use crate::infra::controlplane::ControlPlaneClient;
use crate::observability::logger::Logger;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, Instant};
use tokio::sync::{Mutex, RwLock};

// ─── L1 Cache Primitives ─────────────────────────────────────────────────────

#[derive(Clone, Debug)]
pub enum CacheValue<T> {
    Found(T),
    NotFound, // Negative cache tombstone
}

struct CacheEntry<T> {
    value: CacheValue<T>,
    expiry: Instant,
}

impl<T> CacheEntry<T> {
    fn is_expired(&self) -> bool {
        Instant::now() > self.expiry
    }
}

// ─── MembershipInfo ─────────────────────────────────────────────────────────

#[derive(Clone, Debug)]
pub struct MembershipInfo {
    pub is_member: bool,
    pub role: String,
}

// ─── TenantManager ──────────────────────────────────────────────────────────

pub struct TenantManager {
    control_plane_client: Arc<ControlPlaneClient>,
    redis_client: Arc<redis::Client>,

    // [COMMENT]: L1 Resolution cache: domain → tenant_id (TTL 10 min)
    // domain là source of truth - tenant_code đã bỏ hoàn toàn
    domain_to_id: RwLock<HashMap<String, CacheEntry<String>>>,

    // [COMMENT]: L1 Membership cache: "{tenant_id}:{user_id}" → MembershipInfo (TTL 5 min)
    // TTL ngắn để revoke có hiệu lực nhanh
    membership_cache: RwLock<HashMap<String, CacheEntry<MembershipInfo>>>,

    // [COMMENT]: Single Flight cho resolution - tránh thundering herd khi nhiều request cùng miss
    resolution_sf: Mutex<()>,
}

impl TenantManager {
    pub fn new(
        control_plane_client: Arc<ControlPlaneClient>,
        redis_client: Arc<redis::Client>,
    ) -> Arc<Self> {
        Arc::new(Self {
            control_plane_client,
            redis_client,
            domain_to_id: RwLock::new(HashMap::new()),
            membership_cache: RwLock::new(HashMap::new()),
            resolution_sf: Mutex::new(()),
        })
    }

    // ─── Public API: Resolution ───────────────────────────────────────────────

    /// Phân giải tenant_domain → tenant_id
    /// Thứ tự: L1 → Redis L2 → gRPC CP → Negative Cache
    pub async fn resolve_tenant_id(&self, domain: &str) -> Option<String> {
        let domain = domain.trim().to_lowercase();

        // 1. L1 Cache
        if let Some(result) = self.l1_resolve(&domain).await {
            return result;
        }

        // 2. Redis L2 Cache (shared giữa tất cả ACR node)
        if let Some(l2_result) = self.l2_resolve(&domain).await {
            // [COMMENT]: Populate L1 từ L2 để lần sau khỏi xuống Redis
            match &l2_result {
                Some(id) => self.l1_set(&domain, id).await,
                None => self.l1_set_negative(&domain).await,
            }
            return l2_result;
        }

        // 3. gRPC Controlplane (Single Flight trong node)
        // [COMMENT]: Sau khi gRPC trả về, ghi cả L1 + L2 để các node khác hưởng lợi
        let _lock = self.resolution_sf.lock().await;

        // [COMMENT]: Double-check L1 sau khi có lock (tránh duplicate RPC nếu vừa có node khác fill)
        if let Some(result) = self.l1_resolve(&domain).await {
            return result;
        }

        match self.control_plane_client.resolve_tenant(&domain).await {
            Ok(resp) if resp.found => {
                let id = resp.tenant_id;
                self.l1_set(&domain, &id).await;
                self.l2_set(&domain, &id).await; // Ghi L2 để các node khác dùng
                Logger::sys_info(
                    "tenant.manager",
                    &format!("Resolved domain '{}' → tenant_id '{}'", domain, id),
                );
                Some(id)
            }
            Ok(_) => {
                // [COMMENT]: Domain không tồn tại - ghi Negative Cache
                Logger::sys_warn(
                    "tenant.manager",
                    &format!("Domain '{}' not found in CP. Caching as invalid.", domain),
                    "tenant_not_found",
                );
                self.l1_set_negative(&domain).await;
                self.l2_set_negative(&domain).await;
                None
            }
            Err(e) => {
                Logger::sys_error(
                    "tenant.manager",
                    "gRPC resolve_tenant failed",
                    &e.to_string(),
                );
                None
            }
        }
    }

    // ─── Public API: Membership ───────────────────────────────────────────────

    /// Kiểm tra user có thuộc tenant không.
    /// Thứ tự: L1 Cache (5 min TTL) → gRPC CheckMembership
    /// Không dùng Redis L2 cho membership - security-sensitive, L1 đủ để tránh duplicate RPC
    pub async fn check_membership(&self, tenant_id: &str, user_id: &str) -> MembershipInfo {
        let cache_key = format!("{}:{}", tenant_id, user_id);

        // 1. L1 Membership Cache
        {
            let map = self.membership_cache.read().await;
            if let Some(entry) = map.get(&cache_key) {
                if !entry.is_expired() {
                    if let CacheValue::Found(ref info) = entry.value {
                        return info.clone();
                    }
                }
            }
        }

        // 2. gRPC CheckMembership từ CP (authoritative source)
        match self
            .control_plane_client
            .check_membership(tenant_id, user_id)
            .await
        {
            Ok(resp) => {
                let info = MembershipInfo {
                    is_member: resp.is_member,
                    role: resp.role,
                };
                // [COMMENT]: Cache kết quả 5 phút - membership ít thay đổi,
                // nhưng TTL ngắn đảm bảo revoke có hiệu lực trong 5 phút
                self.membership_cache_set(&cache_key, info.clone()).await;
                info
            }
            Err(e) => {
                Logger::sys_error(
                    "tenant.manager",
                    "gRPC check_membership failed - defaulting to not-member",
                    &e.to_string(),
                );
                // [COMMENT]: Fail-closed: nếu gRPC lỗi, từ chối truy cập (không cache lỗi)
                MembershipInfo {
                    is_member: false,
                    role: String::new(),
                }
            }
        }
    }

    // ─── Warmup: Bootstrap Redis L2 theo chunk ───────────────────────────────

    /// Warmup Redis L2 bằng cách fetch theo chunk từ CP.
    /// Gọi khi ACR bootstrap: check L2 trước, nếu thiếu thì warmup.
    pub async fn warmup_if_needed(&self) {
        // [COMMENT]: Kiểm tra xem Redis L2 đã có data chưa (lấy 1 key bất kỳ)
        let has_data = self.l2_has_any_data().await;
        if has_data {
            Logger::sys_info(
                "tenant.manager",
                "Redis L2 already warmed up, skipping warmup.",
            );
            return;
        }

        Logger::sys_info(
            "tenant.manager",
            "Starting tenant warmup from Controlplane...",
        );
        self.run_warmup().await;
    }

    async fn run_warmup(&self) {
        let chunk_size = 500i32;
        let mut offset = 0i32;
        let mut total = 0usize;

        loop {
            match self
                .control_plane_client
                .warmup_tenants(chunk_size, offset)
                .await
            {
                Ok(resp) => {
                    let count = resp.tenants.len();
                    for entry in &resp.tenants {
                        // [COMMENT]: Chỉ ghi L2 trong warmup - L1 sẽ được fill khi có request thực
                        self.l2_set(&entry.domain.trim().to_lowercase(), &entry.tenant_id)
                            .await;
                    }
                    total += count;
                    Logger::sys_info(
                        "tenant.manager",
                        &format!(
                            "Warmup chunk: {} entries (offset={}, total={})",
                            count, offset, total
                        ),
                    );

                    if !resp.has_more {
                        break;
                    }
                    offset += chunk_size;
                }
                Err(e) => {
                    Logger::sys_error("tenant.manager", "Warmup chunk failed", &e.to_string());
                    break;
                }
            }
        }

        Logger::sys_info(
            "tenant.manager",
            &format!(
                "Tenant warmup complete: {} entries written to Redis L2.",
                total
            ),
        );
    }

    // ─── L1 Helpers ──────────────────────────────────────────────────────────

    async fn l1_resolve(&self, domain: &str) -> Option<Option<String>> {
        // Returns: None = miss, Some(None) = negative, Some(Some(id)) = found
        let map = self.domain_to_id.read().await;
        if let Some(entry) = map.get(domain) {
            if entry.is_expired() {
                return None;
            }
            return match &entry.value {
                CacheValue::NotFound => Some(None),
                CacheValue::Found(id) => Some(Some(id.clone())),
            };
        }
        None
    }

    async fn l1_set(&self, domain: &str, id: &str) {
        let expiry = Instant::now() + Duration::from_secs(600); // 10 min L1 TTL
        self.domain_to_id.write().await.insert(
            domain.to_string(),
            CacheEntry {
                value: CacheValue::Found(id.to_string()),
                expiry,
            },
        );
    }

    async fn l1_set_negative(&self, domain: &str) {
        // [COMMENT]: Negative cache L1 = 3 phút để tránh hammering CP
        let expiry = Instant::now() + Duration::from_secs(180);
        self.domain_to_id.write().await.insert(
            domain.to_string(),
            CacheEntry {
                value: CacheValue::NotFound,
                expiry,
            },
        );
    }

    async fn membership_cache_set(&self, key: &str, info: MembershipInfo) {
        // [COMMENT]: Membership TTL = 5 phút - ngắn hơn resolution TTL vì security-sensitive
        let expiry = Instant::now() + Duration::from_secs(300);
        self.membership_cache.write().await.insert(
            key.to_string(),
            CacheEntry {
                value: CacheValue::Found(info),
                expiry,
            },
        );
    }

    // ─── Redis L2 Helpers ─────────────────────────────────────────────────────

    async fn l2_has_any_data(&self) -> bool {
        if let Ok(mut conn) = self.get_redis_conn().await {
            // [COMMENT]: Kiểm tra nhanh bằng SCAN MATCH với count nhỏ
            let result: Result<(String, Vec<String>), _> = redis::cmd("SCAN")
                .arg("0")
                .arg("MATCH")
                .arg("tenant:domain:*")
                .arg("COUNT")
                .arg(1)
                .query_async(&mut conn)
                .await;
            if let Ok((_, keys)) = result {
                return !keys.is_empty();
            }
        }
        false
    }

    async fn l2_resolve(&self, domain: &str) -> Option<Option<String>> {
        // Returns: None = miss, Some(None) = negative, Some(Some(id)) = found
        let mut conn = self.get_redis_conn().await.ok()?;
        let redis_key = format!("tenant:domain:{}", domain);
        let val: Result<String, _> = redis::cmd("GET")
            .arg(&redis_key)
            .query_async(&mut conn)
            .await;

        match val {
            Err(_) => None, // Miss L2
            Ok(v) if v == "NOT_FOUND" => {
                Logger::sys_debug(
                    "tenant.manager",
                    &format!("L2 negative cache hit: {}", domain),
                );
                Some(None)
            }
            Ok(id) => Some(Some(id)),
        }
    }

    async fn l2_set(&self, domain: &str, id: &str) {
        if let Ok(mut conn) = self.get_redis_conn().await {
            let redis_key = format!("tenant:domain:{}", domain);
            let _: Result<(), _> = redis::cmd("SET")
                .arg(&redis_key)
                .arg(id)
                .arg("EX")
                .arg(86400u64) // TTL L2 = 1 ngày
                .query_async(&mut conn)
                .await;
        }
    }

    async fn l2_set_negative(&self, domain: &str) {
        if let Ok(mut conn) = self.get_redis_conn().await {
            let redis_key = format!("tenant:domain:{}", domain);
            let _: Result<(), _> = redis::cmd("SET")
                .arg(&redis_key)
                .arg("NOT_FOUND")
                .arg("EX")
                .arg(180u64) // Negative TTL L2 = 3 phút
                .query_async(&mut conn)
                .await;
        }
    }

    async fn get_redis_conn(&self) -> Result<redis::aio::Connection, String> {
        self.redis_client
            .get_async_connection()
            .await
            .map_err(|e| e.to_string())
    }
}
