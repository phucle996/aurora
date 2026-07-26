package billingmigrations

import (
	"io/fs"
	"strings"
	"testing"
)

// [COMMENT]: TestAccountMigrationsKeepOwnerSpecificProvisioningInboxes kiểm tra schema bảng ở 000002_tables.up.sql chứa đầy đủ các inbox chuyên biệt theo owner và payment settlement
func TestAccountMigrationsKeepOwnerSpecificProvisioningInboxes(t *testing.T) {
	tables := readMigration(t, "000002_tables.up.sql")

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
