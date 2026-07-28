package iamRepoImpl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamTaxonomy "controlplane/internal/iam/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MfaRepository struct {
	db     *pgxpool.Pool
	schema string
}

func NewMfaRepository(cfg *config.Config, db *pgxpool.Pool) iamRepoInterface.MfaRepository {
	return &MfaRepository{db: db, schema: cfg.SchemaSQL.IAM}
}

func (r *MfaRepository) GetSelfStatus(ctx context.Context, userID uuid.UUID) (*iamEntity.MFASetting, int, error) {
	query := fmt.Sprintf(`
		SELECT ms.id, ms.user_id, ms.secret_ciphertext, ms.secret_key_id,
		       ms.created_at, ms.updated_at, COUNT(rc.id)
		FROM %s.mfa_settings ms
		LEFT JOIN %s.mfa_recovery_codes rc ON rc.mfa_setting_id = ms.id
		WHERE ms.user_id = $1
		GROUP BY ms.id
	`, r.schema, r.schema)

	var setting iamEntity.MFASetting
	var recoveryCount int
	if err := r.db.QueryRow(ctx, query, userID).Scan(
		&setting.ID,
		&setting.UserID,
		&setting.SecretCiphertext,
		&setting.SecretKeyID,
		&setting.CreatedAt,
		&setting.UpdatedAt,
		&recoveryCount,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, iamTaxonomy.ErrPreconditionFailed
		}
		return nil, 0, err
	}
	return &setting, recoveryCount, nil
}

func (r *MfaRepository) GetPlatformStatus(ctx context.Context, userID uuid.UUID, callerLevel uint8) (bool, string, error) {
	query := fmt.Sprintf(`
		WITH target_user AS (
			SELECT u.id, MIN(ur.role_level) AS role_level
			FROM %s.users u
			JOIN %s.user_role ur
			  ON ur.user_id = u.id
			 AND ur.workspace_id = '00000000-0000-0000-0000-000000000000'
			WHERE u.id = $1
			GROUP BY u.id
		),
		authorized_target AS (
			SELECT id
			FROM target_user
			WHERE role_level > $2
		),
		target_mfa AS (
			SELECT ms.created_at
			FROM %s.mfa_settings ms
			JOIN authorized_target authz ON authz.id = ms.user_id
			LIMIT 1
		)
		SELECT
			EXISTS (
				SELECT 1 FROM %s.users WHERE id = $1
			) AS user_exists,
			EXISTS (
				SELECT 1 FROM target_user
			) AS target_has_role,
			EXISTS (
				SELECT 1 FROM authorized_target
			) AS hierarchy_allowed,
			(SELECT created_at FROM target_mfa) AS created_at
	`, r.schema, r.schema, r.schema, r.schema)

	var (
		userExists       bool
		targetHasRole    bool
		hierarchyAllowed bool
		createdAt        *time.Time
	)
	if err := r.db.QueryRow(ctx, query, userID, callerLevel).Scan(
		&userExists,
		&targetHasRole,
		&hierarchyAllowed,
		&createdAt,
	); err != nil {
		return false, "", err
	}
	if !userExists {
		return false, "", iamTaxonomy.ErrUserNotFound
	}
	if !targetHasRole || !hierarchyAllowed {
		return false, "", iamTaxonomy.ErrActionNotAllowed
	}
	if createdAt == nil {
		return false, "", nil
	}
	return true, createdAt.Format(time.RFC3339), nil
}

func (r *MfaRepository) SetupStart(ctx context.Context, userID uuid.UUID) error {
	query := fmt.Sprintf(`
		WITH existing_enrollment AS (
			SELECT id
			FROM %s.mfa_settings
			WHERE user_id = $1
			LIMIT 1
		)
		SELECT EXISTS (
			SELECT 1 FROM existing_enrollment
		)
	`, r.schema)

	var exists bool
	if err := r.db.QueryRow(ctx, query, userID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return iamTaxonomy.ErrMFAAlreadyEnabled
	}
	return nil
}

func (r *MfaRepository) SetupConfirmEnable(
	ctx context.Context,
	userID, settingID uuid.UUID,
	secretCiphertext, secretKeyID string,
	recoveryHashes []string,
) (time.Time, error) {
	values := make([]string, len(recoveryHashes))
	args := make([]any, 0, 4+len(recoveryHashes))
	args = append(args, userID, settingID, secretCiphertext, secretKeyID)
	for i, hash := range recoveryHashes {
		values[i] = fmt.Sprintf("($%d)", 5+i)
		args = append(args, hash)
	}

	query := fmt.Sprintf(`
		WITH inserted_setting AS (
			INSERT INTO %s.mfa_settings (
				id, user_id, secret_ciphertext, secret_key_id, created_at, updated_at
			)
			VALUES ($2, $1, $3, $4, NOW(), NOW())
			ON CONFLICT (user_id) DO NOTHING
			RETURNING id, created_at
		),
		inserted_codes AS (
			INSERT INTO %s.mfa_recovery_codes (mfa_setting_id, code_hash)
			SELECT (SELECT id FROM inserted_setting), code_hash
			FROM (VALUES %s) AS values_list(code_hash)
			WHERE EXISTS (SELECT 1 FROM inserted_setting)
			RETURNING id
		)
		SELECT created_at
		FROM inserted_setting
		WHERE EXISTS (SELECT 1 FROM inserted_codes)
	`, r.schema, r.schema, strings.Join(values, ","))

	var createdAt time.Time
	if err := r.db.QueryRow(ctx, query, args...).Scan(&createdAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, iamTaxonomy.ErrMFAAlreadyEnabled
		}
		return time.Time{}, err
	}
	return createdAt, nil
}

