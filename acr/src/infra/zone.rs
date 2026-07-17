// ======================================================================================================
// 📂 infra/zone.rs — Shared L1/L2 Zone Cache Primitives & NATS Zone Client
//
// 📌 VAI TRÒ:
//   - Cung cấp bộ L1 in-process Cache dùng chung (Shared L1 Cache) cho mọi domain (user, sre, billing).
//   - Nếu miscache mà không dính Negative Cache -> gọi NATS Core `hierarchy.zone.get_zone_list` sync catalog.
// ======================================================================================================

use crate::infra::nats::Nats;
use crate::observability::logger::Logger;
use std::collections::HashMap;
use std::sync::OnceLock;
use std::time::{Duration, Instant};
use tokio::sync::{Mutex, RwLock};

#[allow(dead_code)]
pub mod zone_proto {
    tonic::include_proto!("core.rpc");
}

use prost::Message as _;

/// Gọi NATS Core request-reply đến Controlplane để lấy toàn bộ danh sách Zone.
pub async fn fetch_all_zones_from_nats(nats: &Nats) -> Result<Vec<zone_proto::ZoneEntry>, String> {
    let req = zone_proto::GetZoneListRequest {};
    let mut buf = Vec::new();
    req.encode(&mut buf)
        .map_err(|e| format!("zone.nats: failed to encode GetZoneListRequest: {}", e))?;

    let msg = nats
        .client()
        .request("hierarchy.zone.get_zone_list".to_string(), buf.into())
        .await
        .map_err(|e| format!("zone.nats: NATS request failed: {}", e))?;

    let resp = zone_proto::GetZoneListResponse::decode(msg.payload)
        .map_err(|e| format!("zone.nats: failed to decode GetZoneListResponse: {}", e))?;

    Ok(resp.zones)
}

#[derive(Clone, Debug)]
pub enum CacheValue<T> {
    Found(T),
    NotFound,
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

#[derive(Clone, Debug)]
pub struct ZoneItem {
    pub code: String,
    pub status: String,
    pub name: String,
}

// ─── Shared L1 Cache Instance ──────────────────────────────────────────────────

struct SharedL1ZoneCache {
    code_to_id: RwLock<HashMap<String, CacheEntry<String>>>,
    id_to_status: RwLock<HashMap<String, CacheEntry<String>>>,
    id_to_name: RwLock<HashMap<String, CacheEntry<String>>>,
    single_flight: Mutex<()>,
}

fn get_l1_cache() -> &'static SharedL1ZoneCache {
    static L1_CACHE: OnceLock<SharedL1ZoneCache> = OnceLock::new();
    L1_CACHE.get_or_init(|| SharedL1ZoneCache {
        code_to_id: RwLock::new(HashMap::new()),
        id_to_status: RwLock::new(HashMap::new()),
        id_to_name: RwLock::new(HashMap::new()),
        single_flight: Mutex::new(()),
    })
}

// ─── Public Functions ──────────────────────────────────────────────────────────

/// [COMMENT]: Phân giải zone_code -> (zone_id, status) qua L1 -> Redis L2 -> NATS Fallback
pub async fn resolve_code_to_id_and_status(
    nats: &Nats,
    redis_client: &redis::Client,
    zone_code: &str,
) -> Option<(String, String)> {
    let clean_code = zone_code.trim().to_lowercase();

    // 1. L1 Lookup
    if let Some((id, status)) = l1_lookup_code(&clean_code).await {
        if id == "__NOT_FOUND__" {
            return None; // Negative hit
        }
        return Some((id, status));
    }

    // 2. Redis L2 Lookup
    if let Some(l2_res) = l2_lookup_code(redis_client, &clean_code).await {
        match l2_res {
            Some((id, status)) => {
                l1_set_zone(&clean_code, &id, &status, "").await;
                return Some((id, status));
            }
            None => {
                l1_set_negative(&clean_code).await;
                return None; // Negative hit from Redis
            }
        }
    }

    // 3. Mis-cache (không dính Negative) -> Call NATS Core sync toàn bộ zone catalog
    sync_zones_from_nats(nats, redis_client).await;

    // 4. Retry L1 sau sync
    if let Some((id, status)) = l1_lookup_code(&clean_code).await {
        if id == "__NOT_FOUND__" {
            return None;
        }
        return Some((id, status));
    }

    // 5. Vẫn không có -> Đánh dấu Negative Cache (3 phút)
    Logger::sys_warn(
        "zone.cache",
        &format!(
            "Zone code '{}' not found after NATS sync. Set negative cache.",
            clean_code
        ),
        "invalid_zone",
    );
    l1_set_negative(&clean_code).await;
    l2_set_negative(redis_client, &clean_code).await;

    None
}


