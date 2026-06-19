// ============================================================================
// 📂 TEST: controlplane/internal/mail/test/svc_test/e2e_job_proxy_test.go
//          Kiểm Thử E2E - Luồng Outbox Đồng Bộ Sang Job Proxy Qua Logical Replication
// ============================================================================

package svc_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	"controlplane/internal/http/middleware"
	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoImpl "controlplane/internal/mail/repository/postgres"
	mailSvcImpl "controlplane/internal/mail/service"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

func TestE2E_OutboxToJobProxy(t *testing.T) {
	// Kiểm tra nếu biến môi trường RUN_E2E không được bật thì bỏ qua test E2E này
	if os.Getenv("RUN_E2E") != "true" {
		t.Skip("Bỏ qua E2E test. Thiết lập RUN_E2E=true để khởi chạy kiểm thử thực tế.")
	}

	cfg := config.LoadConfig()
	// Ghi đè địa chỉ host để kết nối trực tiếp từ máy host vào các cổng exposed của docker containers
	cfg.Psql.Host = "127.0.0.1"
	cfg.Psql.Port = 15434 // Cổng Postgres debug trực tiếp
	cfg.Psql.User = "postgres"
	cfg.Psql.Password = "postgres"
	cfg.Psql.DBName = "controlplane"
	cfg.SchemaSQL.Mail = "mail"

	cfg.RedisJob.Addr = "127.0.0.1:6380" // Cổng Redis Job exposed


	// Khởi tạo kết nối tới cơ sở dữ liệu Postgres thực tế
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Psql.Host, cfg.Psql.Port, cfg.Psql.User, cfg.Psql.Password, cfg.Psql.DBName, cfg.Psql.SSLMode)
	dbPool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("kết nối Postgres thất bại: %v", err)
	}
	defer dbPool.Close()

	// Khởi tạo kết nối tới Redis Job thực tế
	redisClient := goredis.NewClient(&goredis.Options{
		Addr: cfg.RedisJob.Addr,
	})
	defer redisClient.Close()

	// Kiểm tra tính sẵn sàng của Redis Job
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("kết nối Redis Job tại %s thất bại: %v", cfg.RedisJob.Addr, err)
	}

	// Tạo một ZoneID ngẫu nhiên bằng UUIDv7 để cô lập stream testing
	zoneID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("sinh zone id thất bại: %v", err)
	}

	// Tạo L1 cache registry mock
	l1Cache := cacheengine.NewShardedCache()
	registry := cacheengine.NewCacheRegistry(l1Cache)
	cacheengine.Register(registry, "zone_by_code", 5*time.Minute, func(ctx context.Context, param string) (string, error) {
		return zoneID.String(), nil
	})

	outboxRepo := mailRepoImpl.NewMailOutboxRepository(dbPool, cfg)
	repo := mailRepoImpl.NewEndpointRepository(dbPool, cfg, outboxRepo)
	service := mailSvcImpl.NewEndpointService(cfg, repo, outboxRepo, registry)

	ctx := context.Background()
	ctx = middleware.ContextWithZoneID(ctx, zoneID)

	// Key của Redis Stream mà job-proxy sẽ đẩy công việc vào sau khi bắt được WAL từ Postgres
	streamKey := fmt.Sprintf("jobs:%s", zoneID.String())

	// Làm sạch dữ liệu stream cũ nếu có trùng hợp ngẫu nhiên
	redisClient.Del(ctx, streamKey)
	defer redisClient.Del(ctx, streamKey)

	// Khởi tạo thông tin mail endpoint thử nghiệm
	createParams := mailEntity.CreateEndpoint{
		ZoneID:         zoneID,
		Name:           "E2E Test SMTP Server",
		Host:           "smtp.mailtrap.io",
		Port:           2525,
		Username:       "e2e-user",
		Password:       "e2e-pass",
		TLSMode:        mailEntity.TLSModeStartTLS,
		Status:         "active",
		MaxConnections: 5,
		Priority:       1,
		Weight:         1,
	}

	t.Logf("Tiến hành gọi service để tạo mail endpoint tại Postgres cho zone %s...", zoneID.String())
	err = service.CreateEndpoint(ctx, &createParams)
	if err != nil {
		t.Fatalf("tạo mail endpoint thất bại: %v", err)
	}

	// Lúc này bản ghi đã nằm trong outbox table. Job-proxy chạy trong Docker sẽ bắt được WAL event,
	// đóng gói thành job và ghi vào Redis Stream jobs:<zone_id>. Ta thực hiện polling để kiểm chứng.
	t.Logf("Đang chờ job-proxy xử lý sự kiện WAL và đẩy job vào Redis Stream %s...", streamKey)

	var jobFound bool
	var jobIDStr string
	var jobTopic string

	// Thực hiện polling Redis Stream liên tục tối đa 10 giây
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		streams, err := redisClient.XRead(ctx, &goredis.XReadArgs{
			Streams: []string{streamKey, "0"},
			Count:   1,
		}).Result()

		if err == nil && len(streams) > 0 && len(streams[0].Messages) > 0 {
			message := streams[0].Messages[0]
			jobIDStr = message.Values["job_id"].(string)
			jobTopic = message.Values["job_topic"].(string)
			jobFound = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !jobFound {
		t.Fatalf("LỖI KIỂM THỬ E2E: Job không được đẩy vào Redis Stream %s trong thời gian chờ. Job-proxy có thể chưa chạy, hoặc Logical Replication Slot bị tắc nghẽn.", streamKey)
	}

	t.Logf("THÀNH CÔNG: Job đã được job-proxy đẩy vào Redis stream.")
	t.Logf("Thông tin Job - ID: %s, Topic: %s", jobIDStr, jobTopic)

	if jobTopic != "mail.create_endpoint" {
		t.Errorf("Kỳ vọng topic 'mail.create_endpoint', nhưng nhận được: %q", jobTopic)
	}
}
