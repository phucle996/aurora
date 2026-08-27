package migrations

import (
	"io/fs"
	"slices"
	"strings"
	"testing"
)

func TestHypervisorMigrationLayers(t *testing.T) {
	entries, err := fs.ReadDir(Files, ".")
	if err != nil {
		t.Fatalf("read embedded hypervisor migrations: %v", err)
	}

	var upMigrations []string
	var downMigrations []string
	for _, entry := range entries {
		switch {
		case strings.HasSuffix(entry.Name(), ".up.sql"):
			upMigrations = append(upMigrations, entry.Name())
		case strings.HasSuffix(entry.Name(), ".down.sql"):
			downMigrations = append(downMigrations, entry.Name())
		}
	}

	expectedUp := []string{
		"000001_hypervisor_enums.up.sql",
		"000002_hypervisor_tables.up.sql",
		"000003_hypervisor_indexes.up.sql",
		"000004_hypervisor_functions.up.sql",
		"000005_hypervisor_triggers.up.sql",
		"000006_outbox_payload_protection.up.sql",
	}
	expectedDown := []string{
		"000001_hypervisor_enums.down.sql",
		"000002_hypervisor_tables.down.sql",
		"000003_hypervisor_indexes.down.sql",
		"000004_hypervisor_functions.down.sql",
		"000005_hypervisor_triggers.down.sql",
		"000006_outbox_payload_protection.down.sql",
	}
	if !slices.Equal(upMigrations, expectedUp) {
		t.Fatalf("unexpected hypervisor up-migration layers: %v", upMigrations)
	}
	if !slices.Equal(downMigrations, expectedDown) {
		t.Fatalf("unexpected hypervisor down-migration layers: %v", downMigrations)
	}
}

func TestHypervisorTableMigrationKeepsResourcesBeforeSharedOutbox(t *testing.T) {
	sqlBytes, err := fs.ReadFile(Files, "000002_hypervisor_tables.up.sql")
	if err != nil {
		t.Fatalf("read hypervisor table migration: %v", err)
	}
	sql := string(sqlBytes)

	imageAt := strings.Index(sql, "CREATE TABLE IF NOT EXISTS image_artifacts")
	vmAt := strings.Index(sql, "CREATE TABLE IF NOT EXISTS personal_vms")
	outboxAt := strings.Index(sql, "CREATE TABLE IF NOT EXISTS hypervisor_outbox_records")
	if imageAt < 0 || vmAt < 0 || outboxAt < 0 || imageAt >= outboxAt || vmAt >= outboxAt {
		t.Fatal("resource tables must be declared before the shared hypervisor outbox")
	}

	for _, legacy := range []string{
		"CREATE TABLE IF NOT EXISTS nodes",
		"CREATE TABLE IF NOT EXISTS vm_outbox_records",
		"CREATE TABLE IF NOT EXISTS image_outbox_records",
		"provider_node",
		"provider_storage",
		"deleted_at",
	} {
		if strings.Contains(sql, legacy) {
			t.Fatalf("legacy hypervisor schema fragment remains: %s", legacy)
		}
	}
}

func TestHypervisorAllocationExportIsIsolatedFromCommandOutbox(t *testing.T) {
	sqlBytes, err := fs.ReadFile(Files, "000002_hypervisor_tables.up.sql")
	if err != nil {
		t.Fatalf("read hypervisor table migration: %v", err)
	}
	sql := string(sqlBytes)
	imageAt := strings.Index(sql, "CREATE TABLE IF NOT EXISTS image_artifacts")
	vmAt := strings.Index(sql, "CREATE TABLE IF NOT EXISTS personal_vms")
	outboxAt := strings.Index(sql, "CREATE TABLE IF NOT EXISTS hypervisor_outbox_records")
	allocationAt := strings.Index(sql, "CREATE TABLE IF NOT EXISTS hypervisor_allocation_outbox")
	walletAt := strings.Index(sql, "CREATE TABLE IF NOT EXISTS commercial_admission_projection")
	if imageAt < 0 || vmAt < 0 || outboxAt < 0 || allocationAt <= outboxAt || walletAt <= allocationAt {
		t.Fatal("hypervisor baseline table boundaries are missing")
	}
	imageSection := sql[imageAt:vmAt]
	commandSection := sql[outboxAt:allocationAt]
	allocationSection := sql[allocationAt:walletAt]
	for _, column := range []string{
		"source_version BIGINT NOT NULL",
		"published_at TIMESTAMPTZ",
		"locked_by VARCHAR(255)",
		"locked_until TIMESTAMPTZ",
		"attempt_count INT NOT NULL DEFAULT 0",
		"last_error VARCHAR(512)",
	} {
		if strings.Contains(imageSection, column) {
			t.Fatalf("allocation export column leaked into image_artifacts: %s", column)
		}
		if strings.Contains(commandSection, column) {
			t.Fatalf("allocation export column leaked into command outbox: %s", column)
		}
		if !strings.Contains(allocationSection, column) {
			t.Fatalf("allocation export column is missing: %s", column)
		}
	}
	for _, invariant := range []string{
		"ux_hypervisor_allocation_resource_version",
		"ck_hypervisor_allocation_version",
		"ck_hypervisor_allocation_lock",
		"ck_hypervisor_allocation_published",
	} {
		if !strings.Contains(allocationSection, invariant) {
			t.Fatalf("hypervisor allocation outbox invariant is missing: %s", invariant)
		}
	}
}

