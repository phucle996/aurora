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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// [COMMENT]: TenantBucketRepoImpl thực thi interface TenantBucketRepo cho kết nối PostgreSQL.
type TenantBucketRepoImpl struct {
	db      *pgxpool.Pool
	storage string // schema storage
}

// [COMMENT]: NewTenantBucketRepo khởi tạo repository quản lý bucket doanh nghiệp.
func NewTenantBucketRepo(
	db *pgxpool.Pool,
	cfg *config.Config,
) storageRepoInterface.TenantBucketRepo {
	return &TenantBucketRepoImpl{
		db:      db,
		storage: cfg.SchemaSQL.Storage,
	}
}

func (r *TenantBucketRepoImpl) Create(ctx context.Context, bucket *storageEntity.TenantBucket, credential *storageEntity.TenantCredential, outbox *storageEntity.StorageOutboxRecord) error {
	// [COMMENT]: Convert Entity sang Model chứa các tag db
	mc := storageModel.TenantCredentialEntityToModel(credential)
	mo := storageModel.OutboxEntityToModel(outbox)

	// [COMMENT]: CTE 3-way: insert nguyên tử (atomic) bucket + credential + outbox record.
	// Status column đã bị drop — không cần truyền status vào INSERT nữa.
	query := fmt.Sprintf(`
		WITH ins_bucket AS (
			INSERT INTO %s.tenant_buckets (
				id, name, workspace_id, zone_id, tenant_id, capacity_quota_bytes, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING id
		),
		ins_credential AS (
			INSERT INTO %s.tenant_credentials (
				id, bucket_id, access_key, policy, created_at, updated_at
			)
			SELECT $9, id, $10, $11, $12, $13
			FROM ins_bucket
		)
		INSERT INTO %s.storage_outbox_records (
			event_id, zone_id, job_topic, payload, owner_id, owner_type, status, completed_at,
			job_version, resource_id, payload_schema_version, trace_id, idle,
			error_code, error_message, actor_user_id
		) VALUES ($14, $15, $16, $17, $18, $28, $19, $20, $21, $22, $23, $24, $25, $26, $27, $29)
	`, r.storage, r.storage, r.storage)

	_, err := r.db.Exec(ctx, query,
		// [COMMENT]: $1-$8 — tenant_buckets fields (no status)
		bucket.ID,
		bucket.Name,
		bucket.WorkspaceID,
		bucket.ZoneID,
		bucket.TenantID,
		bucket.CapacityQuotaBytes,
		bucket.CreatedAt,
		bucket.UpdatedAt,
		// [COMMENT]: $9-$13 — tenant_credentials fields
		mc.ID,
		mc.AccessKey,
		mc.Policy,
		mc.CreatedAt,
		mc.UpdatedAt,
		// [COMMENT]: $14-$27 — storage_outbox_records fields
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
		// [COMMENT]: $28 — owner_type
		mo.OwnerType,
		mo.ActorUserID,
	)

	if err != nil {
		// [COMMENT]: Bắt lỗi trùng lặp mã Key (Unique Constraint 23505) và ánh xạ sang lỗi domain ErrAlreadyExists
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return storageTaxonomy.ErrAlreadyExists
		}
		return fmt.Errorf("storage repo: create tenant bucket failed: %w", err)
	}
	return nil
}

func (r *TenantBucketRepoImpl) GetByID(ctx context.Context, id uuid.UUID) (*storageEntity.TenantBucket, error) {
	query := fmt.Sprintf(`
		SELECT id, name, workspace_id, zone_id, tenant_id, capacity_quota_bytes, used_bytes, created_at, updated_at
		FROM %s.tenant_buckets
		WHERE id = $1
	`, r.storage)

	var m storageModel.TenantBucket

	err := r.db.QueryRow(ctx, query, id).Scan(
		&m.ID,
		&m.Name,
		&m.WorkspaceID,
		&m.ZoneID,
		&m.TenantID,
		&m.CapacityQuotaBytes,
		&m.UsedBytes,
		&m.CreatedAt,
		&m.UpdatedAt,
	)
	if err != nil {
		// [COMMENT]: Ánh xạ lỗi ErrNoRows thành domain error ErrNotFound
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, storageTaxonomy.ErrNotFound
		}
		return nil, fmt.Errorf("storage repo: get tenant bucket by id failed: %w", err)
	}

	return storageModel.TenantBucketModelToEntity(&m), nil
}

