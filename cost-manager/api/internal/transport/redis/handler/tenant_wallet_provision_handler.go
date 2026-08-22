package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"cost-manager/api/internal/config"
	billingSvcInterface "cost-manager/api/internal/domain/service"
	iamlifecyclev1 "cost-manager/api/internal/genproto/iam/lifecycle/v1"
	"cost-manager/api/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const (
	// tenantWalletProvisionEventType là loại sự kiện yêu cầu tạo ví Tenant phát từ Hierarchy / IAM
	tenantWalletProvisionEventType = "billing.tenant_wallet.provision.requested.v1"

	// tenantWalletProvisionStream là Redis Stream nguồn nhận sự kiện tạo ví Tenant
	tenantWalletProvisionStream = "billing:tenant-wallet:provision:requested:v1"

	// tenantWalletProvisionGroup là tên Consumer Group cho tiến trình khởi tạo ví Tenant
	tenantWalletProvisionGroup = "cost-tenant-wallet-provision-v1"

	// tenantWalletProvisionDLQ là hàng đợi thư chết (Dead Letter Queue) lưu trữ các sự kiện lỗi hoặc vượt ngưỡng retry
	tenantWalletProvisionDLQ = "billing:wallet:tenant:provision-dlq"

	// tenantWalletReclaimIdle là khoảng thời gian tối thiểu một message nằm trong PEL trước khi bị worker khác claim lại
	tenantWalletReclaimIdle = 30 * time.Second

	// tenantWalletMaxDeliveries là số lần giao vận tối đa cho một message trước khi chuyển sang DLQ
	tenantWalletMaxDeliveries = 25
)

// TenantWalletProvisionConsumer là Redis Stream Consumer xử lý sự kiện tạo ví Tenant (`TenantWalletProvisionRequestedV1`):
// - Đảm bảo tính Idempotency thông qua Transactional Inbox (`billing.tenant_wallet_provision_inbox`).
// - Khởi tạo ví tiền tổ chức (`billing.wallets` với owner_type = 'TENANT', trạng thái PENDING_ACTIVATION, số dư $0.00 USD).
// - Phát tín hiệu kiểm soát truy cập (`billing.wallet_admission_outbox` với chế độ SUSPEND_BILLABLE).
// - Quản lý chu kỳ phân phối tin cậy với cơ chế tự phục hồi `XAutoClaim` và chuyển hướng thư chết (DLQ).
type TenantWalletProvisionConsumer struct {
	sharedRedis *goredis.Client                         // Client kết nối tới cụm Shared Redis
	service     billingSvcInterface.TenantAccountService // Domain Service thực thi nghiệp vụ tạo ví Tenant
	consumer    string                                  // Mã định danh duy nhất của Consumer instance (Hostname + UUID)

	cancel context.CancelFunc // Hàm hủy context điều khiển vòng lặp tiêu thụ sự kiện
	done   chan struct{}      // Channel báo hiệu goroutine consumer đã dừng hoàn tất
	once   sync.Once          // Đảm bảo phương thức Stop chỉ thực thi một lần duy nhất
}

// NewTenantWalletProvisionConsumer khởi tạo một Consumer instance mới với định danh riêng biệt.
func NewTenantWalletProvisionConsumer(
	sharedRedis *goredis.Client,
	service billingSvcInterface.TenantAccountService,
) *TenantWalletProvisionConsumer {
	return &TenantWalletProvisionConsumer{
		sharedRedis: sharedRedis,
		service:     service,
		// Định danh consumer duy nhất theo từng tiến trình (Hostname + UUIDv4) cho phép XAUTOCLAIM
		// tiếp quản các message tồn đọng của các pod đã chết mà không gây xung đột lease giữa các pod đang chạy.
		consumer: config.GetNodeHostname() + "-" + uuid.NewString(),
		done:     make(chan struct{}),
	}
}

// Start khởi tạo Consumer Group trên Redis Stream (nếu chưa có) và bắt đầu vòng lặp tiêu thụ sự kiện trong background goroutine.
func (s *TenantWalletProvisionConsumer) Start() error {
	if s.cancel != nil {
		return errors.New("tenant wallet provision consumer already started")
	}
	ctx, cancel := context.WithCancel(context.Background())

	// Khởi tạo Consumer Group bắt đầu đọc từ vị trí "0" (đầu stream) kèm tạo stream tự động (MkStream) nếu chưa tồn tại
	if err := s.sharedRedis.XGroupCreateMkStream(
		ctx,
		tenantWalletProvisionStream,
		tenantWalletProvisionGroup,
		"0",
	).Err(); err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		cancel()
		return fmt.Errorf("tenant wallet provision: create consumer group: %w", err)
	}
	s.cancel = cancel
	go s.run(ctx)
	return nil
}

