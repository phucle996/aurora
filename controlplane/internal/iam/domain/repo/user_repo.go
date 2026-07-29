package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

// [COMMENT]: UserRepository định nghĩa interface cho các thao tác quản lý dữ liệu danh bạ người dùng (User Directory)
type UserRepository interface {
	ListUsers(ctx context.Context, query iamEntity.ListUsers) ([]iamEntity.ListUsers, error)
	UpdateUserStatus(ctx context.Context, workflow iamEntity.UpdateUserStatus) error
	GetMyProfile(ctx context.Context, workflow *iamEntity.GetMyProfile) error
	UpdateMyProfile(ctx context.Context, workflow *iamEntity.UpdateMyProfile) error
	GetMySocialLinks(ctx context.Context, workflow *iamEntity.GetMySocialLinks) ([]iamEntity.GetMySocialLinks, error)
	LinkExternalIdentity(ctx context.Context, workflow iamEntity.LinkExternalIdentity) error
	UnlinkMySocialLink(ctx context.Context, workflow iamEntity.UnlinkMySocialLink) error
	GetUserAuthMethods(ctx context.Context, query iamEntity.GetUserAuthMethods) ([]iamEntity.GetUserAuthMethods, error)
	ResetUserPassword(ctx context.Context, workflow iamEntity.ResetUserPassword) error
}