func TestHypervisorAllocationExportHasPendingAndResourceIndexes(t *testing.T) {
	indexBytes, err := fs.ReadFile(Files, "000003_hypervisor_indexes.up.sql")
	if err != nil {
		t.Fatalf("read hypervisor index migration: %v", err)
	}
	indexSQL := string(indexBytes)
	for _, required := range []string{
		"CREATE INDEX IF NOT EXISTS idx_hypervisor_allocation_export_pending",
		"WHERE published_at IS NULL",
		"CREATE INDEX IF NOT EXISTS idx_hypervisor_allocation_export_resource",
		"(resource_id, source_version DESC)",
	} {
		if !strings.Contains(indexSQL, required) {
			t.Fatalf("durable Hypervisor allocation export index is missing %q", required)
		}
	}
}

func TestPersonalVMRetainsDurableFailedState(t *testing.T) {
	enumBytes, err := fs.ReadFile(Files, "000001_hypervisor_enums.up.sql")
	if err != nil {
		t.Fatalf("read hypervisor enum migration: %v", err)
	}
	enumSQL := string(enumBytes)
	vmTypeStart := strings.Index(enumSQL, "CREATE TYPE hypervisor_vm_status")
	imageTypeStart := strings.Index(enumSQL, "CREATE TYPE hypervisor_image_state")
	if vmTypeStart < 0 || imageTypeStart <= vmTypeStart {
		t.Fatal("hypervisor VM enum declaration is missing")
	}
	if !strings.Contains(enumSQL[vmTypeStart:imageTypeStart], "'FAILED'") {
		t.Fatal("personal VM resources must retain terminal provisioning failure")
	}
}

func TestHypervisorResourceDeleteRequiresTransitionalStateAtDatabaseBoundary(t *testing.T) {
	functionBytes, err := fs.ReadFile(Files, "000004_hypervisor_functions.up.sql")
	if err != nil {
		t.Fatalf("read hypervisor function migration: %v", err)
	}
	functionSQL := string(functionBytes)
	for _, required := range []string{
		"hypervisor_require_vm_deleting_before_delete",
		"IF OLD.status <> 'DELETING'",
		"hypervisor_require_image_deleting_before_delete",
		"IF OLD.state <> 'DELETING'",
		"ERRCODE = 'check_violation'",
	} {
		if !strings.Contains(functionSQL, required) {
			t.Fatalf("personal VM deletion guard is missing %q", required)
		}
	}

	triggerBytes, err := fs.ReadFile(Files, "000005_hypervisor_triggers.up.sql")
	if err != nil {
		t.Fatalf("read hypervisor trigger migration: %v", err)
	}
	triggerSQL := string(triggerBytes)
	for _, required := range []string{
		"trg_hypervisor_vm_delete_requires_deleting",
		"BEFORE DELETE ON personal_vms",
		"EXECUTE FUNCTION hypervisor_require_vm_deleting_before_delete()",
		"trg_hypervisor_image_delete_requires_deleting",
		"BEFORE DELETE ON image_artifacts",
		"EXECUTE FUNCTION hypervisor_require_image_deleting_before_delete()",
	} {
		if !strings.Contains(triggerSQL, required) {
			t.Fatalf("personal VM deletion trigger is missing %q", required)
		}
	}
}
