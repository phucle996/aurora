package integration

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstanceRepositoriesKeepOwnerBranchesAndProtectedInputIsOpaque(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	repositoryDir := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", "repository"))

	contracts := []struct {
		file      string
		required  []string
		forbidden []string
	}{
		{
			file: "personal_instance_repo.go",
			required: []string{
				"personal_workspaces", "personal_managed_service_instances",
				"personal_managed_service_operations", "workspace.owner_id=$2",
				"workspace.zone_id=$3", "target.metadata_version=$6",
			},
			forbidden: []string{"tenant_managed_service_", "parameter_envelope", "input_sha256", "desired_spec_sha256", "create_intent_sha256"},
		},
		{
			file: "tenant_instance_repo.go",
			required: []string{
				"tenant_workspaces", "tenant_managed_service_instances",
				"tenant_managed_service_operations", "workspace.tenant_id=$2",
				"workspace.zone_id=$3", "target.metadata_version=$6",
			},
			forbidden: []string{"personal_managed_service_", "parameter_envelope", "input_sha256", "desired_spec_sha256", "create_intent_sha256"},
		},
	}

	for _, contract := range contracts {
		t.Run(contract.file, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(repositoryDir, contract.file))
			if err != nil {
				t.Fatalf("read repository source: %v", err)
			}
			source := string(body)
			for _, required := range contract.required {
				if !strings.Contains(source, required) {
					t.Errorf("missing scope/atomicity invariant %q", required)
				}
			}
			for _, forbidden := range contract.forbidden {
				if strings.Contains(source, forbidden) {
					t.Errorf("repository crosses owner/protected-input boundary with %q", forbidden)
				}
			}
		})
	}
}
