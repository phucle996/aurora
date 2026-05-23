package svc_test

import (
	"testing"

	iamMetrics "controlplane/internal/iam/metrics"
)

func assertOutcomeSet(t *testing.T, name string, values []string) {
	t.Helper()
	seen := map[string]struct{}{}
	for _, value := range values {
		if value == "" {
			t.Fatalf("%s contains empty outcome", name)
		}
		if _, ok := seen[value]; ok {
			t.Fatalf("%s contains duplicate outcome: %s", name, value)
		}
		seen[value] = struct{}{}
	}
}

func TestOutcomeCatalogRegisterAndLogin(t *testing.T) {
	assertOutcomeSet(t, "register_outcomes", []string{
		iamMetrics.OutcomeSuccess,
		iamMetrics.RegisterOutcomeInvalidArgument,
		iamMetrics.RegisterOutcomeExistCheckError,
		iamMetrics.RegisterOutcomeAlreadyExists,
		iamMetrics.RegisterOutcomeHashPasswordErr,
		iamMetrics.RegisterOutcomeIDGenerateErr,
		iamMetrics.RegisterOutcomeInsertError,
	})
	assertOutcomeSet(t, "register_cache_paths", []string{
		iamMetrics.RegisterCachePathMiss,
		iamMetrics.RegisterCachePathNotChecked,
		iamMetrics.RegisterCachePathFallback,
		iamMetrics.RegisterCachePathHitDBCheck,
	})
	assertOutcomeSet(t, "login_outcomes", []string{
		iamMetrics.OutcomeSuccess,
		iamMetrics.LoginOutcomeInvalidCredentials,
		iamMetrics.LoginOutcomeLoadUserError,
		iamMetrics.LoginOutcomeVerificationReq,
		iamMetrics.LoginOutcomeVerificationIssue,
		iamMetrics.LoginOutcomeVerificationPublish,
		iamMetrics.LoginOutcomeIssueAccessError,
		iamMetrics.LoginOutcomeGenerateRefreshErr,
		iamMetrics.LoginOutcomePersistRefreshErr,
		iamMetrics.LoginOutcomeVerifyMailPublishAttempt,
		iamMetrics.LoginOutcomeVerifyMailPublishError,
		iamMetrics.LoginOutcomeVerifyMailPublishSuccess,
		iamMetrics.LoginOutcomeVerifyMailPublishDuplicate,
	})
}

func TestOutcomeCatalogRefreshAndAdmin(t *testing.T) {
	assertOutcomeSet(t, "refresh_token_outcomes", []string{
		iamMetrics.OutcomeSuccess,
		iamMetrics.RefreshOutcomeInvalidSession,
		iamMetrics.RefreshOutcomeLoadSessionError,
		iamMetrics.RefreshOutcomeLoadUserError,
		iamMetrics.RefreshOutcomeIssueAccessError,
		iamMetrics.RefreshOutcomeGenerateRefreshErr,
		iamMetrics.RefreshOutcomeRotateRefreshErr,
	})
	assertOutcomeSet(t, "admin_login_outcomes", []string{
		iamMetrics.OutcomeSuccess,
		iamMetrics.AdminLoginOutcomeInvalidArgument,
		iamMetrics.AdminLoginOutcomeInvalidDevicePublicKey,
		iamMetrics.AdminLoginOutcomeLoadActiveKeyError,
		iamMetrics.AdminLoginOutcomeInvalidCredential,
		iamMetrics.AdminLoginOutcomeLoadTOTPSecretError,
		iamMetrics.AdminLoginOutcomeMFAInvalidEmptyCode,
		iamMetrics.AdminLoginOutcomeMFAInvalidEmptySecret,
		iamMetrics.AdminLoginOutcomeMFAValidateError,
		iamMetrics.AdminLoginOutcomeMFAInvalidCodeOrTimeSkew,
		iamMetrics.AdminLoginOutcomeRecoveryLockError,
		iamMetrics.AdminLoginOutcomeConsumeRecoveryError,
		iamMetrics.AdminLoginOutcomeMFAInvalid,
		iamMetrics.AdminLoginOutcomeDeviceSecretIssueError,
		iamMetrics.AdminLoginOutcomeRuntimeCacheError,
		iamMetrics.AdminLoginOutcomeUpsertDeviceBindingErr,
		iamMetrics.AdminLoginOutcomeAuthUnavailable,
		iamMetrics.AdminLoginOutcomeJTIIssueError,
		iamMetrics.AdminLoginOutcomeRuntimeUpdateError,
		iamMetrics.AdminLoginOutcomeSignTokenError,
	})
	assertOutcomeSet(t, "admin_refresh_outcomes", []string{
		iamMetrics.OutcomeSuccess,
		iamMetrics.AdminRefreshOutcomeInvalidArgument,
		iamMetrics.AdminRefreshOutcomeLoadRuntimeErr,
		iamMetrics.AdminRefreshOutcomeRuntimeNotFound,
		iamMetrics.AdminRefreshOutcomeTouchRuntimeErr,
		iamMetrics.AdminRefreshOutcomeRuntimeConflict,
		iamMetrics.AdminRefreshOutcomeAuthUnavailable,
		iamMetrics.AdminRefreshOutcomeJTIIssueError,
		iamMetrics.AdminRefreshOutcomeSignTokenError,
	})
	assertOutcomeSet(t, "admin_rotation_outcomes", []string{
		iamMetrics.OutcomeSuccess,
		iamMetrics.AdminRotationOutcomeLockContention,
		iamMetrics.AdminRotationOutcomeDeliveryFail,
		iamMetrics.AdminRotationOutcomeRotateFail,
	})
}
