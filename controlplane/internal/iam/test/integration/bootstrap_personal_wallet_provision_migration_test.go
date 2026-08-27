package integration

import (
	"controlplane/internal/iam/migrations"
	"regexp"
	"strings"
	"testing"
)

func TestBootstrapPersonalWalletProvisionMigrationEmitsCanonicalIAMCommands(t *testing.T) {
	sql, err := migrations.Files.ReadFile("000009_bootstrap_personal_wallet_provision.up.sql")
	if err != nil {
		t.Fatalf("read bootstrap personal wallet provision migration: %v", err)
	}

	source := string(sql)
	wantEvents := map[string]string{
		"00000000-0000-0000-0000-000000000001": "59002562-bee4-5f1a-aa66-6a0a9e0efcfa",
		"00000000-0000-0000-0000-000000000002": "cb70c09c-dfee-5a92-b224-85eaf583117e",
		"00000000-0000-0000-0000-000000000003": "5d112cc7-128d-5598-a6c6-376e9c73642f",
		"00000000-0000-0000-0000-000000000004": "66191a81-8a5a-590d-a961-5fa0716c7fbc",
		"00000000-0000-0000-0000-000000000005": "81b33b0c-65a1-538f-9b36-62eeb548b471",
	}
	pairPattern := regexp.MustCompile(`\('([0-9a-f-]{36})'::uuid,\s*'([0-9a-f-]{36})'::uuid\)`)
	pairs := pairPattern.FindAllStringSubmatch(source, -1)
	if len(pairs) != len(wantEvents) {
		t.Fatalf("bootstrap migration must own exactly five owner/event pairs, got %d", len(pairs))
	}
	for _, pair := range pairs {
		ownerID, eventID := pair[1], pair[2]
		wantEventID, ok := wantEvents[ownerID]
		if !ok {
			t.Fatalf("unexpected bootstrap wallet owner %s", ownerID)
		}
		if eventID != wantEventID {
			t.Fatalf("owner %s event ID = %s, want %s", ownerID, eventID, wantEventID)
		}
		delete(wantEvents, ownerID)
	}
	if len(wantEvents) != 0 {
		t.Fatalf("missing bootstrap wallet owners: %v", wantEvents)
	}

	for _, required := range []string{
		"INSERT INTO lifecycle_fact_outbox_records",
		"billing.personal_wallet.provision.requested.v1",
		"'PERSONAL'",
		"'USD'",
		"JOIN users AS user_account",
		"user_account.status = 'active'",
		"ON CONFLICT (event_id) DO NOTHING",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("bootstrap wallet migration must contain %q", required)
		}
	}

	for _, forbidden := range []string{
		"billing.wallets",
		"billing.personal_wallet_provision_inbox",
		"billing.wallet_admission_outbox",
		"'ACTIVE'",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("IAM bootstrap migration must not contain Cost-owned mutation %q", forbidden)
		}
	}
}

func TestBootstrapPersonalWalletProvisionMigrationRequiresAllCanonicalIdentities(t *testing.T) {
	sql, err := migrations.Files.ReadFile("000009_bootstrap_personal_wallet_provision.up.sql")
	if err != nil {
		t.Fatalf("read bootstrap personal wallet provision migration: %v", err)
	}

	source := string(sql)
	for _, required := range []string{
		"IF active_bootstrap_count <> 5 THEN",
		"RAISE EXCEPTION 'IAM bootstrap wallet provision requires five active canonical identities",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("bootstrap wallet migration must fail closed with %q", required)
		}
	}
}
