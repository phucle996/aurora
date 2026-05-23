package coreRepoImpl

import (
	"context"
	"fmt"
	"strings"
	"time"

	"controlplane/internal/config"
	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreModel "controlplane/internal/core/model"
	"controlplane/pkg/id"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const secretBootstrapLockNamespace int32 = 20260512
const secretRotationLockNamespace int32 = 20260513

type SecretRepository struct {
	db     *pgxpool.Pool
	schema string
}

type secretAdvisoryLock struct {
	conn *pgxpool.Conn
	key1 int32
	key2 int32
}

func NewSecretRepository(cfg *config.Config,
	db *pgxpool.Pool) coreRepoInterface.SecretRepository {
	return &SecretRepository{
		db:     db,
		schema: cfg.SchemaSQL.Core,
	}
}

func (r *SecretRepository) AcquireFamilyBootstrapLock(ctx context.Context, familyCode string) (coreRepoInterface.SecretBootstrapLock, error) {
	conn, err := r.db.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	key2 := int32(id.CRC32String(strings.TrimSpace(familyCode)))
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1, $2)`, secretBootstrapLockNamespace, key2); err != nil {
		conn.Release()
		return nil, err
	}
	return &secretAdvisoryLock{conn: conn, key1: secretBootstrapLockNamespace, key2: key2}, nil
}

func (l *secretAdvisoryLock) Release(ctx context.Context) error {
	if l == nil || l.conn == nil {
		return nil
	}
	_, err := l.conn.Exec(ctx, `SELECT pg_advisory_unlock($1, $2)`, l.key1, l.key2)
	l.conn.Release()
	l.conn = nil
	return err
}

func (r *SecretRepository) AcquireFamilyRotationLock(ctx context.Context, familyCode string) (coreRepoInterface.SecretRotationLock, error) {
	conn, err := r.db.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	key2 := int32(id.CRC32String(strings.TrimSpace(familyCode)))
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1, $2)`, secretRotationLockNamespace, key2); err != nil {
		conn.Release()
		return nil, err
	}
	return &secretAdvisoryLock{conn: conn, key1: secretRotationLockNamespace, key2: key2}, nil
}

func (r *SecretRepository) GetFamilyByCode(ctx context.Context, code string) (*coreEntity.SecretFamily, error) {
	query := fmt.Sprintf(`SELECT id, code, name, description, created_at FROM %s.core_secret_families WHERE code = $1`, r.schema)
	var row coreModel.SecretFamily
	if err := r.db.QueryRow(ctx, query, strings.TrimSpace(code)).Scan(&row.ID, &row.Code, &row.Name, &row.Description, &row.CreatedAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	entityValue := coreModel.SecretFamilyModelToEntity(row)
	return &entityValue, nil
}

func (r *SecretRepository) EnsureFamily(ctx context.Context, family coreEntity.SecretFamily) (*coreEntity.SecretFamily, error) {
	query := fmt.Sprintf(`INSERT INTO %s.core_secret_families (id, code, name, description, created_at) VALUES ($1, $2, $3, $4, $5) ON CONFLICT (code) DO NOTHING`, r.schema)
	value := coreModel.SecretFamilyEntityToModel(family)
	if _, err := r.db.Exec(ctx, query, value.ID, value.Code, value.Name, value.Description, value.CreatedAt); err != nil {
		return nil, err
	}
	return r.GetFamilyByCode(ctx, family.Code)
}

func (r *SecretRepository) ListVersionsByFamilyID(ctx context.Context, familyID string) ([]coreEntity.SecretVersion, error) {
	query := fmt.Sprintf(`SELECT id, family_id, version, secret_ciphertext, secret_fingerprint, status, is_primary, not_before, not_after, activated_at, retired_at, revoked_at, rotation_reason, created_at, updated_at FROM %s.core_secret_versions WHERE family_id = $1 ORDER BY version DESC`, r.schema)
	rows, err := r.db.Query(ctx, query, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]coreEntity.SecretVersion, 0)
	for rows.Next() {
		var item coreModel.SecretVersion
		if err := rows.Scan(&item.ID, &item.FamilyID, &item.Version, &item.SecretCiphertext, &item.SecretFingerprint, &item.Status, &item.IsPrimary, &item.NotBefore, &item.NotAfter, &item.ActivatedAt, &item.RetiredAt, &item.RevokedAt, &item.RotationReason, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, coreModel.SecretVersionModelToEntity(item))
	}
	return result, rows.Err()
}

