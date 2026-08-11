package unit_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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

func (s *personalBucketServiceStub) GetBucket(_ context.Context, _ uuid.UUID, _ uuid.UUID) (*storageEntity.PersonalBucket, error) {
	return s.bucket, nil
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
			})
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
