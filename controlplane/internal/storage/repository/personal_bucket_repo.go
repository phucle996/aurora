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

	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// [COMMENT]: PersonalBucketRepoImpl thực thi interface PersonalBucketRepo cho kết nối PostgreSQL.
type PersonalBucketRepoImpl struct {
	db        *pgxpool.Pool
	storage   string // schema storage
	hierarchy string // schema hierarchy
	protector jobpayload.Protector
}

// [COMMENT]: NewPersonalBucketRepo khởi tạo repository quản lý bucket cá nhân.
func NewPersonalBucketRepo(
	db *pgxpool.Pool,
	cfg *config.Config,
	protector jobpayload.Protector,
) storageRepoInterface.PersonalBucketRepo {
	return &PersonalBucketRepoImpl{
		db:        db,
		storage:   cfg.SchemaSQL.Storage,
		hierarchy: cfg.SchemaSQL.Hierarchy,
		protector: protector,
	}
}

func (r *PersonalBucketRepoImpl) Create(ctx context.Context, bucket *storageEntity.PersonalBucket, workspaceID uuid.UUID, zoneID uuid.UUID, credential *storageEntity.PersonalCredential, outbox *storageEntity.StorageOutboxRecord) error {
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: outbox.ZoneID, SourceDomain: "STORAGE", JobTopic: outbox.JobTopic, ResourceID: outbox.ResourceID, JobVersion: outbox.JobVersion, PayloadSchemaVersion: outbox.PayloadSchemaVersion}, outbox.Payload)
	if err != nil {
		return err
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID
	// [COMMENT]: Convert Entity sang Model chứa các tag db cho credential và outbox
	mc := storageModel.PersonalCredentialEntityToModel(credential)
	mo := storageModel.OutboxEntityToModel(outbox)

	// [COMMENT]: CTE 3-way check ownership: insert nguyên tử bucket + credential + outbox record chỉ khi workspace thuộc về user_id ($17).
	// Status column đã bị drop — không cần truyền status vào INSERT nữa.
	query := fmt.Sprintf(`
		WITH check_workspace AS (
			SELECT 1 FROM %s.personal_workspaces WHERE id = $3 AND owner_id = $17
		),
		ins_bucket AS (
			INSERT INTO %s.personal_buckets (
				id, name, workspace_id, zone_id, capacity_quota_bytes, created_at, updated_at,
				encrypt_enabled, versioning_enabled, object_locking_enabled, replication_enabled,
				retention_days, legal_hold_enabled, tags
			)
			SELECT $1, $2, $3, $4, $5, $6, $7, $27, $28, $29, $30, $31, $32, $33
			FROM check_workspace
			RETURNING id
		),
		ins_credential AS (
			INSERT INTO %s.personal_credentials (
				id, bucket_id, access_key, policy, created_at, updated_at
			)
			SELECT $8, id, $9, $10, $11, $12
			FROM ins_bucket
		)
		INSERT INTO %s.storage_outbox_records (
			event_id, zone_id, job_topic, payload, owner_id, owner_type, status, completed_at,
			job_version, resource_id, payload_schema_version, trace_id, idle,
			error_code, error_message, actor_user_id, payload_key_id,
			resource_name, rollback_quota_bytes
		)
		SELECT $13, $14, $15, $16, $17, $34, $18, $19, $20, $21, $22, $23, $24, $25, $26, $35, $36, $37, $38
		FROM ins_bucket
	`, r.hierarchy, r.storage, r.storage, r.storage)

	res, err := r.db.Exec(ctx, query,
		// [COMMENT]: $1-$7 — personal_buckets fields (no status)
		bucket.ID,
		bucket.Name,
		workspaceID,
		zoneID,
		bucket.CapacityQuotaBytes,
		bucket.CreatedAt,
		bucket.UpdatedAt,
		// [COMMENT]: $8-$12 — personal_credentials fields
		mc.ID,
		mc.AccessKey,
		mc.Policy,
		mc.CreatedAt,
		mc.UpdatedAt,
		// [COMMENT]: $13-$26 — storage_outbox_records fields
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
		// [COMMENT]: $27-$33 — advanced configurations
		bucket.EncryptEnabled,
		bucket.VersioningEnabled,
		bucket.ObjectLockingEnabled,
		bucket.ReplicationEnabled,
		bucket.RetentionDays,
		bucket.LegalHoldEnabled,
		func() []byte {
			b, _ := json.Marshal(bucket.Tags)
			return b
		}(),
		// [COMMENT]: $34 — owner_type ("PERSONAL")
		mo.OwnerType,
		mo.ActorUserID,
		mo.PayloadKeyID,
		mo.ResourceName,
		mo.RollbackQuotaBytes,
	)

	if err != nil {
		// [COMMENT]: Bắt lỗi trùng lặp mã Key (Unique Constraint 23505) và ánh xạ sang lỗi domain ErrAlreadyExists
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return storageTaxonomy.ErrAlreadyExists
		}
		return fmt.Errorf("storage repo: create personal bucket failed: %w", err)
	}
	// [COMMENT]: Nếu RowsAffected == 0 tức là workspace không tồn tại hoặc không thuộc sở hữu của userID ($17)
	if res.RowsAffected() == 0 {
		return storageTaxonomy.ErrNotFound
	}
	return nil
}

