package storageRepoImpl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/config"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageModel "controlplane/internal/storage/model"
	storageTaxonomy "controlplane/internal/storage/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// [COMMENT]: PersonalCredentialRepoImpl thực thi interface PersonalCredentialRepo kết nối PostgreSQL.
type PersonalCredentialRepoImpl struct {
	db        *pgxpool.Pool
	storage   string // schema storage
	hierarchy string // schema hierarchy
}

// [COMMENT]: NewPersonalCredentialRepo khởi tạo repository quản lý credentials cho bucket cá nhân.
func NewPersonalCredentialRepo(db *pgxpool.Pool, cfg *config.Config) storageRepoInterface.PersonalCredentialRepo {
	return &PersonalCredentialRepoImpl{
		db:        db,
		storage:   cfg.SchemaSQL.Storage,
		hierarchy: cfg.SchemaSQL.Hierarchy,
	}
}

func (r *PersonalCredentialRepoImpl) Create(ctx context.Context, param *storageEntity.CreatePersonalCredential, outbox *storageEntity.StorageOutboxRecord) (uuid.UUID, error) {
	mo := storageModel.OutboxEntityToModel(outbox)

	// [COMMENT]: Dùng CTE để ghi nhận đồng thời thông tin Credentials và sự kiện Outbox nguyên tử
	// và thực hiện xác minh chéo quyền sở hữu của bucket và workspace dựa trên BucketName vật lý
	query := fmt.Sprintf(`
		WITH verified_bucket AS (
			SELECT pb.id
			FROM %s.personal_buckets pb
			JOIN %s.personal_workspaces w ON pb.workspace_id = w.id
			WHERE pb.name = $2 AND w.owner_id = $3 AND pb.workspace_id = $4
		),
		ins_cred AS (
			INSERT INTO %s.personal_credentials (
				id, bucket_id, access_key, policy, created_at, updated_at
			)
			SELECT $1, id, $5, $6, $7, $8
			FROM verified_bucket
			RETURNING id
		)
		INSERT INTO %s.storage_outbox_records (
			event_id, routing_scope, job_topic, payload, user_id, status, completed_at,
			job_version, resource_id, payload_schema_version, trace_id, idle,
			error_code, error_message
		)
		SELECT $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22
		FROM ins_cred
		RETURNING (SELECT id FROM verified_bucket)
	`, r.storage, r.hierarchy, r.storage, r.storage)

	now := time.Now()
	var bucketID uuid.UUID
	err := r.db.QueryRow(ctx, query,
		param.ID,                // $1
		param.BucketName,        // $2
		param.UserID,            // $3
		param.WorkspaceID,       // $4
		param.AccessKey,         // $5
		param.Policy,            // $6
		now,                     // $7
		now,                     // $8
		mo.EventID,              // $9
		mo.RoutingScope,         // $10
		mo.JobTopic,             // $11
		mo.Payload,              // $12
		mo.UserID,               // $13
		mo.Status,               // $14
		mo.CompletedAt,          // $15
		mo.JobVersion,           // $16
		mo.ResourceID,           // $17
		mo.PayloadSchemaVersion, // $18
		mo.TraceID,              // $19
		mo.Idle,                 // $20
		mo.ErrorCode,            // $21
		mo.ErrorMessage,         // $22
	).Scan(&bucketID)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, storageTaxonomy.ErrNotFound
		}
		return uuid.Nil, err
	}

	return bucketID, nil
}

func (r *PersonalCredentialRepoImpl) ListByBucket(ctx context.Context, bucketID uuid.UUID) ([]*storageEntity.PersonalCredentialListItem, error) {
	// [COMMENT]: Chỉ SELECT các cột cần thiết (bỏ bucket_id, secret_key) để tối ưu IO và dữ liệu truyền tải
	query := fmt.Sprintf(`
		SELECT id, access_key, policy, created_at, updated_at
		FROM %s.personal_credentials
		WHERE bucket_id = $1
		ORDER BY created_at DESC
	`, r.storage)

	rows, err := r.db.Query(ctx, query, bucketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*storageEntity.PersonalCredentialListItem
	for rows.Next() {
		// [COMMENT]: Scan trực tiếp vào struct entity rút gọn PersonalCredentialListItem
		var item storageEntity.PersonalCredentialListItem
		err := rows.Scan(
			&item.ID,
			&item.AccessKey,
			&item.Policy,
			&item.CreatedAt,
			&item.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		result = append(result, &item)
	}

	return result, nil
}

func (r *PersonalCredentialRepoImpl) Delete(ctx context.Context, param *storageEntity.DeletePersonalCredential, outbox *storageEntity.StorageOutboxRecord) error {
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
		mo.RoutingScope,         // $6
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