/// [COMMENT]: Lấy danh sách toàn bộ zones từ Shared L1 Cache
pub async fn get_all_zones(nats: &Nats, redis_client: &redis::Client) -> Vec<ZoneItem> {
    sync_zones_from_nats(nats, redis_client).await;

    let cache = get_l1_cache();
    let map = cache.code_to_id.read().await;
    let status_map = cache.id_to_status.read().await;
    let name_map = cache.id_to_name.read().await;

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

/// [COMMENT]: Invalidate zone cache khi nhận NATS event
pub async fn invalidate_zone(event: &zone_proto::ZoneInvalidatedEvent) {
    let clean_code = event.zone_code.trim().to_lowercase();
    if event.deleted {
        l1_set_negative(&clean_code).await;
    } else {
        l1_set_zone(&clean_code, &event.zone_id, &event.status, &event.name).await;
    }
}

// ─── Private L1 / L2 Helpers ───────────────────────────────────────────────────

async fn l1_lookup_code(code: &str) -> Option<(String, String)> {
    let cache = get_l1_cache();
    let map = cache.code_to_id.read().await;
    if let Some(entry) = map.get(code) {
        if entry.is_expired() {
            return None;
        }
        match &entry.value {
            CacheValue::NotFound => {
                return Some(("__NOT_FOUND__".to_string(), String::new()));
            }
            CacheValue::Found(id) => {
                let status_map = cache.id_to_status.read().await;
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


async fn l1_set_zone(code: &str, id: &str, status: &str, name: &str) {
    let expiry = Instant::now() + Duration::from_secs(600);

    let cache = get_l1_cache();
    cache.code_to_id.write().await.insert(
        code.to_string(),
        CacheEntry {
            value: CacheValue::Found(id.to_string()),
            expiry,
        },
    );
    cache.id_to_status.write().await.insert(
        id.to_string(),
        CacheEntry {
            value: CacheValue::Found(status.to_string()),
            expiry,
        },
    );
    if !name.is_empty() {
        cache.id_to_name.write().await.insert(
            id.to_string(),
            CacheEntry {
                value: CacheValue::Found(name.to_string()),
                expiry,
            },
        );
    }
}

async fn l1_set_negative(code: &str) {
    let expiry = Instant::now() + Duration::from_secs(180); // Negative TTL = 3m
    get_l1_cache().code_to_id.write().await.insert(
        code.to_string(),
        CacheEntry {
            value: CacheValue::NotFound,
            expiry,
        },
    );
}

async fn l2_lookup_code(
    redis_client: &redis::Client,
    code: &str,
) -> Option<Option<(String, String)>> {
    let mut conn = redis_client.get_async_connection().await.ok()?;
    let redis_key = format!("zone:code:{}", code);
    let val: Result<String, _> = redis::cmd("GET")
        .arg(&redis_key)
        .query_async(&mut conn)
        .await;

    match val {
        Err(_) => None,
        Ok(v) if v == "NOT_FOUND" => Some(None),
        Ok(v) => {
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

async fn l2_set_negative(redis_client: &redis::Client, code: &str) {
    if let Ok(mut conn) = redis_client.get_async_connection().await {
        let redis_key = format!("zone:code:{}", code);
        let _: Result<(), _> = redis::cmd("SET")
            .arg(&redis_key)
            .arg("NOT_FOUND")
            .arg("EX")
            .arg(180u64)
            .query_async(&mut conn)
            .await;
    }
}

async fn sync_zones_from_nats(nats: &Nats, redis_client: &redis::Client) {
    let _lock = get_l1_cache().single_flight.lock().await;

    if let Ok(zones) = fetch_all_zones_from_nats(nats).await {
        for z in &zones {
            let clean_code = z.zone_code.trim().to_lowercase();
            l1_set_zone(&clean_code, &z.zone_id, &z.status, &z.name).await;

            if let Ok(mut conn) = redis_client.get_async_connection().await {
                let redis_key = format!("zone:code:{}", clean_code);
                let redis_val = format!("{}:{}", z.zone_id, z.status);
                let _: Result<(), _> = redis::cmd("SET")
                    .arg(&redis_key)
                    .arg(&redis_val)
                    .arg("EX")
                    .arg(86400u64)
                    .query_async(&mut conn)
                    .await;
            }
        }
    }
}
