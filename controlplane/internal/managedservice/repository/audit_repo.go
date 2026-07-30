package repository

import (
	"context"
	"controlplane/internal/managedservice/domain/entity"
	managedrepo "controlplane/internal/managedservice/domain/repo"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type auditRepository struct {
	db     *pgxpool.Pool
	schema string
}

func NewAuditRepository(db *pgxpool.Pool, schema string) managedrepo.AuditRepository {
	return &auditRepository{db: db, schema: schema}
}
func (r *auditRepository) ListAuditEvents(ctx context.Context, in *entity.ListAuditEvents) ([]entity.AuditEventView, error) {
	query := fmt.Sprintf(`SELECT id,actor_subject,critical_proof_id,action,record_kind,record_id,record_version,outcome,error_code,occurred_at FROM %s.catalog_audit_events ORDER BY occurred_at DESC,id DESC LIMIT $1`, r.schema)
	rows, err := r.db.Query(ctx, query, in.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]entity.AuditEventView, 0)
	for rows.Next() {
		var item entity.AuditEventView
		if err := rows.Scan(&item.ID, &item.ActorSubject, &item.CriticalProofID, &item.Action, &item.RecordKind, &item.RecordID, &item.RecordVersion, &item.Outcome, &item.ErrorCode, &item.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
