// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/repository/dataplane_node_repo.go
//            Đặc Tả Hạ Tầng Lưu Trữ & Quản Trị Cơ Sở Dữ Liệu Dataplane Cluster Registry
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & TỐI ƯU HÓA HẠ TẦNG (CONTRACT & PEAK PERFORMANCE INFRASTRUCTURE):
//   - File này chịu trách nhiệm lưu trữ và tương tác trực tiếp với Postgres Database cho hệ thống
//     đăng ký Dataplane Cluster mặt phẳng điều phối Core (Control Plane Core).
//   - Triển khai và áp dụng tối ưu hóa hiệu năng cực hạn (Peak Performance Optimization) nhằm triệt tiêu hoàn toàn:
//
//     1) TRUY VẤN TĨNH KHỞI TẠO SỚM (STATIC QUERY PRE-COMPUTATION):
//        * Tất cả chuỗi truy vấn SQL được định dạng schema và biên dịch trước một lần duy nhất tại
//          hàm khởi tạo `NewDataplaneNodeRepoImpl`.
//        * Triệt tiêu hoàn toàn chi phí sử dụng `fmt.Sprintf` tại runtime ở hot path, giảm thiểu
//          các phân bổ heap dynamic memory và tiết kiệm chu kỳ CPU của Go GC.
//
//     2) RÀNG BUỘC TOÀN VẸN MÔ HÌNH CHỊU LỖI (1-ZONE-TO-1-CLUSTER ENFORCEMENT):
//        * Ép buộc ràng buộc duy nhất 1 Zone chỉ có tối đa 1 cụm Dataplane thông qua UNIQUE constraint
//          ở mức DB, loại bỏ hoàn toàn khả năng split-brain hoặc cấu hình sai lệch dữ liệu.
//
//     3) IDEMPOTENT HEARTBEAT REGISTRATION (UPSERT PATTERN):
//        * Hàm `RegisterCluster` sử dụng câu lệnh `ON CONFLICT (zone_id) DO UPDATE` để bảo đảm
//          idempotent hoàn hảo, giúp hệ thống phục hồi nhanh chóng khi các cụm restart liên tục.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Database schema 'core.dataplane_nodes' dưới PostgreSQL là nguồn tin cậy duy nhất cho Desired State lâu dài.
//
// 🔒 RANH GIỚI BẢO MẬT & KIẾN TRÚC (CRITICAL ARCHITECTURAL BOUNDARY):
//   - Chỉ thực hiện các thao tác SQL cơ bản: INSERT, UPDATE, SELECT.
//   - Tuyệt đối không tự ý áp đặt business logic hay validation nghiệp vụ phức tạp tại đây.
//   - Trả lỗi thô hoặc lỗi nghiệp vụ định nghĩa sẵn (`coreErrorx`) trực tiếp cho tầng Service.
//
// 🔄 CALLSITE FLOW:
//   - Được khởi tạo tại hàm Bootstrap hệ thống và inject trực tiếp vào `DataplaneNodeService`
//     và `DataplaneOrchestrator`.
//
// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
//   - Ràng buộc UNIQUE(zone_id) ở DB đảm bảo tuyệt đối tính toàn vẹn 1-Zone-to-1-Cluster, ngăn chặn split-brain ở mức lưu trữ.
//
// ======================================================================================================

package coreRepoImpl

