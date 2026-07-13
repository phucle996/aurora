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

// [COMMENT]: PersonalBucketRepoImpl thực thi interface PersonalBucketRepo cho kết nối PostgreSQL.
type PersonalBucketRepoImpl struct {
	db        *pgxpool.Pool
	storage   string // schema storage
	hierarchy string // schema hierarchy
}

// [COMMENT]: NewPersonalBucketRepo khởi tạo repository quản lý bucket cá nhân.
func NewPersonalBucketRepo(
	db *pgxpool.Pool,
	cfg *config.Config,
) storageRepoInterface.PersonalBucketRepo {
	return &PersonalBucketRepoImpl{
		db:        db,
		storage:   cfg.SchemaSQL.Storage,
		hierarchy: cfg.SchemaSQL.Hierarchy,
	}
}

func (r *PersonalBucketRepoImpl) Create(ctx context.Context, bucket *storageEntity.PersonalBucket, workspaceID uuid.UUID, zoneID uuid.UUID, credential *storageEntity.PersonalCredential, outbox *storageEntity.StorageOutboxRecord) error {
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
				id, name, workspace_id, zone_id, capacity_quota_bytes, created_at, updated_at
			)
			SELECT $1, $2, $3, $4, $5, $6, $7
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
			event_id, routing_scope, job_topic, payload, user_id, status, completed_at,
			job_version, resource_id, payload_schema_version, trace_id, idle,
			error_code, error_message
		)
		SELECT $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26
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
	// [COMMENT]: Nếu RowsAffected == 0 tức là workspace không tồn tại hoặc không thuộc sở hữu của userID ($17)
	if res.RowsAffected() == 0 {
		return storageTaxonomy.ErrNotFound
	}
	return nil
}

func (r *PersonalBucketRepoImpl) GetByID(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*storageEntity.PersonalBucket, error) {
	query := fmt.Sprintf(`
		SELECT b.id, b.name, b.capacity_quota_bytes, b.used_bytes, b.created_at, b.updated_at
		FROM %s.personal_buckets b
		JOIN %s.personal_workspaces w ON b.workspace_id = w.id
		WHERE b.id = $1 AND w.owner_id = $2
	`, r.storage, r.hierarchy)

	var b storageEntity.PersonalBucket

	err := r.db.QueryRow(ctx, query, id, userID).Scan(
		&b.ID,
		&b.Name,
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
		SELECT id, name, capacity_quota_bytes, used_bytes, created_at, updated_at
		FROM %s.personal_buckets
		WHERE name = $1
	`, r.storage)

	var b storageEntity.PersonalBucket

	err := r.db.QueryRow(ctx, query, name).Scan(
		&b.ID,
		&b.Name,
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
		SELECT b.id, b.name, b.capacity_quota_bytes, b.used_bytes, b.created_at, b.updated_at
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

func (r *PersonalBucketRepoImpl) UpdateQuota(ctx context.Context, id uuid.UUID, userID uuid.UUID, quotaBytes int64) error {
	query := fmt.Sprintf(`
		UPDATE %s.personal_buckets b
		SET capacity_quota_bytes = $1, updated_at = $2
		FROM %s.personal_workspaces w
		WHERE b.id = $3 AND b.workspace_id = w.id AND w.owner_id = $4
	`, r.storage, r.hierarchy)

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
		USING %s.personal_workspaces w
		WHERE b.id = $1 AND b.workspace_id = w.id AND w.owner_id = $2
	`, r.storage, r.hierarchy)

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
