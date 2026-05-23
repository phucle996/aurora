package iamRepoImpl

import (
	"context"
	"fmt"
	"strings"
	"time"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	adminBootstrapLockKey int64 = 20260514
	adminRotationLockKey  int64 = 20260515
)

type AdminAPIKeyRepository struct {
	db     *pgxpool.Pool
	schema string
}

type bootstrapLock struct {
	conn *pgxpool.Conn
	key  int64
}

func NewAdminAPIKeyRepository(
	cfg *config.Config,
	db *pgxpool.Pool,
) iamRepoInterface.AdminAPIKeyRepository {
	return &AdminAPIKeyRepository{
		db:     db,
		schema: cfg.SchemaSQL.IAM,
	}
}

func (r *AdminAPIKeyRepository) AcquireBootstrapLock(ctx context.Context) (iamRepoInterface.BootstrapLock, error) {
	conn, err := r.db.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	var ok bool
	if err := conn.QueryRow(ctx,
		`SELECT pg_try_advisory_lock($1)`, adminBootstrapLockKey).Scan(&ok); err != nil {
		conn.Release()
		return nil, err
	}
	if !ok {
		conn.Release()
		return nil, fmt.Errorf("iam repo: bootstrap lock already held")
	}
	return &bootstrapLock{conn: conn, key: adminBootstrapLockKey}, nil
}

func (r *AdminAPIKeyRepository) AcquireRotationLock(ctx context.Context) (iamRepoInterface.BootstrapLock, error) {
	conn, err := r.db.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	var ok bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, adminRotationLockKey).Scan(&ok); err != nil {
		conn.Release()
		return nil, err
	}
	if !ok {
		conn.Release()
		return nil, fmt.Errorf("iam repo: rotation lock already held")
	}
	return &bootstrapLock{conn: conn, key: adminRotationLockKey}, nil
}

func (r *AdminAPIKeyRepository) PrepareNextAdminAPIKey(ctx context.Context, actor string, keyHash string, expiresAt time.Time) error {
	query := fmt.Sprintf(`
		INSERT INTO %s.admin_api_keys (id, key_hash, created_by, created_at, expires_at)
		VALUES ($1,$2,$3,$4,$5)
	`, r.schema)
	now := time.Now().UTC()
	_, err := r.db.Exec(ctx, query, uuid.New(), keyHash, actor, now, expiresAt.UTC())
	return err
}

func (r *AdminAPIKeyRepository) CommitPreparedAdminAPIKeyRotation(ctx context.Context) error {
	return nil
}

func (r *AdminAPIKeyRepository) RollbackPreparedAdminAPIKeyRotation(ctx context.Context) error {
	return nil
}

func (l *bootstrapLock) Release(ctx context.Context) error {
	if l == nil || l.conn == nil {
		return nil
	}
	key := l.key
	if key == 0 {
		key = adminBootstrapLockKey
	}
	_, err := l.conn.Exec(ctx,
		`SELECT pg_advisory_unlock($1)`, key)
	l.conn.Release()
	l.conn = nil
	return err
}

