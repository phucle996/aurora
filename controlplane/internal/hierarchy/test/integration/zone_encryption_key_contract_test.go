package integration

import (
	"io/fs"
	"os"
	"strings"
	"testing"

	hierarchyMigrations "controlplane/internal/hierarchy/migrations"
)

func TestZoneEncryptionKeyBaselineHasSecurityAndRaceGuards(t *testing.T) {
	tables, err := fs.ReadFile(hierarchyMigrations.Files, "000002_hierarchy_tables.up.sql")
	if err != nil {
		t.Fatalf("read tables migration: %v", err)
	}
	indexes, err := fs.ReadFile(hierarchyMigrations.Files, "000003_hierarchy_indexes.up.sql")
	if err != nil {
		t.Fatalf("read indexes migration: %v", err)
	}
	tableSQL := strings.ToLower(string(tables))
	indexSQL := strings.ToLower(string(indexes))
	for _, required := range []string{
		"create table if not exists zone_encryption_keys",
		"octet_length(public_key) = 32",
		"octet_length(fingerprint) = 32",
		"registered_proof_id uuid not null",
		"hpke_x25519_hkdf_sha256_aes_256_gcm",
	} {
		if !strings.Contains(tableSQL, required) {
			t.Fatalf("missing table invariant %q", required)
		}
	}
	if strings.Contains(tableSQL, "private_key") {
		t.Fatal("Hierarchy baseline must never define a private_key column")
	}
	for _, required := range []string{
		"unique index if not exists ux_zone_encryption_keys_fingerprint",
		"unique index if not exists ux_zone_encryption_keys_one_active_per_zone",
		"index if not exists ix_zone_encryption_keys_zone_created",
		"where status = 'active'",
	} {
		if !strings.Contains(indexSQL, required) {
			t.Fatalf("missing index invariant %q", required)
		}
	}
}

func TestZoneEncryptionKeyRoutesKeepMutationsCritical(t *testing.T) {
	routeSource, err := os.ReadFile("../../route.go")
	if err != nil {
		t.Fatalf("read route source: %v", err)
	}
	source := string(routeSource)
	for _, required := range []string{
		`GET("/admin/hierarchy/zones/:zone_id/encryption-keys"`,
		`POST("/admin/critical/hierarchy/zones/:zone_id/encryption-keys"`,
		`POST("/admin/critical/hierarchy/zones/:zone_id/encryption-keys/:key_id/activate"`,
		`POST("/admin/critical/hierarchy/zones/:zone_id/encryption-keys/:key_id/retire"`,
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("missing route contract %q", required)
		}
	}
}
