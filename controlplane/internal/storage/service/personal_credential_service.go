package storageSvcImpl

import (
	"context"
	"errors"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageSvcInterface "controlplane/internal/storage/domain/service"
)

// [COMMENT]: PersonalCredentialServiceImpl thực thi nghiệp vụ quản lý tài khoản keys của MinIO cho cá nhân.
type PersonalCredentialServiceImpl struct {
	repo storageRepoInterface.PersonalCredentialRepo
}

// [COMMENT]: NewPersonalCredentialService tạo mới instance thực thi PersonalCredentialService.
func NewPersonalCredentialService(repo storageRepoInterface.PersonalCredentialRepo) storageSvcInterface.PersonalCredentialService {
	return &PersonalCredentialServiceImpl{
		repo: repo,
	}
}

func (s *PersonalCredentialServiceImpl) CreateCredential(ctx context.Context, bucketID uuid.UUID, policy string) (*storageEntity.PersonalCredential, error) {
	// [COMMENT]: SKELETON
	return nil, errors.New("method CreateCredential not implemented")
}

func (s *PersonalCredentialServiceImpl) GetCredential(ctx context.Context, credID uuid.UUID) (*storageEntity.PersonalCredential, error) {
	// [COMMENT]: SKELETON
	return nil, errors.New("method GetCredential not implemented")
}

func (s *PersonalCredentialServiceImpl) ListCredentials(ctx context.Context, bucketID uuid.UUID) ([]*storageEntity.PersonalCredential, error) {
	// [COMMENT]: SKELETON
	return nil, errors.New("method ListCredentials not implemented")
}

func (s *PersonalCredentialServiceImpl) RevokeCredential(ctx context.Context, credID uuid.UUID) error {
	// [COMMENT]: SKELETON
	return errors.New("method RevokeCredential not implemented")
}
