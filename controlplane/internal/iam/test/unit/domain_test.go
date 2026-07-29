package unit

import (
	"testing"

	iamTaxonomy "controlplane/internal/iam/taxonomy"
)

// [COMMENT]: Unit test kiểm tra tính toàn vẹn của các định nghĩa lỗi chuẩn hóa trong IAM taxonomy
func TestIAMTaxonomyErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "User Not Found Error",
			err:      iamTaxonomy.ErrUserNotFound,
			expected: "iam: user not found",
		},
		{
			name:     "MFA Already Enabled Error",
			err:      iamTaxonomy.ErrMFAAlreadyEnabled,
			expected: "iam: mfa already enabled",
		},
		{
			name:     "Action Not Allowed Error",
			err:      iamTaxonomy.ErrActionNotAllowed,
			expected: "iam: action not allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Error() != tt.expected {
				t.Errorf("expected error string %q, got %q", tt.expected, tt.err.Error())
			}
		})
	}
}
