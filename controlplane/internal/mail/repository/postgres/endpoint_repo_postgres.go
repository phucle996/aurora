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

// endpointRepoPostgres triển khai mailRepoInterface.EndpointRepository sử dụng Static Statement để tối ưu hot path.
type endpointRepoPostgres struct {
	db              *pgxpool.Pool // Kết nối cơ sở dữ liệu Postgres thread-safe
	schema          string        // Tên schema SQL động được cấu hình
	createQuery     string        // Câu lệnh tạo mới endpoint
	getByIDQuery    string        // Câu lệnh lấy chi tiết endpoint
	listAllQuery    string        // Câu lệnh lấy toàn bộ danh sách endpoint (global)
	listByZoneQuery string        // Câu lệnh lấy danh sách endpoint theo zone
	updateQuery     string        // Câu lệnh cập nhật endpoint
	deleteQuery     string        // Câu lệnh xóa endpoint
}

// NewEndpointRepository khởi tạo một đối tượng EndpointRepository mới cho Postgres và biên dịch sẵn SQL statements.
func NewEndpointRepository(db *pgxpool.Pool, cfg *config.Config) mailRepoInterface.EndpointRepository {
	schema := cfg.SchemaSQL.Mail
	return &endpointRepoPostgres{
		db:     db,
		schema: schema,
		createQuery: fmt.Sprintf(`
			INSERT INTO %s.mail_endpoints (
				id,
				zone_id,
				name,
				host,
				port,
				username,
				password,
				tls_mode,
				status,
				max_connections,
				priority,
				weight,
				ca_cert_pem,
				client_cert_pem,
				client_key_pem,
				is_active,
				created_at,
				updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		`, schema),
		getByIDQuery: fmt.Sprintf(`
			SELECT 
				id,
				zone_id,
				name,
				host,
				port,
				username,
				password,
				tls_mode,
				status,
				max_connections,
				priority,
				weight,
				ca_cert_pem,
				client_cert_pem,
				client_key_pem,
				is_active,
				created_at,
				updated_at
			FROM %s.mail_endpoints
			WHERE zone_id = $1 AND id = $2
		`, schema),
		listAllQuery: fmt.Sprintf(`
			SELECT 
				id,
				zone_id,
				name,
				host,
				port,
				username,
				password,
				tls_mode,
				status,
				max_connections,
				priority,
				weight,
				ca_cert_pem,
				client_cert_pem,
				client_key_pem,
				is_active,
				created_at,
				updated_at
			FROM %s.mail_endpoints
			ORDER BY created_at DESC
		`, schema),
		listByZoneQuery: fmt.Sprintf(`
			SELECT 
				id,
				zone_id,
				name,
				host,
				port,
				username,
				password,
				tls_mode,
				status,
				max_connections,
				priority,
				weight,
				ca_cert_pem,
				client_cert_pem,
				client_key_pem,
				is_active,
				created_at,
				updated_at
			FROM %s.mail_endpoints
			WHERE zone_id = $1
			ORDER BY created_at DESC
		`, schema),
		updateQuery: fmt.Sprintf(`
			UPDATE %s.mail_endpoints
			SET 
				name = $1,
				host = $2,
				port = $3,
				username = $4,
				password = $5,
				tls_mode = $6,
				status = $7,
				max_connections = $8,
				priority = $9,
				weight = $10,
				ca_cert_pem = $11,
				client_cert_pem = $12,
				client_key_pem = $13,
				is_active = $14,
				updated_at = $15
			WHERE zone_id = $16 AND id = $17
		`, schema),
		deleteQuery: fmt.Sprintf(`
			DELETE FROM %s.mail_endpoints
			WHERE zone_id = $1 AND id = $2
		`, schema),
	}
}

