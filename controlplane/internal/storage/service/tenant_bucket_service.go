package storageSvcImpl

import (
	"context"
	"time"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageSvcInterface "controlplane/internal/storage/domain/service"
	"controlplane/pkg/apperr"
)

// [COMMENT]: TenantBucketServiceImpl thực thi nghiệp vụ quản trị Storage Bucket cho đối tượng Doanh nghiệp.
type TenantBucketServiceImpl struct {
	repo storageRepoInterface.TenantBucketRepo
}

// [COMMENT]: NewTenantBucketService khởi tạo instance thực thi TenantBucketService.
func NewTenantBucketService(repo storageRepoInterface.TenantBucketRepo) storageSvcInterface.TenantBucketService {
	return &TenantBucketServiceImpl{
		repo: repo,
	}
}

func (s *TenantBucketServiceImpl) CreateBucketForTenant(
	ctx context.Context,
	tenantID uuid.UUID,
	workspaceID uuid.UUID,
	zoneID uuid.UUID,
	name string,
	quotaBytes int64,
) error {
	// [COMMENT]: Sinh ID ngẫu nhiên cho Bucket
	bucket := &storageEntity.TenantBucket{
		ID:                 uuid.New(),
		Name:               name,
		WorkspaceID:        workspaceID,
		ZoneID:             zoneID,
		TenantID:           tenantID,
		Status:             storageEntity.BucketStatusActive,
		CapacityQuotaBytes: quotaBytes,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	// [COMMENT]: Gọi DB ghi nhận metadata bucket. Lỗi trùng lặp key (trùng tên)
	// sẽ được phát hiện ở tầng DB Unique Index và Repo trả về ErrAlreadyExists.
	if err := s.repo.Create(ctx, bucket); err != nil {
		return apperr.Wrap(err, err, "create_failed")
	}

	return nil
}

func (s *TenantBucketServiceImpl) GetBucket(ctx context.Context, bucketID uuid.UUID) (*storageEntity.TenantBucket, error) {
	bucket, err := s.repo.GetByID(ctx, bucketID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "get_failed")
	}
	return bucket, nil
}

func (s *TenantBucketServiceImpl) ListBuckets(ctx context.Context, tenantID uuid.UUID, zoneID uuid.UUID) ([]*storageEntity.TenantBucket, error) {
	buckets, err := s.repo.ListByTenantAndZone(ctx, tenantID, zoneID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "list_failed")
	}
	return buckets, nil
}

func (s *TenantBucketServiceImpl) UpdateBucketQuota(ctx context.Context, bucketID uuid.UUID, quotaBytes int64) error {
	err := s.repo.UpdateQuota(ctx, bucketID, quotaBytes)
	if err != nil {
		return apperr.Wrap(err, err, "update_quota_failed")
	}
	return nil
}

func (s *TenantBucketServiceImpl) SuspendBucket(ctx context.Context, bucketID uuid.UUID) error {
	err := s.repo.UpdateStatus(ctx, bucketID, storageEntity.BucketStatusSuspended)
	if err != nil {
		return apperr.Wrap(err, err, "suspend_failed")
	}
	return nil
}

func (s *TenantBucketServiceImpl) ResumeBucket(ctx context.Context, bucketID uuid.UUID) error {
	err := s.repo.UpdateStatus(ctx, bucketID, storageEntity.BucketStatusActive)
	if err != nil {
		return apperr.Wrap(err, err, "resume_failed")
	}
	return nil
}

func (s *TenantBucketServiceImpl) DeleteBucket(ctx context.Context, bucketID uuid.UUID) error {
	err := s.repo.Delete(ctx, bucketID)
	if err != nil {
		return apperr.Wrap(err, err, "delete_failed")
	}
	return nil
}
