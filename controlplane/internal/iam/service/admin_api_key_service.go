package iamSvcImpl

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"controlplane/infra/telegram"
	"controlplane/internal/config"
	iamCache "controlplane/internal/iam/cache"
	deviceHint "controlplane/internal/iam/devicehint"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamErrorx "controlplane/internal/iam/errorx"
	iamMetrics "controlplane/internal/iam/metrics"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"
	"errors"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// AdminAPIKeyService implements business flow cho:
// - bootstrap admin key,
// - login admin qua API key + MFA,
// - logout admin runtime.
//
// Boundary:
// - Service không log nghiệp vụ.
// - Error được trả lên handler để mapping HTTP.
type AdminAPIKeyService struct {
	cfg             *config.Config
	repo            iamRepoInterface.AdminAPIKeyRepository
	telegram        *telegram.TelegramClient
	secrets         security.SecretProvider
	deviceRT        iamCache.AdminDeviceRuntimeCache
	apiKeyCache     iamCache.AdminAPIKeyCache
	rotationTrigger iamCache.AdminKeyRotationTriggerCache
}

// NewAdminAPIKeyService tạo service instance với các dependency cần thiết cho
// bootstrap/login/logout flow.
func NewAdminAPIKeyService(
	cfg *config.Config,
	repo iamRepoInterface.AdminAPIKeyRepository,
	telegram *telegram.TelegramClient,
	secrets security.SecretProvider,
	deviceRT iamCache.AdminDeviceRuntimeCache,
	apiKeyCache iamCache.AdminAPIKeyCache,
	rotationTrigger ...iamCache.AdminKeyRotationTriggerCache,
) iamSvcInterface.AdminAPIKeyService {
	var trigger iamCache.AdminKeyRotationTriggerCache
	if len(rotationTrigger) > 0 {
		trigger = rotationTrigger[0]
	}
	return &AdminAPIKeyService{
		cfg:             cfg,
		repo:            repo,
		telegram:        telegram,
		secrets:         secrets,
		deviceRT:        deviceRT,
		apiKeyCache:     apiKeyCache,
		rotationTrigger: trigger,
	}
}

// RotateAdminAPIKeyEmergency thực thi rotation admin API key khẩn cấp
// (compromise hoặc theo trigger từ scheduler/operator).
//
// Flow:
//  1. acquire rotation lock toàn cục để chống đồng thời,
//  2. sinh API key mới + tính hash + expiry,
//  3. notify plaintext key qua Telegram trước khi persist,
//  4. persist key mới vào DB ở trạng thái next-active,
//  5. invalidate RAM cache active key + clear rotation trigger.
//
// Boundary:
// - Plaintext key chỉ tồn tại trong RAM + Telegram message, không log.
// - Nếu Telegram fail thì abort để tránh ghi DB mà operator không nhận được key.
func (s *AdminAPIKeyService) RotateAdminAPIKeyEmergency(ctx context.Context, actor string) error {
	lock, err := s.repo.AcquireRotationLock(ctx)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already held") {
			return apperr.Wrap(iamErrorx.ErrAdminRotationLockBusy, iamErrorx.ReasonAdminRotationLockBusy, err)
		}
		return apperr.Wrap(iamErrorx.ErrAdminRotationFailed, iamErrorx.ReasonAdminRotationDependency, err)
	}
	defer lock.Release(ctx)

	plainKey, err := security.GenerateToken(48)
	if err != nil {
		return apperr.Wrap(iamErrorx.ErrAdminRotationFailed, iamErrorx.ReasonAdminRotationDependency, err)
	}
	keyHash := security.HashTokenSHA256(plainKey)
	expiresAt := time.Now().UTC().Add(s.cfg.Security.AdminAPITokenTTL)

	msg := fmt.Sprintf("<b>ADMIN ROTATION SUCCESS</b>\nAPI Key: <code>%s</code>\nExpires: <code>%s</code>", plainKey, expiresAt.Format(time.RFC3339))
	if sendErr := s.telegram.SendMessage(msg); sendErr != nil {
		return apperr.Wrap(iamErrorx.ErrAdminRotationDelivery, iamErrorx.ReasonAdminRotationDelivery, sendErr)
	}

	if err := s.repo.PrepareNextAdminAPIKey(ctx, actor, keyHash, expiresAt); err != nil {
		return apperr.Wrap(iamErrorx.ErrAdminRotationFailed, iamErrorx.ReasonAdminRotationDependency, err)
	}
	if s.apiKeyCache != nil {
		s.apiKeyCache.InvalidateActiveAPIKey()
	}
	if s.rotationTrigger != nil {
		_ = s.rotationTrigger.ClearRotationRequired(ctx)
	}
	return nil
}

