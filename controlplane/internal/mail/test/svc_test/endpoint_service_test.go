package svc_test

import (
	"context"
	"testing"

	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoImpl "controlplane/internal/mail/repository/postgres"
	mailSvcImpl "controlplane/internal/mail/service"
	"controlplane/internal/mail/test/testutil"

	"github.com/google/uuid"
)

func TestEndpointServiceCRUD(t *testing.T) {
	cfg := testutil.NewMailTestConfig(testutil.UniqueSchema("mail_endpoint_svc"))
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareMailSchema(t, cfg, db)
	testutil.SetRuntimeMasterKeyFromConfig(t, cfg)

	repo := mailRepoImpl.NewEndpointRepository(db, cfg)
	service := mailSvcImpl.NewEndpointService(cfg, repo)
	ctx := context.Background()

	zoneID := uuid.New()

	// 1. Create Endpoint.
	createParams := mailEntity.CreateEndpointParams{
		ZoneID:   zoneID,
		Name:     "Production SendGrid Server",
		Provider: mailEntity.SendGrid,
		ConnectionConfig: map[string]interface{}{
			"host":     "smtp.sendgrid.net",
			"port":     float64(587),
			"username": "apikey",
			"password": "svc-sendgrid-key-xyz",
		},
	}

	err := service.CreateEndpoint(ctx, createParams)
	if err != nil {
		t.Fatalf("create endpoint failed: %v", err)
	}

	// 2. Retrieve Endpoint.
	// Since CreateEndpoint only returns error, we retrieve the generated ID by listing endpoints in the zone.
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

	// 3. List Endpoints.
	if len(list) != 1 {
		t.Errorf("expected 1 endpoint, got %d", len(list))
	}

	// 4. Update Endpoint.
	updateParams := mailEntity.UpdateEndpointParams{
		ZoneID: zoneID,
		ID:     createdID,
		Name:   "Updated SendGrid Server Name",
		ConnectionConfig: map[string]interface{}{
			"host":     "smtp.sendgrid.net",
			"port":     float64(587),
			"username": "apikey",
			"password": "svc-sendgrid-key-new",
		},
		IsActive: false,
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