func (r *PersonalBucketRepoImpl) GetByID(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*storageEntity.PersonalBucket, error) {
	query := fmt.Sprintf(`
		SELECT b.id, b.name, b.zone_id, b.capacity_quota_bytes, b.used_bytes, b.created_at, b.updated_at
		FROM %s.personal_buckets b
		JOIN %s.personal_workspaces w ON b.workspace_id = w.id
		WHERE b.id = $1 AND w.owner_id = $2
	`, r.storage, r.hierarchy)

	var b storageEntity.PersonalBucket

	err := r.db.QueryRow(ctx, query, id, userID).Scan(
		&b.ID,
		&b.Name,
		&b.ZoneID,
		&b.CapacityQuotaBytes,
		&b.UsedBytes,
		&b.CreatedAt,
		&b.UpdatedAt,
	)
	if err != nil {
		// [COMMENT]: Ánh xạ lỗi ErrNoRows thành domain error ErrNotFound
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, storageTaxonomy.ErrNotFound
		}
		return nil, fmt.Errorf("storage repo: get personal bucket by id failed: %w", err)
	}

	return &b, nil
}

func (r *PersonalBucketRepoImpl) GetByName(ctx context.Context, name string) (*storageEntity.PersonalBucket, error) {
	query := fmt.Sprintf(`
		SELECT id, name, zone_id, capacity_quota_bytes, used_bytes, created_at, updated_at
		FROM %s.personal_buckets
		WHERE name = $1
	`, r.storage)

	var b storageEntity.PersonalBucket

	err := r.db.QueryRow(ctx, query, name).Scan(
		&b.ID,
		&b.Name,
		&b.ZoneID,
		&b.CapacityQuotaBytes,
		&b.UsedBytes,
		&b.CreatedAt,
		&b.UpdatedAt,
	)
	if err != nil {
		// [COMMENT]: Ánh xạ lỗi ErrNoRows thành domain error ErrNotFound
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, storageTaxonomy.ErrNotFound
		}
		return nil, fmt.Errorf("storage repo: get personal bucket by name failed: %w", err)
	}

	return &b, nil
}

func (r *PersonalBucketRepoImpl) ListByWorkspace(ctx context.Context, workspaceID uuid.UUID, zoneID uuid.UUID, userID uuid.UUID) ([]*storageEntity.PersonalBucket, error) {
	query := fmt.Sprintf(`
		SELECT b.id, b.name, b.zone_id, b.capacity_quota_bytes, b.used_bytes, b.created_at, b.updated_at
		FROM %s.personal_buckets b
		JOIN %s.personal_workspaces w ON b.workspace_id = w.id
		WHERE b.workspace_id = $1 AND b.zone_id = $2 AND w.owner_id = $3 AND w.zone_id = $2
		ORDER BY b.created_at DESC
	`, r.storage, r.hierarchy)

	rows, err := r.db.Query(ctx, query, workspaceID, zoneID, userID)
	if err != nil {
		return nil, fmt.Errorf("storage repo: list personal buckets by workspace failed: %w", err)
	}
	defer rows.Close()

	var buckets []*storageEntity.PersonalBucket
	for rows.Next() {
		var b storageEntity.PersonalBucket

		err := rows.Scan(
			&b.ID,
			&b.Name,
			&b.ZoneID,
			&b.CapacityQuotaBytes,
			&b.UsedBytes,
			&b.CreatedAt,
			&b.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("storage repo: scan personal bucket row failed: %w", err)
		}
		buckets = append(buckets, &b)
	}

	return buckets, nil
}

