package unit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"controlplane/internal/cacheengine"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamService "controlplane/internal/iam/service"
	"controlplane/internal/observability"
)

type userRepositoryStub struct {
	users       []iamEntity.ListUsers
	profile     *iamEntity.GetMyProfile
	socialLinks []iamEntity.GetMySocialLinks
	authMethods []iamEntity.GetUserAuthMethods
	err         error

	lastLinkRequest iamEntity.LinkExternalIdentity
	lastUnlink      iamEntity.UnlinkMySocialLink
	lastResetHash   string
	lastResetPlain  string
}

func (r *userRepositoryStub) ListUsers(context.Context, iamEntity.ListUsers) ([]iamEntity.ListUsers, error) {
	return r.users, r.err
}

func (r *userRepositoryStub) UpdateUserStatus(context.Context, iamEntity.UpdateUserStatus) error {
	return r.err
}

func (r *userRepositoryStub) GetMyProfile(_ context.Context, workflow *iamEntity.GetMyProfile) error {
	if r.profile != nil {
		*workflow = *r.profile
	}
	return r.err
}

func (r *userRepositoryStub) UpdateMyProfile(_ context.Context, workflow *iamEntity.UpdateMyProfile) error {
	if r.profile != nil {
		workflow.Username = r.profile.Username
		workflow.AccountEmail = r.profile.AccountEmail
	}
	return r.err
}

func (r *userRepositoryStub) GetMySocialLinks(context.Context, *iamEntity.GetMySocialLinks) ([]iamEntity.GetMySocialLinks, error) {
	return r.socialLinks, r.err
}

func (r *userRepositoryStub) LinkExternalIdentity(_ context.Context, workflow iamEntity.LinkExternalIdentity) error {
	r.lastLinkRequest = workflow
	return r.err
}

func (r *userRepositoryStub) UnlinkMySocialLink(_ context.Context, workflow iamEntity.UnlinkMySocialLink) error {
	r.lastUnlink = workflow
	return r.err
}

func (r *userRepositoryStub) GetUserAuthMethods(context.Context, iamEntity.GetUserAuthMethods) ([]iamEntity.GetUserAuthMethods, error) {
	return r.authMethods, r.err
}

func (r *userRepositoryStub) ResetUserPassword(_ context.Context, workflow iamEntity.ResetUserPassword) error {
	r.lastResetHash = workflow.PasswordHash
	r.lastResetPlain = workflow.Password
	return r.err
}

func TestUserServiceDelegatesUserWorkflows(t *testing.T) {
	redisServer, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer redisServer.Close()
	sharedRedis := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	defer sharedRedis.Close()

	userID := uuid.New()
	repo := &userRepositoryStub{
		users:       []iamEntity.ListUsers{{ID: userID}},
		profile:     &iamEntity.GetMyProfile{UserID: userID, Fullname: "Ada"},
		socialLinks: []iamEntity.GetMySocialLinks{{UserID: userID, Provider: "google", State: "linked"}},
		authMethods: []iamEntity.GetUserAuthMethods{{UserID: userID, Provider: "google", State: "linked"}},
	}
	svc := iamService.NewUserService(repo, nil, nil, sharedRedis, observability.NewNoopWorkflowRecorder())
	ctx := context.Background()

	if users, err := svc.ListUsers(ctx, iamEntity.ListUsers{ActorUserID: userID, WorkspaceID: uuid.New(), ZoneID: uuid.New(), Limit: 20}); err != nil || len(users) != 1 {
		t.Fatalf("list users: %#v, %v", users, err)
	}
	profile := &iamEntity.GetMyProfile{UserID: userID}
	if err := svc.GetMyProfile(ctx, profile); err != nil || profile.Fullname != "Ada" {
		t.Fatalf("get profile: %#v, %v", profile, err)
	}
	updated := &iamEntity.UpdateMyProfile{UserID: userID, Fullname: "Ada"}
	if err := svc.UpdateMyProfile(ctx, updated); err != nil || updated.AccountEmail != "" {
		t.Fatalf("update profile: %#v, %v", updated, err)
	}
	if links, err := svc.GetMySocialLinks(ctx, &iamEntity.GetMySocialLinks{UserID: userID}); err != nil || len(links) != 1 {
		t.Fatalf("get social links: %#v, %v", links, err)
	}
	if methods, err := svc.GetUserAuthMethods(ctx, iamEntity.GetUserAuthMethods{CallerLevel: 1, UserID: userID}); err != nil || len(methods) != 1 {
		t.Fatalf("get auth methods: %#v, %v", methods, err)
	}
	link := iamEntity.LinkExternalIdentity{
		OperationID:     uuid.New(),
		UserID:          userID,
		Provider:        "google",
		ProviderSubject: "google-subject",
		ProviderEmail:   "ada@gmail.com",
	}
	if err := svc.LinkExternalIdentity(ctx, link); err != nil || repo.lastLinkRequest != link {
		t.Fatalf("link social identity: %v %#v", err, repo.lastLinkRequest)
	}
}

