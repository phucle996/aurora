package storageSvcImpl

import (
	"context"
	"errors"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageSvcInterface "controlplane/internal/storage/domain/service"
)

// [COMMENT]: TenantCredentialServiceImpl thực thi nghiệp vụ quản lý tài khoản keys của MinIO cho doanh nghiệp.
type TenantCredentialServiceImpl struct {
	repo storageRepoInterface.TenantCredentialRepo
}

// [COMMENT]: NewTenantCredentialService tạo mới instance thực thi TenantCredentialService.
func NewTenantCredentialService(repo storageRepoInterface.TenantCredentialRepo) storageSvcInterface.TenantCredentialService {
	return &TenantCredentialServiceImpl{
		repo: repo,
	}
}

func (s *TenantCredentialServiceImpl) CreateCredential(ctx context.Context, bucketID uuid.UUID, policy string) (*storageEntity.TenantCredential, error) {
	// [COMMENT]: SKELETON
	return nil, errors.New("method CreateCredential not implemented")
}

func (s *TenantCredentialServiceImpl) GetCredential(ctx context.Context, credID uuid.UUID) (*storageEntity.TenantCredential, error) {
	// [COMMENT]: SKELETON
	return nil, errors.New("method GetCredential not implemented")
}

func (s *TenantCredentialServiceImpl) ListCredentials(ctx context.Context, bucketID uuid.UUID) ([]*storageEntity.TenantCredential, error) {
	// [COMMENT]: SKELETON
	return nil, errors.New("method ListCredentials not implemented")
}

func (s *TenantCredentialServiceImpl) RevokeCredential(ctx context.Context, credID uuid.UUID) error {
	// [COMMENT]: SKELETON
	return errors.New("method RevokeCredential not implemented")
}
