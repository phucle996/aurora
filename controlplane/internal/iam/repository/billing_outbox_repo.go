package iamRepoImpl

import (
	"context"
	"fmt"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"

	"github.com/jackc/pgx/v5/pgxpool"
)

type billingOutboxRepository struct {
	db     *pgxpool.Pool
	schema string
}

func NewBillingOutboxRepository(db *pgxpool.Pool, cfg *config.Config) iamRepoInterface.BillingOutboxRepository {
	return &billingOutboxRepository{db: db, schema: cfg.SchemaSQL.IAM}
}

// [COMMENT]: Lease + SKIP LOCKED cho phép scale ngang relay mà không giữ transaction trong lúc chờ PubAck.
func (r *billingOutboxRepository) Claim(ctx context.Context, limit int) ([]iamEntity.BillingOutboxEvent, error) {
	query := fmt.Sprintf(`
		WITH candidates AS (
			SELECT id FROM %s.billing_outbox_records
			WHERE (status = 'PENDING' AND available_at <= NOW())
			   OR (status = 'PUBLISHING' AND lease_until < NOW())
			ORDER BY available_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE %s.billing_outbox_records o
		SET status = 'PUBLISHING', lease_until = NOW() + INTERVAL '30 seconds',
			attempts = attempts + 1, updated_at = NOW()
		FROM candidates c WHERE o.id = c.id
		RETURNING o.id, o.event_id, o.event_type, o.owner_id, o.owner_type::text, o.payload, o.attempts
	`, r.schema, r.schema)
	rows, err := r.db.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("iam billing outbox: claim: %w", err)
	}
	defer rows.Close()

	events := make([]iamEntity.BillingOutboxEvent, 0, limit)
	for rows.Next() {
		var event iamEntity.BillingOutboxEvent
		if err := rows.Scan(&event.ID, &event.EventID, &event.EventType, &event.OwnerID, &event.OwnerType, &event.Payload, &event.Attempts); err != nil {
			return nil, fmt.Errorf("iam billing outbox: scan claim: %w", err)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (r *billingOutboxRepository) MarkPublished(ctx context.Context, id int64) error {
	query := fmt.Sprintf(`UPDATE %s.billing_outbox_records SET status='PUBLISHED', published_at=NOW(), lease_until=NULL, last_error=NULL, updated_at=NOW() WHERE id=$1 AND status='PUBLISHING'`, r.schema)
	_, err := r.db.Exec(ctx, query, id)
	return err
}

func (r *billingOutboxRepository) MarkFailed(ctx context.Context, id int64, message string) error {
	query := fmt.Sprintf(`
		UPDATE %s.billing_outbox_records
		SET status = CASE WHEN attempts >= 25 THEN 'DEAD' ELSE 'PENDING' END,
			available_at = NOW() + (LEAST(300, POWER(2, LEAST(attempts, 8))) * INTERVAL '1 second'),
			lease_until=NULL, last_error=LEFT($2, 2000), updated_at=NOW()
		WHERE id=$1 AND status='PUBLISHING'
	`, r.schema)
	_, err := r.db.Exec(ctx, query, id, message)
	return err
}

func (r *billingOutboxRepository) MarkDead(ctx context.Context, id int64, message string) error {
	query := fmt.Sprintf(`
		UPDATE %s.billing_outbox_records
		SET status='DEAD', lease_until=NULL, last_error=LEFT($2, 2000), updated_at=NOW()
		WHERE id=$1 AND status='PUBLISHING'
	`, r.schema)
	_, err := r.db.Exec(ctx, query, id, message)
	return err
}
