package integration

import (
	"controlplane/internal/iam/migrations"
	"regexp"
	"strings"
	"testing"
)

func TestBootstrapRoleEntriesMatchSeededPermissions(t *testing.T) {
	sql, err := migrations.Files.ReadFile("000006_iam_seeds.up.sql")
	if err != nil {
		t.Fatalf("read IAM bootstrap seed: %v", err)
	}

	source := string(sql)
	if !strings.HasPrefix(source, "-- IAM baseline seed.") {
		t.Fatal("000006 must declare itself as the canonical zero-state baseline")
	}
	if strings.Contains(strings.ToUpper(source), "ON CONFLICT") {
		t.Fatal("zero-state baseline must not merge or patch pre-existing data")
	}
	if strings.Count(source, "iam_seed_role_entry(array_agg(") != 2 {
		t.Fatal("global and workspace assignments must both compile from normalized mappings")
	}
	if !strings.Contains(source, "u.username || ':00000000-0000-0000-0000-000000000000:'") ||
		!strings.Contains(source, "user_account.username || ':' || workspace.id::text || ':'") {
		t.Fatal("every seeded user_role assignment must carry the full five-level identity/workspace prefix")
	}
	for _, roleCode := range []string{
		"platform_root", "platform_admin", "billing_admin", "platform_support_operator", "platform_user",
	} {
		if !strings.Contains(source, "'"+roleCode+"'") {
			t.Fatalf("missing canonical platform role %q", roleCode)
		}
	}
	for _, removedTenantRole := range []string{"tenant_owner", "tenant_admin", "tenant_member", "tenant_viewer"} {
		if strings.Contains(source, "'"+removedTenantRole+"'") {
			t.Fatalf("baseline must not seed tenant-owned role %q", removedTenantRole)
		}
	}
}

func TestIAMSeedRollbackCoversPermissionCatalog(t *testing.T) {
	upSQL, err := migrations.Files.ReadFile("000006_iam_seeds.up.sql")
	if err != nil {
		t.Fatalf("read IAM bootstrap up migration: %v", err)
	}
	downSQL, err := migrations.Files.ReadFile("000006_iam_seeds.down.sql")
	if err != nil {
		t.Fatalf("read IAM bootstrap down migration: %v", err)
	}

	// [COMMENT]: Chỉ đọc đúng statement permission để các VALUES của role/user không bị nhận nhầm là catalog.
	_, upPermissionSQL, found := strings.Cut(string(upSQL), "INSERT INTO permissions")
	if !found {
		t.Fatal("up migration does not contain permission seed statement")
	}
	upPermissionSQL, _, found = strings.Cut(upPermissionSQL, "-- 3. Platform roles only")
	if !found {
		t.Fatal("up migration permission statement has no role-section boundary")
	}
	_, downPermissionSQL, found := strings.Cut(string(downSQL), "DELETE FROM permissions")
	if !found {
		t.Fatal("down migration does not contain permission rollback statement")
	}
	downPermissionSQL, _, found = strings.Cut(downPermissionSQL, "DELETE FROM user_profiles")
	if !found {
		t.Fatal("down migration permission statement has no profile-section boundary")
	}

	// [COMMENT]: Rollback phải theo đúng triple identity; permission table không có cột code dạng legacy.
	upPattern := regexp.MustCompile(`\(gen_random_uuid\(\), '([a-z]+)', '([a-z]+)', '([a-z]+)',`)
	downPattern := regexp.MustCompile(`\('([a-z]+)', '([a-z]+)', '([a-z]+)'\)`)
	upMatches := upPattern.FindAllStringSubmatch(upPermissionSQL, -1)
	downMatches := downPattern.FindAllStringSubmatch(downPermissionSQL, -1)
	if len(upMatches) == 0 || len(upMatches) != len(downMatches) {
		t.Fatalf("permission catalog mismatch: up=%d down=%d", len(upMatches), len(downMatches))
	}

	downCatalog := make(map[string]struct{}, len(downMatches))
	for _, match := range downMatches {
		downCatalog[strings.Join(match[1:], ":")] = struct{}{}
	}
	for _, match := range upMatches {
		permission := strings.Join(match[1:], ":")
		if _, ok := downCatalog[permission]; !ok {
			t.Errorf("rollback does not remove seeded permission %q", permission)
		}
	}
}

