package iamRepoInterface

import (
	"context"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// MfaRepository is the narrow persistence port for the MFA workflows. Each
// method is owned by exactly one workflow so a flow cannot reach through
// another flow's repository contract.
type MfaRepository interface {
	GetSelfStatus(ctx context.Context, userID uuid.UUID) (*iamEntity.MFASetting, int, error)
	GetPlatformStatus(ctx context.Context, userID uuid.UUID, callerLevel uint8) (bool, string, error)
	SetupStart(ctx context.Context, userID uuid.UUID) error
	SetupConfirmEnable(
		ctx context.Context,
		userID, settingID uuid.UUID,
		secretCiphertext, secretKeyID string,
		recoveryHashes []string,
	) (time.Time, error)
	RecoveryRegenerateGetSetting(ctx context.Context, userID uuid.UUID) (*iamEntity.MFASetting, error)
	RecoveryRegenerateReplace(ctx context.Context, userID uuid.UUID, recoveryHashes []string) error
	RemoveGetSetting(ctx context.Context, userID uuid.UUID) (*iamEntity.MFASetting, error)
	RemoveDelete(ctx context.Context, userID uuid.UUID) error
	LoginGateGetSetting(ctx context.Context, userID uuid.UUID) (*iamEntity.MFASetting, error)
	LoginVerifyGetSetting(ctx context.Context, userID uuid.UUID) (*iamEntity.MFASetting, error)
	LoginConsumeRecoveryCode(ctx context.Context, userID, settingID uuid.UUID, codeHash string) error
}
