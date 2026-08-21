package storageRepoImpl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/config"
	jobpayload "controlplane/internal/security"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageModel "controlplane/internal/storage/model"
	storageTaxonomy "controlplane/internal/storage/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// [COMMENT]: PersonalCredentialRepoImpl thực thi interface PersonalCredentialRepo kết nối PostgreSQL.
type PersonalCredentialRepoImpl struct {
	db        *pgxpool.Pool
	storage   string // schema storage
	hierarchy string // schema hierarchy
	protector jobpayload.Protector
}

// [COMMENT]: NewPersonalCredentialRepo khởi tạo repository quản lý credentials cho bucket cá nhân.
func NewPersonalCredentialRepo(db *pgxpool.Pool, cfg *config.Config, protector jobpayload.Protector) storageRepoInterface.PersonalCredentialRepo {
	return &PersonalCredentialRepoImpl{
		db:        db,
		storage:   cfg.SchemaSQL.Storage,
		hierarchy: cfg.SchemaSQL.Hierarchy,
		protector: protector,
	}
}

func (r *PersonalCredentialRepoImpl) Create(ctx context.Context, param *storageEntity.CreatePersonalCredential, outbox *storageEntity.StorageOutboxRecord) (uuid.UUID, error) {
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: outbox.ZoneID, SourceDomain: "STORAGE", JobTopic: outbox.JobTopic, ResourceID: outbox.ResourceID, JobVersion: outbox.JobVersion, PayloadSchemaVersion: outbox.PayloadSchemaVersion}, outbox.Payload)
	if err != nil {
		return uuid.Nil, err
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID
	mo := storageModel.OutboxEntityToModel(outbox)

	// [COMMENT]: Dùng CTE để ghi nhận đồng thời thông tin Credentials và sự kiện Outbox nguyên tử
	// và thực hiện xác minh chéo quyền sở hữu của bucket và workspace dựa trên BucketName vật lý
	query := fmt.Sprintf(`
		WITH admitted AS (
			SELECT EXISTS (
				SELECT 1 FROM %s.commercial_admission_projection
				WHERE owner_id = $3
				  AND owner_type = 'PERSONAL'
				  AND effective_at <= NOW()
				  AND (valid_until IS NULL OR valid_until > NOW())
				  AND decision = 'ALLOW'
			) AS ok
		),
		verified_bucket AS (
			SELECT pb.id
			FROM %s.personal_buckets pb
			JOIN %s.personal_workspaces w ON pb.workspace_id = w.id
			WHERE pb.name = $2 AND w.owner_id = $3 AND pb.workspace_id = $4
			  AND (SELECT ok FROM admitted)
		),
		ins_cred AS (
			INSERT INTO %s.personal_credentials (
				id, bucket_id, access_key, policy, created_at, updated_at
			)
			SELECT $1, id, $5, $6, $7, $8
			FROM verified_bucket
			RETURNING id
		),
		ins_outbox AS (
			INSERT INTO %s.storage_outbox_records (
				event_id, zone_id, job_topic, payload, owner_id, owner_type, status, completed_at,
				job_version, resource_id, payload_schema_version, trace_id, idle,
				error_code, error_message, actor_user_id, payload_key_id
			)
			SELECT $9, $10, $11, $12, $13, $23, $14, $15, $16, $17, $18, $19, $20, $21, $22, $24, $25
			FROM ins_cred
		)
		SELECT
			(SELECT ok FROM admitted) AS admitted,
			(SELECT id FROM verified_bucket) AS bucket_id;
	`, r.storage, r.storage, r.hierarchy, r.storage, r.storage)

	now := time.Now()
	var admitted bool
	var bucketID *uuid.UUID
	err = r.db.QueryRow(ctx, query,
		param.ID,                // $1
		param.BucketName,        // $2
		param.UserID,            // $3
		param.WorkspaceID,       // $4
		param.AccessKey,         // $5
		param.Policy,            // $6
		now,                     // $7
		now,                     // $8
		mo.EventID,              // $9
		mo.ZoneID,               // $10
		mo.JobTopic,             // $11
		mo.Payload,              // $12
		mo.OwnerID,              // $13
		mo.Status,               // $14
		mo.CompletedAt,          // $15
		mo.JobVersion,           // $16
		mo.ResourceID,           // $17
		mo.PayloadSchemaVersion, // $18
		mo.TraceID,              // $19
		mo.Idle,                 // $20
		mo.ErrorCode,            // $21
		mo.ErrorMessage,         // $22
		mo.OwnerType,            // $23
		mo.ActorUserID,          // $24
		mo.PayloadKeyID,         // $25
	).Scan(&admitted, &bucketID)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return uuid.Nil, storageTaxonomy.ErrAlreadyExists
		}
		return uuid.Nil, err
	}
	if !admitted {
		return uuid.Nil, storageTaxonomy.ErrCommercialAdmissionDenied
	}
	if bucketID == nil {
		return uuid.Nil, storageTaxonomy.ErrNotFound
	}

	return *bucketID, nil
}

