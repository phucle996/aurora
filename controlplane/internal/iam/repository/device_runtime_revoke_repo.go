package iamRepoImpl

import (
	"context"
	"fmt"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DeviceRuntimeRevokeRepository owns only the resource-first revoke CTE and
// the leased outbox rows of that same workflow.
type DeviceRuntimeRevokeRepository struct {
	db     *pgxpool.Pool
	schema string
}

func NewDeviceRuntimeRevokeRepository(
	cfg *config.Config,
	db *pgxpool.Pool,
) iamRepoInterface.DeviceRuntimeRevokeRepository {
	return &DeviceRuntimeRevokeRepository{db: db, schema: cfg.SchemaSQL.IAM}
}

func (r *DeviceRuntimeRevokeRepository) RevokeDevice(
	ctx context.Context,
	command iamEntity.DeviceRuntimeRevokeDevice,
) (iamEntity.DeviceRuntimeRevokeResult, error) {
	query := fmt.Sprintf(`
		WITH target_device AS MATERIALIZED (
			SELECT id, COALESCE(client_device_id, id::text) AS client_device_id, revoked_at
			FROM %s.devices
			WHERE user_id = $1 AND COALESCE(client_device_id, id::text) = $2
			FOR UPDATE
		),
		revoked_device AS (
			UPDATE %s.devices AS device
			SET revoked_at = NOW(), updated_at = NOW()
			FROM target_device AS target
			WHERE device.id = target.id
			  AND target.client_device_id <> $3
			  AND target.revoked_at IS NULL
			RETURNING device.id
		),
		deleted_tokens AS (
			DELETE FROM %s.refresh_tokens
			WHERE user_id = $1 AND device_id IN (SELECT id FROM revoked_device)
			RETURNING id
		),
		queued AS (
			INSERT INTO %s.device_runtime_revoke_outbox_records (
				event_id, user_id, client_device_ids
			)
			SELECT $4, $1, ARRAY[target.client_device_id]::text[]
			FROM target_device AS target
			WHERE target.client_device_id <> $3
			ON CONFLICT (event_id) DO NOTHING
			RETURNING id
		)
		SELECT
			EXISTS (SELECT 1 FROM target_device),
			EXISTS (SELECT 1 FROM target_device WHERE client_device_id = $3),
			(SELECT COUNT(*) FROM revoked_device)
	`, r.schema, r.schema, r.schema, r.schema)

	var result iamEntity.DeviceRuntimeRevokeResult
	if err := r.db.QueryRow(
		ctx,
		query,
		command.UserID,
		command.ClientDeviceID.String(),
		command.CurrentDeviceID.String(),
		command.EventID,
	).Scan(&result.TargetExists, &result.CurrentDevice, &result.Affected); err != nil {
		return iamEntity.DeviceRuntimeRevokeResult{}, fmt.Errorf("iam device runtime revoke: revoke device: %w", err)
	}
	return result, nil
}

func (r *DeviceRuntimeRevokeRepository) RevokeOtherDevices(
	ctx context.Context,
	command iamEntity.DeviceRuntimeRevokeOthers,
) (int64, error) {
	query := fmt.Sprintf(`
		WITH target_devices AS MATERIALIZED (
			SELECT id, COALESCE(client_device_id, id::text) AS client_device_id, revoked_at
			FROM %s.devices
			WHERE user_id = $1 AND COALESCE(client_device_id, id::text) <> $2
			FOR UPDATE
		),
		revoked_devices AS (
			UPDATE %s.devices AS device
			SET revoked_at = NOW(), updated_at = NOW()
			FROM target_devices AS target
			WHERE device.id = target.id AND target.revoked_at IS NULL
			RETURNING device.id
		),
		deleted_tokens AS (
			DELETE FROM %s.refresh_tokens
			WHERE user_id = $1 AND device_id IN (SELECT id FROM revoked_devices)
			RETURNING id
		),
		queued AS (
			INSERT INTO %s.device_runtime_revoke_outbox_records (
				event_id, user_id, client_device_ids
			)
			SELECT $3, $1, ARRAY_AGG(client_device_id ORDER BY client_device_id)::text[]
			FROM target_devices
			HAVING COUNT(*) > 0
			ON CONFLICT (event_id) DO NOTHING
			RETURNING id
		)
		SELECT (SELECT COUNT(*) FROM target_devices)
	`, r.schema, r.schema, r.schema, r.schema)

	var affected int64
	if err := r.db.QueryRow(
		ctx,
		query,
		command.UserID,
		command.CurrentDeviceID.String(),
		command.EventID,
	).Scan(&affected); err != nil {
		return 0, fmt.Errorf("iam device runtime revoke: revoke other devices: %w", err)
	}
	return affected, nil
}

func (r *DeviceRuntimeRevokeRepository) Claim(
	ctx context.Context,
	limit int,
) ([]iamEntity.DeviceRuntimeRevokeOutboxEvent, error) {
	query := fmt.Sprintf(`
		WITH candidates AS (
			SELECT id
			FROM %s.device_runtime_revoke_outbox_records
			WHERE (status = 'PENDING' AND available_at <= NOW())
			   OR (status = 'PUBLISHING' AND lease_until < NOW())
			ORDER BY available_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE %s.device_runtime_revoke_outbox_records AS outbox
		SET status = 'PUBLISHING', lease_until = NOW() + INTERVAL '30 seconds',
			attempts = attempts + 1, updated_at = NOW()
		FROM candidates
		WHERE outbox.id = candidates.id
		RETURNING outbox.id, outbox.event_id, outbox.user_id, outbox.client_device_ids, outbox.attempts
	`, r.schema, r.schema)

	rows, err := r.db.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("iam device runtime revoke: claim: %w", err)
	}
	defer rows.Close()

	events := make([]iamEntity.DeviceRuntimeRevokeOutboxEvent, 0, limit)
	for rows.Next() {
		var event iamEntity.DeviceRuntimeRevokeOutboxEvent
		if err := rows.Scan(&event.ID, &event.EventID, &event.UserID, &event.ClientDeviceIDs, &event.Attempts); err != nil {
			return nil, fmt.Errorf("iam device runtime revoke: scan claim: %w", err)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (r *DeviceRuntimeRevokeRepository) MarkPublished(ctx context.Context, id int64) error {
	query := fmt.Sprintf(`
		UPDATE %s.device_runtime_revoke_outbox_records
		SET status = 'PUBLISHED', published_at = NOW(), lease_until = NULL, last_error = NULL, updated_at = NOW()
		WHERE id = $1 AND status = 'PUBLISHING'
	`, r.schema)
	_, err := r.db.Exec(ctx, query, id)
	return err
}

func (r *DeviceRuntimeRevokeRepository) MarkFailed(ctx context.Context, id int64, message string) error {
	query := fmt.Sprintf(`
		UPDATE %s.device_runtime_revoke_outbox_records
		SET status = CASE WHEN attempts >= 25 THEN 'DEAD' ELSE 'PENDING' END,
			available_at = NOW() + (LEAST(300, POWER(2, LEAST(attempts, 8))) * INTERVAL '1 second'),
			lease_until = NULL, last_error = LEFT($2, 2000), updated_at = NOW()
		WHERE id = $1 AND status = 'PUBLISHING'
	`, r.schema)
	_, err := r.db.Exec(ctx, query, id, message)
	return err
}

func (r *DeviceRuntimeRevokeRepository) MarkDead(ctx context.Context, id int64, message string) error {
	query := fmt.Sprintf(`
		UPDATE %s.device_runtime_revoke_outbox_records
		SET status = 'DEAD', lease_until = NULL, last_error = LEFT($2, 2000), updated_at = NOW()
		WHERE id = $1 AND status = 'PUBLISHING'
	`, r.schema)
	_, err := r.db.Exec(ctx, query, id, message)
	return err
}
