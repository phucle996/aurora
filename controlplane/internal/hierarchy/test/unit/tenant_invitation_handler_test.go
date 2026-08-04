package unit

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchySvcInterface "controlplane/internal/hierarchy/domain/service"
	hierarchyHandler "controlplane/internal/hierarchy/transport/http/handler"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type tenantInvitationCapture struct {
	hierarchySvcInterface.TenantInvitationService
	createInput *hierarchyEntity.CreateTenantInvitation
	joinInput   *hierarchyEntity.JoinTenantInvitation
}

func (capture *tenantInvitationCapture) CreateTenantInvitation(_ context.Context, in *hierarchyEntity.CreateTenantInvitation) (*hierarchyEntity.CreateTenantInvitation, error) {
	capture.createInput = in
	return &hierarchyEntity.CreateTenantInvitation{
		ID: in.TenantRoleID, TenantRoleID: in.TenantRoleID,
		RoleCode: "developer", RoleName: "Developer",
		Token:     "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		ExpiresAt: time.Unix(10, 0).UTC(),
	}, nil
}

func (capture *tenantInvitationCapture) JoinTenantInvitation(_ context.Context, in *hierarchyEntity.JoinTenantInvitation) (*hierarchyEntity.JoinTenantInvitation, error) {
	capture.joinInput = in
	return &hierarchyEntity.JoinTenantInvitation{
		TenantID:   uuid.MustParse("10000000-0000-4000-8000-000000000003"),
		TenantCode: "acme", TenantName: "Acme", TenantRoleID: uuid.MustParse("10000000-0000-4000-8000-000000000004"),
		RoleCode: "developer", RoleName: "Developer", RoleLevel: 4,
	}, nil
}

func TestCreateTenantInvitationValidatesAndCanonicalizesAtHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	capture := &tenantInvitationCapture{}
	handler := hierarchyHandler.NewTenantInvitationHandler(capture)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if uid := c.GetHeader("x-user-id"); uid != "" {
			if parsed, err := uuid.Parse(uid); err == nil {
				c.Set("ctx_user_id", parsed)
			}
		}
		if tid := c.GetHeader("x-tenant-id"); tid != "" {
			if parsed, err := uuid.Parse(tid); err == nil {
				c.Set("ctx_tenant_id", parsed)
			}
		}
		c.Next()
	})
	router.POST("/invite", handler.CreateTenantInvitation)

	request := httptest.NewRequest(http.MethodPost, "/invite", bytes.NewBufferString(
		`{"identifier":" User@Example.com ","tenant_role_id":"10000000-0000-4000-8000-000000000002"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-user-id", "10000000-0000-4000-8000-000000000001")
	request.Header.Set("x-tenant-id", "10000000-0000-4000-8000-000000000003")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", response.Code, response.Body.String())
	}
	if capture.createInput == nil || capture.createInput.TargetIdentifier != "user@example.com" || !capture.createInput.TargetByEmail {
		t.Fatalf("handler did not canonicalize the target: %#v", capture.createInput)
	}
	if !strings.Contains(response.Body.String(), "/settings/tenant-invitations/join?token=") {
		t.Fatal("create response must return the one-time console join link")
	}
}

func TestJoinTenantInvitationRejectsMalformedTokenBeforeService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	capture := &tenantInvitationCapture{}
	handler := hierarchyHandler.NewTenantInvitationHandler(capture)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if uid := c.GetHeader("x-user-id"); uid != "" {
			if parsed, err := uuid.Parse(uid); err == nil {
				c.Set("ctx_user_id", parsed)
			}
		}
		c.Next()
	})
	router.POST("/join", handler.JoinTenantInvitation)

	request := httptest.NewRequest(http.MethodPost, "/join", bytes.NewBufferString(`{"token":"short"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-user-id", "10000000-0000-4000-8000-000000000001")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", response.Code)
	}
	if capture.joinInput != nil {
		t.Fatal("service must not receive a malformed bearer token")
	}
}
