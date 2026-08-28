package integration

import (
	"io/fs"
	"os"
	"strings"
	"testing"

	hierarchyMigrations "controlplane/internal/hierarchy/migrations"
	iamMigrations "controlplane/internal/iam/migrations"
)

func TestTenantInvitationBaselineKeepsOwnershipAndOneTimeGuards(t *testing.T) {
	tables, err := fs.ReadFile(iamMigrations.Files, "000002_iam_tables.up.sql")
	if err != nil {
		t.Fatalf("read IAM tables: %v", err)
	}
	source := strings.ToLower(string(tables))
	for _, required := range []string{
		"create table if not exists platform_roles",
		"create table if not exists tenant_roles",
		"create table if not exists tenant_role_revisions",
		"create table if not exists tenant_role_revision_permissions",
		"create table if not exists membership_role",
		"create table if not exists tenant_invitations",
		"unique (tenant_id, target_user_id)",
		"octet_length(token_hash) = 32",
		"inviter_user_id <> target_user_id",
		"tenant_role_revision_id uuid not null",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("missing tenant RBAC invariant %q", required)
		}
	}
	if strings.Contains(source, "create table if not exists roles (") || strings.Contains(source, "create table if not exists tenant_role (") {
		t.Fatal("legacy shared/snapshot role tables must not return")
	}
}

func TestTenantInvitationRoutesKeepMeBeforeCriticalAndMutationsProtected(t *testing.T) {
	routeSource, err := os.ReadFile("../../route.go")
	if err != nil {
		t.Fatalf("read hierarchy routes: %v", err)
	}
	source := string(routeSource)
	for _, required := range []string{
		`POST("/api/v1/tenant/critical/hierarchy/tenant-invitations"`,
		`DELETE("/api/v1/tenant/critical/hierarchy/tenant-invitations/:invitation_id"`,
		`GET("/api/v1/me/hierarchy/tenant-invitations/preview"`,
		`POST("/api/v1/me/critical/hierarchy/tenant-invitations/join"`,
		"middleware.RequireSessionProof()",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("missing invitation route contract %q", required)
		}
	}

	hierarchyTables, err := fs.ReadFile(hierarchyMigrations.Files, "000002_hierarchy_tables.up.sql")
	if err != nil {
		t.Fatalf("read Hierarchy tables: %v", err)
	}
	if !strings.Contains(strings.ToLower(string(hierarchyTables)), "create table if not exists tenant_memberships") {
		t.Fatal("join target membership table is missing")
	}
}
