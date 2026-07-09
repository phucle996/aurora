// ======================================================================================================
// 📂 MODULE: acr/src/core/zone.rs
//            Quản Lý Zone Cache - L1 in-memory → Redis L2 (shared) → gRPC CP fallback
//
// 🔄 LUỒNG CACHE:
//   1. L1 Cache (in-process HashMap với TTL) - riêng từng node ACR
//   2. Redis L2 Cache (shared giữa tất cả ACR node) - node nào gRPC xong thì ghi L2
//   3. gRPC fallback sang Controlplane - chỉ khi cả L1 và L2 đều miss
//      → Sau khi gRPC trả về, ghi ngược lại L1 + L2 để các node khác hưởng lợi
//
// 🔒 NEGATIVE CACHE: key không tồn tại được ghi tombstone 3 phút để tránh stampede DB
// ======================================================================================================

use crate::infra::controlplane::Nats;
use crate::observability::logger::Logger;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, Instant};
use tokio::sync::{Mutex, RwLock};

// ─── L1 Cache Primitives ────────────────────────────────────────────────────

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

// ─── ZoneItem (DTO) ─────────────────────────────────────────────────────────

#[allow(dead_code)]
#[derive(Clone, Debug)]
pub struct ZoneItem {
    pub id: String,
    pub code: String,
    pub status: String,
    pub name: String,
}

// ─── ZoneManager ────────────────────────────────────────────────────────────

pub struct ZoneManager {
    nats: Arc<Nats>,
    redis_client: Arc<redis::Client>,

    // [COMMENT]: L1 in-memory cache riêng từng node - tách biệt theo chiều lookup
    zone_code_to_id: RwLock<HashMap<String, CacheEntry<String>>>,
    zone_id_to_status: RwLock<HashMap<String, CacheEntry<String>>>,
    zone_id_to_name: RwLock<HashMap<String, CacheEntry<String>>>,

    // [COMMENT]: Single Flight Mutex - chỉ 1 goroutine trong node gọi gRPC cùng lúc
    single_flight_mutex: Mutex<()>,
}

impl ZoneManager {
    pub fn new(
        nats: Arc<Nats>,
        redis_client: Arc<redis::Client>,
    ) -> Self {
        Self {
            nats,
            redis_client,
            zone_code_to_id: RwLock::new(HashMap::new()),
            zone_id_to_status: RwLock::new(HashMap::new()),
            zone_id_to_name: RwLock::new(HashMap::new()),
            single_flight_mutex: Mutex::new(()),
        }
    }

    // ─── Public API ─────────────────────────────────────────────────────────

    pub async fn resolve_code_to_id(&self, zone_code: &str) -> Option<String> {
        self.resolve_code_to_id_and_status(zone_code)
            .await
            .map(|(id, _)| id)
    }

    pub async fn get_all_zones(&self) -> Vec<ZoneItem> {
        // [COMMENT]: Luôn sync từ gRPC để đảm bảo danh sách đầy đủ
        self.sync_zones_from_rpc().await;

        let map = self.zone_code_to_id.read().await;
        let status_map = self.zone_id_to_status.read().await;
        let name_map = self.zone_id_to_name.read().await;

        map.iter()
            .filter_map(|(code, entry)| {
                if entry.is_expired() {
                    return None;
                }
                if let CacheValue::Found(id) = &entry.value {
                    let status = status_map
                        .get(id)
                        .filter(|e| !e.is_expired())
                        .and_then(|e| {
                            if let CacheValue::Found(s) = &e.value {
                                Some(s.clone())
                            } else {
                                None
                            }
                        })
                        .unwrap_or_default();
                    let name = name_map
                        .get(id)
                        .filter(|e| !e.is_expired())
                        .and_then(|e| {
                            if let CacheValue::Found(n) = &e.value {
                                Some(n.clone())
                            } else {
                                None
                            }
                        })
                        .unwrap_or_default();
                    Some(ZoneItem {
                        id: id.clone(),
                        code: code.clone(),
                        status,
                        name,
                    })
                } else {
                    None
                }
            })
            .collect()
    }

    // ─── Core Resolution: L1 → Redis L2 → gRPC → Negative Cache ────────────

