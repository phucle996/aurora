package repo_test

import (
	"testing"

	"controlplane/internal/config"
	coreRepoImpl "controlplane/internal/core/repository"
)

func TestNewSecretRepositoryReturnsInterface(t *testing.T) {
	cfg := &config.Config{SchemaSQL: config.SchemaSQLCfg{Core: "coretest"}}
	repo := coreRepoImpl.NewSecretRepository(cfg, nil)
	if repo == nil {
		t.Fatal("NewSecretRepository() returned nil")
	}
}