func (r *PersonalBucketRepoImpl) UpdateQuota(ctx context.Context, id uuid.UUID, userID uuid.UUID, quotaBytes int64, outbox *storageEntity.StorageOutboxRecord) error {
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: outbox.ZoneID, SourceDomain: "STORAGE", JobTopic: outbox.JobTopic, ResourceID: outbox.ResourceID, JobVersion: outbox.JobVersion, PayloadSchemaVersion: outbox.PayloadSchemaVersion}, outbox.Payload)
	if err != nil {
		return err
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID
	// [COMMENT]: Khởi tạo transaction để đảm bảo atomic cho cả kiểm tra quota, update quota và ghi outbox
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("storage repo: begin tx failed: %w", err)
	}
	defer tx.Rollback(ctx)

	// [COMMENT]: 1. SELECT FOR UPDATE để validate ownership và lock row tránh race condition
	selectQuery := fmt.Sprintf(`
		SELECT b.name, b.capacity_quota_bytes, b.used_bytes
		FROM %s.personal_buckets b
		JOIN %s.personal_workspaces w ON b.workspace_id = w.id
		WHERE b.id = $1 AND w.owner_id = $2
		FOR UPDATE
	`, r.storage, r.hierarchy)

	var bucketName string
	var currentQuota, usedBytes int64
	err = tx.QueryRow(ctx, selectQuery, id, userID).Scan(&bucketName, &currentQuota, &usedBytes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storageTaxonomy.ErrNotFound
		}
		return fmt.Errorf("storage repo: select bucket for update failed: %w", err)
	}

	// [COMMENT]: 2. Kiểm tra nghiệp vụ: Hạn mức quota mới phải trống ít nhất 1GB (1_073_741_824 bytes) so với used_bytes hiện tại
	if quotaBytes-usedBytes < 1073741824 {
		return storageTaxonomy.ErrResizeLimitTooLow
	}

	// [COMMENT]: 3. Thực hiện cập nhật DB hạn mức quota mới
	updateQuery := fmt.Sprintf(`
		UPDATE %s.personal_buckets
		SET capacity_quota_bytes = $1, updated_at = $2
		WHERE id = $3
	`, r.storage)

	_, err = tx.Exec(ctx, updateQuery, quotaBytes, time.Now(), id)
	if err != nil {
		return fmt.Errorf("storage repo: update personal bucket capacity failed: %w", err)
	}

	// [COMMENT]: 4. Chèn outbox record để đồng bộ lệnh resize xuống dataplane
	// Settlement metadata comes from the locked row, not the earlier service
	// snapshot, so a concurrent request cannot corrupt the rollback fence.
	outbox.ResourceName = bucketName
	outbox.RollbackQuotaBytes = &currentQuota
	mo := storageModel.OutboxEntityToModel(outbox)
	insertOutboxQuery := fmt.Sprintf(`
		INSERT INTO %s.storage_outbox_records (
			event_id, zone_id, job_topic, payload, owner_id, owner_type, status, completed_at,
			job_version, resource_id, payload_schema_version, trace_id, idle,
			error_code, error_message, actor_user_id, payload_key_id,
			resource_name, rollback_quota_bytes
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
	`, r.storage)

	_, err = tx.Exec(ctx, insertOutboxQuery,
		mo.EventID,
		mo.ZoneID,
		mo.JobTopic,
		mo.Payload,
		mo.OwnerID,
		mo.OwnerType,
		mo.Status,

		mo.CompletedAt,
		mo.JobVersion,
		mo.ResourceID,
		mo.PayloadSchemaVersion,
		mo.TraceID,
		mo.Idle,
		mo.ErrorCode,
		mo.ErrorMessage,
		mo.ActorUserID,
		mo.PayloadKeyID,
		mo.ResourceName,
		mo.RollbackQuotaBytes,
	)
	if err != nil {
		return fmt.Errorf("storage repo: insert resize outbox failed: %w", err)
	}

	// [COMMENT]: Commit transaction sau khi tất cả các bước thành công
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("storage repo: commit resize tx failed: %w", err)
	}

	return nil
}

