package mailRepoImpl

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"controlplane/internal/config"
	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"
	mailModel "controlplane/internal/mail/model"
	mailTaxonomy "controlplane/internal/mail/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Định nghĩa context key dùng cho truyền tải transaction nội bộ trong package postgres
type txKey struct{}

// QueryExecutor đại diện cho một đối tượng thực thi truy vấn SQL (có thể là pgxpool.Pool hoặc pgx.Tx)
type QueryExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// endpointRepoPostgres triển khai mailRepoInterface.EndpointRepository sử dụng Static Statement để tối ưu hot path.
type endpointRepoPostgres struct {
	db                 *pgxpool.Pool                          // Kết nối cơ sở dữ liệu Postgres thread-safe
	schema             string                                 // Tên schema SQL động được cấu hình
	outboxRepo         mailRepoInterface.MailOutboxRepository // Repository cho mail outbox records
	createQuery        string                                 // Câu lệnh tạo mới endpoint
	getByIDQuery       string                                 // Câu lệnh lấy chi tiết endpoint theo zone
	getGlobalByIDQuery string                                 // Câu lệnh lấy chi tiết endpoint toàn cầu
	listGlobalQuery    string                                 // Câu lệnh lấy danh sách endpoint toàn cầu bằng cursor
	listByZoneQuery    string                                 // Câu lệnh lấy danh sách endpoint theo zone bằng cursor
	updateQuery        string                                 // Câu lệnh cập nhật endpoint
	deleteQuery        string                                 // Câu lệnh xóa endpoint
}

