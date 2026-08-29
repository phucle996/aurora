package pubsubHandler

import (
	"errors"
	"testing"

	iamTaxonomy "controlplane/internal/iam/taxonomy"
)

func TestLoginRejectionPreservesOperationalAccountState(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		logMessage string
		errorCode  string
	}{
		{
			name:       "disabled",
			err:        iamTaxonomy.ErrAccountDisabled,
			logMessage: "Login attempt rejected: account disabled",
			errorCode:  "ACCOUNT_DISABLED",
		},
		{
			name:       "suspended",
			err:        iamTaxonomy.ErrAccountSuspended,
			logMessage: "Login attempt rejected: account suspended",
			errorCode:  "ACCOUNT_SUSPENDED",
		},
		{
			name:       "invalid credentials",
			err:        iamTaxonomy.ErrInvalidCredentials,
			logMessage: "Login attempt failed: invalid credentials",
			errorCode:  "INVALID_CREDENTIALS",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rejection, handled := loginRejectionFor(errors.Join(tc.err, errors.New("wrapped")))
			if !handled {
				t.Fatal("expected a handled login rejection")
			}
			if rejection.logMessage != tc.logMessage {
				t.Fatalf("log message = %q, want %q", rejection.logMessage, tc.logMessage)
			}
			if rejection.errorCode != tc.errorCode {
				t.Fatalf("error code = %q, want %q", rejection.errorCode, tc.errorCode)
			}
		})
	}
}

func TestLoginRejectionLeavesInfrastructureFailureUnhandled(t *testing.T) {
	if _, handled := loginRejectionFor(iamTaxonomy.ErrAuthenticationUnavailable); handled {
		t.Fatal("authentication infrastructure failure must not be reported as a credential rejection")
	}
}
