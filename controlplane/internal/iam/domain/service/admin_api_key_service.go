package iamSvcInterface

import (
	"context"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
)

type AdminAPIKeyService interface {
	Bootstrap(ctx context.Context) error
	AdminLogin(ctx context.Context, req iamEntity.AdminLoginRequest) (iamEntity.AdminLoginResult, error)
	AdminLogout(ctx context.Context, ip *string, userAgent *string) error
	FinalizeInactiveSessions(ctx context.Context, inactiveBefore time.Time, limit int) error
	RotateAdminAPIKeyEmergency(ctx context.Context) error
	TryProcessAdminKeyRotationTrigger(ctx context.Context) error
	GetPublicKeyFromSession(ctx context.Context, accessKey string) (string, error)
	// VerifyAdminTrinitySession xác thực credentials của Admin/SRE quản trị qua gRPC
	VerifyAdminTrinitySession(ctx context.Context, token string, accessKey string, accessSecret string) (*iamEntity.VerifySessionResult, error)
}