func (r *SecretRepository) CreateSecretVersion(ctx context.Context, version coreEntity.SecretVersion) error {
	query := fmt.Sprintf(`INSERT INTO %s.core_secret_versions (id, family_id, version, secret_ciphertext, secret_fingerprint, status, is_primary, not_before, not_after, activated_at, retired_at, revoked_at, rotation_reason, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, r.schema)
	value := coreModel.SecretVersionEntityToModel(version)
	_, err := r.db.Exec(ctx, query, value.ID, value.FamilyID, value.Version, value.SecretCiphertext, value.SecretFingerprint, value.Status, value.IsPrimary, value.NotBefore, value.NotAfter, value.ActivatedAt, value.RetiredAt, value.RevokedAt, value.RotationReason, value.CreatedAt, value.UpdatedAt)
	return err
}

func (r *SecretRepository) ReplacePrimaryVersion(ctx context.Context, familyID string, nextVersionID string, previousVersionID string, now time.Time) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	clearQuery := fmt.Sprintf(`UPDATE %s.core_secret_versions SET is_primary = false, updated_at = $2 WHERE family_id = $1 AND id <> $3 AND is_primary = true`, r.schema)
	if _, err := tx.Exec(ctx, clearQuery, familyID, now.UTC(), nextVersionID); err != nil {
		return err
	}

	promoteQuery := fmt.Sprintf(`UPDATE %s.core_secret_versions SET status = $2, is_primary = true, activated_at = COALESCE(activated_at, $3), updated_at = $3 WHERE id = $1`, r.schema)
	if _, err := tx.Exec(ctx, promoteQuery, nextVersionID, coreEntity.SecretStatusActive, now.UTC()); err != nil {
		return err
	}

	if previousVersionID != "" {
		demoteQuery := fmt.Sprintf(`UPDATE %s.core_secret_versions SET is_primary = false, updated_at = $2 WHERE id = $1`, r.schema)
		if _, err := tx.Exec(ctx, demoteQuery, previousVersionID, now.UTC()); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (r *SecretRepository) RetireVersion(ctx context.Context, versionID string, retiredAt time.Time) error {
	query := fmt.Sprintf(`UPDATE %s.core_secret_versions SET status = $2, is_primary = false, retired_at = $3, updated_at = $3 WHERE id = $1`, r.schema)
	_, err := r.db.Exec(ctx, query, versionID, coreEntity.SecretStatusRetired, retiredAt.UTC())
	return err
}

func (r *SecretRepository) DeleteVersion(ctx context.Context, versionID string) error {
	query := fmt.Sprintf(`DELETE FROM %s.core_secret_versions WHERE id = $1`, r.schema)
	_, err := r.db.Exec(ctx, query, versionID)
	return err
}

func (r *SecretRepository) QualifiedTable(table string) string {
	return fmt.Sprintf("%s.%s", r.schema, strings.TrimSpace(table))
}

func (r *SecretRepository) RotateFamilyVersions(ctx context.Context, familyID string, nextVersion coreEntity.SecretVersion, previousPrimaryID string, oldestVersionID string, retirePreviousNow bool, now time.Time) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if strings.TrimSpace(oldestVersionID) != "" {
		deleteQuery := fmt.Sprintf(`DELETE FROM %s.core_secret_versions WHERE id = $1`, r.schema)
		if _, err := tx.Exec(ctx, deleteQuery, oldestVersionID); err != nil {
			return err
		}
	}

	clearQuery := fmt.Sprintf(`UPDATE %s.core_secret_versions SET is_primary = false, updated_at = $2 WHERE family_id = $1 AND is_primary = true`, r.schema)
	if _, err := tx.Exec(ctx, clearQuery, familyID, now.UTC()); err != nil {
		return err
	}

	insertQuery := fmt.Sprintf(`INSERT INTO %s.core_secret_versions (id, family_id, version, secret_ciphertext, secret_fingerprint, status, is_primary, not_before, not_after, activated_at, retired_at, revoked_at, rotation_reason, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, r.schema)
	value := coreModel.SecretVersionEntityToModel(nextVersion)
	if _, err := tx.Exec(ctx, insertQuery, value.ID, value.FamilyID, value.Version, value.SecretCiphertext, value.SecretFingerprint, value.Status, false, value.NotBefore, value.NotAfter, value.ActivatedAt, value.RetiredAt, value.RevokedAt, value.RotationReason, value.CreatedAt, value.UpdatedAt); err != nil {
		return err
	}

	promoteQuery := fmt.Sprintf(`UPDATE %s.core_secret_versions SET status = $2, is_primary = true, activated_at = COALESCE(activated_at, $3), updated_at = $3 WHERE id = $1`, r.schema)
	if _, err := tx.Exec(ctx, promoteQuery, nextVersion.ID, coreEntity.SecretStatusActive, now.UTC()); err != nil {
		return err
	}

	if strings.TrimSpace(previousPrimaryID) != "" {
		demoteQuery := fmt.Sprintf(`UPDATE %s.core_secret_versions SET is_primary = false, updated_at = $2 WHERE id = $1`, r.schema)
		if _, err := tx.Exec(ctx, demoteQuery, previousPrimaryID, now.UTC()); err != nil {
			return err
		}
		if retirePreviousNow {
			retireQuery := fmt.Sprintf(`UPDATE %s.core_secret_versions SET status = $2, is_primary = false, retired_at = $3, updated_at = $3 WHERE id = $1`, r.schema)
			if _, err := tx.Exec(ctx, retireQuery, previousPrimaryID, coreEntity.SecretStatusRetired, now.UTC()); err != nil {
				return err
			}
		}
	}

	return tx.Commit(ctx)
}