func (r *TenantBucketRepoImpl) GetByName(ctx context.Context, name string) (*storageEntity.TenantBucket, error) {
	query := fmt.Sprintf(`
		SELECT id, name, workspace_id, zone_id, tenant_id, capacity_quota_bytes, used_bytes, created_at, updated_at
		FROM %s.tenant_buckets
		WHERE name = $1
	`, r.storage)

	var m storageModel.TenantBucket

	err := r.db.QueryRow(ctx, query, name).Scan(
		&m.ID,
		&m.Name,
		&m.WorkspaceID,
		&m.ZoneID,
		&m.TenantID,
		&m.CapacityQuotaBytes,
		&m.UsedBytes,
		&m.CreatedAt,
		&m.UpdatedAt,
	)
	if err != nil {
		// [COMMENT]: Ánh xạ lỗi ErrNoRows thành domain error ErrNotFound
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, storageTaxonomy.ErrNotFound
		}
		return nil, fmt.Errorf("storage repo: get tenant bucket by name failed: %w", err)
	}

	return storageModel.TenantBucketModelToEntity(&m), nil
}

func (r *TenantBucketRepoImpl) ListByTenantAndZone(ctx context.Context, tenantID uuid.UUID, zoneID uuid.UUID) ([]*storageEntity.TenantBucket, error) {
	query := fmt.Sprintf(`
		SELECT id, name, workspace_id, zone_id, tenant_id, capacity_quota_bytes, used_bytes, created_at, updated_at
		FROM %s.tenant_buckets
		WHERE tenant_id = $1 AND zone_id = $2
		ORDER BY created_at DESC
	`, r.storage)

	rows, err := r.db.Query(ctx, query, tenantID, zoneID)
	if err != nil {
		return nil, fmt.Errorf("storage repo: list tenant buckets failed: %w", err)
	}
	defer rows.Close()

	var buckets []*storageEntity.TenantBucket
	for rows.Next() {
		var m storageModel.TenantBucket

		err := rows.Scan(
			&m.ID,
			&m.Name,
			&m.WorkspaceID,
			&m.ZoneID,
			&m.TenantID,
			&m.CapacityQuotaBytes,
			&m.UsedBytes,
			&m.CreatedAt,
			&m.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("storage repo: scan tenant bucket row failed: %w", err)
		}
		buckets = append(buckets, storageModel.TenantBucketModelToEntity(&m))
	}

	return buckets, nil
}

