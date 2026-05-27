package svc_test

import (
	"testing"

	iamTaxonomy "controlplane/internal/iam/taxonomy"
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
		iamTaxonomy.OutcomeSuccess,
		iamTaxonomy.RegisterOutcomeInvalidArgument,
		iamTaxonomy.RegisterOutcomeExistCheckError,
		iamTaxonomy.RegisterOutcomeAlreadyExists,
		iamTaxonomy.RegisterOutcomeHashPasswordErr,
		iamTaxonomy.RegisterOutcomeIDGenerateErr,
		iamTaxonomy.RegisterOutcomeInsertError,
	})
	assertOutcomeSet(t, "register_cache_paths", []string{
		iamTaxonomy.RegisterCachePathMiss,
		iamTaxonomy.RegisterCachePathNotChecked,
		iamTaxonomy.RegisterCachePathFallback,
		iamTaxonomy.RegisterCachePathHitDBCheck,
	})
	assertOutcomeSet(t, "login_outcomes", []string{
		iamTaxonomy.OutcomeSuccess,
		iamTaxonomy.LoginOutcomeInvalidCredentials,
		iamTaxonomy.LoginOutcomeLoadUserError,
		iamTaxonomy.LoginOutcomeVerificationReq,
		iamTaxonomy.LoginOutcomeVerificationIssue,
		iamTaxonomy.LoginOutcomeVerificationPublish,
		iamTaxonomy.LoginOutcomeIssueAccessError,
		iamTaxonomy.LoginOutcomeGenerateRefreshErr,
		iamTaxonomy.LoginOutcomePersistRefreshErr,
		iamTaxonomy.LoginOutcomeVerifyMailPublishAttempt,
		iamTaxonomy.LoginOutcomeVerifyMailPublishError,
		iamTaxonomy.LoginOutcomeVerifyMailPublishSuccess,
		iamTaxonomy.LoginOutcomeVerifyMailPublishDuplicate,
	})
}

func TestOutcomeCatalogRefreshAndAdmin(t *testing.T) {
	assertOutcomeSet(t, "refresh_token_outcomes", []string{
		iamTaxonomy.OutcomeSuccess,
		iamTaxonomy.RefreshOutcomeInvalidSession,
		iamTaxonomy.RefreshOutcomeLoadSessionError,
		iamTaxonomy.RefreshOutcomeLoadUserError,
		iamTaxonomy.RefreshOutcomeIssueAccessError,
		iamTaxonomy.RefreshOutcomeGenerateRefreshErr,
		iamTaxonomy.RefreshOutcomeRotateRefreshErr,
	})
	assertOutcomeSet(t, "admin_login_outcomes", []string{
		iamTaxonomy.OutcomeSuccess,
		iamTaxonomy.AdminLoginOutcomeInvalidArgument,
		iamTaxonomy.AdminLoginOutcomeInvalidDevicePublicKey,
		iamTaxonomy.AdminLoginOutcomeLoadActiveKeyError,
		iamTaxonomy.AdminLoginOutcomeInvalidCredential,
		iamTaxonomy.AdminLoginOutcomeLoadTOTPSecretError,
		iamTaxonomy.AdminLoginOutcomeMFAInvalidEmptyCode,
		iamTaxonomy.AdminLoginOutcomeMFAInvalidEmptySecret,
		iamTaxonomy.AdminLoginOutcomeMFAValidateError,
		iamTaxonomy.AdminLoginOutcomeMFAInvalidCodeOrTimeSkew,
		iamTaxonomy.AdminLoginOutcomeRecoveryLockError,
		iamTaxonomy.AdminLoginOutcomeConsumeRecoveryError,
		iamTaxonomy.AdminLoginOutcomeMFAInvalid,
		iamTaxonomy.AdminLoginOutcomeDeviceSecretIssueError,
		iamTaxonomy.AdminLoginOutcomeRuntimeCacheError,
		iamTaxonomy.AdminLoginOutcomeUpsertDeviceBindingErr,
		iamTaxonomy.AdminLoginOutcomeAuthUnavailable,
		iamTaxonomy.AdminLoginOutcomeJTIIssueError,
		iamTaxonomy.AdminLoginOutcomeRuntimeUpdateError,
		iamTaxonomy.AdminLoginOutcomeSignTokenError,
	})
	assertOutcomeSet(t, "admin_refresh_outcomes", []string{
		iamTaxonomy.OutcomeSuccess,
		iamTaxonomy.AdminRefreshOutcomeInvalidArgument,
		iamTaxonomy.AdminRefreshOutcomeLoadRuntimeErr,
		iamTaxonomy.AdminRefreshOutcomeRuntimeNotFound,
		iamTaxonomy.AdminRefreshOutcomeTouchRuntimeErr,
		iamTaxonomy.AdminRefreshOutcomeRuntimeConflict,
		iamTaxonomy.AdminRefreshOutcomeAuthUnavailable,
		iamTaxonomy.AdminRefreshOutcomeJTIIssueError,
		iamTaxonomy.AdminRefreshOutcomeSignTokenError,
	})
	assertOutcomeSet(t, "admin_rotation_outcomes", []string{
		iamTaxonomy.OutcomeSuccess,
		iamTaxonomy.AdminRotationOutcomeLockContention,
		iamTaxonomy.AdminRotationOutcomeDeliveryFail,
		iamTaxonomy.AdminRotationOutcomeRotateFail,
	})
}