func (r *PersonalBucketRepoImpl) Delete(ctx context.Context, id uuid.UUID, userID uuid.UUID, outbox *storageEntity.StorageOutboxRecord) error {
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: outbox.ZoneID, SourceDomain: "STORAGE", JobTopic: outbox.JobTopic, ResourceID: outbox.ResourceID, JobVersion: outbox.JobVersion, PayloadSchemaVersion: outbox.PayloadSchemaVersion}, outbox.Payload)
	if err != nil {
		return err
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID
	// [COMMENT]: Map outbox record sang model DB
	mo := storageModel.OutboxEntityToModel(outbox)

	// [COMMENT]: Sử dụng single atomic CTE để kiểm tra ownership (SELECT FOR UPDATE) và chèn outbox record đồng thời
	query := fmt.Sprintf(`
		WITH locked_bucket AS (
			SELECT b.id, b.name
			FROM %s.personal_buckets b
			JOIN %s.personal_workspaces w ON b.workspace_id = w.id
			WHERE b.id = $1 AND w.owner_id = $2
			FOR UPDATE
		)
		INSERT INTO %s.storage_outbox_records (
			event_id, zone_id, job_topic, payload, owner_id, owner_type, status, completed_at,
			job_version, resource_id, payload_schema_version, trace_id, idle,
			error_code, error_message, actor_user_id, payload_key_id,
			resource_name, rollback_quota_bytes
		)
		SELECT $3, $4, $5, $6, $7, $17, $8, $9, $10, $11, $12, $13, $14, $15, $16, $18, $19, locked_bucket.name, NULL
		FROM locked_bucket
	`, r.storage, r.hierarchy, r.storage)

	res, err := r.db.Exec(ctx, query,
		id,
		userID,
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
		return fmt.Errorf("storage repo: atomic delete bucket outbox failed: %w", err)
	}

	// [COMMENT]: Nếu không có dòng nào bị ảnh hưởng (tức locked_bucket rỗng, không đúng ownership), trả về ErrNotFound
	if res.RowsAffected() == 0 {
		return storageTaxonomy.ErrNotFound
	}

	return nil
}

// [COMMENT]: ListNamesByWorkspace truy vấn siêu nhẹ chỉ lấy duy nhất trường name từ CSDL
func (r *PersonalBucketRepoImpl) ListNamesByWorkspace(ctx context.Context, workspaceID uuid.UUID, zoneID uuid.UUID, userID uuid.UUID) ([]string, error) {
	query := fmt.Sprintf(`
		SELECT b.name
		FROM %s.personal_buckets b
		JOIN %s.personal_workspaces w ON b.workspace_id = w.id
		WHERE b.workspace_id = $1 AND b.zone_id = $2 AND w.owner_id = $3 AND w.zone_id = $2
		ORDER BY b.created_at DESC
	`, r.storage, r.hierarchy)

	rows, err := r.db.Query(ctx, query, workspaceID, zoneID, userID)
	if err != nil {
		return nil, fmt.Errorf("storage repo: list personal bucket names failed: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("storage repo: scan personal bucket name row failed: %w", err)
		}
		names = append(names, name)
	}

	return names, nil
}

func (r *PersonalBucketRepoImpl) ListAccessKeys(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID) ([]string, error) {
	// [COMMENT]: Truy vấn danh sách access_key của toàn bộ credentials thuộc bucket chỉ định và kiểm tra quyền sở hữu của user
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
