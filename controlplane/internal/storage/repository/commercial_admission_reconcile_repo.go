package storageRepoImpl

import (
	"context"
	"errors"
	"fmt"
	"time"

	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type StorageCommercialAdmissionReconcileRepo struct {
	db                 *pgxpool.Pool
	schema             string
	zonePayloadEncoder storageRepoInterface.CommercialAdmissionZonePayloadEncoder
}

func NewStorageCommercialAdmissionReconcileRepo(db *pgxpool.Pool, schema string, encoder storageRepoInterface.CommercialAdmissionZonePayloadEncoder) storageRepoInterface.CommercialAdmissionReconcileRepository {
	return &StorageCommercialAdmissionReconcileRepo{db: db, schema: schema, zonePayloadEncoder: encoder}
}

type admissionResourceCandidate struct {
	resourceID    uuid.UUID
	resourceName  string
	zoneID        uuid.UUID
	ownerID       uuid.UUID
	ownerType     string
	policyVersion int64
	decision      string
	restriction   string
	effectiveAt   time.Time
	validUntil    *time.Time
	sourceEventID uuid.UUID
}

func (r *StorageCommercialAdmissionReconcileRepo) ReconcileBatch(ctx context.Context, limit int) (int, error) {
	ownerTable := r.schema + ".commercial_admission_projection"
	resourceTable := r.schema + ".resource_admission_projection"
	zoneOutboxTable := r.schema + ".commercial_admission_zone_outbox"
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		WITH candidates AS (
		SELECT b.id, b.name, b.zone_id, w.owner_id, 'PERSONAL' AS owner_type,
		       w.policy_version, w.decision, COALESCE(w.restriction_reason, '') AS restriction_reason,
		       w.effective_at, w.valid_until, w.source_event_id
		FROM %s.personal_buckets b
		JOIN hierarchy.personal_workspaces pw ON pw.id = b.workspace_id
		JOIN %s w ON w.owner_id = pw.owner_id AND w.owner_type = 'PERSONAL'
		LEFT JOIN %s r ON r.resource_id = b.id AND r.zone_id = b.zone_id
		WHERE r.resource_id IS NULL OR r.policy_version < w.policy_version
		   OR r.owner_id IS DISTINCT FROM w.owner_id OR r.owner_type IS DISTINCT FROM w.owner_type
		UNION ALL
		SELECT b.id, b.name, b.zone_id, w.owner_id, 'TENANT' AS owner_type,
		       w.policy_version, w.decision, COALESCE(w.restriction_reason, '') AS restriction_reason,
		       w.effective_at, w.valid_until, w.source_event_id
		FROM %s.tenant_buckets b
		JOIN hierarchy.tenant_workspaces tw ON tw.id = b.workspace_id
		JOIN %s w ON w.owner_id = tw.tenant_id AND w.owner_type = 'TENANT'
		LEFT JOIN %s r ON r.resource_id = b.id AND r.zone_id = b.zone_id
			WHERE r.resource_id IS NULL OR r.policy_version < w.policy_version
			   OR r.owner_id IS DISTINCT FROM w.owner_id OR r.owner_type IS DISTINCT FROM w.owner_type
		)
		SELECT id, name, zone_id, owner_id, owner_type, policy_version, decision,
		       restriction_reason, effective_at, valid_until, source_event_id
		FROM candidates
		ORDER BY policy_version, id
			LIMIT $1
		`, r.schema, ownerTable, resourceTable, r.schema, ownerTable, resourceTable), limit)
	if err != nil {
		return 0, fmt.Errorf("list storage admission reconciliation candidates: %w", err)
	}
	defer rows.Close()

	candidates := make([]admissionResourceCandidate, 0)
	for rows.Next() {
		var candidate admissionResourceCandidate
		if err := rows.Scan(
			&candidate.resourceID, &candidate.resourceName, &candidate.zoneID,
			&candidate.ownerID, &candidate.ownerType, &candidate.policyVersion,
			&candidate.decision, &candidate.restriction, &candidate.effectiveAt,
			&candidate.validUntil, &candidate.sourceEventID,
		); err != nil {
			return 0, fmt.Errorf("scan storage admission reconciliation candidate: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate storage admission reconciliation candidates: %w", err)
	}
	if len(candidates) == 0 {
		return 0, nil
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin storage admission reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, candidate := range candidates {
		var restrictionReason *string
		var restrictionValue any
		if candidate.restriction != "" {
			restrictionReason = &candidate.restriction
			restrictionValue = candidate.restriction
		}
		var applied uuid.UUID
		err := tx.QueryRow(ctx, fmt.Sprintf(`
			WITH target AS (
				SELECT b.id
				FROM %s.personal_buckets b
				JOIN hierarchy.personal_workspaces workspace ON workspace.id=b.workspace_id
				WHERE $5='PERSONAL' AND b.id=$1 AND b.zone_id=$3 AND workspace.owner_id=$4
				UNION ALL
				SELECT b.id
				FROM %s.tenant_buckets b
				JOIN hierarchy.tenant_workspaces workspace ON workspace.id=b.workspace_id
				WHERE $5='TENANT' AND b.id=$1 AND b.zone_id=$3 AND workspace.tenant_id=$4
			)
			INSERT INTO %s AS current
				(resource_id, resource_name, zone_id, owner_id, owner_type, policy_version,
				 decision, restriction_reason, effective_at, valid_until, source_event_id, updated_at)
			SELECT target.id,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NOW()
			FROM target
			ON CONFLICT (resource_id, zone_id) DO UPDATE SET
				resource_name=EXCLUDED.resource_name, owner_id=EXCLUDED.owner_id,
				owner_type=EXCLUDED.owner_type, policy_version=EXCLUDED.policy_version,
				decision=EXCLUDED.decision, restriction_reason=EXCLUDED.restriction_reason,
				effective_at=EXCLUDED.effective_at, valid_until=EXCLUDED.valid_until,
				source_event_id=EXCLUDED.source_event_id, updated_at=NOW()
			WHERE EXCLUDED.policy_version > current.policy_version
			   OR current.owner_id IS DISTINCT FROM EXCLUDED.owner_id
			RETURNING resource_id`, r.schema, r.schema, resourceTable),
			candidate.resourceID, candidate.resourceName, candidate.zoneID, candidate.ownerID,
			candidate.ownerType, candidate.policyVersion, candidate.decision,
			restrictionValue, candidate.effectiveAt, candidate.validUntil,
			candidate.sourceEventID).Scan(&applied)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("upsert storage admission reconciliation resource: %w", err)
		}
		payload, err := r.zonePayloadEncoder.Encode(&storageEntity.CommercialAdmissionZoneProjection{
			EventID: candidate.sourceEventID, OwnerID: candidate.ownerID,
			OwnerType: candidate.ownerType, PolicyVersion: candidate.policyVersion,
			Decision:          candidate.decision,
			RestrictionReason: restrictionReason,
			EffectiveAt:       candidate.effectiveAt, ValidUntil: candidate.validUntil,
			ResourceID: candidate.resourceID, ResourceName: candidate.resourceName,
			ZoneID: candidate.zoneID,
		})
		if err != nil {
			return 0, fmt.Errorf("marshal reconciled Zone commercial admission event: %w", err)
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
			candidate.resourceID, candidate.zoneID, candidate.sourceEventID,
			candidate.policyVersion, payload); err != nil {
			return 0, fmt.Errorf("append reconciled Zone commercial admission outbox: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit storage admission reconciliation: %w", err)
	}
	return len(candidates), nil
}
