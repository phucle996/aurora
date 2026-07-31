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

func TestPersonalVMHasNoDurableFailedState(t *testing.T) {
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
	if strings.Contains(enumSQL[vmTypeStart:imageTypeStart], "'FAILED'") {
		t.Fatal("personal VM resources must be hard-deleted on terminal failure")
	}
}
