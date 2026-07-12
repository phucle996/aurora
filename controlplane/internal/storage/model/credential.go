package storageModel

import (
	"time"

	storageEntity "controlplane/internal/storage/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: PersonalCredential đại diện cho cấu trúc bảng personal_credentials trong PostgreSQL.
type PersonalCredential struct {
	ID        uuid.UUID `db:"id"`
	BucketID  uuid.UUID `db:"bucket_id"`
	AccessKey string    `db:"access_key"`
	// [COMMENT]: Loại bỏ SecretKey khỏi DB model vì cột này đã được drop khỏi CSDL
	Policy    string    `db:"policy"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// [COMMENT]: TenantCredential đại diện cho cấu trúc bảng tenant_credentials trong PostgreSQL.
type TenantCredential struct {
	ID        uuid.UUID `db:"id"`
	BucketID  uuid.UUID `db:"bucket_id"`
	AccessKey string    `db:"access_key"`
	// [COMMENT]: Loại bỏ SecretKey khỏi DB model vì cột này đã được drop khỏi CSDL
	Policy    string    `db:"policy"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// [COMMENT]: PersonalCredentialEntityToModel chuyển đổi Domain Entity sang Database Model.
func PersonalCredentialEntityToModel(e *storageEntity.PersonalCredential) *PersonalCredential {
	if e == nil {
		return nil
	}
	return &PersonalCredential{
		ID:        e.ID,
		BucketID:  e.BucketID,
		AccessKey: e.AccessKey,
		Policy:    e.Policy,
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
	}
}

// [COMMENT]: PersonalCredentialModelToEntity chuyển đổi Database Model sang Domain Entity.
func PersonalCredentialModelToEntity(m *PersonalCredential) *storageEntity.PersonalCredential {
	if m == nil {
		return nil
	}
	return &storageEntity.PersonalCredential{
		ID:        m.ID,
		BucketID:  m.BucketID,
		AccessKey: m.AccessKey,
		Policy:    m.Policy,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

// [COMMENT]: TenantCredentialEntityToModel chuyển đổi Domain Entity sang Database Model.
func TenantCredentialEntityToModel(e *storageEntity.TenantCredential) *TenantCredential {
	if e == nil {
		return nil
	}
	return &TenantCredential{
		ID:        e.ID,
		BucketID:  e.BucketID,
		AccessKey: e.AccessKey,
		Policy:    e.Policy,
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
	}
}

// [COMMENT]: TenantCredentialModelToEntity chuyển đổi Database Model sang Domain Entity.
func TenantCredentialModelToEntity(m *TenantCredential) *storageEntity.TenantCredential {
	if m == nil {
		return nil
	}
	return &storageEntity.TenantCredential{
		ID:        m.ID,
		BucketID:  m.BucketID,
		AccessKey: m.AccessKey,
		Policy:    m.Policy,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}
