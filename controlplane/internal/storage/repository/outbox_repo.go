package storageRepoImpl

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageModel "controlplane/internal/storage/model"
)

// [COMMENT]: StorageOutboxRepositoryImpl thực thi interface StorageOutboxRepository kết nối PostgreSQL.
type StorageOutboxRepositoryImpl struct {
	db     *pgxpool.Pool
	schema string
}

// [COMMENT]: NewStorageOutboxRepository khởi tạo repository quản lý outbox của module Storage.
func NewStorageOutboxRepository(db *pgxpool.Pool, schema string) storageRepoInterface.StorageOutboxRepository {
	return &StorageOutboxRepositoryImpl{
		db:     db,
		schema: schema,
	}
}

func (r *StorageOutboxRepositoryImpl) Create(ctx context.Context, record *storageEntity.StorageOutboxRecord) error {
	m := storageModel.OutboxEntityToModel(record)
	query := fmt.Sprintf(`
		INSERT INTO %s.storage_outbox_records (
			event_id, routing_scope, job_topic, payload, user_id, status, created_at, updated_at,
			job_version, resource_id, payload_schema_version, trace_id, idle
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id
	`, r.schema)

	now := time.Now()
	err := r.db.QueryRow(ctx, query,
		m.EventID,
		m.RoutingScope,
		m.JobTopic,
		m.Payload,
		m.UserID,
		m.Status,
		now,
		now,
		m.JobVersion,
		m.ResourceID,
		m.PayloadSchemaVersion,
		m.TraceID,
		m.Idle,
	).Scan(&record.ID)
	if err != nil {
		return fmt.Errorf("storage repo: failed to insert outbox record: %w", err)
	}
	return nil
}
