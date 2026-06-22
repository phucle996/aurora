// ======================================================================================================
// 📂 MODULE: acl/src/core/zone.rs
//            Quản Lý & Đồng Bộ L1 Cache Cho Các Zones Từ Controlplane (mTLS gRPC)
// ======================================================================================================
//
// 📜 THIẾT KẾ & TỐI ƯU HÓA:
//   - Duy trì bản đồ ánh xạ song hướng: zone_code <-> zone_id trong RAM L1.
//   - Hỗ trợ cơ chế Single Flight thông qua tokio::sync::Mutex để tránh bão request (thundering herd) lên CP.
//   - Lưu vết last_sync để giới hạn tần suất gọi RPC: tối đa 5 phút một lần cho các trường hợp miss/không tồn tại.
//
// ======================================================================================================

use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, Instant};
use tokio::sync::{RwLock, Mutex};
use crate::infra::controlplane::ControlPlaneClient;
use crate::observability::logger::Logger;

pub struct ZoneManager {
    // Client gọi gRPC sang Controlplane
    control_plane_client: Arc<ControlPlaneClient>,
    
    // Bản đồ L1 RAM cache ánh xạ từ zone_code sang zone_id
    code_to_id: RwLock<HashMap<String, String>>,
    
    // Bản đồ L1 RAM cache ánh xạ từ zone_id sang zone_code
    id_to_code: RwLock<HashMap<String, String>>,
    
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
            last_sync: RwLock::new(None),
            single_flight_mutex: Mutex::new(()),
        }
    }

    // [COMMENT]: Tìm kiếm zone_id dựa vào zone_code, nếu không có sẽ tự động đồng bộ (RPC)
    pub async fn resolve_code_to_id(&self, zone_code: &str) -> Option<String> {
        // [COMMENT]: Thử đọc từ RAM cache L1 (đọc đồng thời)
        {
            let map = self.code_to_id.read().await;
            if let Some(id) = map.get(zone_code) {
                return Some(id.clone());
            }
        }

        // [COMMENT]: Cache miss, tiến hành gọi RPC đồng bộ thông qua cơ chế Single Flight
        if self.sync_zones_if_needed().await {
            // [COMMENT]: Đọc lại sau khi đồng bộ thành công
            let map = self.code_to_id.read().await;
            return map.get(zone_code).cloned();
        }

        None
    }

    // [COMMENT]: Tìm kiếm zone_code dựa vào zone_id, nếu không có sẽ tự động đồng bộ (RPC)
    pub async fn resolve_id_to_code(&self, zone_id: &str) -> Option<String> {
        // [COMMENT]: Thử đọc từ RAM cache L1 (đọc đồng thời)
        {
            let map = self.id_to_code.read().await;
            if let Some(code) = map.get(zone_id) {
                return Some(code.clone());
            }
        }

        // [COMMENT]: Cache miss, tiến hành gọi RPC đồng bộ thông qua cơ chế Single Flight
        if self.sync_zones_if_needed().await {
            // [COMMENT]: Đọc lại sau khi đồng bộ thành công
            let map = self.id_to_code.read().await;
            return map.get(zone_id).cloned();
        }

        None
    }

    // [COMMENT]: Kiểm tra và tiến hành đồng bộ các Zones từ Controlplane
    async fn sync_zones_if_needed(&self) -> bool {
        // [COMMENT]: Kiểm tra xem lần đồng bộ trước có nằm trong vòng 5 phút (300 giây) không
        // Nếu vừa gọi gần đây mà vẫn miss chứng tỏ zone thực sự không tồn tại, chặn spam RPC
        {
            let last = self.last_sync.read().await;
            if let Some(instant) = *last {
                if instant.elapsed() < Duration::from_secs(300) {
                    return false;
                }
            }
        }

        // [COMMENT]: Đăng ký/Chờ Single Flight Lock
        let _lock = self.single_flight_mutex.lock().await;

        // [COMMENT]: Double check sau khi lấy được lock (có thể luồng khác vừa chạy xong)
        {
            let last = self.last_sync.read().await;
            if let Some(instant) = *last {
                if instant.elapsed() < Duration::from_secs(300) {
                    return true;
                }
            }
        }

        Logger::sys_info("zone.manager", "Triggering gRPC sync for zones from Controlplane...");

        // [COMMENT]: Gọi RPC qua CP để lấy zone catalog
        match self.control_plane_client.get_zone_list().await {
            Ok(zones) => {
                let mut code_map = HashMap::new();
                let mut id_map = HashMap::new();
                for z in zones {
                    code_map.insert(z.zone_code.clone(), z.zone_id.clone());
                    id_map.insert(z.zone_id.clone(), z.zone_code.clone());
                }

                // [COMMENT]: Lưu trữ kết quả vào RAM cache L1
                *self.code_to_id.write().await = code_map;
                *self.id_to_code.write().await = id_map;
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
                // [COMMENT]: Cập nhật thời gian đồng bộ kể cả khi lỗi để chặn thòng gọi RPC liên tục khi CP gặp sự cố (Fail-safe)
                *self.last_sync.write().await = Some(Instant::now());
                false
            }
        }
    }
}
