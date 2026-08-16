package storageRepoImpl

import (
	"context"
	"errors"
	"fmt"

	"controlplane/internal/config"
	jobpayload "controlplane/internal/security"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageModel "controlplane/internal/storage/model"
	storageTaxonomy "controlplane/internal/storage/taxonomy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type personalStorageAccessSessionRepository struct {
	db        *pgxpool.Pool
	storage   string
	hierarchy string
	protector jobpayload.Protector
}

func NewPersonalStorageAccessSessionRepository(db *pgxpool.Pool, cfg *config.Config, protector jobpayload.Protector) storageRepoInterface.PersonalStorageAccessSessionRepository {
	return &personalStorageAccessSessionRepository{db: db, storage: cfg.SchemaSQL.Storage, hierarchy: cfg.SchemaSQL.Hierarchy, protector: protector}
}

func (r *personalStorageAccessSessionRepository) GetPersonalStorageAccessSessionTarget(ctx context.Context, resourceID, workspaceID, actorID, zoneID uuid.UUID) (string, error) {
	var bucketName string
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		WITH target AS (
			SELECT bucket.name
			FROM %s.personal_buckets bucket
			JOIN %s.personal_workspaces workspace ON workspace.id=bucket.workspace_id
			WHERE bucket.id=$1 AND bucket.workspace_id=$2 AND workspace.owner_id=$3
			  AND bucket.zone_id=$4 AND workspace.zone_id=$4
		)
		SELECT name FROM target`, r.storage, r.hierarchy), resourceID, workspaceID, actorID, zoneID).Scan(&bucketName)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", storageTaxonomy.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("storage access-session: resolve target: %w", err)
	}
	return bucketName, nil
}

func (r *personalStorageAccessSessionRepository) CreatePersonalStorageAccessSession(ctx context.Context, session *storageEntity.StorageAccessSession, outbox *storageEntity.StorageOutboxRecord) error {
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: outbox.ZoneID, SourceDomain: "STORAGE", JobTopic: outbox.JobTopic, ResourceID: outbox.ResourceID, JobVersion: outbox.JobVersion, PayloadSchemaVersion: outbox.PayloadSchemaVersion}, outbox.Payload)
	if err != nil {
		return err
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID
	row := storageModel.OutboxEntityToModel(outbox)

	result, err := r.db.Exec(ctx, fmt.Sprintf(`
		WITH target AS (
			SELECT bucket.id
			FROM %s.personal_buckets bucket
			JOIN %s.personal_workspaces workspace ON workspace.id=bucket.workspace_id
			WHERE bucket.id=$1 AND bucket.name=$2 AND bucket.workspace_id=$3
			  AND workspace.owner_id=$4 AND bucket.zone_id=$5 AND workspace.zone_id=$5
		)
		INSERT INTO %s.storage_outbox_records (
			event_id, zone_id, job_topic, payload, owner_id, owner_type, status,
			completed_at, job_version, resource_id, payload_schema_version,
			trace_id, idle, error_code, error_message, actor_user_id, payload_key_id
		)
		SELECT $6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22
		FROM target
		ON CONFLICT (event_id) DO NOTHING`, r.storage, r.hierarchy, r.storage),
		session.ResourceID, session.BucketName, session.WorkspaceID, session.ActorID,
		session.ZoneID, row.EventID, row.ZoneID, row.JobTopic, row.Payload, row.OwnerID,
		row.OwnerType, row.Status, row.CompletedAt, row.JobVersion, row.ResourceID,
		row.PayloadSchemaVersion, row.TraceID, row.Idle, row.ErrorCode, row.ErrorMessage,
		row.ActorUserID, row.PayloadKeyID)
	if err != nil {
		return fmt.Errorf("storage access-session: create command: %w", err)
	}
	if result.RowsAffected() == 0 {
		return storageTaxonomy.ErrNotFound
	}
	return nil
}

func (r *personalStorageAccessSessionRepository) GetPersonalStorageAccessSessionStatus(ctx context.Context, accessSessionID, resourceID, workspaceID, actorID, zoneID uuid.UUID) (*storageEntity.StorageAccessSessionStatus, error) {
	var status storageEntity.StorageAccessSessionStatus
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		WITH target AS (
			SELECT bucket.id
			FROM %s.personal_buckets bucket
			JOIN %s.personal_workspaces workspace ON workspace.id=bucket.workspace_id
			WHERE bucket.id=$1 AND bucket.workspace_id=$2 AND workspace.owner_id=$3
			  AND bucket.zone_id=$4 AND workspace.zone_id=$4
		)
		SELECT command.status, command.completed_at, command.error_code
		FROM target
		JOIN %s.storage_outbox_records command ON command.resource_id=target.id::text
		WHERE command.event_id=$5 AND command.job_topic='storage.access.prepare'
		  AND command.owner_type='PERSONAL' AND command.owner_id=$3
		  AND command.actor_user_id=$3 AND command.zone_id=$4`, r.storage, r.hierarchy, r.storage),
		resourceID, workspaceID, actorID, zoneID, accessSessionID).Scan(&status.State, &status.CompletedAt, &status.ErrorCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, storageTaxonomy.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("storage access-session: read status: %w", err)
	}
	return &status, nil
}