func (r *MfaRepository) RecoveryRegenerateGetSetting(ctx context.Context, userID uuid.UUID) (*iamEntity.MFASetting, error) {
	query := fmt.Sprintf(`
		SELECT id, user_id, secret_ciphertext, secret_key_id, created_at, updated_at
		FROM %s.mfa_settings
		WHERE user_id = $1
		LIMIT 1
	`, r.schema)

	var setting iamEntity.MFASetting
	if err := r.db.QueryRow(ctx, query, userID).Scan(
		&setting.ID,
		&setting.UserID,
		&setting.SecretCiphertext,
		&setting.SecretKeyID,
		&setting.CreatedAt,
		&setting.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, iamTaxonomy.ErrPreconditionFailed
		}
		return nil, err
	}
	return &setting, nil
}

func (r *MfaRepository) RecoveryRegenerateReplace(ctx context.Context, userID uuid.UUID, recoveryHashes []string) error {
	values := make([]string, len(recoveryHashes))
	args := make([]any, 0, 1+len(recoveryHashes))
	args = append(args, userID)
	for i, hash := range recoveryHashes {
		values[i] = fmt.Sprintf("($%d)", 2+i)
		args = append(args, hash)
	}

	query := fmt.Sprintf(`
		WITH locked AS (
			SELECT id
			FROM %s.mfa_settings
			WHERE user_id = $1
			FOR UPDATE
		),
		deleted AS (
			DELETE FROM %s.mfa_recovery_codes
			WHERE mfa_setting_id = (SELECT id FROM locked)
			RETURNING id
		)
		INSERT INTO %s.mfa_recovery_codes (mfa_setting_id, code_hash)
		SELECT (SELECT id FROM locked), code_hash
		FROM (VALUES %s) AS values_list(code_hash)
		WHERE EXISTS (SELECT 1 FROM locked)
	`, r.schema, r.schema, r.schema, strings.Join(values, ","))

	tag, err := r.db.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return iamTaxonomy.ErrPreconditionFailed
	}
	return nil
}

func (r *MfaRepository) RemoveGetSetting(ctx context.Context, userID uuid.UUID) (*iamEntity.MFASetting, error) {
	query := fmt.Sprintf(`
		SELECT id, user_id, secret_ciphertext, secret_key_id, created_at, updated_at
		FROM %s.mfa_settings
		WHERE user_id = $1
		LIMIT 1
	`, r.schema)

	var setting iamEntity.MFASetting
	if err := r.db.QueryRow(ctx, query, userID).Scan(
		&setting.ID,
		&setting.UserID,
		&setting.SecretCiphertext,
		&setting.SecretKeyID,
		&setting.CreatedAt,
		&setting.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, iamTaxonomy.ErrPreconditionFailed
		}
		return nil, err
	}
	return &setting, nil
}

func (r *MfaRepository) RemoveDelete(ctx context.Context, userID uuid.UUID) error {
	query := fmt.Sprintf(`
		DELETE FROM %s.mfa_settings
		WHERE user_id = $1
	`, r.schema)

	tag, err := r.db.Exec(ctx, query, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return iamTaxonomy.ErrPreconditionFailed
	}
	return nil
}

func (r *MfaRepository) LoginGateGetSetting(ctx context.Context, userID uuid.UUID) (*iamEntity.MFASetting, error) {
	query := fmt.Sprintf(`
		SELECT id, user_id, secret_ciphertext, secret_key_id, created_at, updated_at
		FROM %s.mfa_settings
		WHERE user_id = $1
		LIMIT 1
	`, r.schema)

	var setting iamEntity.MFASetting
	if err := r.db.QueryRow(ctx, query, userID).Scan(
		&setting.ID,
		&setting.UserID,
		&setting.SecretCiphertext,
		&setting.SecretKeyID,
		&setting.CreatedAt,
		&setting.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, iamTaxonomy.ErrPreconditionFailed
		}
		return nil, err
	}
	return &setting, nil
}

func (r *MfaRepository) LoginVerifyGetSetting(ctx context.Context, userID uuid.UUID) (*iamEntity.MFASetting, error) {
	query := fmt.Sprintf(`
		SELECT id, user_id, secret_ciphertext, secret_key_id, created_at, updated_at
		FROM %s.mfa_settings
		WHERE user_id = $1
		LIMIT 1
	`, r.schema)

	var setting iamEntity.MFASetting
	if err := r.db.QueryRow(ctx, query, userID).Scan(
		&setting.ID,
		&setting.UserID,
		&setting.SecretCiphertext,
		&setting.SecretKeyID,
		&setting.CreatedAt,
		&setting.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, iamTaxonomy.ErrPreconditionFailed
		}
		return nil, err
	}
	return &setting, nil
}

func (r *MfaRepository) LoginConsumeRecoveryCode(
	ctx context.Context,
	userID, settingID uuid.UUID,
	codeHash string,
) error {
	query := fmt.Sprintf(`
		DELETE FROM %s.mfa_recovery_codes rc
		USING %s.mfa_settings ms
		WHERE rc.mfa_setting_id = ms.id
		  AND ms.user_id = $1
		  AND ms.id = $2
		  AND rc.code_hash = $3
	`, r.schema, r.schema)

	tag, err := r.db.Exec(ctx, query, userID, settingID, codeHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return iamTaxonomy.ErrRecoveryCodeInvalid
	}
	return nil
}
