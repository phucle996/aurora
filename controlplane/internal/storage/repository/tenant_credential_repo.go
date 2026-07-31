package storageRepoImpl

import (
	"context"
	"fmt"

	"controlplane/internal/config"
	jobpayload "controlplane/internal/security"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageModel "controlplane/internal/storage/model"
	storageTaxonomy "controlplane/internal/storage/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// [COMMENT]: TenantCredentialRepoImpl thực thi interface TenantCredentialRepo kết nối PostgreSQL.
type TenantCredentialRepoImpl struct {
	db        *pgxpool.Pool
	storage   string // schema storage
	hierarchy string // schema hierarchy
	protector jobpayload.Protector
}

// [COMMENT]: NewTenantCredentialRepo khởi tạo repository quản lý credentials cho bucket doanh nghiệp.
func NewTenantCredentialRepo(db *pgxpool.Pool, cfg *config.Config, protector jobpayload.Protector) storageRepoInterface.TenantCredentialRepo {
	return &TenantCredentialRepoImpl{
		db:        db,
		storage:   cfg.SchemaSQL.Storage,
		hierarchy: cfg.SchemaSQL.Hierarchy,
		protector: protector,
	}
}

func (r *TenantCredentialRepoImpl) Create(ctx context.Context, cred *storageEntity.TenantCredential, outbox *storageEntity.StorageOutboxRecord) error {
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: outbox.ZoneID, SourceDomain: "STORAGE", JobTopic: outbox.JobTopic, ResourceID: outbox.ResourceID, JobVersion: outbox.JobVersion, PayloadSchemaVersion: outbox.PayloadSchemaVersion}, outbox.Payload)
	if err != nil {
		return err
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID
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
			event_id, zone_id, job_topic, payload, owner_id, owner_type, status, completed_at,
			job_version, resource_id, payload_schema_version, trace_id, idle,
			error_code, error_message, actor_user_id, payload_key_id
		) VALUES ($7, $8, $9, $10, $11, $21, $12, $13, $14, $15, $16, $17, $18, $19, $20, $22, $23)
	`, r.storage, r.storage)

	_, err = r.db.Exec(ctx, query,
		m.ID,
		m.BucketID,
		m.AccessKey,
		m.Policy,
		m.CreatedAt,
		m.UpdatedAt,
		mo.EventID,
		mo.ZoneID,
		mo.JobTopic,
		mo.Payload,
		mo.OwnerID,
		mo.Status,
		mo.CompletedAt,
		mo.JobVersion,
		mo.ResourceID,
		mo.PayloadSchemaVersion,
		mo.TraceID,
		mo.Idle,
		mo.ErrorCode,
		mo.ErrorMessage,
		mo.OwnerType,
		mo.ActorUserID,
		mo.PayloadKeyID,
	)

	if err != nil {
		return err
	}

	return nil
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
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: outbox.ZoneID, SourceDomain: "STORAGE", JobTopic: outbox.JobTopic, ResourceID: outbox.ResourceID, JobVersion: outbox.JobVersion, PayloadSchemaVersion: outbox.PayloadSchemaVersion}, outbox.Payload)
	if err != nil {
		return err
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID
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
			event_id, zone_id, job_topic, payload, owner_id, owner_type, status, completed_at,
			job_version, resource_id, payload_schema_version, trace_id, idle,
			error_code, error_message, actor_user_id, payload_key_id
		)
		SELECT $5, $6, $7, $8, $9, $19, $10, $11, $12, $13, $14, $15, $16, $17, $18, $20, $21
		FROM verified_cred
	`, r.storage, r.hierarchy, r.storage, r.storage)

	// [COMMENT]: ZoneID truyền trực tiếp từ outbox đã được handler/service bind với workspace.
	res, err := r.db.Exec(ctx, query,
		param.CredentialID,      // $1
		param.BucketID,          // $2
		param.UserID,            // $3
		param.WorkspaceID,       // $4
		mo.EventID,              // $5
		mo.ZoneID,               // $6
		mo.JobTopic,             // $7
		mo.Payload,              // $8
		mo.OwnerID,              // $9
		mo.Status,               // $10
		mo.CompletedAt,          // $11
		mo.JobVersion,           // $12
		mo.ResourceID,           // $13
		mo.PayloadSchemaVersion, // $14
		mo.TraceID,              // $15
		mo.Idle,                 // $16
		mo.ErrorCode,            // $17
		mo.ErrorMessage,         // $18
		mo.OwnerType,            // $19
		mo.ActorUserID,          // $20
		mo.PayloadKeyID,         // $21
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
