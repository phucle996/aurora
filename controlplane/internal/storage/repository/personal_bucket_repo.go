package storageRepoImpl

import (
	"context"
	"errors"
	"fmt"
	"time"

	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageModel "controlplane/internal/storage/model"
	storageTaxonomy "controlplane/internal/storage/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// [COMMENT]: PersonalBucketRepoImpl thực thi interface PersonalBucketRepo cho kết nối PostgreSQL.
type PersonalBucketRepoImpl struct {
	db     *pgxpool.Pool
	schema string
}

// [COMMENT]: NewPersonalBucketRepo khởi tạo repository quản lý bucket cá nhân.
func NewPersonalBucketRepo(
	db *pgxpool.Pool,
	schema string,
) storageRepoInterface.PersonalBucketRepo {
	return &PersonalBucketRepoImpl{
		db:     db,
		schema: schema,
	}
}

func (r *PersonalBucketRepoImpl) Create(ctx context.Context, bucket *storageEntity.PersonalBucket, credential *storageEntity.PersonalCredential, outbox *storageEntity.StorageOutboxRecord) error {
	// [COMMENT]: Convert Entity sang Model chứa các tag db
	m := storageModel.PersonalBucketEntityToModel(bucket)
	mc := storageModel.PersonalCredentialEntityToModel(credential)
	mo := storageModel.OutboxEntityToModel(outbox)

	// [COMMENT]: CTE 3-way check ownership: insert nguyên tử bucket + credential + outbox record chỉ khi workspace thuộc về user_id ($19).
	query := fmt.Sprintf(`
		WITH check_workspace AS (
			SELECT 1 FROM hierarchy.personal_workspaces WHERE id = $3 AND owner_id = $19
		),
		ins_bucket AS (
			INSERT INTO %s.personal_buckets (
				id, name, workspace_id, zone_id, status, capacity_quota_bytes, created_at, updated_at
			)
			SELECT $1, $2, $3, $4, $5, $6, $7, $8
			FROM check_workspace
			RETURNING id
		),
		ins_credential AS (
			INSERT INTO %s.personal_credentials (
				id, bucket_id, access_key, secret_key, policy, created_at, updated_at
			)
			SELECT $9, id, $10, $11, $12, $13, $14
			FROM ins_bucket
		)
		INSERT INTO %s.storage_outbox_records (
			event_id, routing_scope, job_topic, payload, user_id, status, completed_at,
			job_version, resource_id, payload_schema_version, trace_id, idle,
			error_code, error_message
		)
		SELECT $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28
		FROM ins_bucket
	`, r.schema, r.schema, r.schema)

	res, err := r.db.Exec(ctx, query,
		// [COMMENT]: $1-$8 — personal_buckets fields
		m.ID,
		m.Name,
		m.WorkspaceID,
		m.ZoneID,
		m.Status,
		m.CapacityQuotaBytes,
		m.CreatedAt,
		m.UpdatedAt,
		// [COMMENT]: $9-$14 — personal_credentials fields
		mc.ID,
		mc.AccessKey,
		mc.SecretKey,
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
		return fmt.Errorf("storage repo: create personal bucket failed: %w", err)
	}
	// [COMMENT]: Nếu RowsAffected == 0 tức là workspace không tồn tại hoặc không thuộc sở hữu của userID ($19)
	if res.RowsAffected() == 0 {
		return storageTaxonomy.ErrNotFound
	}
	return nil
}

func (r *PersonalBucketRepoImpl) GetByID(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*storageEntity.PersonalBucket, error) {
	query := fmt.Sprintf(`
		SELECT b.id, b.name, b.workspace_id, b.zone_id, b.status, b.capacity_quota_bytes, b.used_bytes, b.created_at, b.updated_at
		FROM %s.personal_buckets b
		JOIN hierarchy.personal_workspaces w ON b.workspace_id = w.id
		WHERE b.id = $1 AND w.owner_id = $2
	`, r.schema)

	var m storageModel.PersonalBucket

	err := r.db.QueryRow(ctx, query, id, userID).Scan(
		&m.ID,
		&m.Name,
		&m.WorkspaceID,
		&m.ZoneID,
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
		return nil, fmt.Errorf("storage repo: get personal bucket by id failed: %w", err)
	}

	// [COMMENT]: Trả về Domain Entity chuyển đổi từ DB Model
	return storageModel.PersonalBucketModelToEntity(&m), nil
}

func (r *PersonalBucketRepoImpl) GetByName(ctx context.Context, name string) (*storageEntity.PersonalBucket, error) {
	query := fmt.Sprintf(`
		SELECT id, name, workspace_id, zone_id, status, capacity_quota_bytes, used_bytes, created_at, updated_at
		FROM %s.personal_buckets
		WHERE name = $1
	`, r.schema)

	var m storageModel.PersonalBucket

	err := r.db.QueryRow(ctx, query, name).Scan(
		&m.ID,
		&m.Name,
		&m.WorkspaceID,
		&m.ZoneID,
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
		return nil, fmt.Errorf("storage repo: get personal bucket by name failed: %w", err)
	}

	// [COMMENT]: Trả về Domain Entity chuyển đổi từ DB Model
	return storageModel.PersonalBucketModelToEntity(&m), nil
}
func (r *PersonalBucketRepoImpl) ListByWorkspace(ctx context.Context, workspaceID uuid.UUID, zoneID uuid.UUID, userID uuid.UUID) ([]*storageEntity.PersonalBucket, error) {
	query := fmt.Sprintf(`
		SELECT b.id, b.name, b.status, b.capacity_quota_bytes, b.used_bytes, b.created_at, b.updated_at
		FROM %s.personal_buckets b
		JOIN hierarchy.personal_workspaces w ON b.workspace_id = w.id
		WHERE b.workspace_id = $1 AND b.zone_id = $2 AND w.owner_id = $3 AND w.zone_id = $2
		ORDER BY b.created_at DESC
	`, r.schema)

	rows, err := r.db.Query(ctx, query, workspaceID, zoneID, userID)
	if err != nil {
		return nil, fmt.Errorf("storage repo: list personal buckets by workspace failed: %w", err)
	}
	defer rows.Close()

	var buckets []*storageEntity.PersonalBucket
	for rows.Next() {
		var m storageModel.PersonalBucket

		err := rows.Scan(
			&m.ID,
			&m.Name,
			&m.Status,
			&m.CapacityQuotaBytes,
			&m.UsedBytes,
			&m.CreatedAt,
			&m.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("storage repo: scan personal bucket row failed: %w", err)
		}
		buckets = append(buckets, storageModel.PersonalBucketModelToEntity(&m))
	}

	return buckets, nil
}

func (r *PersonalBucketRepoImpl) UpdateStatus(ctx context.Context, id uuid.UUID, userID uuid.UUID, status storageEntity.BucketStatus) error {
	query := fmt.Sprintf(`
		UPDATE %s.personal_buckets b
		SET status = $1, updated_at = $2
		FROM hierarchy.personal_workspaces w
		WHERE b.id = $3 AND b.workspace_id = w.id AND w.owner_id = $4
	`, r.schema)

	res, err := r.db.Exec(ctx, query, string(status), time.Now(), id, userID)
	if err != nil {
		return fmt.Errorf("storage repo: update personal bucket status failed: %w", err)
	}
	// [COMMENT]: Nếu không có bản ghi nào bị tác động thì trả về ErrNotFound
	if res.RowsAffected() == 0 {
		return storageTaxonomy.ErrNotFound
	}
	return nil
}

func (r *PersonalBucketRepoImpl) UpdateQuota(ctx context.Context, id uuid.UUID, userID uuid.UUID, quotaBytes int64) error {
	query := fmt.Sprintf(`
		UPDATE %s.personal_buckets b
		SET capacity_quota_bytes = $1, updated_at = $2
		FROM hierarchy.personal_workspaces w
		WHERE b.id = $3 AND b.workspace_id = w.id AND w.owner_id = $4
	`, r.schema)

	res, err := r.db.Exec(ctx, query, quotaBytes, time.Now(), id, userID)
	if err != nil {
		return fmt.Errorf("storage repo: update personal bucket quota failed: %w", err)
	}
	// [COMMENT]: Nếu không có bản ghi nào bị tác động thì trả về ErrNotFound
	if res.RowsAffected() == 0 {
		return storageTaxonomy.ErrNotFound
	}
	return nil
}

func (r *PersonalBucketRepoImpl) Delete(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	query := fmt.Sprintf(`
		DELETE FROM %s.personal_buckets b
		USING hierarchy.personal_workspaces w
		WHERE b.id = $1 AND b.workspace_id = w.id AND w.owner_id = $2
	`, r.schema)

	res, err := r.db.Exec(ctx, query, id, userID)
	if err != nil {
		return fmt.Errorf("storage repo: delete personal bucket failed: %w", err)
	}
	// [COMMENT]: Nếu không có bản ghi nào bị tác động thì trả về ErrNotFound
	if res.RowsAffected() == 0 {
		return storageTaxonomy.ErrNotFound
	}
	return nil
}

func (r *PersonalBucketRepoImpl) UpdateUsedBytes(ctx context.Context, name string, usedBytes int64) error {
	query := fmt.Sprintf(`
		UPDATE %s.personal_buckets
		SET used_bytes = $1, updated_at = now()
		WHERE name = $2
	`, r.schema)

	res, err := r.db.Exec(ctx, query, usedBytes, name)
	if err != nil {
		return fmt.Errorf("storage repo: update personal bucket used_bytes failed: %w", err)
	}
	if res.RowsAffected() == 0 {
		return storageTaxonomy.ErrNotFound
	}
	return nil
}
