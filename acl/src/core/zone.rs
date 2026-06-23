// ======================================================================================================
// 📂 MODULE: acl/src/core/zone.rs
//            Quản Lý & Đồng Bộ L1 Cache Cho Các Zones Từ Controlplane (mTLS gRPC)
// ======================================================================================================
//
// 📜 THIẾT KẾ & TỐI ƯU HÓA:
//   - Duy trì bản đồ ánh xạ song hướng: zone_code <-> zone_id và lưu thêm status của từng zone trong RAM L1.
//   - Hỗ trợ cơ chế Single Flight thông qua tokio::sync::Mutex để tránh bão request (thundering herd) lên CP.
//   - Lưu vết last_sync để giới hạn tần suất gọi RPC: tối đa 5 phút một lần cho các trường hợp miss/không tồn tại.
//
// ======================================================================================================

use crate::infra::controlplane::ControlPlaneClient;
use crate::observability::logger::Logger;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, Instant};
use tokio::sync::{Mutex, RwLock};

#[allow(dead_code)]
#[derive(Clone, Debug)]
pub struct ZoneItem {
    pub id: String,
    pub code: String,
    pub status: String,
    pub name: String,
}

pub struct ZoneManager {
    // Client gọi gRPC sang Controlplane
    control_plane_client: Arc<ControlPlaneClient>,

    // Bản đồ L1 RAM cache ánh xạ từ zone_code sang zone_id
    code_to_id: RwLock<HashMap<String, String>>,

    // Bản đồ L1 RAM cache ánh xạ từ zone_id sang zone_code
    id_to_code: RwLock<HashMap<String, String>>,

    // [COMMENT]: Bản đồ L1 RAM cache ánh xạ từ zone_id sang status của zone
    id_to_status: RwLock<HashMap<String, String>>,

    // [COMMENT]: Bản đồ L1 RAM cache ánh xạ từ zone_id sang name của zone
    id_to_name: RwLock<HashMap<String, String>>,

    // [COMMENT]: Cache chứa danh sách các zone code không hợp lệ (bad zone codes) để chống spam
    bad_codes: RwLock<HashMap<String, Instant>>,

    // Lưu Instant của lần gọi RPC đồng bộ thành công/thất bại gần nhất
    last_sync: RwLock<Option<Instant>>,

    // Mutex ngăn chặn thundering herd: chỉ cho phép tối đa 1 goroutine/task gọi gRPC tại một thời điểm (Single Flight)
    single_flight_mutex: Mutex<()>,
}

impl ZoneManager {
    // Khởi tạo thực thể quản lý zone cache
    pub fn new(control_plane_client: Arc<ControlPlaneClient>) -> Self {
        Self {
            control_plane_client,
            code_to_id: RwLock::new(HashMap::new()),
            id_to_code: RwLock::new(HashMap::new()),
            id_to_status: RwLock::new(HashMap::new()),
            id_to_name: RwLock::new(HashMap::new()),
            bad_codes: RwLock::new(HashMap::new()),
            last_sync: RwLock::new(None),
            single_flight_mutex: Mutex::new(()),
        }
    }

    // [COMMENT]: Lấy toàn bộ danh sách zone từ L1 cache (hoặc sync nếu cần)
    pub async fn get_all_zones(&self) -> Vec<ZoneItem> {
        // [COMMENT]: Kích hoạt đồng bộ hóa nếu bộ nhớ đệm chưa được nạp hoặc hết hạn
        let _ = self.sync_zones_if_needed().await;

        let code_map = self.code_to_id.read().await;
        let status_map = self.id_to_status.read().await;
        let name_map = self.id_to_name.read().await;

        let mut items = Vec::new();
        for (code, id) in code_map.iter() {
            let status = status_map.get(id).cloned().unwrap_or_default();
            let name = name_map.get(id).cloned().unwrap_or_default();
            items.push(ZoneItem {
                id: id.clone(),
                code: code.clone(),
                status,
                name,
            });
        }
        items
    }

    // [COMMENT]: Tìm kiếm zone_id dựa vào zone_code
    pub async fn resolve_code_to_id(&self, zone_code: &str) -> Option<String> {
        self.resolve_code_to_id_and_status(zone_code)
            .await
            .map(|(id, _)| id)
    }

    // [COMMENT]: Tìm kiếm zone_code dựa vào zone_id
    pub async fn resolve_id_to_code(&self, zone_id: &str) -> Option<String> {
        self.resolve_id_to_code_and_status(zone_id)
            .await
            .map(|(code, _)| code)
    }

