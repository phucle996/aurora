package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// MfaService keeps one method per MFA workflow. Implementations deliberately
// keep workflow decisions local instead of sharing business helpers.
type MfaService interface {
	GetUserMfaStatus(ctx context.Context, userID uuid.UUID, callerLevel uint8) (bool, string, error)
	GetSelfMfaStatus(ctx context.Context, userID uuid.UUID) (*iamEntity.MFAUserStatus, error)
	GetLoginSetting(ctx context.Context, userID uuid.UUID) (*iamEntity.MFASetting, error)
	StartSetup(ctx context.Context, userID uuid.UUID) (*iamEntity.MFASetupResult, error)
	ConfirmSetup(ctx context.Context, userID, setupID uuid.UUID, code string) (*iamEntity.MFAConfirmationResult, error)
	RegenerateRecoveryCodes(ctx context.Context, userID uuid.UUID, code string) ([]string, error)
	Remove(ctx context.Context, userID uuid.UUID, code string) error
	VerifyLogin(ctx context.Context, userID, settingID uuid.UUID, method, code string) error
}
