// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/repository/zone_repo.go
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
//   - Database schema 'hierarchy.zones' và 'hierarchy.zone_services' là PostgreSQL SoT.
//
// ======================================================================================================

package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/config"
	entity "controlplane/internal/hierarchy/domain/entity"
	hierarchyrepo "controlplane/internal/hierarchy/domain/repo"
	model "controlplane/internal/hierarchy/model"
	taxonomy "controlplane/internal/hierarchy/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ZoneRepoImpl struct {
	db                           *pgxpool.Pool
	schema                       string
	listZonesQuery               string
	rpcListZonesQuery            string
	createZoneQuery              string
	getZoneDetailByIDQuery       string
	updateZoneStatusQuery        string
	deleteZoneQuery              string
	hasEnabledZoneSvcQuery       string
	listZoneSvcByZoneIDQuery     string
	upsertZoneServiceQuery       string
	updateZoneServiceStatusQuery string
}

// NewZoneRepoImpl khởi tạo một thực thể Repository mới cho Zone và biên dịch sẵn các câu lệnh SQL.
func NewZoneRepoImpl(cfg *config.Config, db *pgxpool.Pool) hierarchyrepo.ZoneRepository {
	schema := cfg.SchemaSQL.Hierarchy
	return &ZoneRepoImpl{
		db:     db,
		schema: schema,
		listZonesQuery: fmt.Sprintf(`
			SELECT id, code, name, location, status, updated_at 
			FROM %s.zones 
			ORDER BY created_at DESC
		`, schema),
		rpcListZonesQuery: fmt.Sprintf(`
			SELECT id, code, name, status 
			FROM %s.zones 
			ORDER BY created_at DESC
		`, schema),
		createZoneQuery: fmt.Sprintf(`
			INSERT INTO %s.zones (id, code, name, location, description, status, created_at, updated_at) 
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		`, schema),
		getZoneDetailByIDQuery: fmt.Sprintf(`
			SELECT 
				z.id, z.code, z.name, z.location, COALESCE(z.description, '') AS description, z.status, z.created_at, z.updated_at,
				s.id, s.zone_id, s.service_type, s.desired_state, s.actual_state, s.created_at, s.updated_at
			FROM %s.zones z
			LEFT JOIN %s.zone_services s ON z.id = s.zone_id
			WHERE z.id = $1
		`, schema, schema),
		updateZoneStatusQuery: fmt.Sprintf(`
			WITH target AS (
				SELECT code FROM %s.zones WHERE id = $1
			), updated AS (
				UPDATE %s.zones
				SET status = $2, updated_at = now()
				WHERE id = $1 AND status = ANY($3)
				RETURNING code
			)
			SELECT 
				EXISTS(SELECT 1 FROM target) AS exists,
				EXISTS(SELECT 1 FROM updated) AS updated,
				COALESCE((SELECT code FROM target), '') AS zone_code
		`, schema, schema),
		deleteZoneQuery: fmt.Sprintf(`
			WITH target AS (
				SELECT status FROM %s.zones WHERE id = $1
			), svcs_exist AS (
				SELECT EXISTS(SELECT 1 FROM %s.zone_services WHERE zone_id = $1 AND desired_state = true) AS val
			), deleted AS (
				DELETE FROM %s.zones
				WHERE id = $1 
				  AND status = 'disabled'
				  AND NOT EXISTS (SELECT 1 FROM %s.zone_services WHERE zone_id = $1 AND desired_state = true)
				RETURNING code
			)
			SELECT 
				(SELECT COUNT(*) FROM target) AS exists,
				COALESCE((SELECT status::text FROM target), '') AS status,
				(SELECT val FROM svcs_exist) AS has_svcs,
				COALESCE((SELECT code FROM deleted), '') AS deleted_code
		`, schema, schema, schema, schema),
		hasEnabledZoneSvcQuery: fmt.Sprintf(`
			SELECT EXISTS(SELECT 1 FROM %s.zone_services WHERE zone_id=$1 AND desired_state=true)
		`, schema),
		listZoneSvcByZoneIDQuery: fmt.Sprintf(`
			SELECT id, zone_id, service_type, desired_state, actual_state, created_at, updated_at 
			FROM %s.zone_services 
			WHERE zone_id=$1 
			ORDER BY service_type
		`, schema),
		upsertZoneServiceQuery: fmt.Sprintf(`
			INSERT INTO %s.zone_services (id, zone_id, service_type, desired_state, created_at, updated_at) 
			VALUES ($1,$2,$3,$4,now(),now()) 
			ON CONFLICT (zone_id, service_type) 
			DO UPDATE SET desired_state=EXCLUDED.desired_state, updated_at=now() 
			RETURNING id, zone_id, service_type, desired_state, actual_state, created_at, updated_at
		`, schema),
		updateZoneServiceStatusQuery: fmt.Sprintf(`
			WITH target_zone AS (
				SELECT code, status FROM %s.zones WHERE id = $2
			), upserted AS (
				INSERT INTO %s.zone_services (id, zone_id, service_type, desired_state, created_at, updated_at)
				SELECT $1, id, $3, $4, now(), now()
				FROM %s.zones
				WHERE id = $2 AND status = 'maintenance'
				ON CONFLICT (zone_id, service_type)
				DO UPDATE SET desired_state = EXCLUDED.desired_state, updated_at = now()
				RETURNING id, zone_id, service_type, desired_state, actual_state, created_at, updated_at
			)
			SELECT 
				(SELECT COUNT(*) FROM target_zone) AS zone_exists,
				COALESCE((SELECT status::text FROM target_zone), '') AS zone_status,
				COALESCE((SELECT code FROM target_zone), '') AS zone_code,
				(SELECT COUNT(*) FROM upserted) AS upsert_success,
				COALESCE((SELECT id FROM upserted), '00000000-0000-0000-0000-000000000000'::uuid) AS svc_id,
				COALESCE((SELECT actual_state FROM upserted), 'unknown') AS svc_actual_state,
				COALESCE((SELECT created_at FROM upserted), now()) AS svc_created,
				COALESCE((SELECT updated_at FROM upserted), now()) AS svc_updated
		`, schema, schema, schema),
	}
}

