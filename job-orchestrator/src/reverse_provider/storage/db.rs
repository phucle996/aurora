// [COMMENT]: Storage provider DB layer (skeleton).
// Hiện tại trạng thái storage workload được theo dõi qua bảng hierarchy.zone_services chung.
// Module này sẽ được mở rộng khi schema `storage.*` được khởi tạo, ví dụ:
//   - `storage.bucket_stats` (lưu lịch sử dung lượng bucket theo zone)
//   - `storage.credential_audit` (audit log cho credential rotation sự kiện MinIO)
//
// Không có hàm nào ở đây hiện tại; các DB ops của storage workload đang sử dụng
// zone::db::update_zone_service_metrics và zone::db::update_zone_service_status.
