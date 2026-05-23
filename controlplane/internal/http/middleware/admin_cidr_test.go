package middleware

import "testing"

func TestAdminCIDRAllowlist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		allowlist  []string
		clientIP   string
		wantAllow  bool
		wantEnable bool
	}{
		{
			name:       "empty allowlist disables cidr restriction",
			allowlist:  nil,
			clientIP:   "203.0.113.10",
			wantAllow:  true,
			wantEnable: false,
		},
		{
			name:       "cidr match allows client ip",
			allowlist:  []string{"10.0.0.0/8"},
			clientIP:   "10.20.30.40",
			wantAllow:  true,
			wantEnable: true,
		},
		{
			name:       "exact ip match allows client ip",
			allowlist:  []string{"203.0.113.10"},
			clientIP:   "203.0.113.10",
			wantAllow:  true,
			wantEnable: true,
		},
		{
			name:       "non matching ip is blocked",
			allowlist:  []string{"10.0.0.0/8", "203.0.113.10"},
			clientIP:   "198.51.100.5",
			wantAllow:  false,
			wantEnable: true,
		},
		{
			name:       "invalid configured allowlist fails closed",
			allowlist:  []string{"not-a-cidr"},
			clientIP:   "203.0.113.10",
			wantAllow:  false,
			wantEnable: true,
		},
		{
			name:       "ipv6 cidr match allows client ip",
			allowlist:  []string{"2001:db8::/32"},
			clientIP:   "2001:db8::1",
			wantAllow:  true,
			wantEnable: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			allowlist := compileAdminCIDRAllowlist(tt.allowlist)
			if allowlist.enabled != tt.wantEnable {
				t.Fatalf("enabled = %v, want %v", allowlist.enabled, tt.wantEnable)
			}
			if got := isIPAllowed(tt.clientIP, allowlist); got != tt.wantAllow {
				t.Fatalf("isIPAllowed() = %v, want %v", got, tt.wantAllow)
			}
		})
	}
}