// TryProcessAdminKeyRotationTrigger kiểm tra cờ rotation trong cache và chạy
// rotation nếu cờ được bật.
//
// Mục tiêu:
// - được scheduler gọi định kỳ (poll trigger),
// - no-op khi rotationTrigger không được wire hoặc trigger chưa bật.
func (s *AdminAPIKeyService) TryProcessAdminKeyRotationTrigger(ctx context.Context) error {
	if s.rotationTrigger == nil {
		return nil
	}
	required, err := s.rotationTrigger.HasRotationRequired(ctx)
	if err != nil {
		return apperr.Wrap(iamErrorx.ErrAdminRotationFailed, iamErrorx.ReasonAdminRotationTrigger, err)
	}
	if !required {
		return nil
	}
	if err := s.RotateAdminAPIKeyEmergency(ctx, "system-scheduler"); err != nil {
		return err
	}
	return nil
}

// Bootstrap khởi tạo admin API key đầu tiên cho hệ thống.
//
// Flow:
// 1) lock bootstrap toàn cục,
// 2) kiểm tra chưa có key active,
// 3) tạo API key + TOTP secret + recovery codes,
// 4) persist DB,
// 5) notify Telegram,
// 6) nếu notify fail thì rollback DB.
func (s *AdminAPIKeyService) Bootstrap(ctx context.Context, actor string) error {
	lock, err := s.repo.AcquireBootstrapLock(ctx)
	if err != nil {
		return apperr.Wrap(iamErrorx.ErrAdminBootstrapLockFailed, iamErrorx.ReasonAdminBootstrapLockError, err)
	}
	defer lock.Release(ctx)

	now := time.Now().UTC()
	active, err := s.loadActiveAdminAPIKey(ctx)
	if err != nil {
		return apperr.Wrap(iamErrorx.ErrAdminBootstrapPreconditionFailed, iamErrorx.ReasonAdminBootstrapPreconditionError, err)
	}
	if active != nil {
		return apperr.Wrap(iamErrorx.ErrAdminBootstrapNotAllowed, iamErrorx.ReasonAdminBootstrapNotAllowed, nil)
	}

	expiresAt := now.Add(s.cfg.Security.AdminAPITokenTTL)

	plainKey, err := security.GenerateToken(48)
	if err != nil {
		return apperr.Wrap(iamErrorx.ErrAdminBootstrapPersistFailed, iamErrorx.ReasonAdminBootstrapPersistError, err)
	}
	keyHash := security.HashTokenSHA256(plainKey)

	totpResult, err := security.GenerateTOTP("controlplane-admin", "admin@system")
	if err != nil {
		return apperr.Wrap(iamErrorx.ErrAdminBootstrapPersistFailed, iamErrorx.ReasonAdminBootstrapPersistError, err)
	}
	secretCipher, err := security.EncryptSecret(totpResult.Secret)
	if err != nil {
		return apperr.Wrap(iamErrorx.ErrAdminBootstrapPersistFailed, iamErrorx.ReasonAdminBootstrapPersistError, err)
	}

	recoveryHashes := make([]string, 0, 8)
	recoveryPlains := make([]string, 0, 8)
	for i := 0; i < 8; i++ {
		code, genErr := security.GenerateRecoveryCode(24)
		if genErr != nil {
			return apperr.Wrap(iamErrorx.ErrAdminBootstrapPersistFailed, iamErrorx.ReasonAdminBootstrapPersistError, genErr)
		}
		recoveryPlains = append(recoveryPlains, code)
		recoveryHashes = append(recoveryHashes, security.HashRecoveryCode(code))
	}

	payload := iamEntity.AdminBootstrapPayload{
		Actor:              actor,
		KeyHash:            keyHash,
		ExpiresAt:          expiresAt,
		RecoveryCodeHashes: recoveryHashes,
		GeneratedAt:        now,
		SecretCiphertext:   secretCipher,
	}
	_, err = s.repo.Bootstrap(ctx, payload)
	if err != nil {
		return apperr.Wrap(iamErrorx.ErrAdminBootstrapPersistFailed, iamErrorx.ReasonAdminBootstrapPersistError, err)
	}

	msg := fmt.Sprintf(
		"<b>✅ ADMIN BOOTSTRAP READY</b>\n\n"+
			"<b>🔐 API Key</b>\n<code>%s</code>\n\n"+
			"<b>⏰ Expires At (UTC)</b>\n<code>%s</code>\n\n"+
			"<b>🛡️ TOTP Secret</b>\n<code>%s</code>\n\n"+
			"<b>🧩 Recovery Codes (one-time)</b>\n%s\n\n"+
			"<i>Store these secrets in a secure vault. Do not share.</i>",
		plainKey,
		expiresAt.Format(time.RFC3339),
		totpResult.Secret,
		formatRecoveryCodesForTelegram(recoveryPlains),
	)
	backoff := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	var notifyErr error
	for i, wait := range backoff {
		if sendErr := s.telegram.SendMessage(msg); sendErr == nil {
			return nil
		} else {
			notifyErr = sendErr
		}
		if i < len(backoff)-1 {
			time.Sleep(wait)
		}
	}

	if rbErr := s.repo.RollbackBootstrap(ctx, payload); rbErr != nil {
		return apperr.Wrap(iamErrorx.ErrAdminBootstrapRollbackFailed, iamErrorx.ReasonAdminBootstrapRollbackError, rbErr)
	}
	return apperr.Wrap(iamErrorx.ErrAdminBootstrapNotifyFailed, iamErrorx.ReasonAdminBootstrapNotifyError, notifyErr)
}

