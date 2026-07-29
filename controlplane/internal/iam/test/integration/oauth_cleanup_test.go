package integration

import (
	"controlplane/internal/iam/migrations"
	"io/fs"
	"strings"
	"testing"
)

func TestRemovedOAuthAuthorizationServerSchemaIsNotBootstrapped(t *testing.T) {
	removedIdentifiers := []string{
		"oauth_clients",
		"oauth_client_secrets",
		"oauth_authorization_codes",
		"oauth_grants",
		"oauth_tokens",
		"oauth_client_type",
		"oauth_client_status",
	}

	entries, err := fs.ReadDir(migrations.Files, ".")
	if err != nil {
		t.Fatalf("read embedded IAM migrations: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		sql, err := migrations.Files.ReadFile(entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		for _, identifier := range removedIdentifiers {
			if strings.Contains(string(sql), identifier) {
				t.Errorf("%s still bootstraps removed identifier %q", entry.Name(), identifier)
			}
		}
	}
}
