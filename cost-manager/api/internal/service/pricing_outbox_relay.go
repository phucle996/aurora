package service

import (
	"context"
	"cost-manager/api/internal/domain/entity"
	"cost-manager/api/internal/transport/proto/pricingproto"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

const pricingVersionPublishedSubject = "billing.pricing.tier_version.published"

// PricingOutboxRelay phát committed outbox rows; duplicate delivery được phép và Engine xử lý idempotent.
type PricingOutboxRelay struct {
	db   *pgxpool.Pool
	nats *nats.Conn
}

func NewPricingOutboxRelay(db *pgxpool.Pool, natsConn *nats.Conn) *PricingOutboxRelay {
	return &PricingOutboxRelay{db: db, nats: natsConn}
}

// Run poll theo batch nhỏ để nhiều API replica chia việc bằng SKIP LOCKED mà không publish trùng chủ động.
func (r *PricingOutboxRelay) Run(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := r.publishBatch(ctx); err != nil && ctx.Err() == nil {
			// Relay retry ở tick sau; outbox row chưa được đánh dấu published nên không mất event.
			fmt.Printf("Pricing outbox relay error: %v\n", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type pricingOutboxRow struct {
	ID            uuid.UUID
	TierID        uuid.UUID
	TierVersionID uuid.UUID
	VersionNumber int32
	ServiceType   entity.ServiceType
	EffectiveFrom time.Time
	Checksum      string
	OccurredAt    time.Time
}

func (r *PricingOutboxRelay) publishBatch(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin outbox batch: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	// Status là projection theo effective window; pricing content vẫn immutable và Engine không phụ thuộc status chuyển tiếp này.
	if _, err = tx.Exec(ctx, `
		WITH projected AS (
			SELECT id, CASE
				WHEN effective_to IS NOT NULL AND effective_to <= NOW() THEN 'SUPERSEDED'
				WHEN effective_from <= NOW() AND (effective_to IS NULL OR NOW() < effective_to) THEN 'ACTIVE'
				ELSE 'SCHEDULED'
			END AS desired_status
			FROM billing.tier_versions
			WHERE status <> 'CANCELLED'
		)
		UPDATE billing.tier_versions version
		SET status = projected.desired_status
		FROM projected
		WHERE version.id = projected.id AND version.status IS DISTINCT FROM projected.desired_status
	`); err != nil {
		return fmt.Errorf("refresh pricing version statuses: %w", err)
	}

	rows, err := tx.Query(ctx, `
		SELECT id, tier_id, tier_version_id, version_number, service_type, effective_from, checksum, occurred_at
		FROM billing.pricing_outbox
		WHERE published_at IS NULL
		ORDER BY occurred_at, id
		FOR UPDATE SKIP LOCKED
		LIMIT 100
	`)
	if err != nil {
		return fmt.Errorf("select outbox rows: %w", err)
	}
	batch := make([]pricingOutboxRow, 0, 100)
	for rows.Next() {
		var row pricingOutboxRow
		var rawServiceType string
		if err = rows.Scan(&row.ID, &row.TierID, &row.TierVersionID, &row.VersionNumber, &rawServiceType,
			&row.EffectiveFrom, &row.Checksum, &row.OccurredAt); err != nil {
			rows.Close()
			return fmt.Errorf("scan outbox row: %w", err)
		}
		row.ServiceType = entity.ServiceType(rawServiceType)
		batch = append(batch, row)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate outbox rows: %w", err)
	}
	rows.Close()

	for _, row := range batch {
		payload, marshalErr := proto.Marshal(&pricingproto.TierVersionPublished{
			EventId: row.ID.String(), TierId: row.TierID.String(), TierVersionId: row.TierVersionID.String(),
			VersionNumber: row.VersionNumber, ServiceType: string(row.ServiceType),
			EffectiveFromUnixMs: row.EffectiveFrom.UnixMilli(), Checksum: row.Checksum, OccurredAtUnixMs: row.OccurredAt.UnixMilli(),
		})
		if marshalErr != nil {
			return fmt.Errorf("marshal outbox event %s: %w", row.ID, marshalErr)
		}
		if err = r.nats.Publish(pricingVersionPublishedSubject, payload); err != nil {
			_, _ = tx.Exec(ctx, `UPDATE billing.pricing_outbox SET retry_count = retry_count + 1, last_error = $1 WHERE id = $2`, err.Error(), row.ID)
			_ = tx.Commit(ctx)
			return fmt.Errorf("publish outbox event %s: %w", row.ID, err)
		}
		if _, err = tx.Exec(ctx, `UPDATE billing.pricing_outbox SET published_at = NOW(), last_error = NULL WHERE id = $1`, row.ID); err != nil {
			return fmt.Errorf("mark outbox event %s published: %w", row.ID, err)
		}
	}
	if len(batch) > 0 {
		flushCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err = r.nats.FlushWithContext(flushCtx); err != nil {
			return fmt.Errorf("flush pricing events: %w", err)
		}
	}
	return tx.Commit(ctx)
}
