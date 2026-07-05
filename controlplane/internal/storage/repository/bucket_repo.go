package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepo "controlplane/internal/storage/domain/repo"
)

// [COMMENT]: BucketRepoImpl thực thi interface BucketRepo kết nối PostgreSQL.
type BucketRepoImpl struct {
	db *pgxpool.Pool
}

// [COMMENT]: Khẳng định BucketRepoImpl tuân thủ trọn vẹn interface BucketRepo.
var _ storageRepo.BucketRepo = (*BucketRepoImpl)(nil)

// [COMMENT]: NewBucketRepo khởi tạo repository quản lý bucket.
func NewBucketRepo(db *pgxpool.Pool) *BucketRepoImpl {
	return &BucketRepoImpl{
		db: db,
	}
}

func (r *BucketRepoImpl) Create(ctx context.Context, bucket *storageEntity.Bucket) error {
	// [COMMENT]: SKELETON - Chưa cài đặt logic nghiệp vụ.
	return errors.New("method Create not implemented")
}

func (r *BucketRepoImpl) GetByID(ctx context.Context, id uuid.UUID) (*storageEntity.Bucket, error) {
	// [COMMENT]: SKELETON - Chưa cài đặt logic nghiệp vụ.
	return nil, errors.New("method GetByID not implemented")
}

func (r *BucketRepoImpl) GetByName(ctx context.Context, name string) (*storageEntity.Bucket, error) {
	// [COMMENT]: SKELETON - Chưa cài đặt logic nghiệp vụ.
	return nil, errors.New("method GetByName not implemented")
}

func (r *BucketRepoImpl) ListByTenantAndZone(ctx context.Context, tenantID uuid.UUID, zoneID uuid.UUID) ([]*storageEntity.Bucket, error) {
	// [COMMENT]: SKELETON - Chưa cài đặt logic nghiệp vụ.
	return nil, errors.New("method ListByTenantAndZone not implemented")
}

func (r *BucketRepoImpl) UpdateStatus(ctx context.Context, id uuid.UUID, status storageEntity.BucketStatus) error {
	// [COMMENT]: SKELETON - Chưa cài đặt logic nghiệp vụ.
	return errors.New("method UpdateStatus not implemented")
}

func (r *BucketRepoImpl) UpdateQuota(ctx context.Context, id uuid.UUID, quotaBytes int64) error {
	// [COMMENT]: SKELETON - Chưa cài đặt logic nghiệp vụ.
	return errors.New("method UpdateQuota not implemented")
}

func (r *BucketRepoImpl) Delete(ctx context.Context, id uuid.UUID) error {
	// [COMMENT]: SKELETON - Chưa cài đặt logic nghiệp vụ.
	return errors.New("method Delete not implemented")
}
