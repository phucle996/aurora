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
	m := storageModel.TenantBucketEntityToModel(bucket)
	mc := storageModel.TenantCredentialEntityToModel(credential)
	mo := storageModel.OutboxEntityToModel(outbox)

	// [COMMENT]: CTE 3-way: insert nguyên tử (atomic) bucket + credential + outbox record.
	// Dùng RETURNING id để thread credential và outbox vào cùng bucket_id vừa insert.
	query := fmt.Sprintf(`
		WITH ins_bucket AS (
			INSERT INTO %s.tenant_buckets (
				id, name, workspace_id, zone_id, tenant_id, status, capacity_quota_bytes, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			RETURNING id
		),
		ins_credential AS (
			INSERT INTO %s.tenant_credentials (
				id, bucket_id, access_key, policy, created_at, updated_at
			)
			SELECT $10, id, $11, $12, $13, $14
			FROM ins_bucket
		)
		INSERT INTO %s.storage_outbox_records (
			event_id, routing_scope, job_topic, payload, user_id, status, completed_at,
			job_version, resource_id, payload_schema_version, trace_id, idle,
			error_code, error_message
		) VALUES ($15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28)
	`, r.storage, r.storage, r.storage)

	_, err := r.db.Exec(ctx, query,
		// [COMMENT]: $1-$9 — tenant_buckets fields
		m.ID,
		m.Name,
		m.WorkspaceID,
		m.ZoneID,
		m.TenantID,
		m.Status,
		m.CapacityQuotaBytes,
		m.CreatedAt,
		m.UpdatedAt,
		// [COMMENT]: $10-$14 — tenant_credentials fields (secret_key removed)
		mc.ID,
		mc.AccessKey,
		mc.Policy,
		mc.CreatedAt,
		mc.UpdatedAt,
		// [COMMENT]: $15-$28 — storage_outbox_records fields
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
		SELECT id, name, workspace_id, zone_id, tenant_id, status, capacity_quota_bytes, used_bytes, created_at, updated_at
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
		&m.Status,
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

	// [COMMENT]: Trả về Domain Entity chuyển đổi từ DB Model
	return storageModel.TenantBucketModelToEntity(&m), nil
}

func (r *TenantBucketRepoImpl) GetByName(ctx context.Context, name string) (*storageEntity.TenantBucket, error) {
	query := fmt.Sprintf(`
		SELECT id, name, workspace_id, zone_id, tenant_id, status, capacity_quota_bytes, used_bytes, created_at, updated_at
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
		&m.Status,
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

	// [COMMENT]: Trả về Domain Entity chuyển đổi từ DB Model
	return storageModel.TenantBucketModelToEntity(&m), nil
}

func (r *TenantBucketRepoImpl) ListByTenantAndZone(ctx context.Context, tenantID uuid.UUID, zoneID uuid.UUID) ([]*storageEntity.TenantBucket, error) {
	query := fmt.Sprintf(`
		SELECT id, name, workspace_id, zone_id, tenant_id, status, capacity_quota_bytes, used_bytes, created_at, updated_at
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
			&m.Status,
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

func (r *TenantBucketRepoImpl) UpdateStatus(ctx context.Context, id uuid.UUID, status storageEntity.BucketStatus) error {
	query := fmt.Sprintf(`
		UPDATE %s.tenant_buckets
		SET status = $1, updated_at = $2
		WHERE id = $3
	`, r.storage)

	res, err := r.db.Exec(ctx, query, string(status), time.Now(), id)
	if err != nil {
		return fmt.Errorf("storage repo: update tenant bucket status failed: %w", err)
	}
	// [COMMENT]: Nếu không có bản ghi nào bị tác động thì trả về ErrNotFound
	if res.RowsAffected() == 0 {
		return storageTaxonomy.ErrNotFound
	}
	return nil
}

func (r *TenantBucketRepoImpl) UpdateQuota(ctx context.Context, id uuid.UUID, quotaBytes int64) error {
	query := fmt.Sprintf(`
		UPDATE %s.tenant_buckets
		SET capacity_quota_bytes = $1, updated_at = $2
		WHERE id = $3
	`, r.storage)

	res, err := r.db.Exec(ctx, query, quotaBytes, time.Now(), id)
	if err != nil {
		return fmt.Errorf("storage repo: update tenant bucket quota failed: %w", err)
	}
	// [COMMENT]: Nếu không có bản ghi nào bị tác động thì trả về ErrNotFound
	if res.RowsAffected() == 0 {
		return storageTaxonomy.ErrNotFound
	}
	return nil
}

func (r *TenantBucketRepoImpl) Delete(ctx context.Context, id uuid.UUID) error {
	query := fmt.Sprintf(`
		DELETE FROM %s.tenant_buckets
		WHERE id = $1
	`, r.storage)

	res, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("storage repo: delete tenant bucket failed: %w", err)
	}
	// [COMMENT]: Nếu không có bản ghi nào bị tác động thì trả về ErrNotFound
	if res.RowsAffected() == 0 {
		return storageTaxonomy.ErrNotFound
	}
	return nil
}

func (r *TenantBucketRepoImpl) UpdateUsedBytes(ctx context.Context, name string, usedBytes int64) error {
	query := fmt.Sprintf(`
		UPDATE %s.tenant_buckets
		SET used_bytes = $1, updated_at = now()
		WHERE name = $2
	`, r.storage)

	res, err := r.db.Exec(ctx, query, usedBytes, name)
	if err != nil {
		return fmt.Errorf("storage repo: update tenant bucket used_bytes failed: %w", err)
	}
	if res.RowsAffected() == 0 {
		return storageTaxonomy.ErrNotFound
	}
	return nil
}
