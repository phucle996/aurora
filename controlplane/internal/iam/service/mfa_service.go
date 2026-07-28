package iamSvcImpl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/internal/security"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

type MfaService struct {
	cfg       *config.Config
	repo      iamRepoInterface.MfaRepository
	authRedis *goredis.Client
}

func NewMfaService(
	cfg *config.Config,
	repo iamRepoInterface.MfaRepository,
	authRedis *goredis.Client,
) iamSvcInterface.MfaService {
	return &MfaService{cfg: cfg, repo: repo, authRedis: authRedis}
}

func (s *MfaService) GetUserMfaStatus(ctx context.Context, userID uuid.UUID, callerLevel uint8) (bool, string, error) {
	return s.repo.GetPlatformStatus(ctx, userID, callerLevel)
}

func (s *MfaService) GetSelfMfaStatus(ctx context.Context, userID uuid.UUID) (*iamEntity.MFAUserStatus, error) {
	setting, recoveryCount, err := s.repo.GetSelfStatus(ctx, userID)
	if errors.Is(err, iamTaxonomy.ErrPreconditionFailed) {
		return &iamEntity.MFAUserStatus{Status: iamEntity.MFAStatusDisabled}, nil
	}
	if err != nil {
		return nil, err
	}
	enabledAt := setting.CreatedAt
	return &iamEntity.MFAUserStatus{
		Status:                 iamEntity.MFAStatusEnabled,
		EnabledAt:              &enabledAt,
		RecoveryCodesRemaining: recoveryCount,
	}, nil
}

func (s *MfaService) GetLoginSetting(ctx context.Context, userID uuid.UUID) (*iamEntity.MFASetting, error) {
	setting, err := s.repo.LoginGateGetSetting(ctx, userID)
	if errors.Is(err, iamTaxonomy.ErrPreconditionFailed) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return setting, nil
}

