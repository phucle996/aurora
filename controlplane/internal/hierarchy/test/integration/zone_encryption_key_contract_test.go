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
		"loaded_observed_fencing_token bigint",
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

func TestProtectedPayloadRetentionAndCommitRaceGuardsCoverEveryRuntimeOutbox(t *testing.T) {
	repositorySource, err := os.ReadFile("../../repository/zone_encryption_key_repo.go")
	if err != nil {
		t.Fatalf("read encryption-key repository: %v", err)
	}
	for _, required := range []string{
		"retained_ciphertext",
		"storage_outbox_records",
		"mail_outbox_records",
		"mail_protected_projections",
		"hypervisor_outbox_records",
		"managed_service_outbox_records",
		"personal_managed_service_instance_revisions",
		"tenant_managed_service_instance_revisions",
		"interval '5 minutes'",
	} {
		if !strings.Contains(string(repositorySource), required) {
			t.Fatalf("retirement guard is missing %q", required)
		}
	}

	for _, migration := range []string{
		"../../../storage/migrations/000008_outbox_payload_protection.up.sql",
		"../../../mail/migrations/000005_outbox_payload_protection.up.sql",
		"../../../hypervisor/migrations/000006_outbox_payload_protection.up.sql",
		"../../../managedservice/migrations/000007_payload_protection.up.sql",
	} {
		body, readErr := os.ReadFile(migration)
		if readErr != nil {
			t.Fatalf("read %s: %v", migration, readErr)
		}
		sql := strings.ToLower(string(body))
		for _, required := range []string{
			"for key share",
			"zone_encryption_keys",
			"status in ('active', 'decrypt_only')",
			"payload key is not decryptable",
		} {
			if !strings.Contains(sql, required) {
				t.Fatalf("%s is missing commit-race guard %q", migration, required)
			}
		}
	}
}

func TestProducerReadinessRequiresARecentFencedZoneObservation(t *testing.T) {
	producerSource, err := os.ReadFile("../../../security/job_payload.go")
	if err != nil {
		t.Fatalf("read protected payload producer: %v", err)
	}
	for _, required := range []string{
		"status = 'active'",
		"loaded_at is not null",
		"loaded_observed_at >= now() - interval '30 seconds'",
		"loaded_observed_fencing_token is not null",
	} {
		if !strings.Contains(strings.ToLower(string(producerSource)), required) {
			t.Fatalf("protected-payload producer readiness is missing %q", required)
		}
	}
}
