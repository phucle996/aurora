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

func TestOutcomeCatalog(t *testing.T) {
	assertOutcomeSet(t, "core_outcomes", []string{
		iamTaxonomy.Success,
		iamTaxonomy.Failure,
		iamTaxonomy.FailureUnknown,
		iamTaxonomy.InvalidArgument,
		iamTaxonomy.InvalidCredential,
		iamTaxonomy.InvalidSession,
		iamTaxonomy.PreConditionFailed,
		iamTaxonomy.PreConditionSuccess,
		iamTaxonomy.LockBusy,
		iamTaxonomy.LockUnknownError,
		iamTaxonomy.TokenGenerateFail,
		iamTaxonomy.TokenGenerateSuccess,
		iamTaxonomy.TelegramSendFail,
		iamTaxonomy.UuidGenerateFail,
		iamTaxonomy.SetAccessSessionFail,
		iamTaxonomy.ZoneUnavailable,
		iamTaxonomy.GetL1CacheFail,
		iamTaxonomy.GetL2CacheFail,
	})
}