// run là vòng lặp chính thực hiện cơ chế tiêu thụ sự kiện 2 giai đoạn (Two-Phase Consumption):
// 1. Tự phục hồi (Reclaim Phase): Quét và nhận lại các message bị treo trong PEL (Pending Entries List) qua `XAutoClaim`.
// 2. Tiêu thụ thông thường (Normal Phase): Đọc các message mới phát sinh qua `XReadGroup` với cơ chế Block 5 giây.
func (s *TenantWalletProvisionConsumer) run(ctx context.Context) {
	defer close(s.done)
	for {
		if ctx.Err() != nil {
			return
		}

		// Giai đoạn 1: Claim lại các message chưa được ACK có thời gian chờ (idle) vượt quá 30 giây.
		// Khoảng thời gian này lớn hơn timeout của DB Transaction (10 giây) để tránh việc pod mới cướp message khi pod cũ vẫn đang commit.
		claimed, _, err := s.sharedRedis.XAutoClaim(ctx, &goredis.XAutoClaimArgs{
			Stream:   tenantWalletProvisionStream,
			Group:    tenantWalletProvisionGroup,
			Consumer: s.consumer,
			MinIdle:  tenantWalletReclaimIdle,
			Start:    "0-0",
			Count:    32,
		}).Result()
		if err != nil && !errors.Is(err, goredis.Nil) {
			if !waitTenantWalletRetry(ctx, "billing.wallet.tenant.redis.reclaim", err) {
				return
			}
			continue
		}
		for _, message := range claimed {
			s.process(ctx, message)
		}
		// Nếu vừa claim được message tồn đọng, tiếp tục ưu tiên quét cạn PEL trước khi đọc message mới
		if len(claimed) > 0 {
			continue
		}

		// Giai đoạn 2: Đọc các thông điệp mới (kí hiệu '>') từ Redis Stream
		streams, err := s.sharedRedis.XReadGroup(ctx, &goredis.XReadGroupArgs{
			Group:    tenantWalletProvisionGroup,
			Consumer: s.consumer,
			Streams:  []string{tenantWalletProvisionStream, ">"},
			Count:    32,
			Block:    5 * time.Second,
		}).Result()
		if err != nil {
			if errors.Is(err, goredis.Nil) {
				continue
			}
			if !waitTenantWalletRetry(ctx, "billing.wallet.tenant.redis.read", err) {
				return
			}
			continue
		}
		for _, stream := range streams {
			for _, message := range stream.Messages {
				s.process(ctx, message)
			}
		}
	}
}

