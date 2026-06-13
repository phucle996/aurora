package svc_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/http/middleware"
	mailEntity "controlplane/internal/mail/domain/entity"
	mailproto "controlplane/internal/mail/proto"
	mailRepoImpl "controlplane/internal/mail/repository/postgres"
	mailSvcImpl "controlplane/internal/mail/service"
	"controlplane/internal/mail/test/testutil"
	"controlplane/pkg/constant"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
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
	service := mailSvcImpl.NewEndpointService(cfg, repo, outboxRepo, registry)
	ctx := context.Background()
	ctx = middleware.ContextWithZoneID(ctx, zoneID)

	// 1. Create Endpoint.
	createParams := mailEntity.CreateEndpointParams{
		ZoneID:         zoneID,
		Name:           "Production SendGrid Server",
		Host:           "smtp.sendgrid.net",
		Port:           587,
		Username:       "apikey",
		Password:       "svc-sendgrid-key-xyz",
		TLSMode:        mailEntity.TLSModeStartTLS,
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
	list, nextCursor, err := service.ListEndpoints(ctx, "", 10)
	if err != nil {
		t.Fatalf("list endpoints failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected exactly 1 endpoint in zone, got %d", len(list))
	}
	if nextCursor != "" {
		t.Errorf("expected empty nextCursor, got %q", nextCursor)
	}
	createdID := list[0].ID

	if createdID == uuid.Nil {
		t.Errorf("expected UUIDv7 to be generated, got Nil uuid")
	}

	retrieved, err := service.GetEndpoint(ctx, createdID)
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
		TLSMode:        mailEntity.TLSModeStartTLS,
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
	if err := service.DeleteEndpoint(ctx, createdID); err != nil {
		t.Fatalf("delete endpoint failed: %v", err)
	}

	// Verify deletion.
	_, err = service.GetEndpoint(ctx, createdID)
	if err == nil {
		t.Errorf("expected get after delete to return error, but got nil")
	}
}

func TestEndpointServiceTestConnectionRaw(t *testing.T) {
	// Khởi tạo cấu hình schema riêng biệt cho tiến trình kiểm thử độc lập
	cfg := testutil.NewMailTestConfig(testutil.UniqueSchema("mail_endpoint_test_conn_raw"))
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareMailSchema(t, cfg, db)
	testutil.SetRuntimeMasterKeyFromConfig(t, cfg)

	redisServer := miniredis.RunT(t)
	rdsClient := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = rdsClient.Close() })

	zoneID := uuid.New()
	l1Cache := cacheengine.NewShardedCache()
	registry := cacheengine.NewCacheRegistry(l1Cache)

	repo := mailRepoImpl.NewEndpointRepository(db, cfg)
	outboxRepo := mailRepoImpl.NewMailOutboxRepository(db, cfg)
	service := mailSvcImpl.NewEndpointService(cfg, repo, outboxRepo, registry)
	ctx := context.Background()
	ctx = middleware.ContextWithZoneID(ctx, zoneID)
	ctx = context.WithValue(ctx, constant.ContextKeyUserID, "test-user-123")

	// Khởi tạo các tham số cấu hình SMTP thô để gửi yêu cầu test connection
	testReq := mailEntity.TestConnection{
		ZoneID:        zoneID,
		Host:          "smtp.example.com",
		Port:          587,
		Username:      "raw-test-user",
		Password:      "raw-test-password",
		TLSMode:       mailEntity.TLSModeStartTLS,
	}

	// Gọi TestConnectionRaw. Luồng mới sẽ chỉ lưu job vào outbox repository (Postgres)
	// mà không chặn kết nối đồng bộ tới Redis, đảm bảo tính sẵn sàng (HA) và tránh nghẽn luồng.
	err := service.TestConnectionRaw(ctx, testReq)
	if err != nil {
		t.Fatalf("TestConnectionRaw failed: %v", err)
	}

	// Xác nhận xem bản ghi outbox đã được lưu xuống Postgres thành công chưa
	// và trạng thái của bản ghi đó có đúng là PENDING hay không bằng cách truy vấn trực tiếp DB.
	var jobTopic, status, userID string
	var payloadBytes []byte
	query := fmt.Sprintf("SELECT job_topic, status, payload, user_id FROM %s.mail_outbox_records LIMIT 1", cfg.SchemaSQL.Mail)
	err = db.QueryRow(ctx, query).Scan(&jobTopic, &status, &payloadBytes, &userID)
	if err != nil {
		t.Fatalf("Failed to query outbox record from database: %v", err)
	}

	if jobTopic != "mail.test_connection" {
		t.Errorf("expected job topic 'mail.test_connection', got %s", jobTopic)
	}

	if status != string(mailEntity.OutboxStatusPending) {
		t.Errorf("expected outbox record status to be PENDING, got %s", status)
	}

	if userID != "test-user-123" {
		t.Errorf("expected user_id to be 'test-user-123', got %s", userID)
	}

	// Giải mã binary payload dưới dạng Protobuf
	var smtpConfig mailproto.SmtpTestConfig
	if err := proto.Unmarshal(payloadBytes, &smtpConfig); err != nil {
		t.Errorf("failed to unmarshal payload as Protobuf: %v", err)
	} else {
		if smtpConfig.Host != "smtp.example.com" {
			t.Errorf("expected host 'smtp.example.com', got %s", smtpConfig.Host)
		}
		if smtpConfig.Port != 587 {
			t.Errorf("expected port 587, got %d", smtpConfig.Port)
		}
	}
}
