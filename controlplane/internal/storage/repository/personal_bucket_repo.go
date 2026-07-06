package storageRepoImpl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageModel "controlplane/internal/storage/model"
	storageTaxonomy "controlplane/internal/storage/taxonomy"
)

// [COMMENT]: PersonalBucketRepoImpl thực thi interface PersonalBucketRepo cho kết nối PostgreSQL.
type PersonalBucketRepoImpl struct {
	db     *pgxpool.Pool
	schema string
}

// [COMMENT]: NewPersonalBucketRepo khởi tạo repository quản lý bucket cá nhân.
func NewPersonalBucketRepo(db *pgxpool.Pool, schema string) storageRepoInterface.PersonalBucketRepo {
	return &PersonalBucketRepoImpl{
		db:     db,
		schema: schema,
	}
}

func (r *PersonalBucketRepoImpl) Create(ctx context.Context, bucket *storageEntity.PersonalBucket) error {
	// [COMMENT]: Convert Entity sang Model chứa các tag db
	m := storageModel.PersonalBucketEntityToModel(bucket)
	query := fmt.Sprintf(`
		INSERT INTO %s.personal_buckets (
			id, name, workspace_id, zone_id, status, capacity_quota_bytes, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, r.schema)

	now := time.Now()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = now
	}

	_, err := r.db.Exec(ctx, query,
		m.ID,
		m.Name,
		m.WorkspaceID,
		m.ZoneID,
		m.Status,
		m.CapacityQuotaBytes,
		m.CreatedAt,
		m.UpdatedAt,
	)
	if err != nil {
		// [COMMENT]: Bắt lỗi trùng lặp mã Key (Unique Constraint 23505) và ánh xạ sang lỗi domain ErrAlreadyExists
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return storageTaxonomy.ErrAlreadyExists
		}
		return fmt.Errorf("storage repo: create personal bucket failed: %w", err)
	}
	return nil
}

func (r *PersonalBucketRepoImpl) GetByID(ctx context.Context, id uuid.UUID) (*storageEntity.PersonalBucket, error) {
	query := fmt.Sprintf(`
		SELECT id, name, workspace_id, zone_id, status, capacity_quota_bytes, created_at, updated_at
		FROM %s.personal_buckets
		WHERE id = $1
	`, r.schema)

	var m storageModel.PersonalBucket

	err := r.db.QueryRow(ctx, query, id).Scan(
		&m.ID,
		&m.Name,
		&m.WorkspaceID,
		&m.ZoneID,
		&m.Status,
		&m.CapacityQuotaBytes,
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
		SELECT id, name, workspace_id, zone_id, status, capacity_quota_bytes, created_at, updated_at
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

func (r *PersonalBucketRepoImpl) ListByWorkspace(ctx context.Context, workspaceID uuid.UUID) ([]*storageEntity.PersonalBucket, error) {
	query := fmt.Sprintf(`
		SELECT id, name, workspace_id, zone_id, status, capacity_quota_bytes, created_at, updated_at
		FROM %s.personal_buckets
		WHERE workspace_id = $1
		ORDER BY created_at DESC
	`, r.schema)

	rows, err := r.db.Query(ctx, query, workspaceID)
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
			&m.WorkspaceID,
			&m.ZoneID,
			&m.Status,
			&m.CapacityQuotaBytes,
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

func (r *PersonalBucketRepoImpl) UpdateStatus(ctx context.Context, id uuid.UUID, status storageEntity.BucketStatus) error {
	query := fmt.Sprintf(`
		UPDATE %s.personal_buckets
		SET status = $1, updated_at = $2
		WHERE id = $3
	`, r.schema)

	res, err := r.db.Exec(ctx, query, string(status), time.Now(), id)
	if err != nil {
		return fmt.Errorf("storage repo: update personal bucket status failed: %w", err)
	}
	// [COMMENT]: Nếu không có bản ghi nào bị tác động thì trả về ErrNotFound
	if res.RowsAffected() == 0 {
		return storageTaxonomy.ErrNotFound
	}
	return nil
}

func (r *PersonalBucketRepoImpl) UpdateQuota(ctx context.Context, id uuid.UUID, quotaBytes int64) error {
	query := fmt.Sprintf(`
		UPDATE %s.personal_buckets
		SET capacity_quota_bytes = $1, updated_at = $2
		WHERE id = $3
	`, r.schema)

	res, err := r.db.Exec(ctx, query, quotaBytes, time.Now(), id)
	if err != nil {
		return fmt.Errorf("storage repo: update personal bucket quota failed: %w", err)
	}
	// [COMMENT]: Nếu không có bản ghi nào bị tác động thì trả về ErrNotFound
	if res.RowsAffected() == 0 {
		return storageTaxonomy.ErrNotFound
	}
	return nil
}

func (r *PersonalBucketRepoImpl) Delete(ctx context.Context, id uuid.UUID) error {
	query := fmt.Sprintf(`
		DELETE FROM %s.personal_buckets
		WHERE id = $1
	`, r.schema)

	res, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("storage repo: delete personal bucket failed: %w", err)
	}
	// [COMMENT]: Nếu không có bản ghi nào bị tác động thì trả về ErrNotFound
	if res.RowsAffected() == 0 {
		return storageTaxonomy.ErrNotFound
	}
	return nil
}
