package iamSvcImpl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"controlplane/infra/vault"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/internal/observability"
	"controlplane/internal/security"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

type MfaService struct {
	vault     *vault.Client
	repo      iamRepoInterface.MfaRepository
	authRedis *goredis.Client
	metrics   observability.WorkflowRecorder
}

func NewMfaService(
	vaultClient *vault.Client,
	repo iamRepoInterface.MfaRepository,
	authRedis *goredis.Client,
	metrics observability.WorkflowRecorder,
) iamSvcInterface.MfaService {
	return &MfaService{vault: vaultClient, repo: repo, authRedis: authRedis, metrics: metrics}
}

func (s *MfaService) GetUserMfaStatus(ctx context.Context, userID uuid.UUID, callerLevel uint8) (enabled bool, method string, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, iamTaxonomy.ErrNotFound) || errors.Is(err, iamTaxonomy.ErrUserNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			result, reason = observability.ResultRejected, observability.ReasonForbidden
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.GetPlatformStatus(ctx, userID, callerLevel)
}

func (s *MfaService) GetSelfMfaStatus(ctx context.Context, userID uuid.UUID) (out *iamEntity.MFAUserStatus, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

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

func (s *MfaService) StartSetup(ctx context.Context, userID uuid.UUID) (out *iamEntity.MFASetupResult, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, iamTaxonomy.ErrMFAAlreadyEnabled) {
			result, reason = observability.ResultRejected, observability.ReasonAlreadyExists
		} else if errors.Is(err, iamTaxonomy.ErrAuthenticationUnavailable) {
			result, reason = observability.ResultFailure, observability.ReasonUnavailable
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	if err := s.repo.SetupStart(ctx, userID); err != nil {
		return nil, err
	}

	totpResult, err := security.GenerateTOTP("Aurora", userID.String())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", iamTaxonomy.ErrGenTOTPFailed, err)
	}
	secretCiphertext, err := s.vault.TransitEncrypt(ctx, "iam-mfa-secret", totpResult.Secret)
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
		SecretKeyId:      "transit/iam-mfa-secret",
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
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	pendingKey := fmt.Sprintf("iam:mfa:setup:%s", userID)
	pendingBytes, err := s.authRedis.Get(ctx, pendingKey).Bytes()
	if errors.Is(err, goredis.Nil) {
		result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		return nil, iamTaxonomy.ErrMFASetupExpired
	}
	if err != nil {
		return nil, fmt.Errorf("%w: read setup state: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}
	var pending iamproto.MfaSetupPending
	if err := proto.Unmarshal(pendingBytes, &pending); err != nil {
		result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
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
		result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		return nil, iamTaxonomy.ErrMFASetupExpired
	}

	secret, err := s.vault.TransitDecrypt(ctx, "iam-mfa-secret", pending.GetSecretCiphertext())
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
		result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
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
		result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
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

	result, reason = observability.ResultSuccess, observability.ReasonNone
	return &iamEntity.MFAConfirmationResult{
		EnabledAt:     enabledAt,
		RecoveryCodes: recoveryCodes,
	}, nil
}

func (s *MfaService) RegenerateRecoveryCodes(ctx context.Context, userID uuid.UUID, code string) (out []string, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, iamTaxonomy.ErrMFAInvalidCode), errors.Is(err, iamTaxonomy.ErrRecoveryCodeInvalid):
			result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		case errors.Is(err, iamTaxonomy.ErrPreconditionFailed):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		case errors.Is(err, iamTaxonomy.ErrAuthenticationUnavailable):
			result, reason = observability.ResultFailure, observability.ReasonUnavailable
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	setting, err := s.repo.RecoveryRegenerateGetSetting(ctx, userID)
	if err != nil {
		return nil, err
	}
	secret, err := s.vault.TransitDecrypt(ctx, "iam-mfa-secret", setting.SecretCiphertext)
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

func (s *MfaService) Remove(ctx context.Context, userID uuid.UUID, code string) (err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, iamTaxonomy.ErrMFAInvalidCode):
			result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		case errors.Is(err, iamTaxonomy.ErrPreconditionFailed):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		case errors.Is(err, iamTaxonomy.ErrAuthenticationUnavailable):
			result, reason = observability.ResultFailure, observability.ReasonUnavailable
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	setting, err := s.repo.RemoveGetSetting(ctx, userID)
	if err != nil {
		return err
	}
	secret, err := s.vault.TransitDecrypt(ctx, "iam-mfa-secret", setting.SecretCiphertext)
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
		secret, decryptErr := s.vault.TransitDecrypt(ctx, "iam-mfa-secret", setting.SecretCiphertext)
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
