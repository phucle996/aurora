package iamRepoImpl

import (
	"context"
	"fmt"

	"controlplane/internal/config"
	iamRepoInterface "controlplane/internal/iam/domain/repo"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DeviceSessionCapacityEvictionRepository owns the durable CTE for ACR session-cap evictions.
type DeviceSessionCapacityEvictionRepository struct {
	db     *pgxpool.Pool
	schema string
}

func NewDeviceSessionCapacityEvictionRepository(
	cfg *config.Config,
	db *pgxpool.Pool,
) iamRepoInterface.DeviceSessionCapacityEvictionRepository {
	return &DeviceSessionCapacityEvictionRepository{db: db, schema: cfg.SchemaSQL.IAM}
}

func (r *DeviceSessionCapacityEvictionRepository) Evict(
	ctx context.Context,
	userID uuid.UUID,
	clientDeviceIDs []uuid.UUID,
) error {
	deviceIDs := make([]string, len(clientDeviceIDs))
	for i, deviceID := range clientDeviceIDs {
		deviceIDs[i] = deviceID.String()
	}

	query := fmt.Sprintf(`
		WITH requested_devices AS (
			SELECT DISTINCT unnest($2::text[]) AS client_device_id
		),
		revoked_devices AS (
			UPDATE %s.devices AS device
			SET revoked_at = now(), updated_at = now()
			FROM requested_devices
			WHERE device.user_id = $1
			  AND device.client_device_id = requested_devices.client_device_id
			  AND device.revoked_at IS NULL
			RETURNING device.id
		)
		DELETE FROM %s.refresh_tokens
		WHERE user_id = $1
		  AND device_id IN (SELECT id FROM revoked_devices)
	`, r.schema, r.schema)

	if _, err := r.db.Exec(ctx, query, userID, deviceIDs); err != nil {
		return fmt.Errorf("iam device session capacity eviction: apply: %w", err)
	}
	return nil
}