    // [COMMENT]: Tìm kiếm zone_id và status dựa vào zone_code, hỗ trợ RPC sync khi cache miss và cache bad codes
    pub async fn resolve_code_to_id_and_status(&self, zone_code: &str) -> Option<(String, String)> {
        // [COMMENT]: 1. Thử đọc từ RAM cache L1 xem có tồn tại không
        {
            let id_map = self.code_to_id.read().await;
            let status_map = self.id_to_status.read().await;
            if let Some(id) = id_map.get(zone_code) {
                if let Some(status) = status_map.get(id) {
                    return Some((id.clone(), status.clone()));
                }
            }
        }

        // [COMMENT]: 2. Kiểm tra danh sách bad codes xem có phải zone code không tồn tại đang bị spam không
        {
            let bad = self.bad_codes.read().await;
            if let Some(expiry) = bad.get(zone_code) {
                if *expiry > Instant::now() {
                    Logger::sys_debug(
                        "zone.manager",
                        &format!("Zone code '{}' is cached as invalid. Fast failing request to prevent spam.", zone_code),
                    );
                    return None;
                }
            }
        }

        // [COMMENT]: 3. Thực hiện đồng bộ qua gRPC đến Controlplane để cập nhật danh sách mới
        if self.sync_zones_if_needed().await {
            // [COMMENT]: Đọc lại RAM cache L1 sau khi đã đồng bộ thành công
            let id_map = self.code_to_id.read().await;
            let status_map = self.id_to_status.read().await;
            if let Some(id) = id_map.get(zone_code) {
                if let Some(status) = status_map.get(id) {
                    return Some((id.clone(), status.clone()));
                }
            }
        }

        // [COMMENT]: 4. Nếu sau khi đồng bộ vẫn không tìm thấy -> đây là zone code không tồn tại. Cache lại để chặn spam.
        Logger::sys_warn(
            "zone.manager",
            &format!("Zone code '{}' not found after sync. Caching as invalid zone code.", zone_code),
            "invalid_zone_code",
        );
        let mut bad = self.bad_codes.write().await;
        // Cache zone lỗi trong vòng 5 phút (300 giây)
        bad.insert(zone_code.to_string(), Instant::now() + Duration::from_secs(300));

        None
    }

    // [COMMENT]: Tìm kiếm zone_code và status dựa vào zone_id, hỗ trợ RPC sync khi cache miss
    pub async fn resolve_id_to_code_and_status(&self, zone_id: &str) -> Option<(String, String)> {
        // Thử đọc từ RAM cache L1 (đọc đồng thời)
        {
            let code_map = self.id_to_code.read().await;
            let status_map = self.id_to_status.read().await;
            if let Some(code) = code_map.get(zone_id) {
                if let Some(status) = status_map.get(zone_id) {
                    return Some((code.clone(), status.clone()));
                }
            }
        }

        // Cache miss, tiến hành gọi RPC đồng bộ thông qua cơ chế Single Flight
        if self.sync_zones_if_needed().await {
            // Đọc lại sau khi đồng bộ thành công
            let code_map = self.id_to_code.read().await;
            let status_map = self.id_to_status.read().await;
            if let Some(code) = code_map.get(zone_id) {
                if let Some(status) = status_map.get(zone_id) {
                    return Some((code.clone(), status.clone()));
                }
            }
        }

        None
    }

    // [COMMENT]: Kiểm tra và tiến hành đồng bộ các Zones từ Controlplane
    async fn sync_zones_if_needed(&self) -> bool {
        // Kiểm tra xem lần đồng bộ trước có nằm trong vòng 5 phút (300 giây) không
        // Nếu vừa gọi gần đây mà vẫn miss chứng tỏ zone thực sự không tồn tại, chặn spam RPC
        {
            let last = self.last_sync.read().await;
            if let Some(instant) = *last {
                if instant.elapsed() < Duration::from_secs(300) {
                    return false;
                }
            }
        }

        // Đăng ký/Chờ Single Flight Lock
        let _lock = self.single_flight_mutex.lock().await;

        // Double check sau khi lấy được lock (có thể luồng khác vừa chạy xong)
        {
            let last = self.last_sync.read().await;
            if let Some(instant) = *last {
                if instant.elapsed() < Duration::from_secs(300) {
                    return true;
                }
            }
        }

        Logger::sys_info(
            "zone.manager",
            "Triggering gRPC sync for zones from Controlplane...",
        );

        // Gọi RPC qua CP để lấy zone catalog
        match self.control_plane_client.get_zone_list().await {
            Ok(zones) => {
                let mut code_map = HashMap::new();
                let mut id_map = HashMap::new();
                let mut status_map = HashMap::new();
                let mut name_map = HashMap::new();
                for z in zones {
                    code_map.insert(z.zone_code.clone(), z.zone_id.clone());
                    id_map.insert(z.zone_id.clone(), z.zone_code.clone());
                    // [COMMENT]: Lưu trữ thêm status nhận từ gRPC phục vụ kiểm tra điều kiện an toàn
                    status_map.insert(z.zone_id.clone(), z.status.clone());
                    // [COMMENT]: Lưu trữ thêm name phục vụ trả về catalog
                    name_map.insert(z.zone_id.clone(), z.name.clone());
                }

                // Lưu trữ kết quả vào RAM cache L1
                *self.code_to_id.write().await = code_map;
                *self.id_to_code.write().await = id_map;
                *self.id_to_status.write().await = status_map;
                *self.id_to_name.write().await = name_map;
                *self.last_sync.write().await = Some(Instant::now());

                Logger::sys_info("zone.manager", "Zone sync completed successfully.");
                true
            }
            Err(status) => {
                Logger::sys_error(
                    "zone.manager",
                    "Failed to sync zones from Controlplane",
                    &status.to_string(),
                );
                // Cập nhật thời gian đồng bộ kể cả khi lỗi để chặn thòng gọi RPC liên tục khi CP gặp sự cố (Fail-safe)
                *self.last_sync.write().await = Some(Instant::now());
                false
            }
        }
    }
}
