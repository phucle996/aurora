package repo_test

import (
	"context"
	"encoding/json"
	"testing"

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

	// 1. Create a secure connection configuration.
	connConfig := map[string]interface{}{
		"host":     "smtp.sendgrid.net",
		"port":     float64(587),
		"username": "apikey",
		"password": "super-secret-sendgrid-password-123",
	}

	jsonBytes, err := json.Marshal(connConfig)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}

	encryptedPayload, err := security.EncryptSecret(string(jsonBytes))
	if err != nil {
		t.Fatalf("failed to encrypt secret: %v", err)
	}

	endpoint := &mailEntity.Endpoint{
		ID:       endpointID,
		ZoneID:   zoneID,
		Name:     "Main SendGrid Endpoint",
		Provider: mailEntity.SendGrid,
		IsActive: true,
	}

	// Persist
	if err := repo.Create(ctx, endpoint, []byte(encryptedPayload)); err != nil {
		t.Fatalf("create endpoint failed: %v", err)
	}

	// 2. Retrieve the endpoint and verify we get the encrypted payload.
	retrieved, encConfig, err := repo.GetByID(ctx, zoneID, endpointID)
	if err != nil {
		t.Fatalf("get endpoint failed: %v", err)
	}

	if retrieved.ID != endpointID {
		t.Errorf("expected ID %q, got %q", endpointID, retrieved.ID)
	}
	if retrieved.Name != "Main SendGrid Endpoint" {
		t.Errorf("expected name, got %q", retrieved.Name)
	}
	if retrieved.Provider != mailEntity.SendGrid {
		t.Errorf("expected provider, got %v", retrieved.Provider)
	}

	// Verify decryption manually.
	decryptedPayload, err := security.DecryptSecret(string(encConfig))
	if err != nil {
		t.Fatalf("failed to decrypt: %v", err)
	}

	var plainConfig map[string]interface{}
	if err := json.Unmarshal([]byte(decryptedPayload), &plainConfig); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	retrievedHost, _ := plainConfig["host"].(string)
	retrievedPassword, _ := plainConfig["password"].(string)
	if retrievedHost != "smtp.sendgrid.net" {
		t.Errorf("expected host 'smtp.sendgrid.net', got %q", retrievedHost)
	}
	if retrievedPassword != "super-secret-sendgrid-password-123" {
		t.Errorf("expected decrypted password to match, got %q", retrievedPassword)
	}

	// 3. List endpoints within the zone.
	list, encConfigs, err := repo.List(ctx, zoneID)
	if err != nil {
		t.Fatalf("list endpoints failed: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected list length 1, got %d", len(list))
	}
	if len(encConfigs) != 1 {
		t.Errorf("expected 1 encrypted config, got %d", len(encConfigs))
	}

	// 4. Update the endpoint credentials.
	connConfig["password"] = "new-rotated-password-456"
	endpoint.Name = "Updated SendGrid Endpoint"

	jsonBytesNew, err := json.Marshal(connConfig)
	if err != nil {
		t.Fatalf("failed to marshal updated config: %v", err)
	}
	encryptedPayloadNew, err := security.EncryptSecret(string(jsonBytesNew))
	if err != nil {
		t.Fatalf("failed to encrypt updated secret: %v", err)
	}

	if err := repo.Update(ctx, endpoint, []byte(encryptedPayloadNew)); err != nil {
		t.Fatalf("update endpoint failed: %v", err)
	}

	// Retrieve again and verify changes.
	retrievedUpdated, encConfigUpdated, err := repo.GetByID(ctx, zoneID, endpointID)
	if err != nil {
		t.Fatalf("get updated endpoint failed: %v", err)
	}
	if retrievedUpdated.Name != "Updated SendGrid Endpoint" {
		t.Errorf("expected name to be updated, got %q", retrievedUpdated.Name)
	}

	decryptedPayloadNew, err := security.DecryptSecret(string(encConfigUpdated))
	if err != nil {
		t.Fatalf("failed to decrypt updated secret: %v", err)
	}

	var plainConfigNew map[string]interface{}
	if err := json.Unmarshal([]byte(decryptedPayloadNew), &plainConfigNew); err != nil {
		t.Fatalf("failed to unmarshal updated secret: %v", err)
	}

	updatedPassword, _ := plainConfigNew["password"].(string)
	if updatedPassword != "new-rotated-password-456" {
		t.Errorf("expected updated password, got %q", updatedPassword)
	}

	// 5. Delete the endpoint.
	if err := repo.Delete(ctx, zoneID, endpointID); err != nil {
		t.Fatalf("delete endpoint failed: %v", err)
	}

	// Verify it's gone.
	_, _, err = repo.GetByID(ctx, zoneID, endpointID)
	if err == nil {
		t.Errorf("expected get after delete to return error, but got nil")
	}
}