func (r *TenantBucketRepoImpl) UpdateQuota(ctx context.Context, id uuid.UUID, quotaBytes int64, outbox *storageEntity.StorageOutboxRecord) error {
	// [COMMENT]: Khởi tạo transaction để đảm bảo atomic cho cả kiểm tra quota, update quota và ghi outbox
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("storage repo: begin tx failed: %w", err)
	}
	defer tx.Rollback(ctx)

	// [COMMENT]: 1. SELECT FOR UPDATE để validate sự tồn tại và lock row tránh race condition
	selectQuery := fmt.Sprintf(`
		SELECT capacity_quota_bytes, used_bytes
		FROM %s.tenant_buckets
		WHERE id = $1
		FOR UPDATE
	`, r.storage)

	var currentQuota, usedBytes int64
	err = tx.QueryRow(ctx, selectQuery, id).Scan(&currentQuota, &usedBytes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storageTaxonomy.ErrNotFound
		}
		return fmt.Errorf("storage repo: select tenant bucket for update failed: %w", err)
	}

	// [COMMENT]: 2. Kiểm tra nghiệp vụ: Hạn mức quota mới phải trống ít nhất 1GB (1_073_741_824 bytes) so với used_bytes hiện tại
	if quotaBytes-usedBytes < 1073741824 {
		return storageTaxonomy.ErrResizeLimitTooLow
	}

	// [COMMENT]: 3. Thực hiện cập nhật DB hạn mức quota mới
	updateQuery := fmt.Sprintf(`
		UPDATE %s.tenant_buckets
		SET capacity_quota_bytes = $1, updated_at = $2
		WHERE id = $3
	`, r.storage)

	_, err = tx.Exec(ctx, updateQuery, quotaBytes, time.Now(), id)
	if err != nil {
		return fmt.Errorf("storage repo: update tenant bucket capacity failed: %w", err)
	}

	// [COMMENT]: 4. Chèn outbox record để đồng bộ lệnh resize xuống dataplane
	mo := storageModel.OutboxEntityToModel(outbox)
	insertOutboxQuery := fmt.Sprintf(`
		INSERT INTO %s.storage_outbox_records (
			event_id, zone_id, job_topic, payload, owner_id, owner_type, status, completed_at,
			job_version, resource_id, payload_schema_version, trace_id, idle,
			error_code, error_message, actor_user_id
		) VALUES ($1, $2, $3, $4, $5, $15, $6, $7, $8, $9, $10, $11, $12, $13, $14, $16)
	`, r.storage)

	_, err = tx.Exec(ctx, insertOutboxQuery,
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

func (r *TenantBucketRepoImpl) Delete(ctx context.Context, id uuid.UUID, outbox *storageEntity.StorageOutboxRecord) error {
	// [COMMENT]: Map outbox record sang model DB
	mo := storageModel.OutboxEntityToModel(outbox)

	// [COMMENT]: Sử dụng single atomic CTE để kiểm tra tồn tại (SELECT FOR UPDATE) và chèn outbox record đồng thời
	query := fmt.Sprintf(`
		WITH locked_bucket AS (
			SELECT id
			FROM %s.tenant_buckets
			WHERE id = $1
			FOR UPDATE
		)
		INSERT INTO %s.storage_outbox_records (
			event_id, zone_id, job_topic, payload, owner_id, owner_type, status, completed_at,
			job_version, resource_id, payload_schema_version, trace_id, idle,
			error_code, error_message, actor_user_id
		)
		SELECT $2, $3, $4, $5, $6, $16, $7, $8, $9, $10, $11, $12, $13, $14, $15, $17
		FROM locked_bucket
	`, r.storage, r.storage)

	res, err := r.db.Exec(ctx, query,
		id,
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
	)

	if err != nil {
		return fmt.Errorf("storage repo: atomic delete tenant bucket outbox failed: %w", err)
	}

	// [COMMENT]: Nếu không có dòng nào bị ảnh hưởng (tức locked_bucket rỗng), trả về ErrNotFound
	if res.RowsAffected() == 0 {
		return storageTaxonomy.ErrNotFound
	}

	return nil
}

func (r *TenantBucketRepoImpl) ListAccessKeys(ctx context.Context, bucketID uuid.UUID) ([]string, error) {
	// [COMMENT]: Truy vấn danh sách access_key của toàn bộ credentials thuộc bucket chỉ định của tenant
	query := fmt.Sprintf(`
		SELECT access_key
		FROM %s.tenant_credentials
		WHERE bucket_id = $1
	`, r.storage)

	rows, err := r.db.Query(ctx, query, bucketID)
	if err != nil {
		return nil, fmt.Errorf("storage repo: query tenant access keys failed: %w", err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("storage repo: scan tenant access key failed: %w", err)
		}
		keys = append(keys, key)
	}
	return keys, nil
}
