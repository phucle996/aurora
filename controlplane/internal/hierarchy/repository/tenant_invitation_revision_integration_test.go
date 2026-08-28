package hierarchyRepoImpl

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"testing"
	"time"

	"controlplane/internal/config"
	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchyTaxonomy "controlplane/internal/hierarchy/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTenantInvitationPinsRevisionAndRejectsItAfterHeadAdvances(t *testing.T) {
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

	actorID, firstTargetID, staleTargetID := uuid.New(), uuid.New(), uuid.New()
	tenantID, actorMembershipID := uuid.New(), uuid.New()
	rootRoleID, rootRevisionID := uuid.New(), uuid.New()
	targetRoleID, targetRevisionID := uuid.New(), uuid.New()
	for _, user := range []uuid.UUID{actorID, firstTargetID, staleTargetID} {
		if _, err := db.Exec(ctx, `INSERT INTO iam.users (id,username,email,password_hash,status) VALUES ($1,$2,$3,'argon2id$test','active')`, user, "invite_"+user.String()[:8], user.String()+"@test.local"); err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}
	if _, err := db.Exec(ctx, `INSERT INTO hierarchy.tenants (id,code,name,status) VALUES ($1,$2,'Invitation Revision Test','active')`, tenantID, "invite_"+tenantID.String()[:8]); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO hierarchy.tenant_memberships (id,tenant_id,user_id,status,is_ownership) VALUES ($1,$2,$3,'active',true)`, actorMembershipID, tenantID, actorID); err != nil {
		t.Fatalf("seed actor membership: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO iam.tenant_roles (id,tenant_id,code,current_version,created_by) VALUES ($1,$2,'tenant_root',1,$3),($4,$2,'reader',1,$3)`, rootRoleID, tenantID, actorID, targetRoleID); err != nil {
		t.Fatalf("seed role heads: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO iam.tenant_role_revisions (id,tenant_role_id,tenant_id,version,name,role_level,created_by) VALUES ($1,$2,$3,1,'Tenant Root',3,$4),($5,$6,$3,1,'Reader',8,$4)`, rootRevisionID, rootRoleID, tenantID, actorID, targetRevisionID, targetRoleID); err != nil {
		t.Fatalf("seed revisions: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO iam.tenant_role_revision_permissions (tenant_role_revision_id,permission_id) SELECT $1,id FROM iam.permissions`, rootRevisionID); err != nil {
		t.Fatalf("seed root permissions: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO iam.tenant_role_revision_permissions (tenant_role_revision_id,permission_id) SELECT $1,id FROM iam.permissions WHERE module='iam' AND object='role' AND behavior='read'`, targetRevisionID); err != nil {
		t.Fatalf("seed target permission: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO iam.membership_role (id,membership_id,tenant_role_id,tenant_role_revision_id,workspace_id) VALUES ($1,$2,$3,$4,'00000000-0000-0000-0000-000000000000')`, uuid.New(), actorMembershipID, rootRoleID, rootRevisionID); err != nil {
		t.Fatalf("seed actor assignment: %v", err)
	}

	repository := NewTenantInvitationRepository(&config.Config{SchemaSQL: config.SchemaSQLCfg{IAM: "iam", Hierarchy: "hierarchy"}}, db)
	createInvitation := func(targetID uuid.UUID, marker string) *hierarchyEntity.CreateTenantInvitation {
		uniqueMarker := marker + tenantID.String()
		hash := sha256.Sum256([]byte(uniqueMarker))
		return &hierarchyEntity.CreateTenantInvitation{ID: uuid.New(), TenantID: tenantID, InviterUserID: actorID, TargetIdentifier: "invite_" + targetID.String()[:8], TargetUserID: targetID, TenantRoleID: targetRoleID, Token: uniqueMarker, TokenHash: hash[:], ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now().UTC()}
	}
	first := createInvitation(firstTargetID, "first-revision-invitation")
	created, err := repository.CreateTenantInvitation(ctx, first)
	if err != nil || created.RoleVersion != 1 || created.TenantRoleRevisionID != targetRevisionID {
		t.Fatalf("create pinned invitation: out=%+v err=%v", created, err)
	}
	if _, err := repository.PreviewTenantInvitation(ctx, &hierarchyEntity.PreviewTenantInvitation{UserID: firstTargetID, TokenHash: first.TokenHash}); err != nil {
		t.Fatalf("preview current invitation: %v", err)
	}
	joined, err := repository.JoinTenantInvitation(ctx, &hierarchyEntity.JoinTenantInvitation{UserID: firstTargetID, TokenHash: first.TokenHash, MembershipID: uuid.New(), MembershipRoleID: uuid.New()})
	if err != nil || joined.TenantRoleID != targetRoleID {
		t.Fatalf("join current invitation: out=%+v err=%v", joined, err)
	}
	var joinedRevisionID uuid.UUID
	if err := db.QueryRow(ctx, `SELECT tenant_role_revision_id FROM iam.membership_role mr JOIN hierarchy.tenant_memberships tm ON tm.id=mr.membership_id WHERE tm.tenant_id=$1 AND tm.user_id=$2`, tenantID, firstTargetID).Scan(&joinedRevisionID); err != nil || joinedRevisionID != targetRevisionID {
		t.Fatalf("joined assignment did not retain invitation revision: revision=%s err=%v", joinedRevisionID, err)
	}

	stale := createInvitation(staleTargetID, "stale-revision-invitation")
	if _, err := repository.CreateTenantInvitation(ctx, stale); err != nil {
		t.Fatalf("create invitation before role update: %v", err)
	}
	revisionTwoID := uuid.New()
	if _, err := db.Exec(ctx, `INSERT INTO iam.tenant_role_revisions (id,tenant_role_id,tenant_id,version,name,role_level,created_by) VALUES ($1,$2,$3,2,'Reader v2',8,$4)`, revisionTwoID, targetRoleID, tenantID, actorID); err != nil {
		t.Fatalf("seed r2: %v", err)
	}
	if _, err := db.Exec(ctx, `UPDATE iam.tenant_roles SET current_version=2 WHERE id=$1`, targetRoleID); err != nil {
		t.Fatalf("advance role head: %v", err)
	}
	_, err = repository.PreviewTenantInvitation(ctx, &hierarchyEntity.PreviewTenantInvitation{UserID: staleTargetID, TokenHash: stale.TokenHash})
	if !errors.Is(err, hierarchyTaxonomy.ErrNotFound) {
		t.Fatalf("stale invitation preview must fail closed, got %v", err)
	}
}

func TestCreateTenantBuildsRevisionPinnedRoot(t *testing.T) {
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
	ownerID := uuid.New()
	if _, err := db.Exec(ctx, `INSERT INTO iam.users (id,username,email,password_hash,status) VALUES ($1,$2,$3,'argon2id$test','active')`, ownerID, "owner_"+ownerID.String()[:8], ownerID.String()+"@test.local"); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	tenantID := uuid.New()
	in := &hierarchyEntity.CreateTenant{
		ID: tenantID, OwnerID: ownerID, OwnerMembershipID: uuid.New(), TenantRootRoleID: uuid.New(), MembershipRoleID: uuid.New(), DomainID: uuid.New(),
		Code: "created_" + tenantID.String()[:8], Name: "Created Tenant", PrimaryDomain: tenantID.String()[:8] + ".test.local", Status: hierarchyEntity.TenantStatusActive, CreatedAt: time.Now().UTC(),
	}
	repository := NewTenantRepoImpl(&config.Config{SchemaSQL: config.SchemaSQLCfg{IAM: "iam", Hierarchy: "hierarchy"}}, db)
	if _, err := repository.CreateTenant(ctx, in); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	var currentVersion, assignmentVersion int64
	var revisionID, assignmentRevisionID uuid.UUID
	if err := db.QueryRow(ctx, `SELECT role.current_version, revision.id, pinned.version, assignment.tenant_role_revision_id FROM iam.tenant_roles role JOIN iam.tenant_role_revisions revision ON revision.tenant_role_id=role.id AND revision.version=role.current_version JOIN iam.membership_role assignment ON assignment.tenant_role_id=role.id JOIN iam.tenant_role_revisions pinned ON pinned.id=assignment.tenant_role_revision_id WHERE role.id=$1`, in.TenantRootRoleID).Scan(&currentVersion, &revisionID, &assignmentVersion, &assignmentRevisionID); err != nil {
		t.Fatalf("read root revision: %v", err)
	}
	if currentVersion != 1 || assignmentVersion != 1 || revisionID != assignmentRevisionID {
		t.Fatalf("root assignment is not pinned to r1: head=%d assignment=%d revision=%s assignment_revision=%s", currentVersion, assignmentVersion, revisionID, assignmentRevisionID)
	}
}