import (
	"context"
	"errors"
	"fmt"

	"controlplane/internal/config"
	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreErrorx "controlplane/internal/core/errorx"
	coreModel "controlplane/internal/core/model"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DataplaneNodeRepoImpl struct {
	db                       *pgxpool.Pool
	schema                   string
	registerClusterQuery     string
	updateClusterStatusQuery string
	getClusterQuery          string
	getClusterByZoneQuery    string
	listReadyClustersQuery   string
}

// NewDataplaneNodeRepoImpl khởi tạo một thực thể Repository mới cho Dataplane và biên dịch sẵn các câu lệnh SQL.
func NewDataplaneNodeRepoImpl(cfg *config.Config, db *pgxpool.Pool) coreRepoInterface.DataplaneNodeRepository {
	schema := cfg.SchemaSQL.Core
	return &DataplaneNodeRepoImpl{
		db:     db,
		schema: schema,
		registerClusterQuery: fmt.Sprintf(`
			INSERT INTO %s.dataplane_nodes (id, status, zone_id, endpoint, created_at, updated_at) 
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (zone_id) 
			DO UPDATE SET 
				status = EXCLUDED.status,
				endpoint = EXCLUDED.endpoint,
				updated_at = now()
		`, schema),
		updateClusterStatusQuery: fmt.Sprintf(`
			UPDATE %s.dataplane_nodes SET status=$2, updated_at=now() WHERE id=$1
		`, schema),
		getClusterQuery: fmt.Sprintf(`
			SELECT id, status, zone_id, endpoint, created_at, updated_at 
			FROM %s.dataplane_nodes WHERE id=$1 LIMIT 1
		`, schema),
		getClusterByZoneQuery: fmt.Sprintf(`
			SELECT id, status, zone_id, endpoint, created_at, updated_at 
			FROM %s.dataplane_nodes WHERE zone_id=$1 LIMIT 1
		`, schema),
		listReadyClustersQuery: fmt.Sprintf(`
			SELECT id, status, zone_id, endpoint, created_at, updated_at 
			FROM %s.dataplane_nodes WHERE status='ready' ORDER BY created_at DESC
		`, schema),
	}
}

// RegisterCluster đăng ký hoặc cập nhật thông tin cụm Dataplane theo Zone.
func (r *DataplaneNodeRepoImpl) RegisterCluster(ctx context.Context, cluster coreEntity.DataplaneNode) error {
	// Step 1: Chuyển đổi Domain Entity sang Model DTO nguyên bản để thao tác với DB.
	value := coreModel.DataplaneNodeEntityToModel(cluster)

	// Step 2: Thực thi câu lệnh SQL UPSERT được biên dịch sẵn an toàn.
	_, err := r.db.Exec(ctx, r.registerClusterQuery,
		value.ID,
		value.Status,
		value.ZoneID,
		value.Endpoint,
		value.CreatedAt.UTC(),
		value.UpdatedAt.UTC(),
	)
	return err
}

// UpdateClusterStatus cập nhật trực tiếp cột status của cụm Dataplane theo ID.
func (r *DataplaneNodeRepoImpl) UpdateClusterStatus(ctx context.Context, id uuid.UUID, status coreEntity.DataplaneNodeStatus) error {
	// Step 1: Thực thi cập nhật status bằng câu lệnh được biên dịch sẵn.
	result, err := r.db.Exec(ctx, r.updateClusterStatusQuery, id, string(status))
	if err != nil {
		return err
	}

	// Step 2: Kiểm tra xem có dòng nào bị ảnh hưởng không. Nếu không, trả lỗi không tìm thấy.
	if result.RowsAffected() == 0 {
		return coreErrorx.ErrZoneNotFound
	}
	return nil
}

// GetCluster lấy chi tiết thông tin của một cụm Dataplane theo ID duy nhất.
func (r *DataplaneNodeRepoImpl) GetCluster(ctx context.Context, id uuid.UUID) (*coreEntity.DataplaneNode, error) {
	var value coreModel.DataplaneNode

	// Step 1: Thực thi quét dữ liệu và scan vào struct Model sử dụng query compile sẵn.
	err := r.db.QueryRow(ctx, r.getClusterQuery, id).Scan(
		&value.ID,
		&value.Status,
		&value.ZoneID,
		&value.Endpoint,
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	if err != nil {
		// Step 2: Xử lý trường hợp không tìm thấy dòng nào, trả về nil/nil thay vì báo lỗi crash.
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	// Step 3: Map ngược Model DTO thô sang Domain Entity strongly-typed trước khi trả về.
	node := coreModel.DataplaneNodeModelToEntity(value)
	return &node, nil
}

// GetClusterByZone tìm kiếm thông tin cụm Dataplane dựa trên Zone ID.
func (r *DataplaneNodeRepoImpl) GetClusterByZone(ctx context.Context, zoneID uuid.UUID) (*coreEntity.DataplaneNode, error) {
	var value coreModel.DataplaneNode

	// Step 1: Thực thi quét dòng dữ liệu duy nhất của zone bằng câu query pre-computed.
	err := r.db.QueryRow(ctx, r.getClusterByZoneQuery, zoneID).Scan(
		&value.ID,
		&value.Status,
		&value.ZoneID,
		&value.Endpoint,
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	if err != nil {
		// Step 2: Trả về nil/nil nếu zone chưa được đăng ký cụm Dataplane nào.
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	// Step 3: Map sang Domain Entity sạch trước khi trả về tầng Service.
	node := coreModel.DataplaneNodeModelToEntity(value)
	return &node, nil
}

// ListReadyClusters liệt kê toàn bộ các cụm Dataplane đang hoạt động bình thường.
func (r *DataplaneNodeRepoImpl) ListReadyClusters(ctx context.Context) ([]coreEntity.DataplaneNode, error) {
	// Step 1: Truy vấn nhiều dòng từ DB bằng query pre-computed.
	rows, err := r.db.Query(ctx, r.listReadyClustersQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Step 2: Quét qua từng dòng kết quả và build mảng Domain Entities.
	out := make([]coreEntity.DataplaneNode, 0)
	for rows.Next() {
		var value coreModel.DataplaneNode
		err := rows.Scan(
			&value.ID,
			&value.Status,
			&value.ZoneID,
			&value.Endpoint,
			&value.CreatedAt,
			&value.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		out = append(out, coreModel.DataplaneNodeModelToEntity(value))
	}
	return out, rows.Err()
}
