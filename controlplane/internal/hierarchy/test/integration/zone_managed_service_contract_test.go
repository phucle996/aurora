package integration

import (
	"io/fs"
	"strings"
	"testing"

	hierarchyMigrations "controlplane/internal/hierarchy/migrations"
)

func TestManagedServiceIsDurableZoneCapability(t *testing.T) {
	enums, err := fs.ReadFile(hierarchyMigrations.Files, "000001_hierarchy_enums.up.sql")
	if err != nil {
		t.Fatalf("read enum migration: %v", err)
	}
	source := strings.ToLower(string(enums))
	if !strings.Contains(source, "'managed_service'") {
		t.Fatal("zone_service_type must include managed_service")
	}
	if !strings.Contains(source, "add value if not exists %l', current_schema(), 'managed_service'") {
		t.Fatal("existing hierarchy schemas must receive managed_service idempotently")
	}
}
