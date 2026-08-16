package storageRepoImpl

import (
	"context"
	"fmt"
	"time"

	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type StorageCommercialAdmissionProjectionRepo struct {
	db                 *pgxpool.Pool
	schema             string
	zonePayloadEncoder storageRepoInterface.CommercialAdmissionZonePayloadEncoder
}

func NewStorageCommercialAdmissionProjectionRepo(
	db *pgxpool.Pool,
	schema string,
	zonePayloadEncoder storageRepoInterface.CommercialAdmissionZonePayloadEncoder,
) storageRepoInterface.CommercialAdmissionProjectionRepository {
	return &StorageCommercialAdmissionProjectionRepo{
		db: db, schema: schema, zonePayloadEncoder: zonePayloadEncoder,
	}
}

func (r *StorageCommercialAdmissionProjectionRepo) Apply(
	ctx context.Context,
	projection *storageEntity.CommercialAdmissionProjection,
) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ownerTable := r.schema + ".commercial_admission_projection"
	resourceTable := r.schema + ".resource_admission_projection"
	zoneOutboxTable := r.schema + ".commercial_admission_zone_outbox"
	_, err = tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s AS current (owner_id, owner_type, policy_version, decision, restriction_reason, effective_at, valid_until, source_event_id, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NOW())
		ON CONFLICT (owner_id, owner_type) DO UPDATE SET
			policy_version=EXCLUDED.policy_version, decision=EXCLUDED.decision,
			restriction_reason=EXCLUDED.restriction_reason, effective_at=EXCLUDED.effective_at,
			valid_until=EXCLUDED.valid_until, source_event_id=EXCLUDED.source_event_id, updated_at=NOW()
		WHERE EXCLUDED.policy_version > current.policy_version`, ownerTable),
		projection.OwnerID, projection.OwnerType, projection.PolicyVersion,
		projection.Decision, projection.RestrictionReason, projection.EffectiveAt,
		projection.ValidUntil, projection.EventID)
	if err != nil {
		return fmt.Errorf("apply commercial admission owner projection: %w", err)
	}

	var currentOwner struct {
		walletVersion int64
		admissionMode string
		restriction   *string
		effectiveAt   time.Time
		validUntil    *time.Time
		sourceEventID uuid.UUID
	}
	if err := tx.QueryRow(ctx, fmt.Sprintf(`
		SELECT policy_version, decision, restriction_reason, effective_at,
		       valid_until, source_event_id
		FROM %s
		WHERE owner_id=$1 AND owner_type=$2`, ownerTable), projection.OwnerID, projection.OwnerType).Scan(
		&currentOwner.walletVersion, &currentOwner.admissionMode, &currentOwner.restriction,
		&currentOwner.effectiveAt, &currentOwner.validUntil, &currentOwner.sourceEventID,
	); err != nil {
		return fmt.Errorf("read fenced commercial admission owner projection: %w", err)
	}

	targetRows, err := tx.Query(ctx, fmt.Sprintf(`
		WITH owned_resources AS (
			SELECT bucket.id AS resource_id, bucket.name AS resource_name, bucket.zone_id
			FROM %s.personal_buckets bucket
			JOIN hierarchy.personal_workspaces workspace ON workspace.id=bucket.workspace_id
			WHERE $2='PERSONAL' AND workspace.owner_id=$1
			UNION ALL
			SELECT bucket.id AS resource_id, bucket.name AS resource_name, bucket.zone_id
			FROM %s.tenant_buckets bucket
			JOIN hierarchy.tenant_workspaces workspace ON workspace.id=bucket.workspace_id
			WHERE $2='TENANT' AND workspace.tenant_id=$1
		)
		SELECT resource_id, resource_name, zone_id
		FROM owned_resources
		ORDER BY zone_id, resource_id`, r.schema, r.schema), projection.OwnerID, projection.OwnerType)
	if err != nil {
		return fmt.Errorf("resolve Storage-owned admission resources: %w", err)
	}
	type ownedResource struct {
		resourceID   uuid.UUID
		resourceName string
		zoneID       uuid.UUID
	}
	targets := make([]ownedResource, 0)
	for targetRows.Next() {
		var target ownedResource
		if err := targetRows.Scan(&target.resourceID, &target.resourceName, &target.zoneID); err != nil {
			targetRows.Close()
			return fmt.Errorf("scan Storage-owned admission resource: %w", err)
		}
		targets = append(targets, target)
	}
	if err := targetRows.Err(); err != nil {
		targetRows.Close()
		return fmt.Errorf("iterate Storage-owned admission resources: %w", err)
	}
	targetRows.Close()

	for _, target := range targets {
		applied, err := tx.Exec(ctx, fmt.Sprintf(`
			INSERT INTO %s AS current (resource_id, resource_name, zone_id, owner_id, owner_type, policy_version, decision, restriction_reason, effective_at, valid_until, source_event_id, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NOW())
			ON CONFLICT (resource_id, zone_id) DO UPDATE SET
				resource_name=EXCLUDED.resource_name, owner_id=EXCLUDED.owner_id, owner_type=EXCLUDED.owner_type,
				policy_version=EXCLUDED.policy_version, decision=EXCLUDED.decision,
				restriction_reason=EXCLUDED.restriction_reason, effective_at=EXCLUDED.effective_at,
				valid_until=EXCLUDED.valid_until, source_event_id=EXCLUDED.source_event_id, updated_at=NOW()
				WHERE EXCLUDED.policy_version > current.policy_version
				   OR current.owner_id IS DISTINCT FROM EXCLUDED.owner_id
				   OR current.owner_type IS DISTINCT FROM EXCLUDED.owner_type`, resourceTable),
			target.resourceID, target.resourceName, target.zoneID, projection.OwnerID,
			projection.OwnerType, currentOwner.walletVersion, currentOwner.admissionMode,
			currentOwner.restriction, currentOwner.effectiveAt, currentOwner.validUntil,
			currentOwner.sourceEventID)
		if err != nil {
			return fmt.Errorf("apply storage admission target projection: %w", err)
		}
		if applied.RowsAffected() == 0 {
			continue
		}
		payload, marshalErr := r.zonePayloadEncoder.Encode(&storageEntity.CommercialAdmissionZoneProjection{
			EventID: currentOwner.sourceEventID,
			OwnerID: projection.OwnerID, OwnerType: projection.OwnerType,
			PolicyVersion: currentOwner.walletVersion, Decision: currentOwner.admissionMode,
			RestrictionReason: currentOwner.restriction, EffectiveAt: currentOwner.effectiveAt,
			ValidUntil: currentOwner.validUntil,
			ResourceID: target.resourceID, ResourceName: target.resourceName, ZoneID: target.zoneID,
		})
		if marshalErr != nil {
			return fmt.Errorf("marshal Zone commercial admission outbox event: %w", marshalErr)
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`
			INSERT INTO %s AS current
				(resource_id, zone_id, source_event_id, policy_version, payload)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (resource_id, zone_id) DO UPDATE SET
				source_event_id=EXCLUDED.source_event_id,
				policy_version=EXCLUDED.policy_version,
				payload=EXCLUDED.payload,
				claim_token=NULL, claimed_at=NULL, published_at=NULL,
				last_error=NULL, updated_at=NOW()
			WHERE EXCLUDED.policy_version > current.policy_version
			   OR EXCLUDED.source_event_id <> current.source_event_id`, zoneOutboxTable),
			target.resourceID, target.zoneID, currentOwner.sourceEventID,
			currentOwner.walletVersion, payload); err != nil {
			return fmt.Errorf("append Zone commercial admission outbox: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return nil
}
