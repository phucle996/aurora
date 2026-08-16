package app

import (
	"strings"
	"testing"
)

func TestBuildCostEngineEnvironmentDoesNotLeakAPIVaultIdentity(t *testing.T) {
	environment, err := buildCostEngineEnvironment([]string{
		"VAULT_ADDR=https://vault.internal",
		"VAULT_TOKEN=api-token",
		"VAULT_KUBERNETES_ROLE=cost-manager-api",
		"VAULT_ENGINE_TOKEN=engine-token",
		"PG_SSL_MODE=verify-full",
	})
	if err != nil {
		t.Fatalf("build engine environment: %v", err)
	}
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "api-token") || strings.Contains(joined, "cost-manager-api") {
		t.Fatalf("API Vault identity leaked into engine environment: %s", joined)
	}
	if !strings.Contains(joined, "VAULT_TOKEN=engine-token") {
		t.Fatalf("engine token was not mapped to the child identity: %s", joined)
	}
	if !strings.Contains(joined, "PG_SSL_MODE=verify-full") {
		t.Fatalf("non-identity runtime configuration was removed: %s", joined)
	}
}

func TestBuildCostEngineEnvironmentMapsDedicatedKubernetesRole(t *testing.T) {
	environment, err := buildCostEngineEnvironment([]string{
		"VAULT_ADDR=https://vault.internal",
		"VAULT_KUBERNETES_ROLE=cost-manager-api",
		"VAULT_KUBERNETES_JWT_PATH=/var/run/api/token",
		"VAULT_ENGINE_KUBERNETES_ROLE=cost-manager-engine",
		"VAULT_ENGINE_KUBERNETES_JWT_PATH=/var/run/engine/token",
	})
	if err != nil {
		t.Fatalf("build engine environment: %v", err)
	}
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "cost-manager-api") || strings.Contains(joined, "/var/run/api/token") {
		t.Fatalf("API Kubernetes Vault identity leaked into engine environment: %s", joined)
	}
	if !strings.Contains(joined, "VAULT_KUBERNETES_ROLE=cost-manager-engine") ||
		!strings.Contains(joined, "VAULT_KUBERNETES_JWT_PATH=/var/run/engine/token") {
		t.Fatalf("engine Kubernetes Vault identity was not mapped: %s", joined)
	}
}

func TestBuildCostEngineEnvironmentRejectsSharedOrMissingIdentity(t *testing.T) {
	_, err := buildCostEngineEnvironment([]string{
		"VAULT_ADDR=https://vault.internal",
		"VAULT_TOKEN=api-token",
	})
	if err == nil {
		t.Fatal("expected isolated Cost Engine Vault identity to be required")
	}
}
