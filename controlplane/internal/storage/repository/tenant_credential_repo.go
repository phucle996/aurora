package storageRepoImpl

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageModel "controlplane/internal/storage/model"
)

// [COMMENT]: TenantCredentialRepoImpl thực thi interface TenantCredentialRepo kết nối PostgreSQL.
type TenantCredentialRepoImpl struct {
	db *pgxpool.Pool
}

// [COMMENT]: NewTenantCredentialRepo khởi tạo repository quản lý credentials cho bucket doanh nghiệp.
func NewTenantCredentialRepo(db *pgxpool.Pool) storageRepoInterface.TenantCredentialRepo {
	return &TenantCredentialRepoImpl{
		db: db,
	}
}

func (r *TenantCredentialRepoImpl) Create(ctx context.Context, cred *storageEntity.TenantCredential) error {
	m := storageModel.TenantCredentialEntityToModel(cred)
	_ = m // [COMMENT]: SKELETON - Sẽ sử dụng m khi thực thi SQL trên tenant_credentials
	return errors.New("method Create not implemented")
}

func (r *TenantCredentialRepoImpl) GetByID(ctx context.Context, id uuid.UUID) (*storageEntity.TenantCredential, error) {
	// [COMMENT]: SKELETON
	return nil, errors.New("method GetByID not implemented")
}

func (r *TenantCredentialRepoImpl) ListByBucket(ctx context.Context, bucketID uuid.UUID) ([]*storageEntity.TenantCredential, error) {
	// [COMMENT]: SKELETON
	return nil, errors.New("method ListByBucket not implemented")
}

func (r *TenantCredentialRepoImpl) Delete(ctx context.Context, id uuid.UUID) error {
	return errors.New("method Delete not implemented")
}