// Create chèn thêm một mail endpoint mới với đầy đủ các cột phẳng.
func (r *endpointRepoPostgres) Create(ctx context.Context, e *mailEntity.Endpoint) error {
	if e == nil {
		return fmt.Errorf("mail repo: thực thể endpoint entity không được phép nil")
	}

	dbModel := mailModel.EndpointEntityToModel(*e)

	result, err := r.db.Exec(ctx, r.createQuery,
		dbModel.ID.String(),
		dbModel.ZoneID.String(),
		dbModel.Name,
		dbModel.Host,
		dbModel.Port,
		dbModel.Username,
		dbModel.Password,
		dbModel.TLSMode,
		dbModel.Status,
		dbModel.MaxConnections,
		dbModel.Priority,
		dbModel.Weight,
		dbModel.CACertPEM,
		dbModel.ClientCertPEM,
		dbModel.ClientKeyPEM,
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
func (r *endpointRepoPostgres) GetByID(ctx context.Context, zoneID uuid.UUID, id uuid.UUID) (*mailEntity.Endpoint, error) {
	if zoneID == uuid.Nil || id == uuid.Nil {
		return nil, fmt.Errorf("mail repo: zoneID và id không được phép nil/empty")
	}

	var m mailModel.Endpoint
	var idStr, zoneIDStr string
	var createdAt, updatedAt time.Time
	err := r.db.QueryRow(ctx, r.getByIDQuery, zoneID.String(), id.String()).Scan(
		&idStr,
		&zoneIDStr,
		&m.Name,
		&m.Host,
		&m.Port,
		&m.Username,
		&m.Password,
		&m.TLSMode,
		&m.Status,
		&m.MaxConnections,
		&m.Priority,
		&m.Weight,
		&m.CACertPEM,
		&m.ClientCertPEM,
		&m.ClientKeyPEM,
		&m.IsActive,
		&createdAt,
		&updatedAt,
	)
	m.CreatedAt = &createdAt
	m.UpdatedAt = &updatedAt
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("mail repo: không tìm thấy endpoint (id: %s, zone: %s)", id.String(), zoneID.String())
		}
		return nil, fmt.Errorf("mail repo: lỗi truy vấn endpoint %s: %w", id.String(), err)
	}

	m.ID = uuid.MustParse(idStr)
	m.ZoneID = uuid.MustParse(zoneIDStr)

	ent := mailModel.EndpointModelToEntity(m)
	return &ent, nil
}

// List truy vấn toàn bộ danh sách mail endpoints thuộc về một physical Zone chỉ định.
func (r *endpointRepoPostgres) List(ctx context.Context, zoneID uuid.UUID) ([]*mailEntity.Endpoint, error) {
	var rows pgx.Rows
	var err error

	if zoneID == uuid.Nil {
		rows, err = r.db.Query(ctx, r.listAllQuery)
	} else {
		rows, err = r.db.Query(ctx, r.listByZoneQuery, zoneID.String())
	}

	if err != nil {
		return nil, fmt.Errorf("mail repo: lỗi truy vấn các endpoint: %w", err)
	}
	defer rows.Close()

	var endpoints []*mailEntity.Endpoint

	for rows.Next() {
		var m mailModel.Endpoint
		var idStr, zoneIDStr string
		var createdAt, updatedAt time.Time
		err := rows.Scan(
			&idStr,
			&zoneIDStr,
			&m.Name,
			&m.Host,
			&m.Port,
			&m.Username,
			&m.Password,
			&m.TLSMode,
			&m.Status,
			&m.MaxConnections,
			&m.Priority,
			&m.Weight,
			&m.CACertPEM,
			&m.ClientCertPEM,
			&m.ClientKeyPEM,
			&m.IsActive,
			&createdAt,
			&updatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("mail repo: lỗi scan hàng dữ liệu endpoint: %w", err)
		}
		m.CreatedAt = &createdAt
		m.UpdatedAt = &updatedAt

		m.ID = uuid.MustParse(idStr)
		m.ZoneID = uuid.MustParse(zoneIDStr)

		ent := mailModel.EndpointModelToEntity(m)
		endpoints = append(endpoints, &ent)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mail repo: lỗi rows cursor: %w", err)
	}

	return endpoints, nil
}

// Update cập nhật các trường thông tin phẳng của Endpoint.
func (r *endpointRepoPostgres) Update(ctx context.Context, e *mailEntity.Endpoint) error {
	if e == nil {
		return fmt.Errorf("mail repo: endpoint không được phép nil khi cập nhật")
	}

	dbModel := mailModel.EndpointEntityToModel(*e)

	result, err := r.db.Exec(ctx, r.updateQuery,
		dbModel.Name,
		dbModel.Host,
		dbModel.Port,
		dbModel.Username,
		dbModel.Password,
		dbModel.TLSMode,
		dbModel.Status,
		dbModel.MaxConnections,
		dbModel.Priority,
		dbModel.Weight,
		dbModel.CACertPEM,
		dbModel.ClientCertPEM,
		dbModel.ClientKeyPEM,
		dbModel.IsActive,
		time.Now().UTC(),
		dbModel.ZoneID.String(),
		dbModel.ID.String(),
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

	result, err := r.db.Exec(ctx, r.deleteQuery, zoneID.String(), id.String())
	if err != nil {
		return fmt.Errorf("mail repo: không thể xóa endpoint %s khỏi DB: %w", id.String(), err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("mail repo: không tìm thấy endpoint đích để xóa (id: %s, zone: %s)", id.String(), zoneID.String())
	}

	return nil
}
