package iamRepoImpl

import (
	"context"
	"controlplane/internal/config"
	"errors"
	"fmt"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MfaRepository struct {
	db     *pgxpool.Pool
	schema string
}

// NewMfaRepository khởi tạo repository quản lý các tác vụ truy vấn MFA
func NewMfaRepository(cfg *config.Config, db *pgxpool.Pool) iamRepoInterface.MfaRepository {
	return &MfaRepository{
		db:     db,
		schema: cfg.SchemaSQL.IAM,
	}
}

// GetUserMfaStatus truy vấn trạng thái cài đặt MFA của người dùng cụ thể từ database
func (r *MfaRepository) GetUserMfaStatus(ctx context.Context, userID uuid.UUID) (bool, string, error) {
	// [COMMENT]: Lấy cấu hình MFA đang hoạt động (chưa bị disable) của user
	query := fmt.Sprintf(`
		SELECT created_at 
		FROM %s.mfa_settings 
		WHERE user_id = $1 AND disabled_at IS NULL
		LIMIT 1
	`, r.schema)

	var createdAt time.Time

	err := r.db.QueryRow(ctx, query, userID).Scan(&createdAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// [COMMENT]: Nếu không có record enabled, trả về false, không báo lỗi
			return false, "", nil
		}
		return false, "", err
	}

	return true, createdAt.Format("2006-01-02T15:04:05Z07:00"), nil
}
