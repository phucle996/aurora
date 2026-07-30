package integration

import (
	"io/fs"
	"strings"
	"testing"

	managedservicemigrations "controlplane/internal/managedservice/migrations"
)

func TestManagedServiceBaselineHasExactlySixMigrationPairs(t *testing.T) {
	entries, err := fs.ReadDir(managedservicemigrations.Files, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	up := 0
	down := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		switch {
		case strings.HasSuffix(entry.Name(), ".up.sql"):
			up++
		case strings.HasSuffix(entry.Name(), ".down.sql"):
			down++
		}
	}
	if up != 6 || down != 6 {
		t.Fatalf("managed service baseline must contain exactly six migration pairs, got up=%d down=%d", up, down)
	}
}

func TestManagedServiceBaselineDoesNotCreateZoneKeyLifecycle(t *testing.T) {
	entries, err := fs.ReadDir(managedservicemigrations.Files, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		body, err := fs.ReadFile(managedservicemigrations.Files, entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		if strings.Contains(string(body), "parameter_key") || strings.Contains(string(body), "zone_managed_service_parameter") {
			t.Fatalf("%s reintroduced the removed Zone parameter-key lifecycle", entry.Name())
		}
	}
}

func TestManagedServiceBaselineMatchesFrozenLifecycleShape(t *testing.T) {
	expected := map[string][]string{
		"000001_managed_service_enums.up.sql": {
			"'provisioning', 'active', 'deleting'",
			"'accepted',",
			"'terminal_failed'",
			"managed_service_observed_state",
			"managed_service_result_outcome",
		},
		"000003_blueprint_revisions.up.sql": {
			"safe_observed_output_schema",
			"zone_selector",
			"capability_requirement",
			"contract_sha256",
			"validated_row_version",
			"validated_bundle_sha256",
			"validated_contract_sha256",
			"DEFERRABLE INITIALLY DEFERRED",
			"OLD.state = 'published'",
			"NEW.state = 'retired'",
		},
		"000002_system_catalog.up.sql": {
			"actor_subject TEXT",
			"critical_proof_id UUID",
			"row_version BIGINT",
			"name_i18n JSONB",
		},
		"000004_personal_aggregate.up.sql": {
			"active_revision_id",
			"pending_revision_id",
			"create_intent_sha256",
			"personal_managed_service_result_inbox",
			"personal_managed_service_deletion_fences",
			"current_command_event_id",
		},
		"000005_tenant_aggregate.up.sql": {
			"active_revision_id",
			"pending_revision_id",
			"create_intent_sha256",
			"tenant_managed_service_result_inbox",
			"tenant_managed_service_deletion_fences",
			"current_command_event_id",
		},
		"000006_outbox_indexes_triggers.up.sql": {
			"CREATE TABLE IF NOT EXISTS managed_service_outbox_records",
			"owner_type IN ('PERSONAL', 'TENANT')",
			"managed_service.instance.execute",
			"ux_personal_managed_service_operations_nonterminal",
			"ux_tenant_managed_service_operations_nonterminal",
		},
	}

	for name, wanted := range expected {
		body, err := fs.ReadFile(managedservicemigrations.Files, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, fragment := range wanted {
			if !strings.Contains(string(body), fragment) {
				t.Fatalf("%s must contain frozen lifecycle fragment %q", name, fragment)
			}
		}
	}

	for _, name := range []string{
		"000004_personal_aggregate.up.sql",
		"000005_tenant_aggregate.up.sql",
		"000006_outbox_indexes_triggers.up.sql",
	} {
		body, err := fs.ReadFile(managedservicemigrations.Files, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(body), "personal_managed_service_outbox") || strings.Contains(string(body), "tenant_managed_service_outbox") {
			t.Fatalf("%s reintroduced owner-branch outbox tables", name)
		}
	}
}