// ListZones lấy toàn bộ danh sách các zone trong hệ thống.
func (r *ZoneRepoImpl) ListZones(ctx context.Context) ([]entity.Zone, error) {
	rows, err := r.db.Query(ctx, r.listZonesQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]entity.Zone, 0)
	for rows.Next() {
		var value model.Zone
		if err := rows.Scan(
			&value.ID,
			&value.Code,
			&value.Name,
			&value.Location,
			&value.Status,
			&value.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, model.ZoneModelToEntity(value))
	}
	return out, rows.Err()
}

// AcrListZones lấy danh sách các zone tối giản chỉ bao gồm ID, Code, Name, Status.
// Phù hợp sử dụng cho các luồng hot-path phục vụ Edge Gateway/ACR Service.
func (r *ZoneRepoImpl) AcrListZones(ctx context.Context) ([]entity.RPCZone, error) {
	// Thực hiện truy vấn qua rpcListZonesQuery đã biên dịch trước
	rows, err := r.db.Query(ctx, r.rpcListZonesQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]entity.RPCZone, 0)
	for rows.Next() {
		var id uuid.UUID
		var code string
		var name string
		var status string
		// Chỉ scan 4 trường dữ liệu tối giản
		if err := rows.Scan(&id,
			&code,
			&name,
			&status); err != nil {
			return nil, err
		}
		out = append(out, entity.RPCZone{
			ID:     id,
			Code:   code,
			Name:   name,
			Status: entity.ZoneStatus(status),
		})
	}
	return out, nil
}

