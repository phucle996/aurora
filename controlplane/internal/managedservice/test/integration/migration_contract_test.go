package integration

import (
	"io/fs"
	"strings"
	"testing"

	managedservicemigrations "controlplane/internal/managedservice/migrations"
)

func TestManagedServiceBaselineHasExactlyTenMigrationPairs(t *testing.T) {
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
	if up != 10 || down != 10 {
		t.Fatalf("managed service baseline must contain exactly ten migration pairs, got up=%d down=%d", up, down)
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
	// [COMMENT]: Map tên file migration phân lớp mới tới các đoạn SQL fragment quan trọng cần kiểm tra.
	expected := map[string][]string{
		"000001_managed_service_enums.up.sql": {
			"'provisioning', 'active', 'deleting'",
			"'accepted',",
			"'terminal_failed'",
			"managed_service_observed_state",
		},
		"000002_managed_service_tables.up.sql": {
			"safe_observed_output_schema",
			"zone_selector",
			"capability_requirement",
			"contract_sha256",
			"validated_row_version",
			"validated_bundle_sha256",
			"validated_contract_sha256",
			"DEFERRABLE INITIALLY DEFERRED",
			"actor_subject TEXT",
			"critical_proof_id UUID",
			"row_version BIGINT",
			"name_i18n JSONB",
			"active_revision_id",
			"pending_revision_id",
			"create_intent_sha256",
			"protected_command_payload",
			"protected_command_payload_sha256",
			"payload_key_id UUID NOT NULL",
			"personal_managed_service_deletion_fences",
			"current_command_event_id",
			"tenant_managed_service_deletion_fences",
			"CREATE TABLE IF NOT EXISTS managed_service_outbox_records",
			"owner_type IN ('PERSONAL', 'TENANT')",
			"managed_service.instance.execute",
		},
		"000003_managed_service_indexes.up.sql": {
			"ux_personal_managed_service_operations_nonterminal",
			"ux_tenant_managed_service_operations_nonterminal",
			"ix_personal_managed_service_operations_instance_id",
			"ix_tenant_managed_service_operations_instance_id",
			"ix_managed_service_outbox_pending",
		},
		"000004_managed_service_funcs.up.sql": {
			"reject_blueprint_revision_rewrite()",
			"reject_managed_service_outbox_payload_rewrite()",
			"OLD.state = 'published'",
			"NEW.state = 'retired'",
		},
		"000005_managed_service_triggers.up.sql": {
			"trg_blueprint_revisions_immutable",
			"trg_managed_service_outbox_immutable",
		},
		"000006_managed_service_seeds.up.sql": {
			"Managed Service Catalog",
		},
		"000008_delivery_epoch.up.sql": {
			"delivery_epoch BIGINT",
			"enforce_managed_service_outbox_delivery_epoch()",
		},
		"000009_resize_operation.up.sql": {
			"ADD VALUE IF NOT EXISTS 'resize'",
		},
		"000010_remove_result_inbox.up.sql": {
			"reconcile before removal",
			"DROP TABLE IF EXISTS personal_managed_service_result_inbox",
			"DROP TABLE IF EXISTS tenant_managed_service_result_inbox",
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

	// [COMMENT]: Đảm bảo không vi phạm nguyên tắc outbox tập trung (không tạo outbox riêng theo owner).
	for _, name := range []string{
		"000002_managed_service_tables.up.sql",
		"000003_managed_service_indexes.up.sql",
	} {
		body, err := fs.ReadFile(managedservicemigrations.Files, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(body), "personal_managed_service_outbox") || strings.Contains(string(body), "tenant_managed_service_outbox") {
			t.Fatalf("%s reintroduced owner-branch outbox tables", name)
		}
		if strings.Contains(string(body), "managed_service_result_inbox") {
			t.Fatalf("%s reintroduced the removed result inbox SoT", name)
		}
	}
}
