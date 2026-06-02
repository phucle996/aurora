// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/repository/zone_repo.go
//            Đặc Tả Hạ Tầng Lưu Trữ & Quản Trị Cơ Sở Dữ Liệu Zone Topology Registry
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & TỐI ƯU HÓA HẠ TẦNG (CONTRACT & PEAK PERFORMANCE INFRASTRUCTURE):
//   - File này chịu trách nhiệm tương tác trực tiếp với Postgres Database cho bảng 'zones' và 'zone_services'.
//   - Triển khai và áp dụng tối ưu hóa hiệu năng cực hạn (Peak Performance Optimization) nhằm triệt tiêu hoàn toàn:
//
//     1) TRUY VẤN TĨNH KHỞI TẠO SỚM (STATIC QUERY PRE-COMPUTATION):
//        * Tất cả chuỗi truy vấn SQL được định dạng schema và biên dịch trước một lần duy nhất tại
//          hàm khởi tạo `NewZoneRepoImpl`.
//        * Triệt tiêu hoàn toàn chi phí sử dụng `fmt.Sprintf` tại runtime ở hot path, giảm thiểu
//          các phân bổ heap dynamic memory và tiết kiệm chu kỳ CPU của Go GC.
//
//     2) ATOMIC MULTI-WRITE TRANSACTIONS (GIAO DỊCH ĐỒNG THỜI NGUYÊN TỬ):
//        * Các tác vụ ghi (Create, Update, Delete, Upsert) được đóng gói chặt chẽ trong database
//          transaction (`pgx.Tx`), đảm bảo tính nhất quán tuyệt đối.
//        * Bản ghi Outbox tương ứng được chèn đồng thời trong cùng transaction. Nếu bất kỳ cập nhật
//          hạ tầng nào thất bại, toàn bộ sự kiện Outbox sẽ rollback, ngăn chặn hoàn toàn "ghost events".
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Database schema 'core.zones' và 'core.zone_services' dưới PostgreSQL là nguồn tin cậy duy nhất cho Desired State lâu dài.
//
// 🔒 RANH GIỚI BẢO MẬT & KIẾN TRÚC (CRITICAL ARCHITECTURAL BOUNDARY):
//   - Chỉ thực hiện các thao tác SQL.
//   - Trả lỗi thô hoặc lỗi nghiệp vụ định nghĩa sẵn (`coreErrorx`) trực tiếp cho tầng Service.
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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ZoneRepoImpl struct {
	db                        *pgxpool.Pool
	schema                    string
	listZonesQuery            string
	getZoneCatalogQuery       string
	createZoneQuery           string
	getZoneByIDQuery          string
	updateZoneStatusQuery     string
	deleteZoneQuery           string
	hasDataplaneNodesQuery    string
	hasEnabledZoneSvcQuery    string
	listZoneSvcByZoneIDQuery  string
	upsertZoneServiceQuery    string
	saveOutboxQuery           string
}