// AdminLogin xác thực admin credentials và cấp runtime fragments cho `/admin`.
//
// Input business:
// - raw API key,
// - MFA method + MFA code,
// - device public key.
//
// Output business:
// - admin_api_token (JWT),
// - device_id,
// - device_secret,
// - expires_at.
func (s *AdminAPIKeyService) AdminLogin(ctx context.Context, req iamEntity.AdminLoginRequest) (iamEntity.AdminLoginResult, error) {
	loginOutcome := iamMetrics.OutcomeSuccess
	defer func() { iamMetrics.ObserveAdminLoginOutcome(loginOutcome) }()

	if strings.TrimSpace(req.RawAPIKey) == "" || strings.TrimSpace(req.MFAMethod) == "" || strings.TrimSpace(req.MFACode) == "" || strings.TrimSpace(req.DevicePublicKey) == "" {
		loginOutcome = iamMetrics.AdminLoginOutcomeInvalidArgument
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonAdminLoginInvalidArgument, nil)
	}
	canonicalPublicKey, keyErr := normalizeDevicePublicKey(strings.TrimSpace(req.DevicePublicKey))
	if keyErr != nil {
		loginOutcome = iamMetrics.AdminLoginOutcomeInvalidDevicePublicKey
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonAdminLoginInvalidArgument, keyErr)
	}
	now := time.Now().UTC()
	active, err := s.loadActiveAdminAPIKey(ctx)
	if err != nil {
		loginOutcome = iamMetrics.AdminLoginOutcomeLoadActiveKeyError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonAdminLoginDependencyError, err)
	}
	if active == nil {
		loginOutcome = iamMetrics.AdminLoginOutcomeInvalidCredential
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAdminLoginInvalidCredential, iamErrorx.ReasonAdminLoginInvalidCredential, nil)
	}
	if active.KeyHash != security.HashTokenSHA256(req.RawAPIKey) {
		loginOutcome = iamMetrics.AdminLoginOutcomeInvalidCredential
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAdminLoginInvalidCredential, iamErrorx.ReasonAdminLoginInvalidCredential, nil)
	}

	mfaMethod := strings.TrimSpace(strings.ToLower(req.MFAMethod))
	switch mfaMethod {
	case "totp":
		secret, secErr := s.loadAdminTOTPSecret(ctx)
		if secErr != nil {
			loginOutcome = iamMetrics.AdminLoginOutcomeLoadTOTPSecretError
			return iamEntity.AdminLoginResult{}, secErr
		}
		code := strings.TrimSpace(req.MFACode)
		secret = strings.TrimSpace(secret)
		if code == "" {
			loginOutcome = iamMetrics.AdminLoginOutcomeMFAInvalidEmptyCode
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAdminLoginMFAInvalid, iamErrorx.ReasonAdminLoginMFAInvalid, nil)
		}
		if secret == "" {
			loginOutcome = iamMetrics.AdminLoginOutcomeMFAInvalidEmptySecret
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAdminLoginMFAInvalid, iamErrorx.ReasonAdminLoginMFAInvalid, nil)
		}
		okTOTP, totpErr := totp.ValidateCustom(code, secret, now, totp.ValidateOpts{
			Period:    30,
			Skew:      1,
			Digits:    otp.DigitsSix,
			Algorithm: otp.AlgorithmSHA1,
		})
		if totpErr != nil {
			loginOutcome = iamMetrics.AdminLoginOutcomeMFAValidateError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAdminLoginMFAInvalid, iamErrorx.ReasonAdminLoginMFAInvalid, totpErr)
		}
		if !okTOTP {
			loginOutcome = iamMetrics.AdminLoginOutcomeMFAInvalidCodeOrTimeSkew
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAdminLoginMFAInvalid, iamErrorx.ReasonAdminLoginMFAInvalid, nil)
		}
	case "recovery_code":
		codeHash := security.HashRecoveryCode(req.MFACode)
		unlock, lockErr := s.apiKeyCache.AcquireRecoveryConsumeLock(ctx, codeHash, 5*time.Second)
		if lockErr != nil {
			loginOutcome = iamMetrics.AdminLoginOutcomeRecoveryLockError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonAdminLoginDependencyError, lockErr)
		}
		defer unlock()
		consumed, consumeErr := s.repo.ConsumeRecoveryCode(ctx, codeHash, now)
		if consumeErr != nil {
			loginOutcome = iamMetrics.AdminLoginOutcomeConsumeRecoveryError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonAdminLoginDependencyError, consumeErr)
		}
		if !consumed {
			loginOutcome = iamMetrics.AdminLoginOutcomeMFAInvalid
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAdminLoginMFAInvalid, iamErrorx.ReasonAdminLoginMFAInvalid, nil)
		}
	default:
		loginOutcome = iamMetrics.AdminLoginOutcomeMFAInvalid
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAdminLoginMFAInvalid, iamErrorx.ReasonAdminLoginMFAInvalid, nil)
	}

	deviceID := uuid.NewString()
	deviceSecret, genErr := security.GenerateToken(48)
	if genErr != nil {
		loginOutcome = iamMetrics.AdminLoginOutcomeDeviceSecretIssueError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAdminLoginTokenIssueFailed, iamErrorx.ReasonAdminLoginTokenIssue, genErr)
	}
	tokenJTI := ""
	if err := s.deviceRT.SetDeviceRuntime(ctx, iamCache.AdminDeviceRuntime{DeviceID: deviceID, DeviceSecretHash: security.HashTokenSHA256(deviceSecret), TrackedDeviceID: "", TokenJTI: tokenJTI, Version: 1}, s.cfg.Security.AdminSessionTTL); err != nil {
		loginOutcome = iamMetrics.AdminLoginOutcomeRuntimeCacheError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAdminLoginTokenIssueFailed, iamErrorx.ReasonAdminLoginCacheError, err)
	}

	fp := sha256.Sum256([]byte(canonicalPublicKey))
	fph := hex.EncodeToString(fp[:])
	deviceName := deviceHint.ResolveDeviceName(req.HostnameHint, req.HostnameAlias)
	clientDeviceID, clientDeviceProvenance := deviceHint.ResolveClientDeviceID(req.ClientDeviceID)
	cdidPtr := clientDeviceID
	deviceBinding, bindErr := s.repo.UpsertAdminDeviceBinding(ctx, iamEntity.AdminDeviceBindingInput{
		DeviceName:           deviceName,
		PublicKey:            canonicalPublicKey,
		PublicKeyFingerprint: fph,
		ClientDeviceID:       &cdidPtr,
		Now:                  now,
	})
	if bindErr != nil {
		_ = s.deviceRT.DeleteDeviceSecret(ctx, deviceID)
		loginOutcome = iamMetrics.AdminLoginOutcomeUpsertDeviceBindingErr
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAdminLoginDeviceBindingFailed, iamErrorx.ReasonAdminLoginDeviceBindingError, bindErr)
	}

	if s.secrets == nil {
		_ = s.deviceRT.DeleteDeviceSecret(ctx, deviceID)
		loginOutcome = iamMetrics.AdminLoginOutcomeAuthUnavailable
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonAdminLoginAuthUnavailable, nil)
	}
	adminJTI, jtiErr := uuid.NewV7()
	if jtiErr != nil {
		_ = s.deviceRT.DeleteDeviceSecret(ctx, deviceID)
		loginOutcome = iamMetrics.AdminLoginOutcomeJTIIssueError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAdminLoginTokenIssueFailed, iamErrorx.ReasonAdminLoginTokenIssue, jtiErr)
	}
	tokenJTI = adminJTI.String()
	if err := s.deviceRT.SetDeviceRuntime(ctx, iamCache.AdminDeviceRuntime{
		DeviceID:         deviceID,
		DeviceSecretHash: security.HashTokenSHA256(deviceSecret),
		TrackedDeviceID:  deviceBinding.ID.String(),
		DevicePublicKey:  canonicalPublicKey,
		TokenJTI:         tokenJTI,
		Version:          1,
		LastSeenAt:       now.UTC().Unix(),
	}, s.cfg.Security.AdminSessionTTL); err != nil {
		_ = s.deviceRT.DeleteDeviceSecret(ctx, deviceID)
		loginOutcome = iamMetrics.AdminLoginOutcomeRuntimeUpdateError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAdminLoginTokenIssueFailed, iamErrorx.ReasonAdminLoginCacheError, err)
	}
	adminAPIToken, signErr := security.Sign(ctx, s.secrets, security.SecretFamilyAdminAPIKey, security.Claims{
		Subject:   "admin",
		AccessKey: deviceID,
		TokenID:   adminJTI.String(),
		TokenUse:  "admin_api_token",
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(s.cfg.Security.AdminSessionTTL).Unix(),
	})
	if signErr != nil {
		_ = s.deviceRT.DeleteDeviceSecret(ctx, deviceID)
		loginOutcome = iamMetrics.AdminLoginOutcomeSignTokenError
		if errors.Is(signErr, security.ErrEmptySecret) {
			loginOutcome = iamMetrics.AdminLoginOutcomeAuthUnavailable
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonAdminLoginAuthUnavailable, signErr)
		}
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAdminLoginTokenIssueFailed, iamErrorx.ReasonAdminLoginTokenIssue, signErr)
	}

	return iamEntity.AdminLoginResult{
		AdminAPIToken:            adminAPIToken,
		DeviceID:                 deviceID,
		DeviceSecret:             deviceSecret,
		ClientDeviceID:           clientDeviceID,
		ClientDeviceIDProvenance: string(clientDeviceProvenance),
		ExpiresAt:                now.Add(s.cfg.Security.AdminSessionTTL),
	}, nil
}

