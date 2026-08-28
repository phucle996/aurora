package iamRepoImpl

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamproto "controlplane/internal/iam/transport/proto"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
)

func TestTenantRoleRevisionStaysPinnedUntilExplicitUpgrade(t *testing.T) {
	dsn := os.Getenv("AURORA_RBAC_TEST_DSN")
	if dsn == "" {
		t.Skip("AURORA_RBAC_TEST_DSN is not set")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	actorID, targetUserID := uuid.New(), uuid.New()
	tenantID, membershipID, targetMembershipID := uuid.New(), uuid.New(), uuid.New()
	rootRoleID, rootRevisionID, rootAssignmentID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	for _, user := range []struct {
		id              uuid.UUID
		username, email string
	}{
		{actorID, "revision_actor_" + actorID.String()[:8], actorID.String() + "@test.local"},
		{targetUserID, "revision_target_" + targetUserID.String()[:8], targetUserID.String() + "@test.local"},
	} {
		if _, err := db.Exec(ctx, `INSERT INTO iam.users (id, username, email, password_hash, status) VALUES ($1,$2,$3,'argon2id$test','active')`, user.id, user.username, user.email); err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}
	if _, err := db.Exec(ctx, `INSERT INTO hierarchy.tenants (id, code, name, status) VALUES ($1,$2,'Revision Test','active')`, tenantID, "revision_"+tenantID.String()[:8]); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO hierarchy.tenant_memberships (id,tenant_id,user_id,status,is_ownership) VALUES ($1,$3,$4,'active',true),($2,$3,$5,'active',false)`, membershipID, targetMembershipID, tenantID, actorID, targetUserID); err != nil {
		t.Fatalf("seed memberships: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO iam.tenant_roles (id,tenant_id,code,current_version,created_by) VALUES ($1,$2,'tenant_root',1,$3)`, rootRoleID, tenantID, actorID); err != nil {
		t.Fatalf("seed actor role head: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO iam.tenant_role_revisions (id,tenant_role_id,tenant_id,version,name,description,role_level,created_by) VALUES ($1,$2,$3,1,'Tenant Root','test',3,$4)`, rootRevisionID, rootRoleID, tenantID, actorID); err != nil {
		t.Fatalf("seed actor role revision: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO iam.tenant_role_revision_permissions (tenant_role_revision_id,permission_id) SELECT $1,id FROM iam.permissions`, rootRevisionID); err != nil {
		t.Fatalf("seed actor role permissions: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO iam.membership_role (id,membership_id,tenant_role_id,tenant_role_revision_id,workspace_id) VALUES ($1,$2,$3,$4,'00000000-0000-0000-0000-000000000000')`, rootAssignmentID, membershipID, rootRoleID, rootRevisionID); err != nil {
		t.Fatalf("seed actor assignment: %v", err)
	}
	var permissionID, writePermissionID uuid.UUID
	if err := db.QueryRow(ctx, `SELECT id FROM iam.permissions WHERE module='iam' AND object='role' AND behavior='read'`).Scan(&permissionID); err != nil {
		t.Fatalf("load test permission: %v", err)
	}
	if err := db.QueryRow(ctx, `SELECT id FROM iam.permissions WHERE module='iam' AND object='role' AND behavior='write'`).Scan(&writePermissionID); err != nil {
		t.Fatalf("load write permission: %v", err)
	}

	repository := NewTenantRbacRepository(&config.Config{SchemaSQL: config.SchemaSQLCfg{IAM: "iam", Hierarchy: "hierarchy"}}, db)
	roleID, revisionOneID := uuid.New(), uuid.New()
	created, err := repository.CreateTenantRole(ctx, &iamEntity.CreateTenantRole{ID: roleID, RevisionID: revisionOneID, ActorUserID: actorID, TenantID: tenantID, Code: "reader", Name: "Reader", RoleLevel: 8, Version: 1, PermissionIDs: []uuid.UUID{permissionID, writePermissionID}, CreatedAt: now})
	if err != nil || created.Version != 1 {
		t.Fatalf("create r1: out=%+v err=%v", created, err)
	}
	listed, err := repository.ListTenantRoles(ctx, &iamEntity.ListTenantRoles{ActorUserID: actorID, TenantID: tenantID})
	if err != nil || len(listed) != 1 || listed[0].Version != 1 {
		t.Fatalf("list current revision: out=%+v err=%v", listed, err)
	}
	detail, err := repository.GetTenantRole(ctx, &iamEntity.GetTenantRole{ActorUserID: actorID, TenantID: tenantID, ID: roleID})
	if err != nil || detail.Version != 1 || len(detail.Permissions) != 2 {
		t.Fatalf("get current revision: out=%+v err=%v", detail, err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO iam.membership_role (id,membership_id,tenant_role_id,tenant_role_revision_id,workspace_id) VALUES ($1,$2,$3,$4,'00000000-0000-0000-0000-000000000000')`, uuid.New(), targetMembershipID, roleID, revisionOneID); err != nil {
		t.Fatalf("seed pinned assignment: %v", err)
	}
	beforeRollout, err := repository.GetUserTenantRolePermissions(ctx, targetUserID, tenantID)
	if err != nil {
		t.Fatalf("load r1 runtime permissions: %v", err)
	}
	var beforeEntry iamproto.RoleEntry
	if err := proto.Unmarshal(beforeRollout, &beforeEntry); err != nil {
		t.Fatalf("decode r1 runtime permissions: %v", err)
	}
	writeKey := tenantID.String() + ":00000000-0000-0000-0000-000000000000:iam:role:write"
	if !slices.Contains(beforeEntry.Permissions, writeKey) {
		t.Fatalf("r1 runtime authority is missing %s", writeKey)
	}

	revisionTwoID := uuid.New()
	updated, err := repository.CreateTenantRoleRevision(ctx, &iamEntity.CreateTenantRoleRevision{RevisionID: revisionTwoID, ActorUserID: actorID, TenantID: tenantID, RoleID: roleID, ExpectedVersion: 1, Name: "Reader v2", RoleLevel: 7, PermissionIDs: []uuid.UUID{permissionID}, CreatedAt: now.Add(time.Second)})
	if err != nil || updated.Version != 2 {
		t.Fatalf("create r2: out=%+v err=%v", updated, err)
	}
	var pinnedVersion int64
	if err := db.QueryRow(ctx, `SELECT revision.version FROM iam.membership_role assignment JOIN iam.tenant_role_revisions revision ON revision.id=assignment.tenant_role_revision_id WHERE assignment.membership_id=$1`, targetMembershipID).Scan(&pinnedVersion); err != nil || pinnedVersion != 1 {
		t.Fatalf("assignment was not pinned: version=%d err=%v", pinnedVersion, err)
	}
	rolledOut, err := repository.UpgradeTenantRoleAssignments(ctx, &iamEntity.UpgradeTenantRoleAssignments{ActorUserID: actorID, TenantID: tenantID, RoleID: roleID})
	if err != nil || rolledOut.Version != 2 || rolledOut.UpdatedCount != 1 {
		t.Fatalf("roll out r2: out=%+v err=%v", rolledOut, err)
	}
	if err := db.QueryRow(ctx, `SELECT revision.version FROM iam.membership_role assignment JOIN iam.tenant_role_revisions revision ON revision.id=assignment.tenant_role_revision_id WHERE assignment.membership_id=$1`, targetMembershipID).Scan(&pinnedVersion); err != nil || pinnedVersion != 2 {
		t.Fatalf("assignment did not move to r2: version=%d err=%v", pinnedVersion, err)
	}
	afterRollout, err := repository.GetUserTenantRolePermissions(ctx, targetUserID, tenantID)
	if err != nil {
		t.Fatalf("load r2 runtime permissions: %v", err)
	}
	var afterEntry iamproto.RoleEntry
	if err := proto.Unmarshal(afterRollout, &afterEntry); err != nil {
		t.Fatalf("decode r2 runtime permissions: %v", err)
	}
	if slices.Contains(afterEntry.Permissions, writeKey) {
		t.Fatalf("r2 runtime authority retained revoked permission %s", writeKey)
	}

	revokeTx, err := db.Begin(ctx)
	if err != nil {
		t.Fatalf("begin actor revoke: %v", err)
	}
	if _, err := revokeTx.Exec(ctx, `UPDATE hierarchy.tenant_memberships SET status='suspended' WHERE id=$1`, membershipID); err != nil {
		revokeTx.Rollback(ctx)
		t.Fatalf("stage actor revoke: %v", err)
	}
	revokeResult := make(chan error, 1)
	go func() {
		_, rolloutErr := repository.UpgradeTenantRoleAssignments(ctx, &iamEntity.UpgradeTenantRoleAssignments{ActorUserID: actorID, TenantID: tenantID, RoleID: roleID})
		revokeResult <- rolloutErr
	}()
	select {
	case rolloutErr := <-revokeResult:
		revokeTx.Rollback(ctx)
		t.Fatalf("rollout bypassed uncommitted actor revoke lock: %v", rolloutErr)
	case <-time.After(50 * time.Millisecond):
	}
	if err := revokeTx.Commit(ctx); err != nil {
		t.Fatalf("commit actor revoke: %v", err)
	}
	select {
	case rolloutErr := <-revokeResult:
		if !errors.Is(rolloutErr, iamTaxonomy.ErrActionNotAllowed) {
			t.Fatalf("rollout must recheck committed actor revoke, got %v", rolloutErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("rollout did not settle after actor revoke committed")
	}
	if _, err := db.Exec(ctx, `UPDATE hierarchy.tenant_memberships SET status='active' WHERE id=$1`, membershipID); err != nil {
		t.Fatalf("restore actor membership: %v", err)
	}

	revisionThreeID := uuid.New()
	if _, err := db.Exec(ctx, `INSERT INTO iam.tenant_role_revisions (id,tenant_role_id,tenant_id,version,name,role_level,created_by) VALUES ($1,$2,$3,3,'Reader v3',7,$4)`, revisionThreeID, roleID, tenantID, actorID); err != nil {
		t.Fatalf("seed r3: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO iam.tenant_role_revision_permissions (tenant_role_revision_id,permission_id) VALUES ($1,$2)`, revisionThreeID, permissionID); err != nil {
		t.Fatalf("seed r3 permission: %v", err)
	}
	headTx, err := db.Begin(ctx)
	if err != nil {
		t.Fatalf("begin head advance: %v", err)
	}
	if _, err := headTx.Exec(ctx, `UPDATE iam.tenant_roles SET current_version=3 WHERE id=$1`, roleID); err != nil {
		headTx.Rollback(ctx)
		t.Fatalf("stage head advance: %v", err)
	}
	type rolloutResult struct {
		out *iamEntity.UpgradeTenantRoleAssignments
		err error
	}
	headResult := make(chan rolloutResult, 1)
	go func() {
		out, rolloutErr := repository.UpgradeTenantRoleAssignments(ctx, &iamEntity.UpgradeTenantRoleAssignments{ActorUserID: actorID, TenantID: tenantID, RoleID: roleID})
		headResult <- rolloutResult{out: out, err: rolloutErr}
	}()
	select {
	case result := <-headResult:
		headTx.Rollback(ctx)
		t.Fatalf("rollout bypassed uncommitted role head lock: out=%+v err=%v", result.out, result.err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := headTx.Commit(ctx); err != nil {
		t.Fatalf("commit head advance: %v", err)
	}
	select {
	case result := <-headResult:
		if !errors.Is(result.err, iamTaxonomy.ErrConflict) {
			t.Fatalf("rollout must reject a head changed while waiting: out=%+v err=%v", result.out, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("rollout did not settle after role head advance committed")
	}
	rolloutThree, err := repository.UpgradeTenantRoleAssignments(ctx, &iamEntity.UpgradeTenantRoleAssignments{ActorUserID: actorID, TenantID: tenantID, RoleID: roleID})
	if err != nil || rolloutThree.Version != 3 || rolloutThree.UpdatedCount != 1 {
		t.Fatalf("retry must roll out the committed r3 head: out=%+v err=%v", rolloutThree, err)
	}
	if err := db.QueryRow(ctx, `SELECT revision.version FROM iam.membership_role assignment JOIN iam.tenant_role_revisions revision ON revision.id=assignment.tenant_role_revision_id WHERE assignment.membership_id=$1`, targetMembershipID).Scan(&pinnedVersion); err != nil || pinnedVersion != 3 {
		t.Fatalf("assignment did not move to r3: version=%d err=%v", pinnedVersion, err)
	}

	if _, err := db.Exec(ctx, `UPDATE iam.tenant_role_revisions SET name='mutated' WHERE id=$1`, revisionTwoID); err == nil {
		t.Fatal("immutable tenant role revision accepted UPDATE")
	}
	if _, err := db.Exec(ctx, `DELETE FROM iam.tenant_role_revision_permissions WHERE tenant_role_revision_id=$1`, revisionTwoID); err == nil {
		t.Fatal("immutable tenant role revision permissions accepted DELETE")
	}

	writeOnlyUserID, writeOnlyMembershipID := uuid.New(), uuid.New()
	writeOnlyRoleID, writeOnlyRevisionID := uuid.New(), uuid.New()
	if _, err := db.Exec(ctx, `INSERT INTO iam.users (id,username,email,password_hash,status) VALUES ($1,$2,$3,'argon2id$test','active')`, writeOnlyUserID, "write_only_"+writeOnlyUserID.String()[:8], writeOnlyUserID.String()+"@test.local"); err != nil {
		t.Fatalf("seed write-only actor: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO hierarchy.tenant_memberships (id,tenant_id,user_id,status,is_ownership) VALUES ($1,$2,$3,'active',false)`, writeOnlyMembershipID, tenantID, writeOnlyUserID); err != nil {
		t.Fatalf("seed write-only membership: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO iam.tenant_roles (id,tenant_id,code,current_version,created_by) VALUES ($1,$2,$3,1,$4)`, writeOnlyRoleID, tenantID, "write_only_"+writeOnlyRoleID.String()[:8], actorID); err != nil {
		t.Fatalf("seed write-only role: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO iam.tenant_role_revisions (id,tenant_role_id,tenant_id,version,name,role_level,created_by) VALUES ($1,$2,$3,1,'Write only',4,$4)`, writeOnlyRevisionID, writeOnlyRoleID, tenantID, actorID); err != nil {
		t.Fatalf("seed write-only revision: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO iam.tenant_role_revision_permissions (tenant_role_revision_id,permission_id) SELECT $1,id FROM iam.permissions WHERE module='iam' AND object='role' AND behavior='write'`, writeOnlyRevisionID); err != nil {
		t.Fatalf("seed write-only permission: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO iam.membership_role (id,membership_id,tenant_role_id,tenant_role_revision_id,workspace_id) VALUES ($1,$2,$3,$4,'00000000-0000-0000-0000-000000000000')`, uuid.New(), writeOnlyMembershipID, writeOnlyRoleID, writeOnlyRevisionID); err != nil {
		t.Fatalf("seed write-only assignment: %v", err)
	}
	if _, err := repository.UpgradeTenantRoleAssignments(ctx, &iamEntity.UpgradeTenantRoleAssignments{ActorUserID: writeOnlyUserID, TenantID: tenantID, RoleID: roleID}); !errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
		t.Fatalf("write-only actor must not pass durable role:assign gate, got %v", err)
	}
}