func (r *PersonalCredentialRepoImpl) ListByBucket(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID) ([]*storageEntity.PersonalCredentialListItem, error) {
	query := fmt.Sprintf(`
		SELECT b.id, c.id, c.access_key, c.policy, c.created_at, c.updated_at
		FROM %s.personal_buckets b
		JOIN %s.personal_workspaces w ON b.workspace_id = w.id
		LEFT JOIN %s.personal_credentials c ON c.bucket_id = b.id
		WHERE b.id = $1 AND w.owner_id = $2
		ORDER BY c.created_at DESC
	`, r.storage, r.hierarchy, r.storage)

	rows, err := r.db.Query(ctx, query, bucketID, userID)
	if err != nil {
		return nil, fmt.Errorf("storage repo: query personal credentials failed: %w", err)
	}
	defer rows.Close()

	found := false
	result := make([]*storageEntity.PersonalCredentialListItem, 0)
	for rows.Next() {
		found = true
		var dummyBucketID uuid.UUID
		var credID *uuid.UUID
		var accessKey, policy *string
		var createdAt, updatedAt *time.Time

		err := rows.Scan(
			&dummyBucketID,
			&credID,
			&accessKey,
			&policy,
			&createdAt,
			&updatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("storage repo: scan personal credential row failed: %w", err)
		}

		if credID != nil {
			result = append(result, &storageEntity.PersonalCredentialListItem{
				ID:        *credID,
				AccessKey: *accessKey,
				Policy:    *policy,
				CreatedAt: *createdAt,
				UpdatedAt: *updatedAt,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage repo: iterate personal credentials failed: %w", err)
	}

	if !found {
		return nil, storageTaxonomy.ErrNotFound
	}

	return result, nil
}

func (r *PersonalCredentialRepoImpl) Delete(ctx context.Context, param *storageEntity.DeletePersonalCredential, outbox *storageEntity.StorageOutboxRecord) error {
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
			SELECT pb.id
			FROM %s.personal_buckets pb
			JOIN %s.personal_workspaces w ON pb.workspace_id = w.id
			WHERE pb.id = $2 AND w.owner_id = $3 AND pb.workspace_id = $4
		),
		verified_cred AS (
			SELECT id
			FROM %s.personal_credentials
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

func (r *PersonalCredentialRepoImpl) ListAccessKeys(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID) ([]string, error) {
	query := fmt.Sprintf(`
		SELECT c.access_key
		FROM %s.personal_credentials c
		JOIN %s.personal_buckets b ON c.bucket_id = b.id
		JOIN %s.personal_workspaces w ON b.workspace_id = w.id
		WHERE c.bucket_id = $1 AND w.owner_id = $2
	`, r.storage, r.storage, r.hierarchy)

	rows, err := r.db.Query(ctx, query, bucketID, userID)
	if err != nil {
		return nil, fmt.Errorf("storage repo: query personal access keys failed: %w", err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("storage repo: scan personal access key failed: %w", err)
		}
		keys = append(keys, key)
	}
	return keys, nil
}
