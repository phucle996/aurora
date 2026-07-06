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

// [COMMENT]: PersonalBucketServiceImpl thực thi nghiệp vụ quản trị Storage Bucket cho đối tượng Cá nhân.
type PersonalBucketServiceImpl struct {
	repo storageRepoInterface.PersonalBucketRepo
}

// [COMMENT]: NewPersonalBucketService khởi tạo instance thực thi PersonalBucketService.
func NewPersonalBucketService(repo storageRepoInterface.PersonalBucketRepo) storageSvcInterface.PersonalBucketService {
	return &PersonalBucketServiceImpl{
		repo: repo,
	}
}

func (s *PersonalBucketServiceImpl) CreateBucketForPersonal(
	ctx context.Context,
	userID uuid.UUID,
	workspaceID uuid.UUID,
	zoneID uuid.UUID,
	name string,
	quotaBytes int64,
) error {
	// [COMMENT]: Sinh ID ngẫu nhiên cho Bucket cá nhân
	bucket := &storageEntity.PersonalBucket{
		ID:                 uuid.New(),
		Name:               name,
		WorkspaceID:        workspaceID,
		ZoneID:             zoneID,
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

func (s *PersonalBucketServiceImpl) GetBucket(ctx context.Context, bucketID uuid.UUID) (*storageEntity.PersonalBucket, error) {
	bucket, err := s.repo.GetByID(ctx, bucketID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "get_failed")
	}
	return bucket, nil
}

func (s *PersonalBucketServiceImpl) ListBuckets(ctx context.Context, workspaceID uuid.UUID) ([]*storageEntity.PersonalBucket, error) {
	// [COMMENT]: Liệt kê các bucket theo workspace_id cho luồng cá nhân
	buckets, err := s.repo.ListByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "list_failed")
	}
	return buckets, nil
}

func (s *PersonalBucketServiceImpl) UpdateBucketQuota(ctx context.Context, bucketID uuid.UUID, quotaBytes int64) error {
	err := s.repo.UpdateQuota(ctx, bucketID, quotaBytes)
	if err != nil {
		return apperr.Wrap(err, err, "update_quota_failed")
	}
	return nil
}

func (s *PersonalBucketServiceImpl) SuspendBucket(ctx context.Context, bucketID uuid.UUID) error {
	err := s.repo.UpdateStatus(ctx, bucketID, storageEntity.BucketStatusSuspended)
	if err != nil {
		return apperr.Wrap(err, err, "suspend_failed")
	}
	return nil
}

func (s *PersonalBucketServiceImpl) ResumeBucket(ctx context.Context, bucketID uuid.UUID) error {
	err := s.repo.UpdateStatus(ctx, bucketID, storageEntity.BucketStatusActive)
	if err != nil {
		return apperr.Wrap(err, err, "resume_failed")
	}
	return nil
}

func (s *PersonalBucketServiceImpl) DeleteBucket(ctx context.Context, bucketID uuid.UUID) error {
	err := s.repo.Delete(ctx, bucketID)
	if err != nil {
		return apperr.Wrap(err, err, "delete_failed")
	}
	return nil
}
