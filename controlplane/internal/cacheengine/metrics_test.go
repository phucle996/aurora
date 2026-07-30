package cacheengine

import "testing"

func TestMetricNamespaceNeverUsesRawCacheKey(t *testing.T) {
	registered := func(namespace string) bool { return namespace == "user_role" }
	if got := getNamespace("user_role:0e9e2b6a-5e92-4ed8-a69f-6adff1de1133", registered); got != "user_role" {
		t.Fatalf("getNamespace(prefixed) = %q", got)
	}
	if got := getNamespace("user_role", registered); got != "user_role" {
		t.Fatalf("getNamespace(exact namespace) = %q", got)
	}
	if got := getNamespace("attacker_prefix:0e9e2b6a-5e92-4ed8-a69f-6adff1de1133", registered); got != "unknown" {
		t.Fatalf("getNamespace(unregistered prefix) = %q, want unknown", got)
	}
	if got := getNamespace("0e9e2b6a-5e92-4ed8-a69f-6adff1de1133", registered); got != "unknown" {
		t.Fatalf("getNamespace(raw ID) = %q, want unknown", got)
	}
}
