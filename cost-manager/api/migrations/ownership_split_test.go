package billingmigrations

import (
	"io/fs"
	"strings"
	"testing"
)

// [COMMENT]: TestAccountMigrationsKeepOwnerSpecificProvisioningInboxes kiểm tra core tables giữ boundary riêng cho personal/tenant và payment settlement.
func TestAccountMigrationsKeepOwnerSpecificProvisioningInboxes(t *testing.T) {
	tables := readMigration(t, "000002_tables_core.up.sql")

	if !strings.Contains(tables, "billing.personal_wallet_provision_inbox") ||
		!strings.Contains(tables, "billing.personal_referral_reservations") {
		t.Fatal("personal account schema does not enforce its owner-specific tables")
	}
	if !strings.Contains(tables, "billing.tenant_wallet_provision_inbox") ||
		!strings.Contains(tables, "actor_user_id   UUID NOT NULL") {
		t.Fatal("tenant account schema does not enforce actor-only provisioning inbox")
	}
	if !strings.Contains(tables, "billing.payment_intents") ||
		!strings.Contains(tables, "billing.payment_webhook_inbox") ||
		!strings.Contains(tables, "PRIMARY KEY (provider, provider_event_id)") {
		t.Fatal("shared payment schema lost provider-wide durability boundaries")
	}
}

func TestMigrationsUseTheSixDomainFiles(t *testing.T) {
	expected := map[string]bool{
		"000001_enums.up.sql":                true,
		"000002_tables_core.up.sql":          true,
		"000003_tables_pricing.up.sql":       true,
		"000004_tables_settlement.up.sql":    true,
		"000005_indexes_and_triggers.up.sql": true,
		"000006_seeds.up.sql":                true,
	}
	entries, err := fs.ReadDir(Files, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		if !expected[entry.Name()] {
			t.Fatalf("unexpected migration file %s; baseline must not retain overlay migrations", entry.Name())
		}
		delete(expected, entry.Name())
	}
	for name := range expected {
		t.Fatalf("missing domain migration %s", name)
	}
}

func TestBaselineDoesNotOverlayOrDropLegacyTables(t *testing.T) {
	entries, err := fs.ReadDir(Files, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		content := readMigration(t, entry.Name())
		if strings.Contains(content, "ALTER TABLE") || strings.Contains(content, "DROP TABLE") {
			t.Fatalf("%s overlays or drops a table; use the final table definition in its domain file", entry.Name())
		}
		for _, legacy := range []string{"billing.packs", "billing.plans", "billing.subscriptions", "billing.tiers", "billing.tier_versions", "billing.tier_version_ranges", "billing.billing_runs"} {
			if strings.Contains(content, legacy) {
				t.Fatalf("%s retains removed legacy catalog table %s", entry.Name(), legacy)
			}
		}
	}
}

// [COMMENT]: TestMigrationsDoNotReintroducePolymorphicProvisionInbox đảm bảo không re-introduce polymorphic provision inbox
func TestMigrationsDoNotReintroducePolymorphicProvisionInbox(t *testing.T) {
	entries, err := fs.ReadDir(Files, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		content := readMigration(t, entry.Name())
		if strings.Contains(content, "billing.wallet_provision_inbox") {
			t.Fatalf("%s reintroduces the cross-owner provisioning inbox", entry.Name())
		}
	}
}

func readMigration(t *testing.T, name string) string {
	t.Helper()
	content, err := fs.ReadFile(Files, name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(content)
}