// CreateZone khởi tạo Zone mới kèm theo các services cấu hình trong cùng một transaction.
func (r *ZoneRepoImpl) CreateZone(ctx context.Context, zone entity.Zone, svcs map[entity.ZoneServiceType]bool) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	value := model.ZoneEntityToModel(zone)
	_, err = tx.Exec(ctx,
		r.createZoneQuery,
		value.ID,
		value.Code,
		value.Name,
		value.Location,
		value.Description,
		value.Status,
		value.CreatedAt.UTC(),
		value.UpdatedAt.UTC(),
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return taxonomy.ErrCodeAlreadyExists
		}
		return err
	}

	for svcType, enabled := range svcs {
		newID, _ := uuid.NewV7()
		_, err = tx.Exec(ctx,
			r.upsertZoneServiceQuery,
			newID,
			value.ID,
			string(svcType),
			enabled,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// GetZoneDetailByID lấy thông tin chi tiết một Zone kèm theo tất cả các dịch vụ (aggregated flow).
func (r *ZoneRepoImpl) GetZoneDetailByID(ctx context.Context, id uuid.UUID) (*entity.ZoneDetail, error) {
	rows, err := r.db.Query(ctx, r.getZoneDetailByIDQuery, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var detail *entity.ZoneDetail
	for rows.Next() {
		var zVal model.Zone
		var sID, sZoneID *uuid.UUID
		var sType *string
		var sDesiredState *bool
		var sActualState *string
		var sCreatedAt, sUpdatedAt *time.Time
		if err := rows.Scan(
			&zVal.ID,
			&zVal.Code,
			&zVal.Name,
			&zVal.Location,
			&zVal.Description,
			&zVal.Status,
			&zVal.CreatedAt,
			&zVal.UpdatedAt,
			&sID,
			&sZoneID,
			&sType,
			&sDesiredState,
			&sActualState,
			&sCreatedAt,
			&sUpdatedAt,
		); err != nil {
			return nil, err
		}

		if detail == nil {
			zoneEnt := model.ZoneModelToEntity(zVal)
			detail = &entity.ZoneDetail{
				Zone:     zoneEnt,
				Services: []entity.ZoneService{},
			}
		}

		// [COMMENT]: Map các cột từ bảng zone_services bao gồm cả desired_state và actual_state
		if sID != nil && sZoneID != nil && sType != nil && sDesiredState != nil {
			actualStateVal := "unknown"
			if sActualState != nil {
				actualStateVal = *sActualState
			}
			detail.Services = append(detail.Services, entity.ZoneService{
				ID:           *sID,
				ZoneID:       *sZoneID,
				ServiceType:  entity.ZoneServiceType(*sType),
				DesiredState: *sDesiredState,
				ActualState:  actualStateVal,
				CreatedAt:    *sCreatedAt,
				UpdatedAt:    *sUpdatedAt,
			})
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	if detail == nil {
		return nil, taxonomy.ErrZoneNotFound // Not found
	}

	return detail, nil
}

// UpdateZoneStatus cập nhật trạng thái hoạt động của Zone.
func (r *ZoneRepoImpl) UpdateZoneStatus(ctx context.Context, id uuid.UUID, status entity.ZoneStatus, allowedOld []entity.ZoneStatus) (string, error) {
	statusStrings := make([]string, len(allowedOld))
	for i, s := range allowedOld {
		statusStrings[i] = string(s)
	}

	var exists bool
	var updated bool
	var zoneCode string
	// Thực hiện truy vấn kiểm tra sự tồn tại và cập nhật trạng thái theo State Machine.
	err := r.db.QueryRow(ctx,
		r.updateZoneStatusQuery,
		id,
		string(status),
		statusStrings,
	).Scan(
		&exists,
		&updated,
		&zoneCode,
	)
	if err != nil {
		return "", err
	}
	// Trả lỗi nếu bản ghi không tồn tại.
	if !exists {
		return "", taxonomy.ErrZoneNotFound
	}
	// Trả lỗi nếu quá trình chuyển đổi trạng thái không hợp lệ.
	if !updated {
		return "", taxonomy.ErrZoneInvalidTransition
	}
	return zoneCode, nil
}

// DeleteZone xóa Zone khỏi cơ sở dữ liệu (hard delete).
func (r *ZoneRepoImpl) DeleteZone(ctx context.Context, id uuid.UUID) (string, error) {
	var exists int
	var status string
	var hasSvcs bool
	var deletedCode string

	err := r.db.QueryRow(ctx,
		r.deleteZoneQuery,
		id,
	).Scan(
		&exists,
		&status,
		&hasSvcs,
		&deletedCode,
	)
	if err != nil {
		return "", err
	}

	if exists == 0 {
		return "", taxonomy.ErrZoneNotFound
	}
	if status != "disabled" || hasSvcs {
		return "", taxonomy.ErrZoneDeletePreconditionFailed
	}
	return deletedCode, nil
}

// UpdateZoneService cập nhật cấu hình dịch vụ của Zone (DesiredState).
func (r *ZoneRepoImpl) UpdateZoneService(ctx context.Context, zoneID uuid.UUID, serviceType entity.ZoneServiceType, enabled bool) (*entity.ZoneService, string, error) {
	newID, _ := uuid.NewV7()
	var zoneExists int
	var zoneStatus string
	var zoneCode string
	var upsertSuccess int
	var value model.ZoneService

	// [COMMENT]: Thực hiện cập nhật trạng thái mong muốn của dịch vụ trong phân vùng
	err := r.db.QueryRow(ctx,
		r.updateZoneServiceStatusQuery,
		newID,
		zoneID,
		string(serviceType),
		enabled,
	).Scan(
		&zoneExists,
		&zoneStatus,
		&zoneCode,
		&upsertSuccess,
		&value.ID,
		&value.ActualState, // [COMMENT]: Scan actual_state từ database trả về
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	if err != nil {
		return nil, "", err
	}

	if zoneExists == 0 {
		return nil, "", taxonomy.ErrZoneServiceZoneNotFound
	}
	if zoneStatus != "maintenance" {
		return nil, "", taxonomy.ErrZoneServiceStateConflict
	}
	if upsertSuccess == 0 {
		return nil, "", taxonomy.ErrZoneServiceInvalidInput
	}

	value.ZoneID = zoneID
	value.ServiceType = string(serviceType)
	value.DesiredState = enabled // [COMMENT]: Cấu hình desired_state tương ứng với biến enabled nhận vào

	ent := model.ZoneServiceModelToEntity(value)
	return &ent, zoneCode, nil
}