func (s *MfaService) StartSetup(ctx context.Context, userID uuid.UUID) (*iamEntity.MFASetupResult, error) {
	if err := s.repo.SetupStart(ctx, userID); err != nil {
		return nil, err
	}

	totpResult, err := security.GenerateTOTP("Aurora", userID.String())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", iamTaxonomy.ErrGenTOTPFailed, err)
	}
	secretCiphertext, err := security.EncryptMFASecret(s.cfg.Security.RuntimeMasterKey, totpResult.Secret)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", iamTaxonomy.ErrEncryptSecretFailed, err)
	}

	setupID := uuid.New()
	settingID := uuid.New()
	expiresAt := time.Now().UTC().Add(10 * time.Minute)
	payload, err := proto.Marshal(&iamproto.MfaSetupPending{
		SetupId:          setupID.String(),
		UserId:           userID.String(),
		SettingId:        settingID.String(),
		SecretCiphertext: secretCiphertext,
		SecretKeyId:      "runtime-master-v1",
		SchemaVersion:    1,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: encode setup state: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}

	// NX makes concurrent setup starts converge on one short-lived pending record.
	stored, err := s.authRedis.SetNX(
		ctx,
		fmt.Sprintf("iam:mfa:setup:%s", userID),
		payload,
		10*time.Minute,
	).Result()
	if err != nil {
		return nil, fmt.Errorf("%w: store setup state: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}
	if !stored {
		return nil, iamTaxonomy.ErrMFAAlreadyEnabled
	}

	return &iamEntity.MFASetupResult{
		SetupID:         setupID,
		ProvisioningURI: totpResult.ProvisioningURI,
		ManualSecret:    totpResult.Secret,
		ExpiresAt:       expiresAt,
	}, nil
}

func (s *MfaService) ConfirmSetup(
	ctx context.Context,
	userID, setupID uuid.UUID,
	code string,
) (*iamEntity.MFAConfirmationResult, error) {
	pendingKey := fmt.Sprintf("iam:mfa:setup:%s", userID)
	pendingBytes, err := s.authRedis.Get(ctx, pendingKey).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, iamTaxonomy.ErrMFASetupExpired
	}
	if err != nil {
		return nil, fmt.Errorf("%w: read setup state: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}
	var pending iamproto.MfaSetupPending
	if err := proto.Unmarshal(pendingBytes, &pending); err != nil {
		return nil, iamTaxonomy.ErrMFASetupExpired
	}
	pendingUserID, userIDErr := uuid.Parse(strings.TrimSpace(pending.GetUserId()))
	pendingSetupID, setupIDErr := uuid.Parse(strings.TrimSpace(pending.GetSetupId()))
	pendingSettingID, settingIDErr := uuid.Parse(strings.TrimSpace(pending.GetSettingId()))
	if userIDErr != nil || setupIDErr != nil || settingIDErr != nil ||
		pendingUserID != userID || pendingSetupID != setupID ||
		pendingSettingID == uuid.Nil ||
		pending.GetSchemaVersion() != 1 ||
		strings.TrimSpace(pending.GetSecretCiphertext()) == "" ||
		strings.TrimSpace(pending.GetSecretKeyId()) == "" {
		return nil, iamTaxonomy.ErrMFASetupExpired
	}

	secret, err := security.DecryptMFASecret(s.cfg.Security.RuntimeMasterKey, pending.GetSecretCiphertext())
	if err != nil {
		return nil, fmt.Errorf("%w: decrypt setup state: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}
	now := time.Now().UTC()
	currentStep := now.Unix() / 30
	acceptedStep := int64(-1)
	for offset := int64(-1); offset <= 1; offset++ {
		candidateStep := currentStep + offset
		valid, validateErr := totp.ValidateCustom(code, secret, time.Unix(candidateStep*30, 0).UTC(), totp.ValidateOpts{
			Period: 30,
			Skew:   0,
			Digits: otp.DigitsSix,
		})
		if validateErr == nil && valid {
			acceptedStep = candidateStep
			break
		}
	}
	if acceptedStep < 0 {
		return nil, iamTaxonomy.ErrMFAInvalidCode
	}
	totpFenceKey := fmt.Sprintf("iam:mfa:totp:%s:%s", userID, pendingSettingID)
	reserved, err := s.authRedis.Eval(ctx, `
		local current = redis.call("GET", KEYS[1])
		if current and tonumber(current) >= tonumber(ARGV[1]) then return 0 end
		redis.call("SET", KEYS[1], ARGV[1], "EX", ARGV[2])
		return 1
	`, []string{totpFenceKey}, acceptedStep, int64(120)).Int64()
	if err != nil {
		return nil, fmt.Errorf("%w: reserve setup totp step: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}
	if reserved != 1 {
		return nil, iamTaxonomy.ErrMFAInvalidCode
	}

	recoveryCodes := make([]string, 10)
	recoveryHashes := make([]string, 10)
	for i := range recoveryCodes {
		recoveryCodes[i], err = security.GenerateRecoveryCode(16)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", iamTaxonomy.ErrGenRecoveryCodeFailed, err)
		}
		recoveryHashes[i] = security.HashRecoveryCode(recoveryCodes[i])
	}
	enabledAt, err := s.repo.SetupConfirmEnable(
		ctx,
		userID,
		pendingSettingID,
		pending.GetSecretCiphertext(),
		pending.GetSecretKeyId(),
		recoveryHashes,
	)
	if err != nil {
		return nil, err
	}

	// PostgreSQL is durable SoT; cleanup is best effort and the ciphertext still
	// has a TTL if a Redis failover interrupts this compare-delete.
	_, _ = s.authRedis.Eval(ctx, `
		if redis.call("GET", KEYS[1]) ~= ARGV[1] then return 0 end
		return redis.call("DEL", KEYS[1])
	`, []string{pendingKey}, string(pendingBytes)).Int64()

	return &iamEntity.MFAConfirmationResult{
		EnabledAt:     enabledAt,
		RecoveryCodes: recoveryCodes,
	}, nil
}

func (s *MfaService) RegenerateRecoveryCodes(ctx context.Context, userID uuid.UUID, code string) ([]string, error) {
	setting, err := s.repo.RecoveryRegenerateGetSetting(ctx, userID)
	if err != nil {
		return nil, err
	}
	secret, err := security.DecryptMFASecret(s.cfg.Security.RuntimeMasterKey, setting.SecretCiphertext)
	if err != nil {
		return nil, fmt.Errorf("%w: decrypt mfa secret: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}
	now := time.Now().UTC()
	currentStep := now.Unix() / 30
	acceptedStep := int64(-1)
	for offset := int64(-1); offset <= 1; offset++ {
		candidateStep := currentStep + offset
		valid, validateErr := totp.ValidateCustom(code, secret, time.Unix(candidateStep*30, 0).UTC(), totp.ValidateOpts{
			Period: 30,
			Skew:   0,
			Digits: otp.DigitsSix,
		})
		if validateErr == nil && valid {
			acceptedStep = candidateStep
			break
		}
	}
	if acceptedStep < 0 {
		return nil, iamTaxonomy.ErrMFAInvalidCode
	}
	totpFenceKey := fmt.Sprintf("iam:mfa:totp:%s:%s", userID, setting.ID)
	reserved, err := s.authRedis.Eval(ctx, `
		local current = redis.call("GET", KEYS[1])
		if current and tonumber(current) >= tonumber(ARGV[1]) then return 0 end
		redis.call("SET", KEYS[1], ARGV[1], "EX", ARGV[2])
		return 1
	`, []string{totpFenceKey}, acceptedStep, int64(120)).Int64()
	if err != nil {
		return nil, fmt.Errorf("%w: reserve regenerate totp step: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}
	if reserved != 1 {
		return nil, iamTaxonomy.ErrMFAInvalidCode
	}

	recoveryCodes := make([]string, 10)
	recoveryHashes := make([]string, 10)
	for i := range recoveryCodes {
		recoveryCodes[i], err = security.GenerateRecoveryCode(16)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", iamTaxonomy.ErrGenRecoveryCodeFailed, err)
		}
		recoveryHashes[i] = security.HashRecoveryCode(recoveryCodes[i])
	}
	if err := s.repo.RecoveryRegenerateReplace(ctx, userID, recoveryHashes); err != nil {
		return nil, err
	}
	return recoveryCodes, nil
}

func (s *MfaService) Remove(ctx context.Context, userID uuid.UUID, code string) error {
	setting, err := s.repo.RemoveGetSetting(ctx, userID)
	if err != nil {
		return err
	}
	secret, err := security.DecryptMFASecret(s.cfg.Security.RuntimeMasterKey, setting.SecretCiphertext)
	if err != nil {
		return fmt.Errorf("%w: decrypt mfa secret: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}
	now := time.Now().UTC()
	currentStep := now.Unix() / 30
	acceptedStep := int64(-1)
	for offset := int64(-1); offset <= 1; offset++ {
		candidateStep := currentStep + offset
		valid, validateErr := totp.ValidateCustom(code, secret, time.Unix(candidateStep*30, 0).UTC(), totp.ValidateOpts{
			Period: 30,
			Skew:   0,
			Digits: otp.DigitsSix,
		})
		if validateErr == nil && valid {
			acceptedStep = candidateStep
			break
		}
	}
	if acceptedStep < 0 {
		return iamTaxonomy.ErrMFAInvalidCode
	}
	totpFenceKey := fmt.Sprintf("iam:mfa:totp:%s:%s", userID, setting.ID)
	reserved, err := s.authRedis.Eval(ctx, `
		local current = redis.call("GET", KEYS[1])
		if current and tonumber(current) >= tonumber(ARGV[1]) then return 0 end
		redis.call("SET", KEYS[1], ARGV[1], "EX", ARGV[2])
		return 1
	`, []string{totpFenceKey}, acceptedStep, int64(120)).Int64()
	if err != nil {
		return fmt.Errorf("%w: reserve remove totp step: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}
	if reserved != 1 {
		return iamTaxonomy.ErrMFAInvalidCode
	}
	return s.repo.RemoveDelete(ctx, userID)
}

func (s *MfaService) VerifyLogin(
	ctx context.Context,
	userID, settingID uuid.UUID,
	method, code string,
) error {
	setting, err := s.repo.LoginVerifyGetSetting(ctx, userID)
	if err != nil {
		return iamTaxonomy.ErrMFAChallengeInvalid
	}
	if setting.ID != settingID {
		return iamTaxonomy.ErrMFAChallengeInvalid
	}

	switch method {
	case "recovery_code":
		if consumeErr := s.repo.LoginConsumeRecoveryCode(
			ctx,
			userID,
			settingID,
			security.HashRecoveryCode(code),
		); consumeErr != nil {
			return consumeErr
		}
		return nil
	case "totp":
		secret, decryptErr := security.DecryptMFASecret(s.cfg.Security.RuntimeMasterKey, setting.SecretCiphertext)
		if decryptErr != nil {
			return fmt.Errorf("%w: decrypt mfa secret: %v", iamTaxonomy.ErrAuthenticationUnavailable, decryptErr)
		}
		now := time.Now().UTC()
		currentStep := now.Unix() / 30
		acceptedStep := int64(-1)
		for offset := int64(-1); offset <= 1; offset++ {
			candidateStep := currentStep + offset
			valid, validateErr := totp.ValidateCustom(code, secret, time.Unix(candidateStep*30, 0).UTC(), totp.ValidateOpts{
				Period: 30,
				Skew:   0,
				Digits: otp.DigitsSix,
			})
			if validateErr == nil && valid {
				acceptedStep = candidateStep
				break
			}
		}
		if acceptedStep < 0 {
			return iamTaxonomy.ErrMFAInvalidCode
		}
		totpFenceKey := fmt.Sprintf("iam:mfa:totp:%s:%s", userID, settingID)
		reserved, reserveErr := s.authRedis.Eval(ctx, `
			local current = redis.call("GET", KEYS[1])
			if current and tonumber(current) >= tonumber(ARGV[1]) then return 0 end
			redis.call("SET", KEYS[1], ARGV[1], "EX", ARGV[2])
			return 1
		`, []string{totpFenceKey}, acceptedStep, int64(120)).Int64()
		if reserveErr != nil {
			return fmt.Errorf("%w: reserve login totp step: %v", iamTaxonomy.ErrAuthenticationUnavailable, reserveErr)
		}
		if reserved != 1 {
			return iamTaxonomy.ErrMFAInvalidCode
		}
		return nil
	default:
		return iamTaxonomy.ErrMFAChallengeInvalid
	}
}
