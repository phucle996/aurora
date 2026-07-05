package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepo "controlplane/internal/storage/domain/repo"
	storageSvc "controlplane/internal/storage/domain/service"
)

// [COMMENT]: BucketServiceImpl thực thi nghiệp vụ quản trị Storage Bucket.
type BucketServiceImpl struct {
	repo storageRepo.BucketRepo
	// [COMMENT]: Thêm client kết nối KMS/Vault hoặc MinIO Gateway ở đây khi implement thực tế.
}

// [COMMENT]: Đảm bảo BucketServiceImpl tuân thủ nghiêm ngặt interface BucketService.
var _ storageSvc.BucketService = (*BucketServiceImpl)(nil)

// [COMMENT]: NewBucketService khởi tạo instance thực thi BucketService.
func NewBucketService(repo storageRepo.BucketRepo) *BucketServiceImpl {
	return &BucketServiceImpl{
		repo: repo,
	}
}

func (s *BucketServiceImpl) CreateBucket(ctx context.Context, tenantID uuid.UUID, zoneID uuid.UUID, name string, quotaBytes int64) (*storageEntity.Bucket, error) {
	// [COMMENT]: SKELETON - Chưa cài đặt logic nghiệp vụ.
	return nil, errors.New("method CreateBucket not implemented")
}

func (s *BucketServiceImpl) GetBucket(ctx context.Context, bucketID uuid.UUID) (*storageEntity.Bucket, error) {
	// [COMMENT]: SKELETON - Chưa cài đặt logic nghiệp vụ.
	return nil, errors.New("method GetBucket not implemented")
}

func (s *BucketServiceImpl) ListBuckets(ctx context.Context, tenantID uuid.UUID, zoneID uuid.UUID) ([]*storageEntity.Bucket, error) {
	// [COMMENT]: SKELETON - Chưa cài đặt logic nghiệp vụ.
	return nil, errors.New("method ListBuckets not implemented")
}

func (s *BucketServiceImpl) UpdateBucketQuota(ctx context.Context, bucketID uuid.UUID, quotaBytes int64) error {
	// [COMMENT]: SKELETON - Chưa cài đặt logic nghiệp vụ.
	return errors.New("method UpdateBucketQuota not implemented")
}

func (s *BucketServiceImpl) SuspendBucket(ctx context.Context, bucketID uuid.UUID) error {
	// [COMMENT]: SKELETON - Chưa cài đặt logic nghiệp vụ.
	return errors.New("method SuspendBucket not implemented")
}

func (s *BucketServiceImpl) ResumeBucket(ctx context.Context, bucketID uuid.UUID) error {
	// [COMMENT]: SKELETON - Chưa cài đặt logic nghiệp vụ.
	return errors.New("method ResumeBucket not implemented")
}

func (s *BucketServiceImpl) DeleteBucket(ctx context.Context, bucketID uuid.UUID) error {
	// [COMMENT]: SKELETON - Chưa cài đặt logic nghiệp vụ.
	return errors.New("method DeleteBucket not implemented")
}
