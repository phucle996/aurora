package iamRepoImpl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamTaxonomy "controlplane/internal/iam/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RefreshTokenRepository struct {
	db     *pgxpool.Pool
	schema string
}

func NewRefreshTokenRepository(
	cfg *config.Config,
	db *pgxpool.Pool,
) iamRepoInterface.RefreshTokenRepository {
	return &RefreshTokenRepository{
		db:     db,
		schema: cfg.SchemaSQL.IAM,
	}
}

// [COMMENT]: Xóa bỏ Refresh Token session dựa trên hash để thực hiện thu hồi/logout khi nhận tín hiệu từ ACR
func (r *RefreshTokenRepository) DeleteByHash(ctx context.Context, tokenHash string) (int64, error) {
	query := fmt.Sprintf(`
		DELETE FROM %s.refresh_tokens
		WHERE token_hash = $1
	`, r.schema)
	res, err := r.db.Exec(ctx, query, tokenHash)
	if err != nil {
		// [COMMENT]: Trả lỗi trực tiếp không wrap theo yêu cầu
		return 0, err
	}
	rowsAffected := res.RowsAffected()
	if rowsAffected == 0 {
		// [COMMENT]: Trả lỗi ErrZeroRowsAffected đặc thù nếu không có bản ghi nào bị ảnh hưởng
		return 0, iamTaxonomy.ErrZeroRowsAffected
	}
	return rowsAffected, nil
}

func (r *RefreshTokenRepository) DeleteByUserID(ctx context.Context, userID uuid.UUID, exceptDeviceID *uuid.UUID) (int64, error) {
	query := fmt.Sprintf(`
		DELETE FROM %s.refresh_tokens
		WHERE user_id = $1 AND ($2::uuid IS NULL OR device_id <> $2)
	`, r.schema)
	res, err := r.db.Exec(ctx, query, userID, exceptDeviceID)
	if err != nil {
		return 0, fmt.Errorf("iam repo: delete refresh tokens by user id: %w", err)
	}
	return res.RowsAffected(), nil
}

func (r *RefreshTokenRepository) DeleteByDeviceID(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) (int64, error) {
	query := fmt.Sprintf(`
		DELETE FROM %s.refresh_tokens
		WHERE user_id = $1 AND device_id = $2
	`, r.schema)
	res, err := r.db.Exec(ctx, query, userID, deviceID)
	if err != nil {
		return 0, fmt.Errorf("iam repo: delete refresh tokens by device and user: %w", err)
	}
	return res.RowsAffected(), nil
}

func (r *RefreshTokenRepository) DeleteByDeviceIDs(ctx context.Context, userID uuid.UUID, deviceIDs []uuid.UUID) (int64, error) {
	if len(deviceIDs) == 0 {
		return 0, nil
	}
	query := fmt.Sprintf(`
		DELETE FROM %s.refresh_tokens
		WHERE user_id = $1 AND device_id = ANY($2)
	`, r.schema)
	res, err := r.db.Exec(ctx, query, userID, deviceIDs)
	if err != nil {
		return 0, fmt.Errorf("iam repo: delete refresh tokens by device ids: %w", err)
	}
	return res.RowsAffected(), nil
}



func (r *RefreshTokenRepository) LoadContextByHash(ctx context.Context, tokenHash string) (*iamEntity.RefreshContext, error) {
	query := fmt.Sprintf(`
		SELECT
			r.id, r.user_id, r.device_id, r.token_hash, r.tenant_id, r.expires_at,
			r.used_at, r.revoked_at,
			u.id, u.status, u.username,
			d.id, d.revoked_at
		FROM %s.refresh_tokens r
		LEFT JOIN %s.users u ON u.id = r.user_id
		LEFT JOIN %s.devices d ON d.id = r.device_id
		WHERE r.token_hash = $1 AND r.device_id IS NOT NULL
		LIMIT 1
	`, r.schema, r.schema, r.schema)
	var (
		ctxOut        iamEntity.RefreshContext
		deviceID      *uuid.UUID
		deviceRevoked *time.Time
	)
	if err := r.db.QueryRow(ctx, query, tokenHash).Scan(
		&ctxOut.Session.ID,
		&ctxOut.Session.UserID,
		&ctxOut.Session.DeviceID,
		&ctxOut.Session.TokenHash,
		&ctxOut.Session.TenantID,
		&ctxOut.Session.ExpiresAt,
		&ctxOut.Session.UsedAt,
		&ctxOut.Session.RevokedAt,
		&ctxOut.User.ID,
		&ctxOut.User.Status,
		&ctxOut.User.Username,
		&deviceID,
		&deviceRevoked,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, iamTaxonomy.ErrNotFound
		}
		return nil, fmt.Errorf("iam repo: load refresh context: %w", err)
	}
	if deviceID != nil {
		ctxOut.Device = &iamEntity.RefreshTokenDevice{ID: *deviceID, RevokedAt: deviceRevoked}
	}
	return &ctxOut, nil
}

// CreateSession lưu trực tiếp một thực thể RefreshToken mới vào bảng database refresh_tokens.
// Đây là hàm hỗ trợ cho flow Login ban đầu khi chọn trust_device, giảm thiểu logic trung gian.
func (r *RefreshTokenRepository) CreateToken(ctx context.Context, token iamEntity.RefreshToken) error {
	// Khởi tạo câu lệnh INSERT chèn trực tiếp dòng dữ liệu phiên làm việc
	query := fmt.Sprintf(`
		INSERT INTO %s.refresh_tokens (
			id,
			user_id,
			device_id,
			token_hash,
			tenant_id,
			issued_at,
			expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, r.schema)

	// Thực thi câu lệnh SQL với các tham số truyền vào
	if _, err := r.db.Exec(
		ctx,
		query,
		token.ID,
		token.UserID,
		token.DeviceID,
		token.TokenHash,
		token.TenantID,
		token.IssuedAt,
		token.ExpiresAt,
	); err != nil {
		return fmt.Errorf("iam repo: create refresh token session: %w", err)
	}

	return nil
}