// RefreshAdminSession cấp lại admin_api_token cho device đang active mà không
// yêu cầu login lại bằng API key + MFA.
//
// Flow:
//  1. validate device_id,
//  2. load runtime fragment + CAS touch để gia hạn TTL theo phiên bản hiện tại,
//  3. ký lại admin_api_token mới với JTI mới.
//
// Boundary:
//   - Không reset device_secret để tránh phá vỡ HMAC từ phía client.
//   - Không sinh device_id mới; reuse device_id của runtime hiện tại.
//   - Mọi lỗi cache/sign quy về ErrAuthenticationUnavailable hoặc
//     ErrAdminLoginTokenIssueFailed để handler map HTTP nhất quán.
func (s *AdminAPIKeyService) RefreshAdminSession(ctx context.Context, deviceID string, ip *string, userAgent *string) (iamEntity.AdminLoginResult, error) {
	startedAt := time.Now()
	refreshOutcome := iamMetrics.OutcomeSuccess
	defer func() {
		var observeErr error
		if refreshOutcome != iamMetrics.OutcomeSuccess {
			observeErr = errors.New(refreshOutcome)
		}
		iamMetrics.ObserveAdminRefreshOutcome(refreshOutcome)
		iamMetrics.ObserveAdminRefreshLatency(time.Since(startedAt), observeErr)
	}()

	trimmedDeviceID := strings.TrimSpace(deviceID)
	if trimmedDeviceID == "" {
		refreshOutcome = iamMetrics.AdminRefreshOutcomeInvalidArgument
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonAdminRefreshInvalidArgument, nil)
	}
	runtimeRecord, runtimeErr := s.deviceRT.GetDeviceRuntime(ctx, trimmedDeviceID)
	if runtimeErr != nil {
		refreshOutcome = iamMetrics.AdminRefreshOutcomeLoadRuntimeErr
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonAdminRefreshCacheError, runtimeErr)
	}
	if runtimeRecord == nil {
		refreshOutcome = iamMetrics.AdminRefreshOutcomeRuntimeNotFound
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonAdminRefreshRuntimeInvalid, nil)
	}
	casOK, casErr := s.deviceRT.CompareAndTouchDeviceRuntime(ctx, trimmedDeviceID, runtimeRecord.Version, s.cfg.Security.AdminSessionTTL, ip, userAgent)
	if casErr != nil {
		refreshOutcome = iamMetrics.AdminRefreshOutcomeTouchRuntimeErr
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonAdminRefreshCacheError, casErr)
	}
	if !casOK {
		refreshOutcome = iamMetrics.AdminRefreshOutcomeRuntimeConflict
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonAdminRefreshRuntimeInvalid, nil)
	}
	if s.secrets == nil {
		refreshOutcome = iamMetrics.AdminRefreshOutcomeAuthUnavailable
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonAdminRefreshAuthUnavailable, nil)
	}

	now := time.Now().UTC()
	adminJTI, jtiErr := uuid.NewV7()
	if jtiErr != nil {
		refreshOutcome = iamMetrics.AdminRefreshOutcomeJTIIssueError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAdminLoginTokenIssueFailed, iamErrorx.ReasonAdminRefreshTokenIssue, jtiErr)
	}
	adminAPIToken, signErr := security.Sign(ctx, s.secrets, security.SecretFamilyAdminAPIKey, security.Claims{
		Subject:   "admin",
		AccessKey: trimmedDeviceID,
		TokenID:   adminJTI.String(),
		TokenUse:  "admin_api_token",
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(s.cfg.Security.AdminSessionTTL).Unix(),
	})
	if signErr != nil {
		refreshOutcome = iamMetrics.AdminRefreshOutcomeSignTokenError
		if errors.Is(signErr, security.ErrEmptySecret) {
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonAdminRefreshAuthUnavailable, signErr)
		}
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamErrorx.ErrAdminLoginTokenIssueFailed, iamErrorx.ReasonAdminRefreshTokenIssue, signErr)
	}

	return iamEntity.AdminLoginResult{
		AdminAPIToken: adminAPIToken,
		DeviceID:      trimmedDeviceID,
		ExpiresAt:     now.Add(s.cfg.Security.AdminSessionTTL),
	}, nil
}

