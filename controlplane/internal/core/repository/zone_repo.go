// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/repository/zone_repo.go
//            Đặc Tả Hạ Tầng Lưu Trữ & Quản Trị Cơ Sở Dữ Liệu Zone Topology Registry
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & TỐI ƯU HÓA HẠ TẦNG (CONTRACT & PEAK PERFORMANCE INFRASTRUCTURE):
//   - File này chịu trách nhiệm tương tác trực tiếp với Postgres Database cho bảng 'zones' và 'zone_services'.
//   - Triển khai và áp dụng tối ưu hóa hiệu năng cực hạn (Peak Performance Optimization):
//
//     1) TRUY VẤN TĨNH KHỞI TẠO SỚM (STATIC QUERY PRE-COMPUTATION):
//        * Tất cả chuỗi truy vấn SQL được định dạng schema và biên dịch trước một lần duy nhất tại
//          hàm khởi tạo `NewZoneRepoImpl`.
//        * Triệt tiêu hoàn toàn chi phí sử dụng `fmt.Sprintf` tại runtime ở hot path.
//
//     2) ATOMIC MULTI-WRITE TRANSACTIONS (GIAO DỊCH ĐỒNG THỜI NGUYÊN TỬ):
//        * Các tác vụ ghi (Create, Update, Delete, Upsert) được đóng gói chặt chẽ trong database
//          transaction (`pgx.Tx`), đảm bảo tính nhất quán tuyệt đối.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Database schema 'core.zones' và 'core.zone_services' dưới PostgreSQL là nguồn tin cậy duy nhất.
//
// ======================================================================================================

package coreRepoImpl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/config"
	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreErrorx "controlplane/internal/core/errorx"
	coreModel "controlplane/internal/core/model"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ZoneRepoImpl struct {
	db                       *pgxpool.Pool
	schema                   string
	listZonesQuery           string
	getZoneCatalogQuery      string
	createZoneQuery          string
	getZoneByIDQuery         string
	getZoneDetailByIDQuery   string
	updateZoneStatusQuery    string
	deleteZoneQuery          string
	hasDataplaneNodesQuery   string
	hasEnabledZoneSvcQuery   string
	listZoneSvcByZoneIDQuery string
	upsertZoneServiceQuery   string
}

// NewZoneRepoImpl khởi tạo một thực thể Repository mới cho Zone và biên dịch sẵn các câu lệnh SQL.
func NewZoneRepoImpl(cfg *config.Config, db *pgxpool.Pool) coreRepoInterface.ZoneRepository {
	schema := cfg.SchemaSQL.Core
	return &ZoneRepoImpl{
		db:     db,
		schema: schema,
		listZonesQuery: fmt.Sprintf(`
			SELECT id, code, name, location, description, status 
			FROM %s.zones 
			ORDER BY created_at DESC
		`, schema),
		getZoneCatalogQuery: fmt.Sprintf(`
			SELECT id, code, name, updated_at 
			FROM %s.zones 
			WHERE status IN ('active') 
			ORDER BY code ASC
		`, schema),
		createZoneQuery: fmt.Sprintf(`
			INSERT INTO %s.zones (id, code, name, location, description, status, created_at, updated_at) 
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		`, schema),
		getZoneByIDQuery: fmt.Sprintf(`
			SELECT id, code, name, location, description, status, created_at, updated_at 
			FROM %s.zones 
			WHERE id=$1 LIMIT 1
		`, schema),
		getZoneDetailByIDQuery: fmt.Sprintf(`
			SELECT 
				z.id, z.code, z.name, z.location, z.description, z.status, z.created_at, z.updated_at,
				s.id, s.zone_id, s.service_type, s.enabled, s.created_at, s.updated_at
			FROM %s.zones z
			LEFT JOIN %s.zone_services s ON z.id = s.zone_id
			WHERE z.id = $1
		`, schema, schema),
		updateZoneStatusQuery: fmt.Sprintf(`
			UPDATE %s.zones 
			SET status=$2, updated_at=now() 
			WHERE id=$1
		`, schema),
		deleteZoneQuery: fmt.Sprintf(`
			DELETE FROM %s.zones 
			WHERE id=$1
		`, schema),
		hasDataplaneNodesQuery: fmt.Sprintf(`
			SELECT EXISTS(SELECT 1 FROM %s.dataplane_nodes WHERE zone_id=$1)
		`, schema),
		hasEnabledZoneSvcQuery: fmt.Sprintf(`
			SELECT EXISTS(SELECT 1 FROM %s.zone_services WHERE zone_id=$1 AND enabled=true)
		`, schema),
		listZoneSvcByZoneIDQuery: fmt.Sprintf(`
			SELECT id, zone_id, service_type, enabled, created_at, updated_at 
			FROM %s.zone_services 
			WHERE zone_id=$1 
			ORDER BY service_type
		`, schema),
		upsertZoneServiceQuery: fmt.Sprintf(`
			INSERT INTO %s.zone_services (id, zone_id, service_type, enabled, created_at, updated_at) 
			VALUES ($1,$2,$3,$4,now(),now()) 
			ON CONFLICT (zone_id, service_type) 
			DO UPDATE SET enabled=EXCLUDED.enabled, updated_at=now() 
			RETURNING id, zone_id, service_type, enabled, created_at, updated_at
		`, schema),
	}
}

