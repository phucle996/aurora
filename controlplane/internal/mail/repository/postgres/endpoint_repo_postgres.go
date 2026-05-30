package mailRepoImpl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/config"
	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"
	mailModel "controlplane/internal/mail/model"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// endpointRepoPostgres triển khai mailRepoInterface.EndpointRepository.
// Lưu trữ và truy vấn cấu hình đã được mã hóa thô trong suốt, tách biệt hoàn toàn với domain.
type endpointRepoPostgres struct {
	db     *pgxpool.Pool // Kết nối cơ sở dữ liệu Postgres thread-safe
	schema string        // Tên schema SQL động được cấu hình
}

// NewEndpointRepository khởi tạo một đối tượng EndpointRepository mới cho Postgres.
func NewEndpointRepository(db *pgxpool.Pool, cfg *config.Config) mailRepoInterface.EndpointRepository {
	return &endpointRepoPostgres{
		db:     db,
		schema: cfg.SchemaSQL.Mail,
	}
}

// Create chèn thêm một mail endpoint mới. Nó lưu trữ cấu hình đã được mã hóa phong bì sẵn.
func (r *endpointRepoPostgres) Create(ctx context.Context, e *mailEntity.Endpoint, encryptedConfig []byte) error {
	if e == nil {
		return fmt.Errorf("mail repo: thực thể endpoint entity không được phép nil")
	}

	// Chuyển đổi thực thể domain entity sang mô hình DB model.
	dbModel := mailModel.Endpoint{
		ID:               e.ID,
		ZoneID:           e.ZoneID,
		Name:             e.Name,
		Provider:         e.Provider,
		ConnectionConfig: encryptedConfig,
		IsActive:         e.IsActive,
		CreatedAt:        e.CreatedAt,
		UpdatedAt:        e.UpdatedAt,
	}

	query := fmt.Sprintf(`
		INSERT INTO %s.mail_endpoints (
			id,
			zone_id,
			name,
			provider,
			connection_config,
			is_active,
			created_at,
			updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, r.schema)

	result, err := r.db.Exec(ctx, query,
		dbModel.ID.String(),
		dbModel.ZoneID.String(),
		dbModel.Name,
		dbModel.Provider,
		dbModel.ConnectionConfig,
		dbModel.IsActive,
		dbModel.CreatedAt,
		dbModel.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("mail repo: không thể lưu endpoint %s: %w", e.ID.String(), err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("mail repo: không thể lưu endpoint %s: zero rows affected", e.ID.String())
	}

	return nil
}

// GetByID truy vấn chi tiết một Endpoint nằm dưới phân vùng của một Zone chỉ định.
// Trả về thực thể domain entity (chưa giải mã kết nối) kèm theo bytes cấu hình kết nối đã mã hóa.
func (r *endpointRepoPostgres) GetByID(ctx context.Context, zoneID uuid.UUID, id uuid.UUID) (*mailEntity.Endpoint, []byte, error) {
	if zoneID == uuid.Nil || id == uuid.Nil {
		return nil, nil, fmt.Errorf("mail repo: zoneID và id không được phép nil/empty")
	}

	query := fmt.Sprintf(`
		SELECT 
			id,
			zone_id,
			name,
			provider,
			connection_config,
			is_active,
			created_at,
			updated_at
		FROM %s.mail_endpoints
		WHERE zone_id = $1 AND id = $2
	`, r.schema)

	var m mailModel.Endpoint
	var idStr, zoneIDStr string
	err := r.db.QueryRow(ctx, query, zoneID.String(), id.String()).Scan(
		&idStr,
		&zoneIDStr,
		&m.Name,
		&m.Provider,
		&m.ConnectionConfig,
		&m.IsActive,
		&m.CreatedAt,
		&m.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, fmt.Errorf("mail repo: không tìm thấy endpoint (id: %s, zone: %s)", id.String(), zoneID.String())
		}
		return nil, nil, fmt.Errorf("mail repo: lỗi truy vấn endpoint %s: %w", id.String(), err)
	}

	m.ID = uuid.MustParse(idStr)
	m.ZoneID = uuid.MustParse(zoneIDStr)

	// Ánh xạ DB model ngược lại thành thực thể domain entity.
	ent := mailEntity.Endpoint{
		ID:        m.ID,
		ZoneID:    m.ZoneID,
		Name:      m.Name,
		Provider:  m.Provider,
		IsActive:  m.IsActive,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}

	return &ent, m.ConnectionConfig, nil
}

// List truy vấn toàn bộ danh sách mail endpoints thuộc về một physical Zone chỉ định.
func (r *endpointRepoPostgres) List(ctx context.Context, zoneID uuid.UUID) ([]*mailEntity.Endpoint, [][]byte, error) {
	if zoneID == uuid.Nil {
		return nil, nil, fmt.Errorf("mail repo: zoneID không được phép nil")
	}

	query := fmt.Sprintf(`
		SELECT 
			id,
			zone_id,
			name,
			provider,
			connection_config,
			is_active,
			created_at,
			updated_at
		FROM %s.mail_endpoints
		WHERE zone_id = $1
		ORDER BY created_at DESC
	`, r.schema)

	rows, err := r.db.Query(ctx, query, zoneID.String())
	if err != nil {
		return nil, nil, fmt.Errorf("mail repo: lỗi truy vấn các endpoint thuộc zone %s: %w", zoneID.String(), err)
	}
	defer rows.Close()

	var endpoints []*mailEntity.Endpoint
	var encryptedConfigs [][]byte

	for rows.Next() {
		var m mailModel.Endpoint
		var idStr, zoneIDStr string
		err := rows.Scan(
			&idStr,
			&zoneIDStr,
			&m.Name,
			&m.Provider,
			&m.ConnectionConfig,
			&m.IsActive,
			&m.CreatedAt,
			&m.UpdatedAt,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("mail repo: lỗi scan hàng dữ liệu endpoint: %w", err)
		}

		m.ID = uuid.MustParse(idStr)
		m.ZoneID = uuid.MustParse(zoneIDStr)

		ent := mailEntity.Endpoint{
			ID:        m.ID,
			ZoneID:    m.ZoneID,
			Name:      m.Name,
			Provider:  m.Provider,
			IsActive:  m.IsActive,
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		}

		endpoints = append(endpoints, &ent)
		encryptedConfigs = append(encryptedConfigs, m.ConnectionConfig)
	}

	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("mail repo: lỗi rows cursor: %w", err)
	}

	return endpoints, encryptedConfigs, nil
}

// Update cập nhật các trường thông tin của Endpoint và cấu hình kết nối đã được mã hóa mới.
func (r *endpointRepoPostgres) Update(ctx context.Context, e *mailEntity.Endpoint, encryptedConfig []byte) error {
	if e == nil {
		return fmt.Errorf("mail repo: endpoint không được phép nil khi cập nhật")
	}

	query := fmt.Sprintf(`
		UPDATE %s.mail_endpoints
		SET 
			name = $1,
			provider = $2,
			connection_config = $3,
			is_active = $4,
			updated_at = $5
		WHERE zone_id = $6 AND id = $7
	`, r.schema)

	result, err := r.db.Exec(ctx, query,
		e.Name,
		e.Provider,
		encryptedConfig,
		e.IsActive,
		time.Now().UTC(),
		e.ZoneID.String(),
		e.ID.String(),
	)
	if err != nil {
		return fmt.Errorf("mail repo: lỗi cập nhật bản ghi database %s: %w", e.ID.String(), err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("mail repo: không tìm thấy endpoint đích để cập nhật (id: %s, zone: %s)", e.ID.String(), e.ZoneID.String())
	}

	return nil
}

// Delete xóa vĩnh viễn cấu hình Endpoint vật lý khỏi DB.
func (r *endpointRepoPostgres) Delete(ctx context.Context, zoneID uuid.UUID, id uuid.UUID) error {
	if zoneID == uuid.Nil || id == uuid.Nil {
		return fmt.Errorf("mail repo: zoneID và id không được phép nil/empty khi xóa")
	}

	query := fmt.Sprintf(`
		DELETE FROM %s.mail_endpoints
		WHERE zone_id = $1 AND id = $2
	`, r.schema)

	result, err := r.db.Exec(ctx, query, zoneID.String(), id.String())
	if err != nil {
		return fmt.Errorf("mail repo: không thể xóa endpoint %s khỏi DB: %w", id.String(), err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("mail repo: không tìm thấy endpoint đích để xóa (id: %s, zone: %s)", id.String(), zoneID.String())
	}

	return nil
}