// formatRecoveryCodesForTelegram render danh sách recovery code thành block
// HTML có đánh số, an toàn cho Telegram parse_mode=HTML.
func formatRecoveryCodesForTelegram(codes []string) string {
	if len(codes) == 0 {
		return "<i>(none)</i>"
	}
	formatted := make([]string, 0, len(codes))
	for index, code := range codes {
		formatted = append(formatted, fmt.Sprintf("%d) <code>%s</code>", index+1, code))
	}
	return strings.Join(formatted, "\n")
}

// AdminLogout cleanup runtime fragment state theo device_id (nếu có).
// Handler sẽ chịu trách nhiệm clear cookie phía client.
func (s *AdminAPIKeyService) AdminLogout(ctx context.Context, deviceID string, ip *string, userAgent *string) error {
	trimmedDeviceID := strings.TrimSpace(deviceID)
	if trimmedDeviceID == "" {
		return nil
	}
	runtimeRecord, runtimeErr := s.deviceRT.GetDeviceRuntime(ctx, trimmedDeviceID)
	if runtimeErr != nil {
		return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonAdminLogoutCacheError, runtimeErr)
	}
	if runtimeRecord != nil && strings.TrimSpace(runtimeRecord.TrackedDeviceID) != "" {
		runtimeIP := strings.TrimSpace(runtimeRecord.LastSeenIP)
		runtimeUA := strings.TrimSpace(runtimeRecord.LastSeenUserAgent)
		if ip != nil {
			requestIP := strings.TrimSpace(*ip)
			if requestIP != "" && requestIP != runtimeIP {
				runtimeIP = requestIP
				runtimeRecord.LastSeenDirty = true
			}
		}
		if userAgent != nil {
			requestUA := strings.TrimSpace(*userAgent)
			if requestUA != "" && requestUA != runtimeUA {
				runtimeUA = requestUA
				runtimeRecord.LastSeenDirty = true
			}
		}
		// Chỉ flush DB khi runtime đánh dấu dirty để tránh write mỗi request.
		if runtimeRecord.LastSeenDirty {
			seenAtUnix := runtimeRecord.LastSeenAt
			if seenAtUnix <= 0 {
				seenAtUnix = time.Now().UTC().Unix()
			}
			if err := s.repo.TouchAdminDeviceLastSeen(ctx, runtimeRecord.TrackedDeviceID, optionalStringPointer(runtimeIP), optionalStringPointer(runtimeUA), time.Unix(seenAtUnix, 0).UTC()); err != nil {
				return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonAdminLogoutRuntimeError, err)
			}
		}
	}
	if err := s.deviceRT.DeleteDeviceSecret(ctx, trimmedDeviceID); err != nil {
		return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonAdminLogoutCacheError, err)
	}
	return nil
}