// waitTenantWalletRetry xử lý chờ đợi an toàn trước khi thử lại kết nối Redis khi xảy ra lỗi mạng hoặc timeout.
func waitTenantWalletRetry(ctx context.Context, operation string, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	logger.SysError(operation, err.Error())
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// process thực hiện toàn bộ quy trình kiểm tra bảo mật, giải mã hợp đồng và thực thi tạo ví Tenant:
// 1. Kiểm tra giới hạn kích thước payload (Security Gate: Payload <= 64KB).
// 2. Giải mã và kiểm tra tính toàn vẹn của Protobuf `TenantWalletProvisionRequestedV1`.
// 3. Tính mã băm SHA-256 checksum của payload để chống trùng lặp tại Transactional Inbox.
// 4. Mở Transaction cơ sở dữ liệu PostgreSQL (`ProvisionTenantWallet`): Ghi Inbox, Khởi tạo ví, Ghi Outbox Admission.
// 5. Nếu thành công: Gửi `XACK` + `XDEL` và xóa biến đếm retry atomically.
// 6. Nếu lỗi DB: Tăng biến đếm `delivery-attempts`. Nếu >= 25 lần thì đẩy sang DLQ.
func (s *TenantWalletProvisionConsumer) process(ctx context.Context, message goredis.XMessage) {
	// ============================================================================
	// TRÍCH XUẤT TRƯỜNG DỮ LIỆU TỪ REDIS MESSAGE
	// ============================================================================
	var eventIDText string
	switch value := message.Values["event_id"].(type) {
	case string:
		eventIDText = value
	case []byte:
		eventIDText = string(value)
	}
	var eventType string
	switch value := message.Values["event_type"].(type) {
	case string:
		eventType = value
	case []byte:
		eventType = string(value)
	}
	var payload []byte
	switch value := message.Values["payload"].(type) {
	case string:
		payload = []byte(value)
	case []byte:
		payload = value
	}

	// ============================================================================
	// KIỂM TRA BẢO MẬT VÀ GIẢI MÃ HỢP ĐỒNG (SECURITY GATE & CONTRACT VALIDATION)
	// ============================================================================
	// Giới hạn kích thước trước khi giải mã Protobuf để ngăn chặn tấn công cấp phát bộ nhớ vô hạn (Memory Exhaustion / DOS)
	if len(payload) == 0 || len(payload) > 64*1024 {
		s.deadLetter(ctx, message, "invalid_contract")
		return
	}

	wire := &iamlifecyclev1.TenantWalletProvisionRequestedV1{}
	eventID, eventErr := uuid.Parse(eventIDText)
	protoErr := proto.Unmarshal(payload, wire)
	wireEventID, wireEventErr := uuid.FromBytes(wire.GetEventId())
	tenantID, tenantErr := uuid.FromBytes(wire.GetTenantId())
	actorID, actorErr := uuid.FromBytes(wire.GetActorUserId())
	_, occurredErr := time.Parse(time.RFC3339Nano, wire.GetOccurredAt())

	// Kiểm tra tính toàn vẹn của Envelope và các trường định danh bắt buộc
	if eventErr != nil || protoErr != nil || wireEventErr != nil ||
		tenantErr != nil || actorErr != nil ||
		eventID == uuid.Nil || wireEventID != eventID ||
		tenantID == uuid.Nil || actorID == uuid.Nil ||
		eventType != tenantWalletProvisionEventType ||
		wire.GetCurrency() != "USD" ||
		wire.GetSchemaVersion() != 1 || occurredErr != nil {
		// Poison contract không thể tự hồi phục $\to$ Đẩy ngay sang DLQ để không làm nghẽn worker loop
		s.deadLetter(ctx, message, "invalid_contract")
		return
	}

	// ============================================================================
	// THỰC THI TRANSACTION TẠO VÍ TRONG POSTGRESQL (TRANSACTIONAL INBOX)
	// ============================================================================
	hash := sha256.Sum256(payload)
	applyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err := s.service.ProvisionTenantWallet(
		applyCtx,
		eventID,
		tenantID,
		actorID,
		hex.EncodeToString(hash[:]),
	)
	cancel()

	// ============================================================================
	// XỬ LÝ LỖI VÀ CHÍNH SÁCH RETRY / DLQ
	// ============================================================================
	if err != nil {
		attemptKey := "billing:wallet:tenant:delivery-attempts:" + message.ID
		attempts, countErr := s.sharedRedis.Incr(ctx, attemptKey).Result()
		if countErr == nil {
			_ = s.sharedRedis.Expire(ctx, attemptKey, 30*24*time.Hour).Err()
		}
		logger.SysError("billing.wallet.tenant.redis.apply", fmt.Sprintf("event_id=%s: %v", eventID, err))

		// Nếu số lần giao vận vượt ngưỡng tối đa (25 lần), chuyển vào hàng đợi thư chết (DLQ)
		if countErr == nil && attempts >= tenantWalletMaxDeliveries {
			s.deadLetter(ctx, message, "apply_retries_exhausted")
		}
		// Không gửi ACK khi Transaction DB gặp lỗi; message sẽ được XAutoClaim nhận lại sau thời gian MinIdle
		return
	}

	// ============================================================================
	// HẬU COMMIT: ACK, XDEL VÀ DỌN DẸP TRÊN REDIS (ATOMIC PIPELINE)
	// ============================================================================
	if _, err := s.sharedRedis.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
		pipe.XAck(ctx, tenantWalletProvisionStream, tenantWalletProvisionGroup, message.ID)
		pipe.XDel(ctx, tenantWalletProvisionStream, message.ID)
		pipe.Del(ctx, "billing:wallet:tenant:delivery-attempts:"+message.ID)
		return nil
	}); err != nil {
		logger.SysError("billing.wallet.tenant.redis.ack", err.Error())
	}
}

// deadLetter di dời message bị lỗi hợp đồng hoặc quá số lần retry sang Dead Letter Queue (DLQ):
// - Giữ nguyên toàn bộ payload gốc để phục vụ kiểm toán hoặc replay thủ công từ phía kỹ sư vận hành.
// - Xóa thông điệp khỏi Stream chính để giải phóng hàng đợi và ngăn chặn worker nghẽn vô hạn.
func (s *TenantWalletProvisionConsumer) deadLetter(
	ctx context.Context,
	message goredis.XMessage,
	reason string,
) {
	if _, err := s.sharedRedis.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
		// 1. Chép sang DLQ stream
		pipe.XAdd(ctx, &goredis.XAddArgs{
			Stream: tenantWalletProvisionDLQ,
			Values: map[string]any{
				"source_stream_id": message.ID,
				"reason":           reason,
				"event_id":         message.Values["event_id"],
				"event_type":       message.Values["event_type"],
				"payload":          message.Values["payload"],
			},
		})
		// 2. ACK và XDEL khỏi Stream chính
		pipe.XAck(ctx, tenantWalletProvisionStream, tenantWalletProvisionGroup, message.ID)
		pipe.XDel(ctx, tenantWalletProvisionStream, message.ID)
		// 3. Xóa biến đếm delivery attempts
		pipe.Del(ctx, "billing:wallet:tenant:delivery-attempts:"+message.ID)
		return nil
	}); err != nil {
		logger.SysError("billing.wallet.tenant.redis.dlq", err.Error())
		return
	}
	logger.SysWarn("billing.wallet.tenant.redis.dlq", "tenant wallet provision event moved to DLQ: "+reason)
}

// Stop thực hiện dừng an toàn Tenant Wallet Provision Consumer (Graceful Shutdown).
func (s *TenantWalletProvisionConsumer) Stop() {
	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		select {
		case <-s.done:
		case <-time.After(6 * time.Second):
			logger.SysWarn("billing.wallet.tenant.redis.stop", "timed out waiting for tenant wallet provision consumer")
		}
	})
}