    /// Phân giải zone_code → (zone_id, status)
    /// Thứ tự: L1 Cache → Redis L2 (shared) → gRPC Controlplane → Negative Cache
    pub async fn resolve_code_to_id_and_status(&self, zone_code: &str) -> Option<(String, String)> {
        let clean_code = zone_code.trim().to_lowercase();

        // 1. L1 Cache (in-process, nhanh nhất)
        if let Some((id, status)) = self.l1_lookup(&clean_code).await {
            return Some((id, status));
        }

        // 2. Redis L2 Cache (shared giữa tất cả ACR node)
        if let Some(result) = self.l2_lookup(&clean_code).await {
            // [COMMENT]: Ghi ngược L1 để lần sau khỏi xuống Redis
            if let Some((ref id, ref status)) = result {
                self.l1_set_zone(&clean_code, id, status).await;
            } else {
                self.l1_set_negative(&clean_code).await;
            }
            return result;
        }

        // 3. gRPC Controlplane fallback (Single Flight bảo vệ)
        // [COMMENT]: Sau khi sync gRPC, kết quả ghi vào Redis L2 để các node khác dùng
        self.sync_zones_from_rpc().await;

        // Đọc lại L1 sau sync
        if let Some(result) = self.l1_lookup(&clean_code).await {
            return Some(result);
        }

        // 4. Không tìm thấy - ghi Negative Cache để tránh stampede
        Logger::sys_warn(
            "zone.manager",
            &format!(
                "Zone code '{}' not found after all fallbacks. Caching as invalid.",
                clean_code
            ),
            "invalid_zone_code",
        );
        self.l1_set_negative(&clean_code).await;
        self.l2_set_negative(&clean_code).await;

        None
    }

    pub async fn resolve_id_to_code_and_status(&self, zone_id: &str) -> Option<(String, String)> {
        // [COMMENT]: Đọc từ L1 cache theo chiều ngược lại (id → code+status)
        let status_map = self.zone_id_to_status.read().await;
        let name_map = self.zone_id_to_name.read().await;

        if let Some(status_entry) = status_map.get(zone_id) {
            if !status_entry.is_expired() {
                if let CacheValue::Found(ref status) = status_entry.value {
                    if let Some(name_entry) = name_map.get(zone_id) {
                        if !name_entry.is_expired() {
                            if let CacheValue::Found(ref code) = name_entry.value {
                                return Some((code.clone(), status.clone()));
                            }
                        }
                    }
                }
            }
        }
        drop(status_map);
        drop(name_map);

        // Fallback: sync gRPC rồi đọc lại
        self.sync_zones_from_rpc().await;

        let status_map = self.zone_id_to_status.read().await;
        let name_map = self.zone_id_to_name.read().await;
        if let (Some(s_entry), Some(n_entry)) = (status_map.get(zone_id), name_map.get(zone_id)) {
            if let (CacheValue::Found(status), CacheValue::Found(code)) =
                (&s_entry.value, &n_entry.value)
            {
                return Some((code.clone(), status.clone()));
            }
        }

        None
    }

    // ─── L1 Cache Helpers ───────────────────────────────────────────────────

    async fn l1_lookup(&self, code: &str) -> Option<(String, String)> {
        let map = self.zone_code_to_id.read().await;
        if let Some(entry) = map.get(code) {
            if entry.is_expired() {
                return None;
            }
            match &entry.value {
                CacheValue::NotFound => {
                    Logger::sys_debug("zone.manager", &format!("L1 negative cache hit: {}", code));
                    return Some(("__NOT_FOUND__".to_string(), String::new()));
                }
                CacheValue::Found(id) => {
                    let status_map = self.zone_id_to_status.read().await;
                    if let Some(s_entry) = status_map.get(id) {
                        if !s_entry.is_expired() {
                            if let CacheValue::Found(status) = &s_entry.value {
                                return Some((id.clone(), status.clone()));
                            }
                        }
                    }
                }
            }
        }
        None
    }

    async fn l1_set_zone(&self, code: &str, id: &str, status: &str) {
        let ttl = Duration::from_secs(600); // 10 phút TTL L1
        let expiry = Instant::now() + ttl;

        self.zone_code_to_id.write().await.insert(
            code.to_string(),
            CacheEntry {
                value: CacheValue::Found(id.to_string()),
                expiry,
            },
        );
        self.zone_id_to_status.write().await.insert(
            id.to_string(),
            CacheEntry {
                value: CacheValue::Found(status.to_string()),
                expiry,
            },
        );
    }

