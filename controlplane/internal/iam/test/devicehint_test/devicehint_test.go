package devicehint_test

import (
	"strings"
	"testing"

	deviceHint "controlplane/internal/iam/devicehint"
)

func TestSanitizeHostname_AllowedAndTruncated(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"happy", "GF63-Thin-11UC", "GF63-Thin-11UC"},
		{"with-trim", "  laptop_01.local  ", "laptop_01.local"},
		{"strip-illegal", "../etc/passwd; rm -rf", "..etcpasswdrm-rf"},
		{"empty", "", ""},
		{"too-short-after-strip", "/", ""},
		{"single-letter-rejected", "a", ""},
		{"unicode-stripped-but-keeps-2-letters", "máy", "my"},
		{"truncate-200", strings.Repeat("a", 200), strings.Repeat("a", 64)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deviceHint.SanitizeHostname(tc.in)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestResolveDeviceName_PriorityAndFallback(t *testing.T) {
	if got := deviceHint.ResolveDeviceName("HostA", "HostB"); got != "HostA" {
		t.Fatalf("expected primary header to win, got %q", got)
	}
	if got := deviceHint.ResolveDeviceName("", "alias-host"); got != "alias-host" {
		t.Fatalf("expected alias fallback, got %q", got)
	}
	if got := deviceHint.ResolveDeviceName("", ""); got != deviceHint.DefaultDeviceName {
		t.Fatalf("expected default, got %q", got)
	}
}

func TestSanitizeClientDeviceID_RejectsInvalid(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"abc-123_X.Y", "abc-123_X.Y"},
		{strings.Repeat("a", 200), ""},
		{"contains spaces", ""},
		{"contains/slash", ""},
	}
	for _, tc := range cases {
		got := deviceHint.SanitizeClientDeviceID(tc.in)
		if got != tc.want {
			t.Fatalf("input %q got %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolveClientDeviceID_BootstrapWhenMissing(t *testing.T) {
	id, prov := deviceHint.ResolveClientDeviceID("abc-123")
	if id != "abc-123" || prov != deviceHint.ProvenanceClient {
		t.Fatalf("expected client provenance, got id=%q prov=%q", id, prov)
	}
	id, prov = deviceHint.ResolveClientDeviceID("")
	if prov != deviceHint.ProvenanceServerBootstrap {
		t.Fatalf("expected server-bootstrap, got %q", prov)
	}
	if id == "" {
		t.Fatal("expected generated id, got empty")
	}
}
