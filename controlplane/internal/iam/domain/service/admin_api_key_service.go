package iamSvcInterface

import (
	"context"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
)

type AdminAPIKeyService interface {
	Bootstrap(ctx context.Context, actor string) error
	AdminLogin(ctx context.Context, req iamEntity.AdminLoginRequest) (iamEntity.AdminLoginResult, error)
	RefreshAdminSession(ctx context.Context, accessKey string, ip *string, userAgent *string) (iamEntity.AdminLoginResult, error)
	AdminLogout(ctx context.Context, accessKey string, ip *string, userAgent *string) error
	FinalizeInactiveSessions(ctx context.Context, inactiveBefore time.Time, limit int) error
	RotateAdminAPIKeyEmergency(ctx context.Context) error
	TryProcessAdminKeyRotationTrigger(ctx context.Context) error
}