// FinalizeInactiveSessions quét runtime cache, flush last_seen xuống DB cho
// các device không hoạt động trước inactiveBefore, sau đó xoá runtime.
//
// Mục tiêu:
// - giảm áp lực ghi DB so với việc flush mỗi request,
// - đảm bảo last_seen vẫn được persist khi session timeout.
//
// Boundary:
// - Chỉ flush DB khi runtime đánh dấu LastSeenDirty = true.
// - Lỗi flush DB hoặc xoá cache bị nuốt để vòng quét tiếp tục với device kế.
func (s *AdminAPIKeyService) FinalizeInactiveSessions(ctx context.Context, inactiveBefore time.Time, limit int) error {
	records, err := s.deviceRT.ScanDeviceRuntimes(ctx, limit)
	if err != nil {
		return apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonAdminFinalizeDependencyError, err)
	}
	threshold := inactiveBefore.UTC().Unix()
	for _, record := range records {
		if record.LastSeenAt <= 0 || record.LastSeenAt > threshold {
			continue
		}
		// Finalize chỉ ghi last_seen nếu có delta đã track trong Redis runtime.
		if strings.TrimSpace(record.TrackedDeviceID) != "" && record.LastSeenDirty {
			_ = s.repo.TouchAdminDeviceLastSeen(ctx, record.TrackedDeviceID, optionalStringPointer(record.LastSeenIP), optionalStringPointer(record.LastSeenUserAgent), time.Unix(record.LastSeenAt, 0).UTC())
		}
		_ = s.deviceRT.DeleteDeviceSecret(ctx, record.DeviceID)
	}
	return nil
}

