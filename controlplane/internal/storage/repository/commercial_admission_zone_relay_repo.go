package storageRepoImpl

import (
	"context"
	"fmt"

	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CommercialAdmissionZoneRelayRepo struct {
	db     *pgxpool.Pool
	schema string
}

func NewCommercialAdmissionZoneRelayRepo(
	db *pgxpool.Pool,
	schema string,
) storageRepoInterface.CommercialAdmissionZoneRelayRepository {
	return &CommercialAdmissionZoneRelayRepo{db: db, schema: schema}
}

func (r *CommercialAdmissionZoneRelayRepo) Claim(
	ctx context.Context,
	claimToken uuid.UUID,
	limit int,
) ([]storageEntity.CommercialAdmissionZoneDelivery, error) {
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		WITH picked AS (
			SELECT resource_id, zone_id
			FROM %s.commercial_admission_zone_outbox
			WHERE published_at IS NULL
			  AND (claimed_at IS NULL OR claimed_at < NOW() - INTERVAL '1 minute')
			ORDER BY updated_at, resource_id
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE %s.commercial_admission_zone_outbox outbox
		SET claim_token=$1, claimed_at=NOW()
		FROM picked
		WHERE outbox.resource_id=picked.resource_id AND outbox.zone_id=picked.zone_id
		RETURNING outbox.resource_id, outbox.zone_id, outbox.source_event_id,
		          outbox.policy_version, outbox.payload, outbox.claim_token`, r.schema, r.schema),
		claimToken, limit)
	if err != nil {
		return nil, fmt.Errorf("claim Storage commercial admission Zone deliveries: %w", err)
	}
	defer rows.Close()

	deliveries := make([]storageEntity.CommercialAdmissionZoneDelivery, 0, limit)
	for rows.Next() {
		var delivery storageEntity.CommercialAdmissionZoneDelivery
		if err := rows.Scan(
			&delivery.ResourceID, &delivery.ZoneID, &delivery.SourceEventID,
			&delivery.PolicyVersion, &delivery.Payload, &delivery.ClaimToken,
		); err != nil {
			return nil, fmt.Errorf("scan Storage commercial admission Zone delivery: %w", err)
		}
		deliveries = append(deliveries, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Storage commercial admission Zone deliveries: %w", err)
	}
	return deliveries, nil
}

func (r *CommercialAdmissionZoneRelayRepo) Release(
	ctx context.Context,
	delivery storageEntity.CommercialAdmissionZoneDelivery,
	lastError string,
) error {
	_, err := r.db.Exec(ctx, fmt.Sprintf(`
		UPDATE %s.commercial_admission_zone_outbox
		SET retry_count=retry_count+1, last_error=$1,
		    claim_token=NULL, claimed_at=NULL, updated_at=NOW()
		WHERE resource_id=$2 AND zone_id=$3 AND policy_version=$4 AND claim_token=$5`, r.schema),
		lastError, delivery.ResourceID, delivery.ZoneID,
		delivery.PolicyVersion, delivery.ClaimToken)
	return err
}

func (r *CommercialAdmissionZoneRelayRepo) MarkPublished(
	ctx context.Context,
	delivery storageEntity.CommercialAdmissionZoneDelivery,
) error {
	_, err := r.db.Exec(ctx, fmt.Sprintf(`
		UPDATE %s.commercial_admission_zone_outbox
		SET published_at=NOW(), claim_token=NULL, claimed_at=NULL,
		    last_error=NULL, updated_at=NOW()
		WHERE resource_id=$1 AND zone_id=$2 AND policy_version=$3 AND claim_token=$4`, r.schema),
		delivery.ResourceID, delivery.ZoneID, delivery.PolicyVersion, delivery.ClaimToken)
	return err
}
