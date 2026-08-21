package hypervisorRepoImpl

import (
	"context"
	"fmt"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorRepoInterface "controlplane/internal/hypervisor/domain/repo"

	"github.com/jackc/pgx/v5/pgxpool"
)

type HypervisorCommercialAdmissionProjectionRepo struct {
	db     *pgxpool.Pool
	schema string
}

func NewHypervisorCommercialAdmissionProjectionRepo(
	db *pgxpool.Pool,
	schema string,
) hypervisorRepoInterface.CommercialAdmissionProjectionRepository {
	return &HypervisorCommercialAdmissionProjectionRepo{db: db, schema: schema}
}

func (r *HypervisorCommercialAdmissionProjectionRepo) Upsert(
	ctx context.Context,
	projection *hypervisorEntity.CommercialAdmissionProjection,
) error {
	_, err := r.db.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.commercial_admission_projection AS current
		(owner_id,owner_type,policy_version,decision,restriction_reason,effective_at,valid_until,source_event_id,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NOW())
		ON CONFLICT (owner_id,owner_type) DO UPDATE SET
		  policy_version=EXCLUDED.policy_version, decision=EXCLUDED.decision,
		  restriction_reason=EXCLUDED.restriction_reason, effective_at=EXCLUDED.effective_at,
		  valid_until=EXCLUDED.valid_until, source_event_id=EXCLUDED.source_event_id, updated_at=NOW()
		WHERE EXCLUDED.policy_version > current.policy_version
	`, r.schema), projection.OwnerID, projection.OwnerType, projection.PolicyVersion,
		projection.Decision, projection.RestrictionReason, projection.EffectiveAt,
		projection.ValidUntil, projection.EventID)
	return err
}
