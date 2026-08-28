package iamHandler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"controlplane/internal/http/middleware"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamTaxonomy "controlplane/internal/iam/taxonomy"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type userDirectoryServiceStub struct {
	query  iamEntity.ListUsers
	err    error
	called bool
}

func (s *userDirectoryServiceStub) ListUsers(_ context.Context, query iamEntity.ListUsers) ([]iamEntity.ListUsers, error) {
	s.called = true
	s.query = query
	return nil, s.err
}

func (s *userDirectoryServiceStub) UpdateUserStatus(context.Context, iamEntity.UpdateUserStatus) error {
	return nil
}
func (s *userDirectoryServiceStub) GetMyProfile(context.Context, *iamEntity.GetMyProfile) error {
	return nil
}
func (s *userDirectoryServiceStub) UpdateMyProfile(context.Context, *iamEntity.UpdateMyProfile) error {
	return nil
}
func (s *userDirectoryServiceStub) GetMySocialLinks(context.Context, *iamEntity.GetMySocialLinks) ([]iamEntity.GetMySocialLinks, error) {
	return nil, nil
}
func (s *userDirectoryServiceStub) LinkExternalIdentity(context.Context, iamEntity.LinkExternalIdentity) error {
	return nil
}
func (s *userDirectoryServiceStub) UnlinkMySocialLink(context.Context, iamEntity.UnlinkMySocialLink) error {
	return nil
}
func (s *userDirectoryServiceStub) GetUserAuthMethods(context.Context, iamEntity.GetUserAuthMethods) ([]iamEntity.GetUserAuthMethods, error) {
	return nil, nil
}
func (s *userDirectoryServiceStub) ResetUserPassword(context.Context, iamEntity.ResetUserPassword) error {
	return nil
}

func TestListUsersPlatformPassesTrustedCallerLevelAndWorkspaceCoordinates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	actorID, workspaceID, zoneID := uuid.New(), uuid.New(), uuid.New()
	service := &userDirectoryServiceStub{}
	router := gin.New()
	router.Use(middleware.ContextInjector())
	router.GET("/users", NewUserHandler(service).ListUsersPlatform)

	request := httptest.NewRequest(http.MethodGet, "/users?limit=20&offset=0", nil)
	request.Header.Set("X-User-ID", actorID.String())
	request.Header.Set("X-User-Level", "2")
	request.Header.Set("X-Workspace-ID", workspaceID.String())
	request.Header.Set("X-Zone-ID", zoneID.String())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("list users status = %d, body = %s", response.Code, response.Body.String())
	}
	query := reflect.ValueOf(service.query)
	for field, expected := range map[string]uuid.UUID{
		"ActorUserID": actorID,
		"WorkspaceID": workspaceID,
		"ZoneID":      zoneID,
	} {
		actual := query.FieldByName(field)
		if !actual.IsValid() {
			t.Fatalf("list users workflow is missing %s", field)
		}
		if actual.Interface().(uuid.UUID) != expected {
			t.Fatalf("%s = %v, want %v", field, actual.Interface(), expected)
		}
	}
	if service.query.CallerLevel != 2 {
		t.Fatalf("CallerLevel = %d, want 2", service.query.CallerLevel)
	}
}

func TestListUsersPlatformRejectsMissingCallerLevel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &userDirectoryServiceStub{}
	router := gin.New()
	router.Use(middleware.ContextInjector())
	router.GET("/users", NewUserHandler(service).ListUsersPlatform)

	request := httptest.NewRequest(http.MethodGet, "/users", nil)
	request.Header.Set("X-User-ID", uuid.NewString())
	request.Header.Set("X-Workspace-ID", uuid.NewString())
	request.Header.Set("X-Zone-ID", uuid.NewString())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("missing caller level status = %d, want 403; body = %s", response.Code, response.Body.String())
	}
	if service.called {
		t.Fatal("service was called without a trusted caller level")
	}
}

func TestListUsersPlatformMapsWorkspaceFenceFailureToForbidden(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &userDirectoryServiceStub{err: iamTaxonomy.ErrActionNotAllowed}
	router := gin.New()
	router.Use(middleware.ContextInjector())
	router.GET("/users", NewUserHandler(service).ListUsersPlatform)

	request := httptest.NewRequest(http.MethodGet, "/users", nil)
	request.Header.Set("X-User-ID", uuid.NewString())
	request.Header.Set("X-User-Level", "0")
	request.Header.Set("X-Workspace-ID", uuid.NewString())
	request.Header.Set("X-Zone-ID", uuid.NewString())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("authority failure status = %d, want 403; body = %s", response.Code, response.Body.String())
	}
	if !errors.Is(service.err, iamTaxonomy.ErrActionNotAllowed) {
		t.Fatal("test setup lost durable authority failure")
	}
}