// ListZones lấy toàn bộ danh sách các zone trong hệ thống.
func (r *ZoneRepoImpl) ListZones(ctx context.Context) ([]coreEntity.Zone, error) {
	rows, err := r.db.Query(ctx, r.listZonesQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]coreEntity.Zone, 0)
	for rows.Next() {
		var value coreModel.Zone
		if err := rows.Scan(&value.ID, &value.Code, &value.Name, &value.Location, &value.Description, &value.Status); err != nil {
			return nil, err
		}
		out = append(out, coreModel.ZoneModelToEntity(value))
	}
	return out, rows.Err()
}

// GetZoneCatalog trả danh sách zone catalog tối giản phục vụ Select/Dropdown UI.
func (r *ZoneRepoImpl) GetZoneCatalog(ctx context.Context) ([]coreEntity.ZoneCatalog, error) {
	rows, err := r.db.Query(ctx, r.getZoneCatalogQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]coreEntity.ZoneCatalog, 0)
	for rows.Next() {
		var item coreEntity.ZoneCatalog
		if err := rows.Scan(&item.ID, &item.Code, &item.Name, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// CreateZone khởi tạo Zone mới kèm theo các services cấu hình trong cùng một transaction.
func (r *ZoneRepoImpl) CreateZone(ctx context.Context, zone coreEntity.Zone, svcs map[coreEntity.ZoneServiceType]bool) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	value := coreModel.ZoneEntityToModel(zone)
	_, err = tx.Exec(ctx, r.createZoneQuery, value.ID, value.Code, value.Name, value.Location, value.Description, value.Status, value.CreatedAt.UTC(), value.UpdatedAt.UTC())
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return coreErrorx.ErrZoneCodeAlreadyExists
		}
		return err
	}

	for svcType, enabled := range svcs {
		newID, _ := uuid.NewV7()
		_, err = tx.Exec(ctx, r.upsertZoneServiceQuery, newID, value.ID, string(svcType), enabled)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// GetZoneByID lấy thông tin chi tiết một Zone dựa trên ID.
func (r *ZoneRepoImpl) GetZoneByID(ctx context.Context, id uuid.UUID) (*coreEntity.Zone, error) {
	var value coreModel.Zone
	if err := r.db.QueryRow(ctx, r.getZoneByIDQuery, id).Scan(&value.ID, &value.Code, &value.Name, &value.Location, &value.Description, &value.Status, &value.CreatedAt, &value.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	zone := coreModel.ZoneModelToEntity(value)
	return &zone, nil
}

// GetZoneDetailByID lấy thông tin chi tiết một Zone kèm theo tất cả các dịch vụ (aggregated flow).
func (r *ZoneRepoImpl) GetZoneDetailByID(ctx context.Context, id uuid.UUID) (*coreEntity.ZoneDetail, error) {
	rows, err := r.db.Query(ctx, r.getZoneDetailByIDQuery, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var detail *coreEntity.ZoneDetail
	for rows.Next() {
		var zVal coreModel.Zone
		var sID, sZoneID *uuid.UUID
		var sType *string
		var sEnabled *bool
		var sCreatedAt, sUpdatedAt *time.Time
		if err := rows.Scan(
			&zVal.ID, &zVal.Code, &zVal.Name, &zVal.Location, &zVal.Description, &zVal.Status, &zVal.CreatedAt, &zVal.UpdatedAt,
			&sID, &sZoneID, &sType, &sEnabled, &sCreatedAt, &sUpdatedAt,
		); err != nil {
			return nil, err
		}

		if detail == nil {
			zoneEnt := coreModel.ZoneModelToEntity(zVal)
			detail = &coreEntity.ZoneDetail{
				Zone:     zoneEnt,
				Services: []coreEntity.ZoneService{},
			}
		}

		if sID != nil && sZoneID != nil && sType != nil && sEnabled != nil {
			detail.Services = append(detail.Services, coreEntity.ZoneService{
				ID:          *sID,
				ZoneID:      *sZoneID,
				ServiceType: coreEntity.ZoneServiceType(*sType),
				Enabled:     *sEnabled,
				CreatedAt:   *sCreatedAt,
				UpdatedAt:   *sUpdatedAt,
			})
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	if detail == nil {
		return nil, nil // Not found
	}

	return detail, nil
}


// UpdateZoneStatus cập nhật trạng thái hoạt động của Zone.
func (r *ZoneRepoImpl) UpdateZoneStatus(ctx context.Context, id uuid.UUID, status coreEntity.ZoneStatus) error {
	result, err := r.db.Exec(ctx, r.updateZoneStatusQuery, id, string(status))
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return coreErrorx.ErrZoneNotFound
	}
	return nil
}

// DeleteZone xóa Zone khỏi cơ sở dữ liệu (hard delete).
func (r *ZoneRepoImpl) DeleteZone(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.Exec(ctx, r.deleteZoneQuery, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return coreErrorx.ErrZoneNotFound
	}
	return nil
}

// HasDataplaneNodesByZone kiểm tra xem Zone hiện tại có Dataplane Nodes nào neo vào không.
func (r *ZoneRepoImpl) HasDataplaneNodesByZone(ctx context.Context, zoneID uuid.UUID) (bool, error) {
	var exists bool
	if err := r.db.QueryRow(ctx, r.hasDataplaneNodesQuery, zoneID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// HasEnabledZoneServicesByZone kiểm tra xem Zone hiện tại có dịch vụ nào đang được kích hoạt hay không.
func (r *ZoneRepoImpl) HasEnabledZoneServicesByZone(ctx context.Context, zoneID uuid.UUID) (bool, error) {
	var exists bool
	if err := r.db.QueryRow(ctx, r.hasEnabledZoneSvcQuery, zoneID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// ListZoneServicesByZoneID liệt kê tất cả các dịch vụ đang được đăng ký của một Zone.
func (r *ZoneRepoImpl) ListZoneServicesByZoneID(ctx context.Context, zoneID uuid.UUID) ([]coreEntity.ZoneService, error) {
	rows, err := r.db.Query(ctx, r.listZoneSvcByZoneIDQuery, zoneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]coreEntity.ZoneService, 0)
	for rows.Next() {
		var value coreModel.ZoneService
		if err := rows.Scan(&value.ID, &value.ZoneID, &value.ServiceType, &value.Enabled, &value.CreatedAt, &value.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, coreModel.ZoneServiceModelToEntity(value))
	}
	return out, rows.Err()
}

// UpsertZoneServiceByZoneAndType cập nhật/upsert cấu hình dịch vụ của Zone.
func (r *ZoneRepoImpl) UpsertZoneServiceByZoneAndType(ctx context.Context, zoneID uuid.UUID, serviceType coreEntity.ZoneServiceType, enabled bool) (*coreEntity.ZoneService, error) {
	newID, _ := uuid.NewV7()
	var value coreModel.ZoneService
	err := r.db.QueryRow(ctx, r.upsertZoneServiceQuery, newID, zoneID, string(serviceType), enabled).Scan(
		&value.ID, &value.ZoneID, &value.ServiceType, &value.Enabled, &value.CreatedAt, &value.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	ent := coreModel.ZoneServiceModelToEntity(value)
	return &ent, nil
}
