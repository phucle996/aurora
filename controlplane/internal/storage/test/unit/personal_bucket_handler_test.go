package unit_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	storageEntity "controlplane/internal/storage/domain/entity"
	storageSvcInterface "controlplane/internal/storage/domain/service"
	storageHandler "controlplane/internal/storage/transport/http/handler"
	pkgcontext "controlplane/pkg/context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type personalBucketServiceStub struct {
	storageSvcInterface.PersonalBucketService
	bucket *storageEntity.PersonalBucket
}

type personalStorageAccessSessionServiceStub struct {
	storageSvcInterface.PersonalStorageAccessSessionService
	session             *storageEntity.StorageAccessSession
	status              *storageEntity.StorageAccessSessionStatus
	statusAccessSession uuid.UUID
	statusResource      uuid.UUID
	statusWorkspace     uuid.UUID
	statusActor         uuid.UUID
	statusZone          uuid.UUID
}

func (s *personalStorageAccessSessionServiceStub) GetPersonalStorageAccessSessionStatus(_ context.Context, accessSessionID, resourceID, workspaceID, actorID, zoneID uuid.UUID) (*storageEntity.StorageAccessSessionStatus, error) {
	s.statusAccessSession = accessSessionID
	s.statusResource = resourceID
	s.statusWorkspace = workspaceID
	s.statusActor = actorID
	s.statusZone = zoneID
	return s.status, nil
}

func (s *personalStorageAccessSessionServiceStub) CreatePersonalStorageAccessSession(_ context.Context, session *storageEntity.StorageAccessSession) error {
	s.session = session
	return nil
}

func TestPersonalAccessSessionStatusUsesTrustedOwnerContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID, workspaceID, zoneID, bucketID, accessSessionID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	completedAt := time.Date(2026, time.August, 15, 8, 30, 0, 0, time.UTC)
	service := &personalStorageAccessSessionServiceStub{status: &storageEntity.StorageAccessSessionStatus{State: "ACTIVE", CompletedAt: &completedAt}}
	handler := storageHandler.NewPersonalBucketHandler(&personalBucketServiceStub{}, service)
	router := gin.New()
	router.GET("/buckets/:id/access-sessions/:access_session_id", func(c *gin.Context) {
		c.Set(pkgcontext.CtxUserID, userID)
		c.Set(pkgcontext.CtxWorkspaceID, workspaceID)
		c.Set(pkgcontext.CtxZoneID, zoneID)
		handler.GetAccessSessionStatus(c)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/buckets/"+bucketID.String()+"/access-sessions/"+accessSessionID.String(), nil))
	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if service.statusAccessSession != accessSessionID || service.statusResource != bucketID || service.statusWorkspace != workspaceID || service.statusActor != userID || service.statusZone != zoneID {
		t.Fatal("status workflow did not preserve the trusted session/resource/workspace/actor/Zone binding")
	}
	var envelope struct {
		Data struct {
			Status      string `json:"status"`
			CompletedAt string `json:"completed_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Data.Status != "ACTIVE" || envelope.Data.CompletedAt != "2026-08-15T08:30:00Z" {
		t.Fatalf("unexpected status payload: %#v", envelope.Data)
	}
}

func (s *personalBucketServiceStub) GetBucket(_ context.Context, _ uuid.UUID, _ uuid.UUID) (*storageEntity.PersonalBucket, error) {
	return s.bucket, nil
}

func TestPersonalAccessSessionCanonicalizesDuplicateActions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID, workspaceID, zoneID, bucketID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	service := &personalStorageAccessSessionServiceStub{}
	handler := storageHandler.NewPersonalBucketHandler(&personalBucketServiceStub{}, service)
	router := gin.New()
	router.POST("/buckets/:id/access-sessions", func(c *gin.Context) {
		c.Set(pkgcontext.CtxUserID, userID)
		c.Set(pkgcontext.CtxWorkspaceID, workspaceID)
		c.Set(pkgcontext.CtxZoneID, zoneID)
		handler.CreateAccessSession(c)
	})

	request := httptest.NewRequest(http.MethodPost, "/buckets/"+bucketID.String()+"/access-sessions", strings.NewReader(`{"duration_seconds":60,"actions":["GetObject","GetObject","PutObject"]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d: %s", response.Code, response.Body.String())
	}
	if service.session == nil {
		t.Fatal("access-session service was not called")
	}
	want := []string{"GetObject", "PutObject"}
	if len(service.session.Actions) != len(want) || service.session.Actions[0] != want[0] || service.session.Actions[1] != want[1] {
		t.Fatalf("canonical actions = %#v, want %#v", service.session.Actions, want)
	}
}

func TestPersonalBucketUsageResponseUsesSafeMegabyteString(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	bucketID := uuid.New()
	tests := []struct {
		name  string
		bytes int64
		want  string
	}{
		{name: "empty", bytes: 0, want: "0.000000"},
		{name: "one megabyte", bytes: 1_048_576, want: "1.000000"},
		{name: "fractional megabyte", bytes: 1_572_864, want: "1.500000"},
		{name: "maximum int64", bytes: 1<<63 - 1, want: "8796093022207.999999"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := storageHandler.NewPersonalBucketHandler(&personalBucketServiceStub{
				bucket: &storageEntity.PersonalBucket{
					ID:                 bucketID,
					Name:               "ws-test-bucket",
					CapacityQuotaBytes: 1 << 40,
					UsedBytes:          tt.bytes,
				},
			}, &personalStorageAccessSessionServiceStub{})
			router := gin.New()
			router.GET("/buckets/:id", func(c *gin.Context) {
				c.Set(pkgcontext.CtxUserID, userID)
				handler.Get(c)
			})

			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/buckets/"+bucketID.String(), nil))
			if response.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
			}

			var envelope struct {
				Data struct {
					UsedMB string `json:"used_mb"`
				} `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if envelope.Data.UsedMB != tt.want {
				t.Fatalf("used_mb = %q, want %q", envelope.Data.UsedMB, tt.want)
			}
		})
	}
}
