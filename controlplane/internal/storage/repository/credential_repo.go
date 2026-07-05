package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepo "controlplane/internal/storage/domain/repo"
)

// [COMMENT]: CredentialRepoImpl thực thi interface CredentialRepo kết nối PostgreSQL.
type CredentialRepoImpl struct {
	db *pgxpool.Pool
}

// [COMMENT]: Khẳng định CredentialRepoImpl tuân thủ trọn vẹn interface CredentialRepo.
var _ storageRepo.CredentialRepo = (*CredentialRepoImpl)(nil)

// [COMMENT]: NewCredentialRepo khởi tạo repository quản lý credentials.
func NewCredentialRepo(db *pgxpool.Pool) *CredentialRepoImpl {
	return &CredentialRepoImpl{
		db: db,
	}
}

func (r *CredentialRepoImpl) Create(ctx context.Context, cred *storageEntity.Credential) error {
	// [COMMENT]: SKELETON - Chưa cài đặt logic nghiệp vụ.
	return errors.New("method Create not implemented")
}

func (r *CredentialRepoImpl) GetByID(ctx context.Context, id uuid.UUID) (*storageEntity.Credential, error) {
	// [COMMENT]: SKELETON - Chưa cài đặt logic nghiệp vụ.
	return nil, errors.New("method GetByID not implemented")
}

func (r *CredentialRepoImpl) ListByBucket(ctx context.Context, bucketID uuid.UUID) ([]*storageEntity.Credential, error) {
	// [COMMENT]: SKELETON - Chưa cài đặt logic nghiệp vụ.
	return nil, errors.New("method ListByBucket not implemented")
}

func (r *CredentialRepoImpl) Delete(ctx context.Context, id uuid.UUID) error {
	// [COMMENT]: SKELETON - Chưa cài đặt logic nghiệp vụ.
	return errors.New("method Delete not implemented")
}