func TestUserServicePropagatesRepositoryErrors(t *testing.T) {
	repoErr := errors.New("database unavailable")
	svc := iamService.NewUserService(&userRepositoryStub{err: repoErr}, nil, nil, nil, observability.NewNoopWorkflowRecorder())
	ctx := context.Background()
	userID := uuid.New()

	if _, err := svc.ListUsers(ctx, iamEntity.ListUsers{}); !errors.Is(err, repoErr) {
		t.Fatalf("list users error: %v", err)
	}
	if err := svc.UpdateUserStatus(ctx, iamEntity.UpdateUserStatus{TargetUserID: userID}); !errors.Is(err, repoErr) {
		t.Fatalf("update status error: %v", err)
	}
	if err := svc.GetMyProfile(ctx, &iamEntity.GetMyProfile{UserID: userID}); !errors.Is(err, repoErr) {
		t.Fatalf("get profile error: %v", err)
	}
	if err := svc.UpdateMyProfile(ctx, &iamEntity.UpdateMyProfile{UserID: userID}); !errors.Is(err, repoErr) {
		t.Fatalf("update profile error: %v", err)
	}
	if _, err := svc.GetMySocialLinks(ctx, &iamEntity.GetMySocialLinks{UserID: userID}); !errors.Is(err, repoErr) {
		t.Fatalf("get social links error: %v", err)
	}
	if err := svc.LinkExternalIdentity(ctx, iamEntity.LinkExternalIdentity{UserID: userID}); !errors.Is(err, repoErr) {
		t.Fatalf("link social identity error: %v", err)
	}
	if _, err := svc.GetUserAuthMethods(ctx, iamEntity.GetUserAuthMethods{UserID: userID}); !errors.Is(err, repoErr) {
		t.Fatalf("get auth methods error: %v", err)
	}
	if err := svc.ResetUserPassword(ctx, iamEntity.ResetUserPassword{TargetUserID: userID, Password: "A-strong-password-123"}); !errors.Is(err, repoErr) {
		t.Fatalf("reset password error: %v", err)
	}
}

func TestUserServiceMutationsUseInjectedRedisAndWorkflowEntities(t *testing.T) {
	redisServer, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer redisServer.Close()
	authRedis := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	defer authRedis.Close()
	sharedRedis := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	defer sharedRedis.Close()

	userID := uuid.New()
	repo := &userRepositoryStub{}
	registry := cacheengine.NewCacheRegistry(cacheengine.NewL1Cache(), observability.NewNoopCacheRecorder())
	registry.L1.Set("user_role:"+userID.String(), "cached", time.Minute)
	svc := iamService.NewUserService(repo, registry, authRedis, sharedRedis, observability.NewNoopWorkflowRecorder())
	ctx := context.Background()

	if err := svc.UpdateUserStatus(ctx, iamEntity.UpdateUserStatus{
		CallerLevel: 1, TargetUserID: userID, Status: "disabled",
	}); err != nil {
		t.Fatalf("update user status: %v", err)
	}
	if _, exists := registry.L1.Get("user_role:" + userID.String()); exists {
		t.Fatal("user role L1 cache must be invalidated")
	}

	link := iamEntity.LinkExternalIdentity{
		OperationID: uuid.New(), UserID: userID, Provider: "google",
		ProviderSubject: "subject", ProviderEmail: "provider@example.com",
	}
	if err := svc.LinkExternalIdentity(ctx, link); err != nil {
		t.Fatalf("link social identity with activity: %v", err)
	}
	unlink := iamEntity.UnlinkMySocialLink{
		OperationID: uuid.New(), UserID: userID, Provider: "google",
	}
	if err := svc.UnlinkMySocialLink(ctx, unlink); err != nil || repo.lastUnlink != unlink {
		t.Fatalf("unlink social identity: %v %#v", err, repo.lastUnlink)
	}
	if err := svc.ResetUserPassword(ctx, iamEntity.ResetUserPassword{
		OperationID: uuid.New(), CallerLevel: 1, TargetUserID: userID, Password: "A-strong-password-123",
	}); err != nil {
		t.Fatalf("reset password with activity: %v", err)
	}
}

func TestUserServiceResetPasswordHashesBeforeRepositoryCall(t *testing.T) {
	redisServer, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer redisServer.Close()
	sharedRedis := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	defer sharedRedis.Close()

	repo := &userRepositoryStub{}
	svc := iamService.NewUserService(repo, nil, nil, sharedRedis, observability.NewNoopWorkflowRecorder())
	workflow := iamEntity.ResetUserPassword{
		CallerLevel: 1, TargetUserID: uuid.New(), Password: "A-strong-password-123",
	}
	if err := svc.ResetUserPassword(context.Background(), workflow); err != nil {
		t.Fatalf("reset password: %v", err)
	}
	if repo.lastResetHash == "" || repo.lastResetHash == workflow.Password {
		t.Fatal("repository must receive a password hash, not plaintext")
	}
	if repo.lastResetPlain != "" {
		t.Fatal("plaintext password must be cleared before repository handoff")
	}
}
