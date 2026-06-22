package iamRepoImpl

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamModel "controlplane/internal/iam/model"
	iamTaxonomy "controlplane/internal/iam/taxonomy"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AuthRepository struct {
	db     *pgxpool.Pool
	schema string
}

func NewAuthRepository(
	cfg *config.Config,
	db *pgxpool.Pool,
) iamRepoInterface.AuthRepository {
	return &AuthRepository{
		db:     db,
		schema: cfg.SchemaSQL.IAM,
	}
}

func (r *AuthRepository) CheckUserExist(ctx context.Context, username string, email string) (bool, error) {
	query := fmt.Sprintf(`
		SELECT EXISTS (
			SELECT 1
			FROM %s.users
			WHERE username = $1
			   OR email = $2
		)
	`, r.schema)

	var exists bool

	if err := r.db.QueryRow(ctx, query, username, email).Scan(&exists); err != nil {
		return false, fmt.Errorf("iam repo: check user exist: %w", err)
	}

	return exists, nil
}

func (r *AuthRepository) GetLoginUserByUsername(ctx context.Context, username string) (*iamEntity.LoginUser, error) {
	// [COMMENT]: Thực hiện truy vấn trực tiếp bảng users để lấy thông tin đăng nhập mà không join sang bảng user_profiles
	query := fmt.Sprintf(`
		SELECT 
			id,
			username,
			email,
			password_hash, 
			status
		FROM %s.users
		WHERE username = $1
		LIMIT 1
	`, r.schema)

	// [COMMENT]: Khởi tạo đối tượng DB model đại diện cho bảng users để hứng dữ liệu quét từ DB
	var userModel iamModel.User
	if err := r.db.QueryRow(ctx, query, username).Scan(
		&userModel.ID,
		&userModel.Username,
		&userModel.Email,
		&userModel.PasswordHash,
		&userModel.Status,
	); err != nil {
		// [COMMENT]: Nếu không tìm thấy bản ghi người dùng, trả về lỗi nghiệp vụ ErrUserNotFound
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, iamTaxonomy.ErrUserNotFound
		}
		return nil, fmt.Errorf("iam repo: get login user by username: %w", err)
	}

	// [COMMENT]: Chuyển đổi từ cấu trúc DB Model sang Domain Entity để phục vụ Business Logic của Service
	loginUser := &iamEntity.LoginUser{
		ID:           userModel.ID,
		Username:     userModel.Username,
		Email:        userModel.Email,
		PasswordHash: userModel.PasswordHash,
		Status:       iamEntity.UserStatus(userModel.Status),
	}

	return loginUser, nil
}

func (r *AuthRepository) CreateRegisteredUser(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("iam repo: begin register tx: %w", err)
	}
	defer tx.Rollback(ctx)

	userModel := iamModel.UserEntityToModel(user)

	userQuery := fmt.Sprintf(`
		INSERT INTO %s.users (
			id,
			username,
			email,
			phone,
			password_hash,
			status,
			created_at,
			updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		)
	`, r.schema)

	if _, err := tx.Exec(
		ctx,
		userQuery,
		userModel.ID,
		userModel.Username,
		userModel.Email,
		userModel.Phone,
		userModel.PasswordHash,
		userModel.Status,
		userModel.CreatedAt,
		userModel.UpdatedAt,
	); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			constraint := strings.ToLower(pgErr.ConstraintName)
			if strings.Contains(constraint, "users_email_lower_uidx") || strings.Contains(constraint, "users_username_lower_uidx") {
				return fmt.Errorf("%w: %v", iamTaxonomy.ErrUserAlreadyExist, err)
			}
		}
		return fmt.Errorf("iam repo: insert user: %w", err)
	}

	profileModel := iamModel.UserProfileEntityToModel(profile)

	profileQuery := fmt.Sprintf(`
		INSERT INTO %s.user_profiles (
			user_id,
			fullname,
			avatar_url,
			bio,
			locale,
			timezone,
			created_at,
			updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		)
	`, r.schema)

	if _, err := tx.Exec(
		ctx,
		profileQuery,
		profileModel.UserID,
		profileModel.Fullname,
		profileModel.AvatarURL,
		profileModel.Bio,
		profileModel.Locale,
		profileModel.Timezone,
		profileModel.CreatedAt,
		profileModel.UpdatedAt,
	); err != nil {
		return fmt.Errorf("iam repo: insert user profile: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("iam repo: commit register tx: %w", err)
	}

	return nil
}

func (r *AuthRepository) CreateRefreshTokenSession(ctx context.Context, token iamEntity.RefreshToken) error {
	tokenModel := iamModel.RefreshTokenEntityToModel(token)
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
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	`, r.schema)

	if _, err := r.db.Exec(ctx, query,
		tokenModel.ID,
		tokenModel.UserID,
		tokenModel.DeviceID,
		tokenModel.TokenHash,
		tokenModel.TokenFamilyID,
		tokenModel.TenantID,
		tokenModel.IssuedAt,
		tokenModel.ExpiresAt,
	); err != nil {
		return fmt.Errorf("iam repo: create refresh token session: %w", err)
	}

	return nil
}
