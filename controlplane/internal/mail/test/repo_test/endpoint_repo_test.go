package repo_test

import (
	"context"
	"testing"
	"time"

	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoImpl "controlplane/internal/mail/repository/postgres"
	"controlplane/internal/mail/test/testutil"
	"controlplane/internal/security"

	"github.com/google/uuid"
)

func TestEndpointRepoPostgres(t *testing.T) {
	cfg := testutil.NewMailTestConfig(testutil.UniqueSchema("mail_endpoint_repo"))
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareMailSchema(t, cfg, db)
	testutil.SetRuntimeMasterKeyFromConfig(t, cfg)

	repo := mailRepoImpl.NewEndpointRepository(db, cfg)
	ctx := context.Background()

	zoneID := uuid.New()
	endpointID := uuid.New()

	encPassword, err := security.EncryptSecret("super-secret-sendgrid-password-123")
	if err != nil {
		t.Fatalf("failed to encrypt secret: %v", err)
	}

	now := time.Now().UTC()
	endpoint := &mailEntity.Endpoint{
		ID:             endpointID,
		ZoneID:         zoneID,
		Name:           "Main SendGrid Endpoint",
		Host:           "smtp.sendgrid.net",
		Port:           587,
		Username:       "apikey",
		Password:       encPassword,
		TLSMode:        "starttls",
		Status:         "active",
		MaxConnections: 10,
		Priority:       100,
		Weight:         1,
		IsActive:       true,
		CreatedAt:      &now,
		UpdatedAt:      &now,
	}

	// Persist
	if err := repo.Create(ctx, endpoint); err != nil {
		t.Fatalf("create endpoint failed: %v", err)
	}

	// 2. Retrieve the endpoint.
	retrieved, err := repo.GetByID(ctx, zoneID, endpointID)
	if err != nil {
		t.Fatalf("get endpoint failed: %v", err)
	}

	if retrieved.ID != endpointID {
		t.Errorf("expected ID %q, got %q", endpointID, retrieved.ID)
	}
	if retrieved.Name != "Main SendGrid Endpoint" {
		t.Errorf("expected name, got %q", retrieved.Name)
	}

	// Verify decryption manually.
	decryptedPassword, err := security.DecryptSecret(retrieved.Password)
	if err != nil {
		t.Fatalf("failed to decrypt: %v", err)
	}

	if retrieved.Host != "smtp.sendgrid.net" {
		t.Errorf("expected host 'smtp.sendgrid.net', got %q", retrieved.Host)
	}
	if decryptedPassword != "super-secret-sendgrid-password-123" {
		t.Errorf("expected decrypted password to match, got %q", decryptedPassword)
	}

	// 3. List endpoints within the zone.
	list, err := repo.List(ctx, zoneID)
	if err != nil {
		t.Fatalf("list endpoints failed: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected list length 1, got %d", len(list))
	}

	// 4. Update the endpoint credentials.
	encPasswordNew, err := security.EncryptSecret("new-rotated-password-456")
	if err != nil {
		t.Fatalf("failed to encrypt updated secret: %v", err)
	}
	endpoint.Password = encPasswordNew
	endpoint.Name = "Updated SendGrid Endpoint"

	if err := repo.Update(ctx, endpoint); err != nil {
		t.Fatalf("update endpoint failed: %v", err)
	}

	// Retrieve again and verify changes.
	retrievedUpdated, err := repo.GetByID(ctx, zoneID, endpointID)
	if err != nil {
		t.Fatalf("get updated endpoint failed: %v", err)
	}
	if retrievedUpdated.Name != "Updated SendGrid Endpoint" {
		t.Errorf("expected name to be updated, got %q", retrievedUpdated.Name)
	}

	decryptedPasswordNew, err := security.DecryptSecret(retrievedUpdated.Password)
	if err != nil {
		t.Fatalf("failed to decrypt updated secret: %v", err)
	}

	if decryptedPasswordNew != "new-rotated-password-456" {
		t.Errorf("expected updated password, got %q", decryptedPasswordNew)
	}

	// 5. Delete the endpoint.
	if err := repo.Delete(ctx, zoneID, endpointID); err != nil {
		t.Fatalf("delete endpoint failed: %v", err)
	}

	// Verify it's gone.
	_, err = repo.GetByID(ctx, zoneID, endpointID)
	if err == nil {
		t.Errorf("expected get after delete to return error, but got nil")
	}
}