// NewEndpointRepository khởi tạo một đối tượng EndpointRepository mới cho Postgres và biên dịch sẵn SQL statements.
func NewEndpointRepository(db *pgxpool.Pool, cfg *config.Config, outboxRepo mailRepoInterface.MailOutboxRepository) mailRepoInterface.EndpointRepository {
	schema := cfg.SchemaSQL.Mail
	return &endpointRepoPostgres{
		db:         db,
		schema:     schema,
		outboxRepo: outboxRepo,
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
		getGlobalByIDQuery: fmt.Sprintf(`
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
			WHERE id = $1
		`, schema),
		listGlobalQuery: fmt.Sprintf(`
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
			WHERE ($1::timestamp IS NULL OR (created_at, id) < ($1::timestamp, $2::varchar))
			ORDER BY created_at DESC, id DESC
			LIMIT $3
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
			WHERE zone_id = $1 AND ($2::timestamp IS NULL OR (created_at, id) < ($2::timestamp, $3::varchar))
			ORDER BY created_at DESC, id DESC
			LIMIT $4
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

// Create chèn thêm một mail endpoint mới và ghi outbox record đồng bộ trong cùng một transaction ở repo layer.
func (r *endpointRepoPostgres) Create(ctx context.Context, e *mailEntity.Endpoint, outbox *mailEntity.MailOutboxRecord) error {
	// Bắt đầu một database transaction
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("mail repo: không thể khởi tạo transaction: %w", err)
	}
	// Đảm bảo rollback khi hàm thoát với lỗi hoặc panic
	defer tx.Rollback(ctx)

	dbModel := mailModel.EndpointEntityToModel(*e)

	// Thực thi chèn endpoint sử dụng transaction
	result, err := tx.Exec(ctx, r.createQuery,
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
		return err
	}

	if result.RowsAffected() == 0 {
		return mailTaxonomy.ErrZeroRowsAffected
	}

	// Đưa transaction vào context và gọi outboxRepo.Create để lưu trữ outbox
	txCtx := context.WithValue(ctx, txKey{}, tx)
	if err := r.outboxRepo.Create(txCtx, outbox); err != nil {
		return err
	}

	// Commit giao dịch
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("mail repo: không thể commit transaction: %w", err)
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

// GetGlobalByID truy vấn chi tiết một Endpoint trên toàn bộ các zone dựa trên ID.
func (r *endpointRepoPostgres) GetGlobalByID(ctx context.Context, id uuid.UUID) (*mailEntity.Endpoint, error) {
	if id == uuid.Nil {
		return nil, fmt.Errorf("mail repo: id không được phép nil/empty")
	}

	var m mailModel.Endpoint
	var idStr, zoneIDStr string
	var createdAt, updatedAt time.Time
	err := r.db.QueryRow(ctx, r.getGlobalByIDQuery, id.String()).Scan(
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
			return nil, fmt.Errorf("mail repo: không tìm thấy endpoint toàn cầu (id: %s)", id.String())
		}
		return nil, fmt.Errorf("mail repo: lỗi truy vấn endpoint toàn cầu %s: %w", id.String(), err)
	}

	m.ID = uuid.MustParse(idStr)
	m.ZoneID = uuid.MustParse(zoneIDStr)

	ent := mailModel.EndpointModelToEntity(m)
	return &ent, nil
}

// ListByZone truy vấn danh sách mail endpoints thuộc về một physical Zone chỉ định bằng cursor.
func (r *endpointRepoPostgres) ListByZone(ctx context.Context, zoneID uuid.UUID, cursor string, limit int) ([]*mailEntity.Endpoint, string, error) {
	if zoneID == uuid.Nil {
		return nil, "", fmt.Errorf("mail repo: zoneID không được phép nil khi liệt kê theo zone")
	}

	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if cursor != "" {
		t, uid, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		cursorTime = &t
		cursorID = &uid
	}

	queryLimit := limit + 1
	rows, err := r.db.Query(ctx, r.listByZoneQuery, zoneID.String(), cursorTime, cursorID, queryLimit)
	if err != nil {
		return nil, "", fmt.Errorf("mail repo: lỗi truy vấn các endpoint theo zone: %w", err)
	}
	defer rows.Close()

	return r.scanEndpointsAndBuildCursor(rows, limit)
}

// ListGlobal truy vấn danh sách mail endpoints trên toàn bộ các zone bằng cursor.
func (r *endpointRepoPostgres) ListGlobal(ctx context.Context, cursor string, limit int) ([]*mailEntity.Endpoint, string, error) {
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if cursor != "" {
		t, uid, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		cursorTime = &t
		cursorID = &uid
	}

	queryLimit := limit + 1
	rows, err := r.db.Query(ctx, r.listGlobalQuery, cursorTime, cursorID, queryLimit)
	if err != nil {
		return nil, "", fmt.Errorf("mail repo: lỗi truy vấn các endpoint toàn cầu: %w", err)
	}
	defer rows.Close()

	return r.scanEndpointsAndBuildCursor(rows, limit)
}

func (r *endpointRepoPostgres) scanEndpointsAndBuildCursor(rows pgx.Rows, limit int) ([]*mailEntity.Endpoint, string, error) {
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
			return nil, "", fmt.Errorf("mail repo: lỗi scan hàng dữ liệu endpoint: %w", err)
		}
		m.CreatedAt = &createdAt
		m.UpdatedAt = &updatedAt

		m.ID = uuid.MustParse(idStr)
		m.ZoneID = uuid.MustParse(zoneIDStr)

		ent := mailModel.EndpointModelToEntity(m)
		endpoints = append(endpoints, &ent)
	}

	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("mail repo: lỗi rows cursor: %w", err)
	}

	var nextCursor string
	if len(endpoints) > limit {
		lastRecord := endpoints[limit-1]
		nextCursor = encodeCursor(*lastRecord.CreatedAt, lastRecord.ID)
		endpoints = endpoints[:limit]
	}

	return endpoints, nextCursor, nil
}

func encodeCursor(t time.Time, id uuid.UUID) string {
	str := fmt.Sprintf("%d,%s", t.UnixNano(), id.String())
	return base64.StdEncoding.EncodeToString([]byte(str))
}

func decodeCursor(cursorStr string) (time.Time, uuid.UUID, error) {
	if cursorStr == "" {
		return time.Time{}, uuid.Nil, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(cursorStr)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("invalid cursor encoding: %w", err)
	}
	parts := strings.Split(string(decoded), ",")
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, fmt.Errorf("invalid cursor format")
	}
	nano, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("invalid cursor timestamp: %w", err)
	}
	uid, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("invalid cursor uuid: %w", err)
	}
	return time.Unix(0, nano), uid, nil
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
