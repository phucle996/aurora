/*
============================================================================
MAP: BILLING REPOSITORY IMPLEMENTATION - LIFECYCLE REPOSITORY
============================================================================
CONTRACT:
1. Thực thi các câu lệnh PostgreSQL nguyên tử cho Inbox, Advisory Lock, Ownership Projection và Head Version.
2. Bảo toàn dữ liệu lịch sử ledger và chống đụng độ out-of-order delivery.
============================================================================
*/

package repository

import (
	"context"
	"errors"
	"fmt"
	"log"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingTaxonomy "cost-manager/api/internal/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type resourceOwnershipRepository struct {
	db *pgxpool.Pool
}

// NewResourceOwnershipRepository khởi tạo instance repository thực thi SQL cho sự kiện vòng đời.
func NewResourceOwnershipRepository(db *pgxpool.Pool) billingRepoInterface.ResourceOwnershipRepository {
	return &resourceOwnershipRepository{db: db}
}

// ApplyResourceOwnershipEvent thực thi transaction nguyên tử gồm: Inbox Idempotency, Advisory Lock, Out-of-order Head Check và Projection Upsert.
func (r *resourceOwnershipRepository) ApplyResourceOwnershipEvent(ctx context.Context, event *entity.ResourceOwnershipEvent) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("lifecycle repo: begin tx failed: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Inbox Idempotency Check: Thử INSERT vào ownership_event_inbox
	var dummy int
	err = tx.QueryRow(ctx,
		`INSERT INTO billing.ownership_event_inbox 
			(event_id, event_type, schema_version, payload_hash, resource_id, source_version, status)
		 VALUES ($1, $2, 1, $3, $4, $5, 'RECEIVED')
		 ON CONFLICT (event_id) DO NOTHING
		 RETURNING 1`,
		event.EventID, event.EventType, event.PayloadHashHex, event.ResourceID, event.SourceVersion,
	).Scan(&dummy)

	if errors.Is(err, pgx.ErrNoRows) {
		// Cùng event_id nhưng payload khác là collision/corruption, không phải retry hợp lệ.
		var storedHash string
		if hashErr := tx.QueryRow(ctx,
			`SELECT payload_hash FROM billing.ownership_event_inbox WHERE event_id = $1`,
			event.EventID,
		).Scan(&storedHash); hashErr != nil {
			return fmt.Errorf("lifecycle repo: read duplicate inbox failed: %w", hashErr)
		}
		if storedHash != event.PayloadHashHex {
			return fmt.Errorf(
				"%w: event_id %s reused with a different payload",
				billingTaxonomy.ErrResourceOwnershipIntegrity,
				event.EventID,
			)
		}
		// Conflict + cùng hash -> retry hợp lệ, có thể ACK idempotently.
		log.Printf("[ResourceOwnershipRepo] Event %s đã tồn tại trong inbox. Bỏ qua xử lý trùng lặp.", event.EventID)
		return nil
	} else if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) &&
			pgErr.Code == "23505" &&
			pgErr.ConstraintName == "uq_ownership_inbox_resource_version" {
			return fmt.Errorf(
				"%w: resource %s source version %d is already bound to another event",
				billingTaxonomy.ErrResourceOwnershipIntegrity,
				event.ResourceID,
				event.SourceVersion,
			)
		}
		return fmt.Errorf("lifecycle repo: insert inbox failed: %w", err)
	}

	// 2. Advisory lock theo resource_id để chống race condition out-of-order delivery giữa các replicas
	var lockAcquired bool
	err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1, 0))`, event.ResourceID.String()).Scan(&lockAcquired)
	if err != nil || !lockAcquired {
		return fmt.Errorf("lifecycle repo: could not acquire advisory lock for resource %s", event.ResourceID)
	}

	// 3. Kiểm tra ownership head version hiện tại.
	var lastVersion int64
	var currentState string
	err = tx.QueryRow(ctx,
		`SELECT last_source_version, resource_state FROM billing.resource_ownership_head WHERE resource_id = $1`,
		event.ResourceID,
	).Scan(&lastVersion, &currentState)

	if err == nil {
		// A late older event cannot overwrite the current head.
		if event.SourceVersion <= lastVersion {
			log.Printf("[ResourceOwnershipRepo] Bỏ qua out-of-order event. Resource %s current head version=%d, event version=%d", event.ResourceID, lastVersion, event.SourceVersion)
			// Cập nhật status inbox sang APPLIED
			_, _ = tx.Exec(ctx, `UPDATE billing.ownership_event_inbox SET status = 'APPLIED', processed_at = NOW() WHERE event_id = $1`, event.EventID)
			return tx.Commit(ctx)
		}
		// Redis consumer groups distribute adjacent entries across replicas.
		// Reject a gap so DELETE cannot overtake CREATE and erase ownership history.
		if event.SourceVersion != lastVersion+1 {
			return fmt.Errorf(
				"lifecycle repo: source version gap for resource %s: head=%d event=%d",
				event.ResourceID,
				lastVersion,
				event.SourceVersion,
			)
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("lifecycle repo: query lifecycle head failed: %w", err)
	} else if event.SourceVersion != 1 {
		return fmt.Errorf(
			"lifecycle repo: initial source version for resource %s must be 1, got %d",
			event.ResourceID,
			event.SourceVersion,
		)
	}

	// 4. Xử lý cập nhật Projection theo loại Event
	switch event.EventType {
	case entity.ResourceOwnershipEventCreated:
		projID := uuid.New()
		_, err = tx.Exec(ctx, `
			INSERT INTO billing.resource_ownership_projection
				(id, resource_type, resource_id, resource_name, owner_id, owner_type, zone_id, ownership_version, effective_from, source_updated_at)
			VALUES ($1, $2, $3, $4, $5, $6::billing.owner_type, $7, $8, $9, NOW())
			ON CONFLICT (id) DO NOTHING
		`, projID, event.ResourceType, event.ResourceID, event.ResourceName, event.OwnerID, event.OwnerType, event.ZoneID, event.SourceVersion, event.EffectiveAt)
		if err != nil {
			return fmt.Errorf("lifecycle repo: insert ownership projection failed: %w", err)
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO billing.resource_ownership_head (resource_id, last_source_version, resource_state, updated_at)
			VALUES ($1, $2, 'ACTIVE', NOW())
			ON CONFLICT (resource_id) DO UPDATE
			SET last_source_version = EXCLUDED.last_source_version, resource_state = 'ACTIVE', updated_at = NOW()
		`, event.ResourceID, event.SourceVersion)
		if err != nil {
			return fmt.Errorf("lifecycle repo: upsert lifecycle head ACTIVE failed: %w", err)
		}

	case entity.ResourceOwnershipEventDeleted:
		_, err = tx.Exec(ctx, `
			UPDATE billing.resource_ownership_projection
			SET effective_to = $1
			WHERE resource_id = $2 AND effective_to IS NULL
		`, event.EffectiveAt, event.ResourceID)
		if err != nil {
			return fmt.Errorf("lifecycle repo: close ownership projection failed: %w", err)
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO billing.resource_ownership_head (resource_id, last_source_version, resource_state, updated_at)
			VALUES ($1, $2, 'DELETED', NOW())
			ON CONFLICT (resource_id) DO UPDATE
			SET last_source_version = EXCLUDED.last_source_version, resource_state = 'DELETED', updated_at = NOW()
		`, event.ResourceID, event.SourceVersion)
		if err != nil {
			return fmt.Errorf("lifecycle repo: upsert lifecycle head DELETED failed: %w", err)
		}
	default:
		return fmt.Errorf("lifecycle repo: unsupported event type %q", event.EventType)
	}

	// 5. Đánh dấu status inbox sang APPLIED
	_, err = tx.Exec(ctx, `UPDATE billing.ownership_event_inbox SET status = 'APPLIED', processed_at = NOW() WHERE event_id = $1`, event.EventID)
	if err != nil {
		return fmt.Errorf("lifecycle repo: update inbox status failed: %w", err)
	}

	return tx.Commit(ctx)
}
