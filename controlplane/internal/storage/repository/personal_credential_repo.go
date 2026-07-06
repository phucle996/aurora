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

// [COMMENT]: PersonalCredentialRepoImpl thực thi interface PersonalCredentialRepo kết nối PostgreSQL.
type PersonalCredentialRepoImpl struct {
	db *pgxpool.Pool
}

// [COMMENT]: NewPersonalCredentialRepo khởi tạo repository quản lý credentials cho bucket cá nhân.
func NewPersonalCredentialRepo(db *pgxpool.Pool) storageRepoInterface.PersonalCredentialRepo {
	return &PersonalCredentialRepoImpl{
		db: db,
	}
}

func (r *PersonalCredentialRepoImpl) Create(ctx context.Context, cred *storageEntity.PersonalCredential) error {
	m := storageModel.PersonalCredentialEntityToModel(cred)
	_ = m // [COMMENT]: SKELETON - Sẽ sử dụng m khi thực thi SQL trên personal_credentials
	return errors.New("method Create not implemented")
}

func (r *PersonalCredentialRepoImpl) GetByID(ctx context.Context, id uuid.UUID) (*storageEntity.PersonalCredential, error) {
	// [COMMENT]: SKELETON
	return nil, errors.New("method GetByID not implemented")
}

func (r *PersonalCredentialRepoImpl) ListByBucket(ctx context.Context, bucketID uuid.UUID) ([]*storageEntity.PersonalCredential, error) {
	// [COMMENT]: SKELETON
	return nil, errors.New("method ListByBucket not implemented")
}

func (r *PersonalCredentialRepoImpl) Delete(ctx context.Context, id uuid.UUID) error {
	return errors.New("method Delete not implemented")
}
