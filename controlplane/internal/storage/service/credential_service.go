package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepo "controlplane/internal/storage/domain/repo"
	storageSvc "controlplane/internal/storage/domain/service"
)

// [COMMENT]: CredentialServiceImpl thực thi nghiệp vụ quản lý tài khoản keys của MinIO.
type CredentialServiceImpl struct {
	repo storageRepo.CredentialRepo
}

// [COMMENT]: Khẳng định CredentialServiceImpl implement trọn vẹn interface CredentialService.
var _ storageSvc.CredentialService = (*CredentialServiceImpl)(nil)

// [COMMENT]: NewCredentialService tạo mới instance thực thi CredentialService.
func NewCredentialService(repo storageRepo.CredentialRepo) *CredentialServiceImpl {
	return &CredentialServiceImpl{
		repo: repo,
	}
}

func (s *CredentialServiceImpl) CreateCredential(ctx context.Context, bucketID uuid.UUID, policy string) (*storageEntity.Credential, error) {
	// [COMMENT]: SKELETON - Chưa cài đặt logic nghiệp vụ.
	return nil, errors.New("method CreateCredential not implemented")
}

func (s *CredentialServiceImpl) GetCredential(ctx context.Context, credID uuid.UUID) (*storageEntity.Credential, error) {
	// [COMMENT]: SKELETON - Chưa cài đặt logic nghiệp vụ.
	return nil, errors.New("method GetCredential not implemented")
}

func (s *CredentialServiceImpl) ListCredentials(ctx context.Context, bucketID uuid.UUID) ([]*storageEntity.Credential, error) {
	// [COMMENT]: SKELETON - Chưa cài đặt logic nghiệp vụ.
	return nil, errors.New("method ListCredentials not implemented")
}

func (s *CredentialServiceImpl) RevokeCredential(ctx context.Context, credID uuid.UUID) error {
	// [COMMENT]: SKELETON - Chưa cài đặt logic nghiệp vụ.
	return errors.New("method RevokeCredential not implemented")
}
