package repo_test

import (
	"testing"

	"controlplane/internal/config"
	iamRepoImpl "controlplane/internal/iam/repository"
)

func TestNewAuthRepository(t *testing.T) {
	repo := iamRepoImpl.NewAuthRepository(&config.Config{SchemaSQL: config.SchemaSQLCfg{IAM: "iam"}}, nil)
	if repo == nil {
		t.Fatal("expected repository instance")
	}
}