func (r *AdminAPIKeyRepository) GetActiveAdminAPIKey(ctx context.Context) (*iamEntity.AdminAPIKey, error) {
	query := fmt.Sprintf(`
		SELECT 
			id, 
			key_hash, 
			created_by, 
			created_at, 
			expires_at 
		FROM %s.admin_api_keys 
		WHERE expires_at > CURRENT_TIMESTAMP
		ORDER BY created_at DESC 
		LIMIT 1`, r.schema)
	var item iamEntity.AdminAPIKey
	if err := r.db.QueryRow(ctx, query).Scan(&item.ID, &item.KeyHash, &item.CreatedBy, &item.CreatedAt, &item.ExpiresAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

func (r *AdminAPIKeyRepository) Bootstrap(ctx context.Context, payload iamEntity.AdminBootstrapPayload) (time.Time, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback(ctx)

	now := payload.GeneratedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	deleteKey := fmt.Sprintf(`
		DELETE FROM %s.admin_api_keys`, r.schema)
	if _, err := tx.Exec(ctx, deleteKey); err != nil {
		return time.Time{}, err
	}
	insertKey := fmt.Sprintf(`
		INSERT INTO %s.admin_api_keys (id, key_hash, created_by, created_at, expires_at) 
		VALUES ($1,$2,$3,$4,$5)`, r.schema)
	if _, err := tx.Exec(ctx, insertKey, uuid.New(), payload.KeyHash, payload.Actor, now, payload.ExpiresAt.UTC()); err != nil {
		return time.Time{}, err
	}

	upsert2FA := fmt.Sprintf(`
		INSERT INTO 
			%s.admin_2fa_settings (
				id, 
				secret_ciphertext, 
				created_at, 
				updated_at) 
		VALUES (
			$1,
			$2,
			$3,
			$3)
		ON CONFLICT (id) DO UPDATE SET 
			secret_ciphertext = EXCLUDED.secret_ciphertext, 
			updated_at = EXCLUDED.updated_at`, r.schema)
	if _, err := tx.Exec(ctx, upsert2FA, uuid.Nil, payload.SecretCiphertext, now); err != nil {
		return time.Time{}, err
	}

	deleteRecovery := fmt.Sprintf(`
		DELETE FROM 
			%s.admin_recovery_codes`, r.schema)
	if _, err := tx.Exec(ctx, deleteRecovery); err != nil {
		return time.Time{}, err
	}
	insertRecovery := fmt.Sprintf(`
		INSERT INTO 
			%s.admin_recovery_codes (id, 
			code_hash, used_at, created_at) 
		VALUES ($1,$2,NULL,$3)`, r.schema)
	for _, hash := range payload.RecoveryCodeHashes {
		if _, err := tx.Exec(ctx, insertRecovery, uuid.New(), hash, now); err != nil {
			return time.Time{}, err
		}
	}

	insertAudit := fmt.Sprintf(`
		INSERT INTO 
			%s.admin_action_audits (
			id, 
			action, 
			resource_type, 
			resource_id, 
			status, 
			request_ip, 
			request_path, 
			request_method, 
			error_code, 
			metadata, 
			created_at
			) 
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		r.schema)
	metadata := map[string]any{"actor": payload.Actor}
	if _, err := tx.Exec(ctx, insertAudit, uuid.New(), "admin_bootstrap_succeeded", "admin_api_key", nil, "success", nil, "/internal/iam/bootstrap", "SYSTEM", nil, metadata, now); err != nil {
		return time.Time{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return time.Time{}, err
	}
	return now, nil
}

func (r *AdminAPIKeyRepository) RollbackBootstrap(ctx context.Context, payload iamEntity.AdminBootstrapPayload) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, fmt.Sprintf(`
		DELETE FROM %s.admin_recovery_codes
		WHERE created_at = $1`, r.schema), payload.GeneratedAt); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`
		DELETE FROM %s.admin_2fa_settings
		WHERE updated_at = $1 AND secret_ciphertext = $2`, r.schema), payload.GeneratedAt, payload.SecretCiphertext); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`
		DELETE FROM %s.admin_api_keys
		WHERE key_hash = $1 AND created_at = $2`, r.schema), payload.KeyHash, payload.GeneratedAt); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`
		DELETE FROM %s.admin_action_audits
		WHERE action = $1 AND created_at = $2 AND request_path = $3 AND request_method = $4`, r.schema), "admin_bootstrap_succeeded", payload.GeneratedAt, payload.RequestPath, payload.RequestMethod); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *AdminAPIKeyRepository) GetAdmin2FASettings(ctx context.Context) (*iamEntity.Admin2FASettings, error) {
	query := fmt.Sprintf(`
		SELECT id, secret_ciphertext, created_at, updated_at
		FROM %s.admin_2fa_settings
		ORDER BY updated_at DESC
		LIMIT 1`, r.schema)
	var item iamEntity.Admin2FASettings
	if err := r.db.QueryRow(ctx, query).Scan(&item.ID, &item.SecretCiphertext, &item.CreatedAt, &item.UpdatedAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

func (r *AdminAPIKeyRepository) ConsumeRecoveryCode(ctx context.Context, codeHash string, now time.Time) (bool, error) {
	query := fmt.Sprintf(`
		UPDATE %s.admin_recovery_codes
		SET used_at = $2
		WHERE code_hash = $1 AND used_at IS NULL`, r.schema)
	cmd, err := r.db.Exec(ctx, query, codeHash, now.UTC())
	if err != nil {
		return false, err
	}
	return cmd.RowsAffected() > 0, nil
}

func (r *AdminAPIKeyRepository) GetAdminDeviceByID(ctx context.Context, deviceID string) (*iamEntity.AdminDevice, error) {
	query := fmt.Sprintf(`
		SELECT id, device_name, device_type, os_name, browser_name,
			public_key, public_key_fingerprint, client_device_id,
			quarantined_at, revoked_at, last_seen_ip, last_seen_user_agent,
			last_seen_at, created_at, updated_at
		FROM %s.admin_devices
		WHERE id = $1
		LIMIT 1`, r.schema)

	item := iamEntity.AdminDevice{}
	err := r.db.QueryRow(ctx, query, deviceID).Scan(
		&item.ID,
		&item.DeviceName,
		&item.DeviceType,
		&item.OSName,
		&item.BrowserName,
		&item.PublicKey,
		&item.PublicKeyFingerprint,
		&item.ClientDeviceID,
		&item.QuarantinedAt,
		&item.RevokedAt,
		&item.LastSeenIP,
		&item.LastSeenUserAgent,
		&item.LastSeenAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *AdminAPIKeyRepository) UpsertAdminDeviceBinding(ctx context.Context, input iamEntity.AdminDeviceBindingInput) (*iamEntity.AdminDevice, error) {
	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	query := fmt.Sprintf(`
		INSERT INTO %s.admin_devices (
			id, device_name, device_type, os_name, browser_name,
			public_key, public_key_fingerprint, client_device_id,
			last_seen_ip, last_seen_user_agent, last_seen_at, created_at, updated_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12)
		ON CONFLICT (client_device_id) WHERE client_device_id IS NOT NULL
		DO UPDATE SET
			device_name = EXCLUDED.device_name,
			device_type = EXCLUDED.device_type,
			os_name = EXCLUDED.os_name,
			browser_name = EXCLUDED.browser_name,
			public_key = EXCLUDED.public_key,
			public_key_fingerprint = EXCLUDED.public_key_fingerprint,
			last_seen_ip = EXCLUDED.last_seen_ip,
			last_seen_user_agent = EXCLUDED.last_seen_user_agent,
			last_seen_at = EXCLUDED.last_seen_at,
			updated_at = EXCLUDED.updated_at
		RETURNING id, device_name, device_type, os_name, browser_name,
			public_key, public_key_fingerprint, client_device_id,
			quarantined_at, revoked_at, last_seen_ip, last_seen_user_agent,
			last_seen_at, created_at, updated_at`, r.schema)

	deviceID := uuid.New()
	item := iamEntity.AdminDevice{}
	err := r.db.QueryRow(ctx, query,
		deviceID,
		input.DeviceName,
		input.DeviceType,
		input.OSName,
		input.BrowserName,
		input.PublicKey,
		input.PublicKeyFingerprint,
		input.ClientDeviceID,
		input.LastSeenIP,
		input.LastSeenUserAgent,
		input.LastSeenAt,
		now,
	).Scan(
		&item.ID,
		&item.DeviceName,
		&item.DeviceType,
		&item.OSName,
		&item.BrowserName,
		&item.PublicKey,
		&item.PublicKeyFingerprint,
		&item.ClientDeviceID,
		&item.QuarantinedAt,
		&item.RevokedAt,
		&item.LastSeenIP,
		&item.LastSeenUserAgent,
		&item.LastSeenAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *AdminAPIKeyRepository) TouchAdminDeviceLastSeen(ctx context.Context, deviceID string, ip *string, userAgent *string, seenAt time.Time) error {
	query := fmt.Sprintf(`
		UPDATE %s.admin_devices
		SET last_seen_ip = $2,
			last_seen_user_agent = $3,
			last_seen_at = $4,
			updated_at = $4
		WHERE id = $1`, r.schema)
	_, err := r.db.Exec(ctx, query, strings.TrimSpace(deviceID), ip, userAgent, seenAt.UTC())
	return err
}
