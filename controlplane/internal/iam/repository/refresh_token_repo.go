package iamRepoImpl

import (
	"context"
	"errors"
	"fmt"

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

func (r *RefreshTokenRepository) GetRefreshTokenByHash(ctx context.Context,
	tokenHash string) (*iamEntity.RefreshTokenSession, error) {
	query := fmt.Sprintf(`
	SELECT id, 
		user_id, 
		device_id,
		token_hash,
		token_family_id, 
		tenant_id,
		expires_at
		FROM %s.refresh_tokens
		WHERE token_hash = $1
		LIMIT 1
	`, r.schema)

	var session iamEntity.RefreshTokenSession
	if err := r.db.QueryRow(ctx, query, tokenHash).Scan(
		&session.ID,
		&session.UserID,
		&session.DeviceID,
		&session.TokenHash,
		&session.TokenFamilyID,
		&session.TenantID,
		&session.ExpiresAt,
	); err != nil {
		return nil, fmt.Errorf("iam repo: get refresh token session by hash: %w", err)
	}

	return &session, nil
}

func (r *RefreshTokenRepository) GetRefreshTokenDeviceByID(ctx context.Context, deviceID uuid.UUID) (*iamEntity.RefreshTokenDevice, error) {
	query := fmt.Sprintf(`
		SELECT id, status
		FROM %s.devices
		WHERE id = $1
		LIMIT 1
	`, r.schema)

	var device iamEntity.RefreshTokenDevice
	if err := r.db.QueryRow(ctx, query, deviceID).Scan(&device.ID, &device.Status); err != nil {
		return nil, fmt.Errorf("iam repo: get refresh token device by id: %w", err)
	}

	return &device, nil
}

func (r *RefreshTokenRepository) GetRefreshTokenUserByID(ctx context.Context, userID uuid.UUID) (*iamEntity.RefreshTokenUser, error) {
	query := fmt.Sprintf(`
		SELECT id, status
		FROM %s.users
		WHERE id = $1
		LIMIT 1
	`, r.schema)

	var user iamEntity.RefreshTokenUser
	if err := r.db.QueryRow(ctx, query, userID).Scan(&user.ID, &user.Status); err != nil {
		return nil, fmt.Errorf("iam repo: get refresh token user by id: %w", err)
	}

	return &user, nil
}

func (r *RefreshTokenRepository) RotateRefreshToken(ctx context.Context, current iamEntity.RefreshTokenSession, next iamEntity.RefreshToken) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("iam repo: begin rotate refresh token tx: %w", err)
	}
	defer tx.Rollback(ctx)

	deleteQuery := fmt.Sprintf(`
		DELETE FROM %s.refresh_tokens
		WHERE id = $1 AND token_hash = $2
		RETURNING id
	`, r.schema)

	var deletedID uuid.UUID
	if err := tx.QueryRow(ctx, deleteQuery, current.ID, current.TokenHash).Scan(&deletedID); err != nil {
		return fmt.Errorf("iam repo: delete current refresh token session: %w", err)
	}

	insertQuery := fmt.Sprintf(`
		INSERT INTO %s.refresh_tokens (
			id,
			user_id,
			device_id,
			token_hash,
			token_family_id,
			tenant_id,
			issued_at,
			expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, r.schema)

	if _, err := tx.Exec(
		ctx,
		insertQuery,
		next.ID,
		next.UserID,
		next.DeviceID,
		next.TokenHash,
		next.TokenFamilyID,
		next.TenantID,
		next.IssuedAt,
		next.ExpiresAt,
	); err != nil {
		return fmt.Errorf("iam repo: insert rotated refresh token session: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("iam repo: commit rotate refresh token tx: %w", err)
	}

	return nil
}

// [COMMENT]: Xóa bỏ Refresh Token session dựa trên hash để thực hiện thu hồi/logout khi nhận tín hiệu từ ACL
func (r *RefreshTokenRepository) DeleteRefreshTokenSessionByHash(ctx context.Context, tokenHash string) (int64, error) {
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

func (r *RefreshTokenRepository) RevokeRefreshTokensByUserID(ctx context.Context, userID uuid.UUID, exceptDeviceID *uuid.UUID) (int64, error) {
	query := fmt.Sprintf(`
		DELETE FROM %s.refresh_tokens
		WHERE user_id = $1 AND ($2::uuid IS NULL OR device_id <> $2)
	`, r.schema)
	res, err := r.db.Exec(ctx, query, userID, exceptDeviceID)
	if err != nil {
		return 0, fmt.Errorf("iam repo: revoke refresh tokens by user id: %w", err)
	}
	return res.RowsAffected(), nil
}

func (r *RefreshTokenRepository) RevokeRefreshTokensByDeviceIDAndUserID(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) (int64, error) {
	query := fmt.Sprintf(`
		DELETE FROM %s.refresh_tokens
		WHERE user_id = $1 AND device_id = $2
	`, r.schema)
	res, err := r.db.Exec(ctx, query, userID, deviceID)
	if err != nil {
		return 0, fmt.Errorf("iam repo: revoke refresh tokens by device and user: %w", err)
	}
	return res.RowsAffected(), nil
}

func (r *RefreshTokenRepository) RevokeRefreshTokensByDeviceIDsAndUserID(ctx context.Context, userID uuid.UUID, deviceIDs []uuid.UUID) (int64, error) {
	if len(deviceIDs) == 0 {
		return 0, nil
	}
	query := fmt.Sprintf(`
		DELETE FROM %s.refresh_tokens
		WHERE user_id = $1 AND device_id = ANY($2)
	`, r.schema)
	res, err := r.db.Exec(ctx, query, userID, deviceIDs)
	if err != nil {
		return 0, fmt.Errorf("iam repo: revoke refresh tokens by device ids: %w", err)
	}
	return res.RowsAffected(), nil
}

func (r *RefreshTokenRepository) LoadRefreshContextByHash(ctx context.Context, tokenHash string) (*iamEntity.RefreshContext, error) {
	query := fmt.Sprintf(`
		SELECT
			r.id, r.user_id, r.device_id, r.token_hash, r.token_family_id, r.tenant_id, r.expires_at,
			u.id, u.status,
			d.id, d.status
		FROM %s.refresh_tokens r
		LEFT JOIN %s.users u ON u.id = r.user_id
		LEFT JOIN %s.devices d ON d.id = r.device_id
		WHERE r.token_hash = $1 AND r.device_id IS NOT NULL
		LIMIT 1
	`, r.schema, r.schema, r.schema)
	var (
		ctxOut    iamEntity.RefreshContext
		deviceID  *uuid.UUID
		deviceSts *string
	)
	if err := r.db.QueryRow(ctx, query, tokenHash).Scan(
		&ctxOut.Session.ID,
		&ctxOut.Session.UserID,
		&ctxOut.Session.DeviceID,
		&ctxOut.Session.TokenHash,
		&ctxOut.Session.TokenFamilyID,
		&ctxOut.Session.TenantID,
		&ctxOut.Session.ExpiresAt,
		&ctxOut.User.ID,
		&ctxOut.User.Status,
		&deviceID,
		&deviceSts,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, iamTaxonomy.ErrNotFound
		}
		return nil, fmt.Errorf("iam repo: load refresh context: %w", err)
	}
	if deviceID != nil && deviceSts != nil {
		ctxOut.Device = &iamEntity.RefreshTokenDevice{ID: *deviceID, Status: iamEntity.DeviceStatus(*deviceSts)}
	}
	return &ctxOut, nil
}

// CreateRefreshTokenSession lưu trực tiếp một thực thể RefreshToken mới vào bảng database refresh_tokens.
// Đây là hàm hỗ trợ cho flow Login ban đầu khi chọn trust_device, giảm thiểu logic trung gian.
func (r *RefreshTokenRepository) CreateRefreshTokenSession(ctx context.Context, token iamEntity.RefreshToken) error {
	// Khởi tạo câu lệnh INSERT chèn trực tiếp dòng dữ liệu phiên làm việc
	query := fmt.Sprintf(`
		INSERT INTO %s.refresh_tokens (
			id,
			user_id,
			device_id,
			token_hash,
			token_family_id,
			tenant_id,
			issued_at,
			expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, r.schema)

	// Thực thi câu lệnh SQL với các tham số truyền vào
	if _, err := r.db.Exec(
		ctx,
		query,
		token.ID,
		token.UserID,
		token.DeviceID,
		token.TokenHash,
		token.TokenFamilyID,
		token.TenantID,
		token.IssuedAt,
		token.ExpiresAt,
	); err != nil {
		return fmt.Errorf("iam repo: create refresh token session: %w", err)
	}

	return nil
}

