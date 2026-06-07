package svc_test

import (
	"context"
	"testing"
	"time"

	"controlplane/internal/cacheengine"
	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoImpl "controlplane/internal/mail/repository/postgres"
	mailSvcImpl "controlplane/internal/mail/service"
	"controlplane/internal/mail/test/testutil"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

func TestEndpointServiceCRUD(t *testing.T) {
	cfg := testutil.NewMailTestConfig(testutil.UniqueSchema("mail_endpoint_svc"))
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareMailSchema(t, cfg, db)
	testutil.SetRuntimeMasterKeyFromConfig(t, cfg)

	redisServer := miniredis.RunT(t)
	rdsClient := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = rdsClient.Close() })

	zoneID := uuid.New()
	l1Cache := cacheengine.NewShardedCache()
	registry := cacheengine.NewCacheRegistry(l1Cache)
	cacheengine.Register(registry, "zone_by_code", 5*time.Minute, func(ctx context.Context, param string) (string, error) {
		return zoneID.String(), nil
	})

	repo := mailRepoImpl.NewEndpointRepository(db, cfg)
	outboxRepo := mailRepoImpl.NewMailOutboxRepository(db, cfg)
	service := mailSvcImpl.NewEndpointService(cfg, repo, outboxRepo, rdsClient, registry)
	ctx := context.Background()
	ctx = mailSvcImpl.WithZoneCode(ctx, "test-zone")

	// 1. Create Endpoint.
	createParams := mailEntity.CreateEndpointParams{
		ZoneID:         zoneID,
		Name:           "Production SendGrid Server",
		Host:           "smtp.sendgrid.net",
		Port:           587,
		Username:       "apikey",
		Password:       "svc-sendgrid-key-xyz",
		TLSMode:        "starttls",
		Status:         "active",
		MaxConnections: 10,
		Priority:       100,
		Weight:         1,
	}

	err := service.CreateEndpoint(ctx, createParams)
	if err != nil {
		t.Fatalf("create endpoint failed: %v", err)
	}

	// 2. Retrieve Endpoint.
	list, err := service.ListEndpoints(ctx, zoneID)
	if err != nil {
		t.Fatalf("list endpoints failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected exactly 1 endpoint in zone, got %d", len(list))
	}
	createdID := list[0].ID

	if createdID == uuid.Nil {
		t.Errorf("expected UUIDv7 to be generated, got Nil uuid")
	}

	retrieved, err := service.GetEndpoint(ctx, zoneID, createdID)
	if err != nil {
		t.Fatalf("get endpoint failed: %v", err)
	}
	if retrieved.ID != createdID {
		t.Errorf("expected ID %q, got %q", createdID.String(), retrieved.ID.String())
	}
	if retrieved.Name != "Production SendGrid Server" {
		t.Errorf("expected name Production SendGrid Server, got %q", retrieved.Name)
	}
	if retrieved.Host != "smtp.sendgrid.net" {
		t.Errorf("expected host, got %q", retrieved.Host)
	}
	if retrieved.Password != "svc-sendgrid-key-xyz" {
		t.Errorf("expected decrypted password to match, got %q", retrieved.Password)
	}

	// 3. List Endpoints.
	if len(list) != 1 {
		t.Errorf("expected 1 endpoint, got %d", len(list))
	}

	// 4. Update Endpoint.
	updateParams := mailEntity.UpdateEndpointParams{
		ZoneID:         zoneID,
		ID:             createdID,
		Name:           "Updated SendGrid Server Name",
		Host:           "smtp.sendgrid.net",
		Port:           587,
		Username:       "apikey",
		Password:       "svc-sendgrid-key-new",
		TLSMode:        "starttls",
		Status:         "active",
		MaxConnections: 10,
		Priority:       100,
		Weight:         1,
		IsActive:       false,
	}

	updated, err := service.UpdateEndpoint(ctx, updateParams)
	if err != nil {
		t.Fatalf("update endpoint failed: %v", err)
	}

	if updated.Name != "Updated SendGrid Server Name" {
		t.Errorf("expected updated name, got %q", updated.Name)
	}
	if updated.IsActive != false {
		t.Errorf("expected inactive status, got true")
	}

	// 5. Delete Endpoint.
	if err := service.DeleteEndpoint(ctx, zoneID, createdID); err != nil {
		t.Fatalf("delete endpoint failed: %v", err)
	}

	// Verify deletion.
	_, err = service.GetEndpoint(ctx, zoneID, createdID)
	if err == nil {
		t.Errorf("expected get after delete to return error, but got nil")
	}
}
