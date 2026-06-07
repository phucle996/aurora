package coreRepoImpl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"controlplane/internal/config"
	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	"controlplane/internal/security"
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

func NewSecretRepository(cfg *config.Config, db *pgxpool.Pool) coreRepoInterface.SecretRepository {
	return &SecretRepository{
		db:     db,
		schema: cfg.SchemaSQL.Core,
	}
}

func (r *SecretRepository) AcquireSecretTypeBootstrapLock(ctx context.Context, secretType string) (coreRepoInterface.SecretBootstrapLock, error) {
	conn, err := r.db.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, "BEGIN"); err != nil {
		conn.Release()
		return nil, err
	}
	key2 := int32(id.CRC32String(strings.TrimSpace(secretType)))
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, secretBootstrapLockNamespace, key2); err != nil {
		_, _ = conn.Exec(ctx, "ROLLBACK")
		conn.Release()
		return nil, err
	}
	return &secretAdvisoryLock{conn: conn, key1: secretBootstrapLockNamespace, key2: key2}, nil
}

func (l *secretAdvisoryLock) Release(ctx context.Context) error {
	if l == nil || l.conn == nil {
		return nil
	}
	_, err := l.conn.Exec(ctx, "ROLLBACK")
	l.conn.Release()
	l.conn = nil
	return err
}

func (r *SecretRepository) AcquireSecretTypeRotationLock(ctx context.Context, secretType string) (coreRepoInterface.SecretRotationLock, error) {
	conn, err := r.db.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, "BEGIN"); err != nil {
		conn.Release()
		return nil, err
	}
	key2 := int32(id.CRC32String(strings.TrimSpace(secretType)))
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, secretRotationLockNamespace, key2); err != nil {
		_, _ = conn.Exec(ctx, "ROLLBACK")
		conn.Release()
		return nil, err
	}
	return &secretAdvisoryLock{conn: conn, key1: secretRotationLockNamespace, key2: key2}, nil
}

func (r *SecretRepository) GetSecretsByType(ctx context.Context, secretType string) (*coreEntity.RuntimeSecrets, error) {
	secretType = strings.TrimSpace(secretType)
	if secretType == "" {
		return nil, errors.New("empty secret type")
	}

	query := fmt.Sprintf(`
		SELECT secret_type, active_secret, active_fingerprint, active_created_at,
		       standby_secret, standby_fingerprint, standby_created_at, updated_at
		FROM %s.core_secrets
		WHERE secret_type = $1`, r.schema)

	var row coreEntity.CoreSecretRow
	err := r.db.QueryRow(ctx, query, secretType).Scan(
		&row.SecretType,
		&row.ActiveSecret,
		&row.ActiveFingerprint,
		&row.ActiveCreatedAt,
		&row.StandbySecret,
		&row.StandbyFingerprint,
		&row.StandbyCreatedAt,
		&row.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	activePlain, err := security.DecryptSecretBytes(row.ActiveSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt active secret for %s: %w", secretType, err)
	}

	standbyPlain, err := security.DecryptSecretBytes(row.StandbySecret)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt standby secret for %s: %w", secretType, err)
	}

	return &coreEntity.RuntimeSecrets{
		SecretType: row.SecretType,
		Active: coreEntity.RuntimeSecret{
			Secret:      activePlain,
			Fingerprint: row.ActiveFingerprint,
			CreatedAt:   row.ActiveCreatedAt,
		},
		Standby: coreEntity.RuntimeSecret{
			Secret:      standbyPlain,
			Fingerprint: row.StandbyFingerprint,
			CreatedAt:   row.StandbyCreatedAt,
		},
		LoadedAt: time.Now().UTC(),
	}, nil
}

func (r *SecretRepository) SaveSecrets(ctx context.Context, row coreEntity.CoreSecretRow) error {
	query := fmt.Sprintf(`
		INSERT INTO %s.core_secrets (
			secret_type, active_secret, active_fingerprint, active_created_at,
			standby_secret, standby_fingerprint, standby_created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (secret_type) DO UPDATE SET
			active_secret = EXCLUDED.active_secret,
			active_fingerprint = EXCLUDED.active_fingerprint,
			active_created_at = EXCLUDED.active_created_at,
			standby_secret = EXCLUDED.standby_secret,
			standby_fingerprint = EXCLUDED.standby_fingerprint,
			standby_created_at = EXCLUDED.standby_created_at,
			updated_at = EXCLUDED.updated_at`, r.schema)

	_, err := r.db.Exec(ctx, query,
		row.SecretType,
		row.ActiveSecret,
		row.ActiveFingerprint,
		row.ActiveCreatedAt,
		row.StandbySecret,
		row.StandbyFingerprint,
		row.StandbyCreatedAt,
		row.UpdatedAt,
	)
	return err
}

func (r *SecretRepository) UpdateSecrets(ctx context.Context, secretType string, activeSecret, activeFingerprint string, standbySecret, standbyFingerprint string) error {
	query := fmt.Sprintf(`
		UPDATE %s.core_secrets
		SET active_secret = $2,
			active_fingerprint = $3,
			active_created_at = $4,
			standby_secret = $5,
			standby_fingerprint = $6,
			standby_created_at = $7,
			updated_at = $8
		WHERE secret_type = $1`, r.schema)

	now := time.Now().UTC()
	_, err := r.db.Exec(ctx, query,
		secretType,
		activeSecret,
		activeFingerprint,
		now,
		standbySecret,
		standbyFingerprint,
		now,
		now,
	)
	return err
}

func (r *SecretRepository) GetAccessSecret(ctx context.Context) (*coreEntity.RuntimeSecrets, error) {
	return r.GetSecretsByType(ctx, "access_secret")
}

func (r *SecretRepository) GetRefreshSecret(ctx context.Context) (*coreEntity.RuntimeSecrets, error) {
	return r.GetSecretsByType(ctx, "refresh_secret")
}

func (r *SecretRepository) GetAdminAPIKey(ctx context.Context) (*coreEntity.RuntimeSecrets, error) {
	return r.GetSecretsByType(ctx, "admin_api_key")
}

func (r *SecretRepository) GetOneTimeTokenSecret(ctx context.Context) (*coreEntity.RuntimeSecrets, error) {
	return r.GetSecretsByType(ctx, "one_time_token")
}
