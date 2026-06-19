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

func TestOutcomeCatalog(t *testing.T) {
	assertOutcomeSet(t, "core_outcomes", []string{
		iamMetrics.OutcomeSuccess,
		iamMetrics.OutcomeFailure,
		iamMetrics.OutcomeFailureUnknown,
		iamMetrics.OutcomePreConditionFailed,
		iamMetrics.OutcomeInvalidCredential,
		iamMetrics.OutcomeLockBusy,
	})
}
