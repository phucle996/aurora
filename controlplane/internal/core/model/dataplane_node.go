// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/model/dataplane_node.go
//            Đặc Tả Mô Hình DTO (Data Transfer Object) Dataplane Node
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & TÁCH BIỆT MÔ HÌNH (CONTRACT & MODEL-ENTITY DECOUPLING):
//   - Định nghĩa DTO (Data Transfer Object) thô dùng để truyền tải dữ liệu qua mạng (API HTTP/gRPC)
//     hoặc ánh xạ trực tiếp từ các trường thô trong PostgreSQL.
//   - Thực thi triết lý thiết kế decoupled hoàn hảo:
//
//     1) TRUYỀN TẢI NGUYÊN BẢN (PRIMITIVE TRANSPORT TYPES):
//        * Sử dụng các kiểu dữ liệu cơ bản (primitive types như string thay vì custom enum)
//          giúp tối giản hóa quá trình serialize/deserialize JSON và tương thích hoàn hảo các API contract.
//
//     2) KHÔNG CHỨA LOGIC NGHIỆP VỤ (ZERO BUSINESS LOGIC):
//        * Struct thô không chứa bất kỳ ràng buộc state machine hay business rules nào,
//          bảo vệ domain layer khỏi sự ảnh hưởng khi database schema thay đổi.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Nguồn tin cậy dữ liệu là luồng payload HTTP/gRPC động nhận được từ network hoặc rows database driver thô.
//
// 🔒 RANH GIỚI BẢO MẬT & KIẾN TRÚC (CRITICAL ARCHITECTURAL BOUNDARY):
//   - Định hình ranh giới trung chuyển dữ liệu giữa Network/Database và Core Domain Entity.
//   - Việc chuyển đổi bắt buộc phải đi qua các mapper hàm tường minh, tuyệt đối không ép kiểu ép buộc bừa bãi.
//
// 🔄 CALLSITE FLOW:
//   - Được sử dụng bởi `DataplaneNodeRepoImpl` khi tương tác đọc/ghi PostgreSQL DB.
//   - Được sử dụng bởi các API Handler để trả về kết quả cấu trúc Dataplane ra ngoài.
//
// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
//   - Các mapper được thiết kế tối ưu hiệu năng để giảm thiểu cấp phát heap memory khi xử lý hàng loạt bản ghi.
//
// ======================================================================================================

package coreModel

import (
	coreEntity "controlplane/internal/core/domain/entity"
	"time"
)

type DataplaneNode struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	ZoneID    string    `json:"zone_id"`
	Endpoint  string    `json:"endpoint"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DataplaneNodeEntityToModel thực hiện chuyển đổi một Domain Entity strongly-typed sang Model DTO nguyên bản.
func DataplaneNodeEntityToModel(e coreEntity.DataplaneNode) DataplaneNode {
	// Step 1: Khởi tạo struct DTO mới và sao chép từng trường tương ứng.
	// Step 2: Ép kiểu strongly-typed DataplaneNodeStatus sang string thô để phục vụ serialization/transport.
	return DataplaneNode{
		ID:        e.ID,
		Status:    string(e.Status),
		ZoneID:    e.ZoneID,
		Endpoint:  e.Endpoint,
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
	}
}

// DataplaneNodeModelToEntity thực hiện chuyển đổi một Model DTO thô sang Domain Entity strongly-typed có kiểm soát kiểu.
func DataplaneNodeModelToEntity(m DataplaneNode) coreEntity.DataplaneNode {
	// Step 1: Khởi tạo struct Entity mới và sao chép các trường.
	// Step 2: Ép kiểu string thô sang kiểu domain strongly-typed DataplaneNodeStatus.
	return coreEntity.DataplaneNode{
		ID:        m.ID,
		Status:    coreEntity.DataplaneNodeStatus(m.Status),
		ZoneID:    m.ZoneID,
		Endpoint:  m.Endpoint,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}
