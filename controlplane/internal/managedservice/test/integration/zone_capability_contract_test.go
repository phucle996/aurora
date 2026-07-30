package integration

import (
	"os"
	"strings"
	"testing"
)

func TestCustomerDiscoveryRequiresManagedServiceZoneCapability(t *testing.T) {
	for _, sourcePath := range []string{
		"../../repository/personal_catalog_repo.go",
		"../../repository/tenant_catalog_repo.go",
		"../../repository/personal_version_repo.go",
		"../../repository/tenant_version_repo.go",
	} {
		source, err := os.ReadFile(sourcePath)
		if err != nil {
			t.Fatalf("read %s: %v", sourcePath, err)
		}
		query := string(source)
		if !strings.Contains(query, "capability.service_type::text='managed_service'") ||
			!strings.Contains(query, "capability.desired_state=true") {
			t.Fatalf("%s must fail closed when the Zone managed_service capability is disabled", sourcePath)
		}
	}
}
