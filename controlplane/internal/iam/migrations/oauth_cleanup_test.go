package migrations

import (
	"io/fs"
	"strings"
	"testing"
)

func TestRemovedOAuthAuthorizationServerSchemaIsNotBootstrapped(t *testing.T) {
	const cleanupMigration = "000010_remove_unused_oauth_authorization_server.up.sql"
	removedIdentifiers := []string{
		"oauth_clients",
		"oauth_client_secrets",
		"oauth_authorization_codes",
		"oauth_grants",
		"oauth_tokens",
		"oauth_client_type",
		"oauth_client_status",
	}

	entries, err := fs.ReadDir(Files, ".")
	if err != nil {
		t.Fatalf("read embedded IAM migrations: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == cleanupMigration {
			continue
		}
		sql, err := Files.ReadFile(entry.Name())
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

func TestOAuthCleanupRefusesUnexpectedDataBeforeDrop(t *testing.T) {
	sql, err := Files.ReadFile("000010_remove_unused_oauth_authorization_server.up.sql")
	if err != nil {
		t.Fatalf("read OAuth cleanup migration: %v", err)
	}
	migration := string(sql)

	// [COMMENT]: The row guard must execute before the first DROP so a stale or
	// unexpectedly used table aborts the enclosing migration transaction intact.
	guardAt := strings.Index(migration, "refusing to drop non-empty dead-code table")
	dropAt := strings.Index(migration, "'DROP TABLE IF EXISTS %I.%I'")
	if guardAt < 0 || dropAt < 0 || guardAt >= dropAt {
		t.Fatal("OAuth cleanup must guard all tables for unexpected rows before destructive DDL")
	}
}