    async fn l1_set_negative(&self, code: &str) {
        // [COMMENT]: Negative cache TTL = 3 phút để tránh DB stampede
        let expiry = Instant::now() + Duration::from_secs(180);
        self.zone_code_to_id.write().await.insert(
            code.to_string(),
            CacheEntry {
                value: CacheValue::NotFound,
                expiry,
            },
        );
    }

    // ─── Redis L2 Helpers (shared giữa các ACR node) ─────────────────────────

    async fn l2_lookup(&self, code: &str) -> Option<Option<(String, String)>> {
        // Returns: None = miss L2, Some(None) = negative, Some(Some(..)) = found
        let mut conn = self.get_redis_conn().await.ok()?;
        let redis_key = format!("zone:code:{}", code);
        let val: Result<String, _> = redis::cmd("GET")
            .arg(&redis_key)
            .query_async(&mut conn)
            .await;

        match val {
            Err(_) => None, // Redis miss
            Ok(v) if v == "NOT_FOUND" => {
                Logger::sys_debug("zone.manager", &format!("L2 negative cache hit: {}", code));
                Some(None) // Negative hit
            }
            Ok(v) => {
                // Format: "uuid:status"
                if let Some(pos) = v.find(':') {
                    let id = v[..pos].to_string();
                    let status = v[pos + 1..].to_string();
                    Some(Some((id, status)))
                } else {
                    None
                }
            }
        }
    }

    async fn l2_set_zone(&self, code: &str, id: &str, status: &str) {
        if let Ok(mut conn) = self.get_redis_conn().await {
            let redis_key = format!("zone:code:{}", code);
            let redis_val = format!("{}:{}", id, status);
            let _: Result<(), _> = redis::cmd("SET")
                .arg(&redis_key)
                .arg(&redis_val)
                .arg("EX")
                .arg(86400u64) // TTL L2 = 1 ngày
                .query_async(&mut conn)
                .await;
        }
    }

    async fn l2_set_negative(&self, code: &str) {
        if let Ok(mut conn) = self.get_redis_conn().await {
            let redis_key = format!("zone:code:{}", code);
            let _: Result<(), _> = redis::cmd("SET")
                .arg(&redis_key)
                .arg("NOT_FOUND")
                .arg("EX")
                .arg(180u64) // Negative TTL L2 = 3 phút
                .query_async(&mut conn)
                .await;
        }
    }

    // ─── gRPC Sync từ Controlplane ───────────────────────────────────────────

    /// Sync toàn bộ zone list từ CP qua gRPC, ghi vào L1 + Redis L2.
    /// Dùng Mutex (single flight) để chỉ 1 task trong node thực hiện tại 1 thời điểm.
    async fn sync_zones_from_rpc(&self) {
        let _lock = self.single_flight_mutex.lock().await;

        Logger::sys_info(
            "zone.manager",
            "Syncing zones from Controlplane via gRPC...",
        );

        match self.nats.get_zone_list().await {
            Ok(zones) => {
                for z in &zones {
                    let clean_code = z.zone_code.trim().to_lowercase();

                    // [COMMENT]: Ghi L1 cache cho node này
                    self.l1_set_zone(&clean_code, &z.zone_id, &z.status).await;

                    // [COMMENT]: Ghi display name vào L1 (dùng z.name, KHÔNG phải clean_code).
                    // Bug trước: ghi clean_code vào name_map → catalog trả name == code.
                    let expiry = Instant::now() + Duration::from_secs(600);
                    self.zone_id_to_name.write().await.insert(
                        z.zone_id.clone(),
                        CacheEntry {
                            value: CacheValue::Found(z.name.clone()), // ✅ display name đúng
                            expiry,
                        },
                    );

                    // [COMMENT]: Ghi Redis L2 để các ACR node khác trong cluster đọc được
                    // Đây là cơ chế HA: 1 node gọi gRPC → ghi L2 → các node khác tự lấy L2
                    self.l2_set_zone(&clean_code, &z.zone_id, &z.status).await;
                }
                Logger::sys_info(
                    "zone.manager",
                    &format!("Synced {} zones to L1 + Redis L2.", zones.len()),
                );
            }
            Err(e) => {
                Logger::sys_error("zone.manager", "gRPC zone sync failed", &e.to_string());
            }
        }
    }

    async fn get_redis_conn(&self) -> Result<redis::aio::Connection, String> {
        self.redis_client
            .get_async_connection()
            .await
            .map_err(|e| e.to_string())
    }
}