// loadAdminTOTPSecret đọc TOTP secret active từ DB, decrypt và cache RAM 5 phút.
//
// Mục tiêu:
// - giảm số lần decrypt + call DB trong burst login,
// - vẫn fallback DB khi cache miss/expired.
func (s *AdminAPIKeyService) loadAdminTOTPSecret(ctx context.Context) (string, error) {
	settings, err := s.repo.GetAdmin2FASettings(ctx)
	if err != nil {
		return "", apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonAdminLoginDependencyError, err)
	}
	if settings == nil || strings.TrimSpace(settings.SecretCiphertext) == "" {
		return "", apperr.Wrap(iamErrorx.ErrAdminLoginMFAInvalid, iamErrorx.ReasonAdminLoginMFAInvalid, nil)
	}
	if s.apiKeyCache != nil {
		if secret, ok := s.apiKeyCache.GetTOTPSecret(settings.UpdatedAt); ok {
			return secret, nil
		}
	}

	secret, decErr := security.DecryptSecret(settings.SecretCiphertext)
	if decErr != nil {
		return "", apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonAdminLoginDependencyError, decErr)
	}
	if s.apiKeyCache != nil {
		s.apiKeyCache.SetTOTPSecret(settings.UpdatedAt, secret, 5*time.Minute)
	}
	return secret, nil
}