// NewZoneRepoImpl khởi tạo một thực thể Repository mới cho Zone và biên dịch sẵn các câu lệnh SQL.
func NewZoneRepoImpl(cfg *config.Config, db *pgxpool.Pool) coreRepoInterface.ZoneRepository {
	schema := cfg.SchemaSQL.Core
	return &ZoneRepoImpl{
		db:     db,
		schema: schema,
		listZonesQuery: fmt.Sprintf(`
			SELECT id, code, name, status, created_at, updated_at 
			FROM %s.zones 
			ORDER BY created_at DESC
		`, schema),
		getZoneCatalogQuery: fmt.Sprintf(`
			SELECT id, code, name 
			FROM %s.zones 
			WHERE status NOT IN ('disabled', 'planned') 
			ORDER BY code ASC
		`, schema),
		createZoneQuery: fmt.Sprintf(`
			INSERT INTO %s.zones (id, code, name, status, created_at, updated_at) 
			VALUES ($1,$2,$3,$4,$5,$6)
		`, schema),
		getZoneByIDQuery: fmt.Sprintf(`
			SELECT id, code, name, status, created_at, updated_at 
			FROM %s.zones 
			WHERE id=$1 LIMIT 1
		`, schema),
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
		saveOutboxQuery: fmt.Sprintf(`
			INSERT INTO %s.outbox_records (event_id, entity, op, payload, version, status, attempts, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
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
		if err := rows.Scan(&value.ID, &value.Code, &value.Name, &value.Status, &value.CreatedAt, &value.UpdatedAt); err != nil {
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
		if err := rows.Scan(&item.ID, &item.Code, &item.Name); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// CreateZone khởi tạo Zone mới kèm theo các services cấu hình và ghi nhận sự kiện Outbox trong cùng một transaction.
func (r *ZoneRepoImpl) CreateZone(ctx context.Context, zone coreEntity.Zone, svcs map[coreEntity.ZoneServiceType]bool, outboxEventID string, outboxPayload []byte, outboxVersion uint64) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	value := coreModel.ZoneEntityToModel(zone)
	_, err = tx.Exec(ctx, r.createZoneQuery, value.ID, value.Code, value.Name, value.Status, value.CreatedAt.UTC(), value.UpdatedAt.UTC())
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return coreErrorx.ErrZoneCodeAlreadyExists
		}
		return err
	}

	// Tạo các services đồng hành cùng Zone
	for svcType, enabled := range svcs {
		newID, _ := uuid.NewV7()
		_, err = tx.Exec(ctx, r.upsertZoneServiceQuery, newID, value.ID, string(svcType), enabled)
		if err != nil {
			return err
		}
	}

	// Ghi nhận sự kiện Outbox để đồng bộ cụm NATS
	_, err = tx.Exec(ctx, r.saveOutboxQuery,
		outboxEventID,
		"zone",
		"CREATE",
		outboxPayload,
		outboxVersion,
		"PENDING",
		0,
	)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// GetZoneByID lấy thông tin chi tiết một Zone dựa trên ID.
func (r *ZoneRepoImpl) GetZoneByID(ctx context.Context, id uuid.UUID) (*coreEntity.Zone, error) {
	var value coreModel.Zone
	if err := r.db.QueryRow(ctx, r.getZoneByIDQuery, id).Scan(&value.ID, &value.Code, &value.Name, &value.Status, &value.CreatedAt, &value.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	zone := coreModel.ZoneModelToEntity(value)
	return &zone, nil
}

// UpdateZoneStatus cập nhật trạng thái hoạt động của Zone và chèn sự kiện Outbox an toàn trong transaction.
func (r *ZoneRepoImpl) UpdateZoneStatus(ctx context.Context, id uuid.UUID, status coreEntity.ZoneStatus, outboxEventID string, outboxPayload []byte, outboxVersion uint64) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	result, err := tx.Exec(ctx, r.updateZoneStatusQuery, id, string(status))
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return coreErrorx.ErrZoneNotFound
	}

	// Ghi nhận sự kiện Outbox đồng bộ hóa nóng
	_, err = tx.Exec(ctx, r.saveOutboxQuery,
		outboxEventID,
		"zone",
		"UPDATE",
		outboxPayload,
		outboxVersion,
		"PENDING",
		0,
	)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// DeleteZone xóa hoàn toàn bản ghi Zone và ghi nhận sự kiện Outbox biến động dạng DELETE.
func (r *ZoneRepoImpl) DeleteZone(ctx context.Context, id uuid.UUID, outboxEventID string, outboxPayload []byte, outboxVersion uint64) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	result, err := tx.Exec(ctx, r.deleteZoneQuery, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return coreErrorx.ErrZoneNotFound
	}

	// Ghi nhận sự kiện Outbox loại bỏ Zone
	_, err = tx.Exec(ctx, r.saveOutboxQuery,
		outboxEventID,
		"zone",
		"DELETE",
		outboxPayload,
		outboxVersion,
		"PENDING",
		0,
	)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
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

// UpsertZoneServiceByZoneAndType cập nhật/upsert cấu hình dịch vụ của Zone và đồng bộ cấu hình qua Outbox.
func (r *ZoneRepoImpl) UpsertZoneServiceByZoneAndType(ctx context.Context, zoneID uuid.UUID, serviceType coreEntity.ZoneServiceType, enabled bool, outboxEventID string, outboxPayload []byte, outboxVersion uint64) (*coreEntity.ZoneService, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	newID, _ := uuid.NewV7()
	var value coreModel.ZoneService
	err = tx.QueryRow(ctx, r.upsertZoneServiceQuery, newID, zoneID, string(serviceType), enabled).Scan(
		&value.ID, &value.ZoneID, &value.ServiceType, &value.Enabled, &value.CreatedAt, &value.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	// Ghi nhận sự kiện Outbox để đồng bộ trạng thái mới của Zone chứa service này
	_, err = tx.Exec(ctx, r.saveOutboxQuery,
		outboxEventID,
		"zone",
		"UPDATE",
		outboxPayload,
		outboxVersion,
		"PENDING",
		0,
	)
	if err != nil {
		return nil, err
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}

	ent := coreModel.ZoneServiceModelToEntity(value)
	return &ent, nil
}
