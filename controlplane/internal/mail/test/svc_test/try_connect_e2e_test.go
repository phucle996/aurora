package svc_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	"controlplane/internal/http/middleware"
	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoImpl "controlplane/internal/mail/repository/postgres"
	mailSvcImpl "controlplane/internal/mail/service"
	"controlplane/pkg/constant"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	otelTrace "go.opentelemetry.io/otel/trace"
)

func TestTryConnectE2E(t *testing.T) {
	// 1. Cấu hình kết nối tới database thật (development database)
	cfg := config.LoadConfig()
	cfg.SchemaSQL.Mail = "mail" // Sử dụng schema mail thật
	cfg.Psql.Host = "127.0.0.1"
	cfg.Psql.Port = 15434 // Cổng debug của Docker Postgres
	cfg.Psql.User = "postgres"
	cfg.Psql.Password = "postgres"
	cfg.Psql.DBName = "controlplane"
	cfg.Psql.SSLMode = "disable"

	// 2. Khởi tạo kết nối DB pool
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Psql.Host, cfg.Psql.Port, cfg.Psql.User, cfg.Psql.Password, cfg.Psql.DBName, cfg.Psql.SSLMode)
	dbPool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("Failed to connect to real Postgres: %v", err)
	}
	defer dbPool.Close()

	// 3. Khởi tạo service với real repositories
	outboxRepo := mailRepoImpl.NewMailOutboxRepository(dbPool, cfg)
	repo := mailRepoImpl.NewEndpointRepository(dbPool, cfg, outboxRepo)

	l1Cache := cacheengine.NewShardedCache()
	registry := cacheengine.NewCacheRegistry(l1Cache)
	service := mailSvcImpl.NewEndpointService(cfg, repo, outboxRepo, registry)

	// 4. Thiết lập context giả lập user và zone
	ctx := context.Background()
	zoneID, err := uuid.Parse("019eba50-596b-7b77-b3e2-966e3f8ef885") // Zone 'viet-nam' từ DB
	if err != nil {
		t.Fatalf("Failed to parse zone ID: %v", err)
	}

	ctx = middleware.ContextWithZoneID(ctx, zoneID)

	uniqueUserID := fmt.Sprintf("e2e-user-%d", time.Now().UnixNano())
	ident := &constant.Identity{UserID: uniqueUserID}
	ctx = context.WithValue(ctx, constant.IdentityKey, ident)

	// [COMMENT]: Khởi tạo trace ID và span ID giả lập cố định để đồng bộ hóa và dễ dàng kiểm tra E2E distributed tracing
	traceIDHex := "fb100e26985cc00b3851cf65cd34e0e6"
	spanIDHex := "00f067aa0ba902b7"
	tID, err := otelTrace.TraceIDFromHex(traceIDHex)
	if err != nil {
		t.Fatalf("Failed to parse trace ID: %v", err)
	}
	sID, err := otelTrace.SpanIDFromHex(spanIDHex)
	if err != nil {
		t.Fatalf("Failed to parse span ID: %v", err)
	}
	spanCtx := otelTrace.NewSpanContext(otelTrace.SpanContextConfig{
		TraceID:    tID,
		SpanID:     sID,
		TraceFlags: otelTrace.FlagsSampled,
	})
	ctx = otelTrace.ContextWithSpanContext(ctx, spanCtx)

	// 5. Khởi tạo tham số TestConnection (SMTP kết nối lỗi cố ý)
	testReq := mailEntity.TestConnection{
		Host:     "127.0.0.1",
		Port:     45321, // Cổng không có dịch vụ nào nghe để nó connection refused nhanh chóng
		Username: "e2e-test-user",
		Password: "e2e-test-password",
		TLSMode:  mailEntity.TLSModeNone,
	}

	t.Logf("Calling TestConnectionRaw for user %s on zone %s...", uniqueUserID, zoneID.String())

	// 6. Thực thi qua service
	err = service.TestConnectionRaw(ctx, testReq)
	if err != nil {
		t.Fatalf("TestConnectionRaw failed: %v", err)
	}

	t.Log("Successfully called TestConnectionRaw. Polling database for outbox record status update...")

	// 7. Polling kiểm tra kết quả xử lý từ CDC + Dataplane + Result Consumer
	var dbStatus, errorMsg string
	var eventIDStr string
	var found bool

	query := fmt.Sprintf("SELECT event_id, status, COALESCE(error_message, '') FROM %s.mail_outbox_records WHERE user_id = $1", cfg.SchemaSQL.Mail)

	timeout := time.After(15 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			t.Fatalf("Timeout waiting for job execution. Last checked status: %s", dbStatus)
		case <-ticker.C:
			err = dbPool.QueryRow(ctx, query, uniqueUserID).Scan(&eventIDStr, &dbStatus, &errorMsg)
			if err == nil {
				found = true
				t.Logf("Found outbox record! EventID: %s, Current Status: %s", eventIDStr, dbStatus)
				if dbStatus == "FAILED" || dbStatus == "SUCCEEDED" {
					goto Done
				}
			}
		}
	}

Done:
	if !found {
		t.Fatal("Outbox record was never inserted or could not be found.")
	}

	t.Logf("Workflow completed! EventID: %s, Final Status: %s, Error Message: %q", eventIDStr, dbStatus, errorMsg)

	// Kiểm tra xem trạng thái có đúng là FAILED (vì ta dùng cổng 45321 không hợp lệ) hay không
	if dbStatus != "FAILED" {
		t.Errorf("Expected final status FAILED, got %s", dbStatus)
	}
}
