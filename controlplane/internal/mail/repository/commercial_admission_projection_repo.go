package mailRepoImpl

import (
	"context"
	"fmt"

	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"

	"github.com/jackc/pgx/v5/pgxpool"
)

type MailCommercialAdmissionProjectionRepo struct {
	db     *pgxpool.Pool
	schema string
}

func NewMailCommercialAdmissionProjectionRepo(
	db *pgxpool.Pool,
	schema string,
) mailRepoInterface.CommercialAdmissionProjectionRepository {
	return &MailCommercialAdmissionProjectionRepo{db: db, schema: schema}
}

func (r *MailCommercialAdmissionProjectionRepo) Upsert(
	ctx context.Context,
	projection *mailEntity.CommercialAdmissionProjection,
) error {
	_, err := r.db.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.commercial_admission_projection AS current
		(owner_id,owner_type,policy_version,decision,restriction_reason,effective_at,valid_until,source_event_id,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NOW())
		ON CONFLICT (owner_id,owner_type) DO UPDATE SET
		  policy_version=EXCLUDED.policy_version,decision=EXCLUDED.decision,
		  restriction_reason=EXCLUDED.restriction_reason,effective_at=EXCLUDED.effective_at,
		  valid_until=EXCLUDED.valid_until,source_event_id=EXCLUDED.source_event_id,updated_at=NOW()
		WHERE EXCLUDED.policy_version > current.policy_version
	`, r.schema), projection.OwnerID, projection.OwnerType, projection.PolicyVersion,
		projection.Decision, projection.RestrictionReason, projection.EffectiveAt,
		projection.ValidUntil, projection.EventID)
	return err
}