// loadActiveAdminAPIKey đọc key active với chiến lược RAM cache + fallback DB.
//
// Chính sách cache:
// - TTL mặc định 5 phút,
// - nếu key hết hạn sớm hơn 5 phút thì TTL co lại theo expires_at,
// - không trả key đã expired.
func (s *AdminAPIKeyService) loadActiveAdminAPIKey(ctx context.Context) (*iamEntity.AdminAPIKey, error) {
	now := time.Now().UTC()

	if s.apiKeyCache != nil {
		if cached, ok := s.apiKeyCache.GetActiveAPIKey(now); ok {
			item := cached
			return &item, nil
		}
	}

	item, err := s.repo.GetActiveAdminAPIKey(ctx)
	if err != nil || item == nil {
		return item, err
	}

	ttl := 5 * time.Minute
	if item.ExpiresAt.Before(now.Add(ttl)) {
		ttl = item.ExpiresAt.Sub(now)
	}
	if ttl <= 0 {
		return item, nil
	}

	if s.apiKeyCache != nil {
		s.apiKeyCache.SetActiveAPIKey(*item, ttl)
	}

	return item, nil
}

// joinCodes format danh sách recovery codes thành chuỗi nhiều dòng để notify.
func joinCodes(codes []string) string {
	if len(codes) == 0 {
		return ""
	}
	out := ""
	for i, code := range codes {
		if i > 0 {
			out += "\n"
		}
		out += code
	}
	return out
}

// optionalStringPointer trả *string khi raw có nội dung sau trim, ngược lại
// trả nil để repo phân biệt "không cập nhật" vs "set rỗng".
func optionalStringPointer(raw string) *string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil
	}
	return &value
}

// normalizeDevicePublicKey decode + validate ed25519 public key (32 bytes) từ
// chuỗi base64 (std hoặc raw), sau đó re-encode về base64 std làm canonical
// form cho storage và so sánh fingerprint nhất quán.
func normalizeDevicePublicKey(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("empty key")
	}

	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(value)
	}
	if err != nil {
		return "", err
	}
	if len(decoded) != ed25519.PublicKeySize {
		return "", fmt.Errorf("invalid key size")
	}

	return base64.StdEncoding.EncodeToString(decoded), nil
}
