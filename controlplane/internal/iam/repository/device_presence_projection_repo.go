package iamRepoImpl

import (
	"context"
	"fmt"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DevicePresenceProjectionRepository owns the IAM device-presence projection CTE.
type DevicePresenceProjectionRepository struct {
	db     *pgxpool.Pool
	schema string
}

func NewDevicePresenceProjectionRepository(
	cfg *config.Config,
	db *pgxpool.Pool,
) iamRepoInterface.DevicePresenceProjectionRepository {
	return &DevicePresenceProjectionRepository{db: db, schema: cfg.SchemaSQL.IAM}
}

func (r *DevicePresenceProjectionRepository) Apply(
	ctx context.Context,
	updates []iamEntity.DevicePresenceUpdate,
) error {
	ids := make([]string, len(updates))
	timestamps := make([]int64, len(updates))
	ips := make([]string, len(updates))
	userAgents := make([]string, len(updates))
	for i, update := range updates {
		ids[i] = update.DeviceID
		timestamps[i] = update.LastSeenAt
		ips[i] = update.LastSeenIP
		userAgents[i] = update.LastSeenUserAgent
	}

	query := fmt.Sprintf(`
		WITH raw_updates AS (
			SELECT
				unnest($1::text[]) AS client_device_id,
				unnest($2::bigint[]) AS last_seen_at_unix,
				unnest($3::text[]) AS last_seen_ip,
				unnest($4::text[]) AS last_seen_user_agent
		),
		updates AS (
			SELECT DISTINCT ON (client_device_id)
				client_device_id,
				last_seen_at_unix,
				last_seen_ip,
				last_seen_user_agent
			FROM raw_updates
			ORDER BY client_device_id, last_seen_at_unix DESC
		)
		UPDATE %s.devices AS device
		SET
			last_seen_at = to_timestamp(updates.last_seen_at_unix),
			last_seen_ip = NULLIF(updates.last_seen_ip, '')::inet,
			last_seen_user_agent = NULLIF(updates.last_seen_user_agent, ''),
			updated_at = now()
		FROM updates
		WHERE device.client_device_id = updates.client_device_id
		  AND (device.last_seen_at IS NULL OR device.last_seen_at <= to_timestamp(updates.last_seen_at_unix))
	`, r.schema)

	if _, err := r.db.Exec(ctx, query, ids, timestamps, ips, userAgents); err != nil {
		return fmt.Errorf("iam device presence projection: apply: %w", err)
	}
	return nil
}
