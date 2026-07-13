package storageRepoImpl

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"controlplane/internal/config"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageModel "controlplane/internal/storage/model"
	storageTaxonomy "controlplane/internal/storage/taxonomy"
)

// [COMMENT]: TenantCredentialRepoImpl thực thi interface TenantCredentialRepo kết nối PostgreSQL.
type TenantCredentialRepoImpl struct {
	db        *pgxpool.Pool
	storage   string // schema storage
	hierarchy string // schema hierarchy
}

// [COMMENT]: NewTenantCredentialRepo khởi tạo repository quản lý credentials cho bucket doanh nghiệp.
func NewTenantCredentialRepo(db *pgxpool.Pool, cfg *config.Config) storageRepoInterface.TenantCredentialRepo {
	return &TenantCredentialRepoImpl{
		db:        db,
		storage:   cfg.SchemaSQL.Storage,
		hierarchy: cfg.SchemaSQL.Hierarchy,
	}
}

func (r *TenantCredentialRepoImpl) Create(ctx context.Context, cred *storageEntity.TenantCredential, outbox *storageEntity.StorageOutboxRecord) error {
	m := storageModel.TenantCredentialEntityToModel(cred)
	mo := storageModel.OutboxEntityToModel(outbox)

	// [COMMENT]: Dùng CTE để ghi nhận đồng thời thông tin Credentials và sự kiện Outbox nguyên tử (lược bỏ secret_key)
	query := fmt.Sprintf(`
		WITH ins_cred AS (
			INSERT INTO %s.tenant_credentials (
				id, bucket_id, access_key, policy, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6)
		)
		INSERT INTO %s.storage_outbox_records (
			event_id, routing_scope, job_topic, payload, user_id, status, completed_at,
			job_version, resource_id, payload_schema_version, trace_id, idle,
			error_code, error_message
		) VALUES ($7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
	`, r.storage, r.storage)

	_, err := r.db.Exec(ctx, query,
		m.ID,
		m.BucketID,
		m.AccessKey,
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

func (r *TenantCredentialRepoImpl) GetByID(ctx context.Context, id uuid.UUID) (*storageEntity.TenantCredential, error) {
	// [COMMENT]: Lược bỏ cột secret_key trong SELECT query
	query := fmt.Sprintf(`
		SELECT id, bucket_id, access_key, policy, created_at, updated_at
		FROM %s.tenant_credentials
		WHERE id = $1
	`, r.storage)

	var m storageModel.TenantCredential
	err := r.db.QueryRow(ctx, query, id).Scan(
		&m.ID,
		&m.BucketID,
		&m.AccessKey,
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

	return storageModel.TenantCredentialModelToEntity(&m), nil
}

func (r *TenantCredentialRepoImpl) ListByBucket(ctx context.Context, bucketID uuid.UUID) ([]*storageEntity.TenantCredential, error) {
	// [COMMENT]: Lược bỏ cột secret_key trong SELECT query
	query := fmt.Sprintf(`
		SELECT id, bucket_id, access_key, policy, created_at, updated_at
		FROM %s.tenant_credentials
		WHERE bucket_id = $1
		ORDER BY created_at DESC
	`, r.storage)

	rows, err := r.db.Query(ctx, query, bucketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*storageEntity.TenantCredential
	for rows.Next() {
		var m storageModel.TenantCredential
		err := rows.Scan(
			&m.ID,
			&m.BucketID,
			&m.AccessKey,
			&m.Policy,
			&m.CreatedAt,
			&m.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		result = append(result, storageModel.TenantCredentialModelToEntity(&m))
	}

	return result, nil
}

func (r *TenantCredentialRepoImpl) Delete(ctx context.Context, param *storageEntity.DeleteTenantCredential, outbox *storageEntity.StorageOutboxRecord) error {
	mo := storageModel.OutboxEntityToModel(outbox)

	// [COMMENT]: CTE 3 bước nguyên tử:
	//   1. verified_bucket: xác minh toàn bộ ownership chain (credential → bucket → workspace → user).
	//   2. verified_cred: kiểm tra credential thuộc bucket hợp lệ mà không thực hiện xóa cứng ngay.
	//   3. INSERT outbox: chỉ ghi nhận sự kiện xóa khi kiểm tra tính hợp lệ thành công.
	query := fmt.Sprintf(`
		WITH verified_bucket AS (
			SELECT tb.id
			FROM %s.tenant_buckets tb
			JOIN %s.tenant_workspaces w ON tb.workspace_id = w.id
			WHERE tb.id = $2 AND w.owner_id = $3 AND tb.workspace_id = $4
		),
		verified_cred AS (
			SELECT id
			FROM %s.tenant_credentials
			WHERE id = $1 AND bucket_id = (SELECT id FROM verified_bucket)
		)
		INSERT INTO %s.storage_outbox_records (
			event_id, routing_scope, job_topic, payload, user_id, status, completed_at,
			job_version, resource_id, payload_schema_version, trace_id, idle,
			error_code, error_message
		)
		SELECT $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18
		FROM verified_cred
	`, r.storage, r.hierarchy, r.storage, r.storage)


	// [COMMENT]: routing_scope truyền trực tiếp từ outbox.RoutingScope (=zone_id từ context, đã có sẵn)
	res, err := r.db.Exec(ctx, query,
		param.CredentialID,      // $1
		param.BucketID,          // $2
		param.UserID,            // $3
		param.WorkspaceID,       // $4
		mo.EventID,              // $5
		mo.RoutingScope,         // $6  ('zone:' + zoneID từ context)
		mo.JobTopic,             // $7
		mo.Payload,              // $8
		mo.UserID,               // $9
		mo.Status,               // $10
		mo.CompletedAt,          // $11
		mo.JobVersion,           // $12
		mo.ResourceID,           // $13
		mo.PayloadSchemaVersion, // $14
		mo.TraceID,              // $15
		mo.Idle,                 // $16
		mo.ErrorCode,            // $17
		mo.ErrorMessage,         // $18
	)
	if err != nil {
		return err
	}

	// [COMMENT]: RowsAffected == 0 khi: credential không tồn tại, bucket không khớp, workspace sai, hoặc user không phải chủ sở hữu
	if res.RowsAffected() == 0 {
		return storageTaxonomy.ErrNotFound
	}

	return nil
}