func TestTenantWalletTopUpIsNotAPlatformPermission(t *testing.T) {
	sql, err := migrations.Files.ReadFile("000006_iam_seeds.up.sql")
	if err != nil {
		t.Fatalf("read IAM bootstrap seed: %v", err)
	}
	source := string(sql)
	if !strings.Contains(source, "(gen_random_uuid(), 'billing', 'wallet', 'top_up'") {
		t.Fatal("tenant wallet top_up must remain in the normalized permission catalog")
	}
	if !strings.Contains(source, "AND NOT (permission.module='billing' AND permission.object='wallet' AND permission.behavior='top_up')") ||
		!strings.Contains(source, "AND NOT (permission.object='wallet' AND permission.behavior='top_up')") {
		t.Fatal("platform bootstrap roles must not receive tenant wallet top_up")
	}
}

func TestPlatformUserCanManagePersonalWorkspaces(t *testing.T) {
	sql, err := migrations.Files.ReadFile("000006_iam_seeds.up.sql")
	if err != nil {
		t.Fatalf("read IAM bootstrap seed: %v", err)
	}

	source := string(sql)
	for _, required := range []string{
		"permission.module='hierarchy'",
		"permission.object='workspace'",
		"permission.behavior IN ('create', 'read', 'delete')",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("platform_user baseline must include %q", required)
		}
	}
}

func TestPlatformUserWorkspaceGrantMigrationRefreshesCompiledRoles(t *testing.T) {
	sql, err := migrations.Files.ReadFile("000008_platform_user_workspace_permissions.up.sql")
	if err != nil {
		t.Fatalf("read platform user workspace grant migration: %v", err)
	}

	source := string(sql)
	for _, required := range []string{
		"INSERT INTO platform_role_permissions",
		"WHERE role.code = 'platform_user'",
		"WITH compiled AS",
		"UPDATE user_role AS assignment",
		"iam_workspace_role_entry",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("workspace grant migration must contain %q", required)
		}
	}
}

func TestIAMTablesEnforceSinglePlatformRolePerUser(t *testing.T) {
	sql, err := migrations.Files.ReadFile("000002_iam_tables.up.sql")
	if err != nil {
		t.Fatalf("read IAM tables migration: %v", err)
	}

	// [COMMENT]: Partial uniqueness giữ semantics multi-role ở workspace nhưng chặn race gán hai platform role.
	migration := string(sql)
	if !strings.Contains(migration, "CREATE UNIQUE INDEX IF NOT EXISTS uq_user_role_platform") ||
		!strings.Contains(migration, "WHERE workspace_id = '00000000-0000-0000-0000-000000000000'") {
		t.Fatal("IAM tables migration must enforce one nil-workspace platform role per user")
	}
}

func TestRefreshCredentialBaselineIsUserDeviceOnly(t *testing.T) {
	tablesSQL, err := migrations.Files.ReadFile("000002_iam_tables.up.sql")
	if err != nil {
		t.Fatalf("read IAM tables migration: %v", err)
	}
	indexesSQL, err := migrations.Files.ReadFile("000003_iam_indexes.up.sql")
	if err != nil {
		t.Fatalf("read IAM indexes migration: %v", err)
	}

	_, refreshSection, found := strings.Cut(string(tablesSQL), "CREATE TABLE IF NOT EXISTS refresh_tokens")
	if !found {
		t.Fatal("refresh_tokens table is missing")
	}
	refreshSection, _, found = strings.Cut(refreshSection, "-- [COMMENT]: Bảng lưu thiết bị")
	if !found {
		t.Fatal("refresh_tokens table has no device-table boundary")
	}
	for _, required := range []string{
		"device_id uuid NOT NULL",
		"CONSTRAINT refresh_tokens_user_device_uk UNIQUE (user_id, device_id)",
		"Opaque user/device credentials",
	} {
		if !strings.Contains(refreshSection, required) {
			t.Fatalf("refresh credential baseline is missing %q", required)
		}
	}
	for _, forbidden := range []string{"tenant_id", "workspace_id", "zone_id", "used_at", "revoked_at"} {
		if strings.Contains(refreshSection, forbidden) {
			t.Fatalf("refresh credential must not persist runtime context/state %q", forbidden)
		}
	}
	if strings.Contains(string(indexesSQL), "refresh_tokens_tenant_id_idx") {
		t.Fatal("refresh credential must not retain a tenant lookup index")
	}
}
