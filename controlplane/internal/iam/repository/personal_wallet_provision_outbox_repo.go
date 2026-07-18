package iamRepoImpl

import (
	"context"
	"fmt"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"

	"github.com/jackc/pgx/v5/pgxpool"
)

type personalWalletProvisionOutboxRepository struct {
	db     *pgxpool.Pool
	schema string
}

func NewPersonalWalletProvisionOutboxRepository(db *pgxpool.Pool, cfg *config.Config) iamRepoInterface.PersonalWalletProvisionOutboxRepository {
	return &personalWalletProvisionOutboxRepository{db: db, schema: cfg.SchemaSQL.IAM}
}

// [COMMENT]: SKIP LOCKED + lease cho phép nhiều pod relay mà không publish cùng claim bình thường.
func (r *personalWalletProvisionOutboxRepository) Claim(ctx context.Context, limit int) ([]iamEntity.PersonalWalletProvisionEvent, error) {
	query := fmt.Sprintf(`
		WITH candidates AS (
			SELECT id FROM %s.billing_wallet_provision_outbox
			WHERE (status = 'PENDING' AND available_at <= NOW())
			   OR (status = 'PUBLISHING' AND lease_until < NOW())
			ORDER BY available_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE %s.billing_wallet_provision_outbox o
		SET status = 'PUBLISHING', lease_until = NOW() + INTERVAL '30 seconds',
			attempts = attempts + 1, updated_at = NOW()
		FROM candidates c WHERE o.id = c.id
		RETURNING o.id, o.event_id, o.payload, o.attempts
	`, r.schema, r.schema)
	rows, err := r.db.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("iam account outbox: claim: %w", err)
	}
	defer rows.Close()

	events := make([]iamEntity.PersonalWalletProvisionEvent, 0, limit)
	for rows.Next() {
		var event iamEntity.PersonalWalletProvisionEvent
		if err := rows.Scan(&event.ID, &event.EventID, &event.Payload, &event.Attempts); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (r *personalWalletProvisionOutboxRepository) MarkPublished(ctx context.Context, id int64) error {
	query := fmt.Sprintf(`UPDATE %s.billing_wallet_provision_outbox SET status='PUBLISHED', published_at=NOW(), lease_until=NULL, last_error=NULL, updated_at=NOW() WHERE id=$1 AND status='PUBLISHING'`, r.schema)
	_, err := r.db.Exec(ctx, query, id)
	return err
}

func (r *personalWalletProvisionOutboxRepository) MarkFailed(ctx context.Context, id int64, message string) error {
	query := fmt.Sprintf(`
		UPDATE %s.billing_wallet_provision_outbox
		SET status = CASE WHEN attempts >= 25 THEN 'DEAD' ELSE 'PENDING' END,
			available_at = NOW() + (LEAST(300, POWER(2, LEAST(attempts, 8))) * INTERVAL '1 second'),
			lease_until=NULL, last_error=LEFT($2, 2000), updated_at=NOW()
		WHERE id=$1 AND status='PUBLISHING'
	`, r.schema)
	_, err := r.db.Exec(ctx, query, id, message)
	return err
}

func (r *personalWalletProvisionOutboxRepository) CleanupPublished(ctx context.Context, limit int) (int64, error) {
	query := fmt.Sprintf(`
		WITH expired AS (
			SELECT id FROM %s.billing_wallet_provision_outbox
			WHERE status='PUBLISHED' AND published_at < NOW() - INTERVAL '30 days'
			ORDER BY id LIMIT $1
		)
		DELETE FROM %s.billing_wallet_provision_outbox o USING expired e WHERE o.id=e.id
	`, r.schema, r.schema)
	result, err := r.db.Exec(ctx, query, limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}
