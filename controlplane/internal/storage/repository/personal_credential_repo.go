package storageRepoImpl

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageModel "controlplane/internal/storage/model"
)

// [COMMENT]: PersonalCredentialRepoImpl thực thi interface PersonalCredentialRepo kết nối PostgreSQL.
type PersonalCredentialRepoImpl struct {
	db     *pgxpool.Pool
	schema string
}

// [COMMENT]: NewPersonalCredentialRepo khởi tạo repository quản lý credentials cho bucket cá nhân.
func NewPersonalCredentialRepo(db *pgxpool.Pool, schema string) storageRepoInterface.PersonalCredentialRepo {
	return &PersonalCredentialRepoImpl{
		db:     db,
		schema: schema,
	}
}

func (r *PersonalCredentialRepoImpl) Create(ctx context.Context, cred *storageEntity.PersonalCredential, outbox *storageEntity.StorageOutboxRecord) error {
	m := storageModel.PersonalCredentialEntityToModel(cred)
	mo := storageModel.OutboxEntityToModel(outbox)

	// [COMMENT]: Dùng CTE để ghi nhận đồng thời thông tin Credentials và sự kiện Outbox nguyên tử.
	query := fmt.Sprintf(`
		WITH ins_cred AS (
			INSERT INTO %s.personal_credentials (
				id, bucket_id, access_key, secret_key, policy, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
		)
		INSERT INTO %s.storage_outbox_records (
			event_id, routing_scope, job_topic, payload, user_id, status, completed_at,
			job_version, resource_id, payload_schema_version, trace_id, idle,
			error_code, error_message
		) VALUES ($8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
	`, r.schema, r.schema)

	_, err := r.db.Exec(ctx, query,
		m.ID,
		m.BucketID,
		m.AccessKey,
		m.SecretKey,
		m.Policy,
		m.CreatedAt,
		m.UpdatedAt,
		mo.EventID,
		mo.RoutingScope,
		mo.JobTopic,
		mo.Payload,
		mo.UserID,
		mo.Status,
		mo.CompletedAt,
		mo.JobVersion,
		mo.ResourceID,
		mo.PayloadSchemaVersion,
		mo.TraceID,
		mo.Idle,
		mo.ErrorCode,
		mo.ErrorMessage,
	)
	if err != nil {
		return err
	}

	return nil
}

func (r *PersonalCredentialRepoImpl) GetByID(ctx context.Context, id uuid.UUID) (*storageEntity.PersonalCredential, error) {
	query := fmt.Sprintf(`
		SELECT id, bucket_id, access_key, secret_key, policy, created_at, updated_at
		FROM %s.personal_credentials
		WHERE id = $1
	`, r.schema)

	var m storageModel.PersonalCredential
	err := r.db.QueryRow(ctx, query, id).Scan(
		&m.ID,
		&m.BucketID,
		&m.AccessKey,
		&m.SecretKey,
		&m.Policy,
		&m.CreatedAt,
		&m.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	return storageModel.PersonalCredentialModelToEntity(&m), nil
}

func (r *PersonalCredentialRepoImpl) ListByBucket(ctx context.Context, bucketID uuid.UUID) ([]*storageEntity.PersonalCredential, error) {
	query := fmt.Sprintf(`
		SELECT id, bucket_id, access_key, secret_key, policy, created_at, updated_at
		FROM %s.personal_credentials
		WHERE bucket_id = $1
		ORDER BY created_at DESC
	`, r.schema)

	rows, err := r.db.Query(ctx, query, bucketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*storageEntity.PersonalCredential
	for rows.Next() {
		var m storageModel.PersonalCredential
		err := rows.Scan(
			&m.ID,
			&m.BucketID,
			&m.AccessKey,
			&m.SecretKey,
			&m.Policy,
			&m.CreatedAt,
			&m.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		result = append(result, storageModel.PersonalCredentialModelToEntity(&m))
	}

	return result, nil
}

func (r *PersonalCredentialRepoImpl) Delete(ctx context.Context, id uuid.UUID, outbox *storageEntity.StorageOutboxRecord) error {
	mo := storageModel.OutboxEntityToModel(outbox)

	// [COMMENT]: Dùng CTE để xóa bản ghi Credentials và chèn sự kiện Outbox báo revoke nguyên tử.
	query := fmt.Sprintf(`
		WITH del_cred AS (
			DELETE FROM %s.personal_credentials
			WHERE id = $1
		)
		INSERT INTO %s.storage_outbox_records (
			event_id, routing_scope, job_topic, payload, user_id, status, completed_at,
			job_version, resource_id, payload_schema_version, trace_id, idle,
			error_code, error_message
		) VALUES ($2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	`, r.schema, r.schema)

	_, err := r.db.Exec(ctx, query,
		id,
		mo.EventID,
		mo.RoutingScope,
		mo.JobTopic,
		mo.Payload,
		mo.UserID,
		mo.Status,
		mo.CompletedAt,
		mo.JobVersion,
		mo.ResourceID,
		mo.PayloadSchemaVersion,
		mo.TraceID,
		mo.Idle,
		mo.ErrorCode,
		mo.ErrorMessage,
	)
	if err != nil {
		return err
	}

	return nil
}
