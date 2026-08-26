package iamSvcImpl

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamproto "controlplane/internal/iam/transport/proto"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const (
	// personalWalletProvisionRequestedEventType là loại sự kiện yêu cầu tạo ví cá nhân phát sang Cost Manager
	personalWalletProvisionRequestedEventType = "billing.personal_wallet.provision.requested.v1"

	// personalWalletProvisionRequestedStream là Redis Stream đích nhận sự kiện tạo ví cá nhân
	personalWalletProvisionRequestedStream = "billing:personal-wallet:provision:requested:v1"

	// lifecycleFactOutboxClaimBatch là số lượng bản ghi outbox tối đa quét trong một lượt xử lý (Batch size)
	lifecycleFactOutboxClaimBatch = 50

	// lifecycleFactOutboxFallbackInterval là chu kỳ quét định kỳ dự phòng (Safety Net) khi không nhận được tín hiệu wake
	lifecycleFactOutboxFallbackInterval = 30 * time.Second

	// lifecycleFactOutboxFallbackJitter là khoảng thời gian ngẫu nhiên cộng thêm để làm lệch nhịp giữa các Pod Controlplane
	lifecycleFactOutboxFallbackJitter = 10 * time.Second

	// lifecycleFactOutboxRetryMin là khoảng thời gian chờ thử lại tối thiểu khi gặp lỗi DB / Redis
	lifecycleFactOutboxRetryMin = time.Second

	// lifecycleFactOutboxRetryMax là khoảng thời gian chờ thử lại tối đa (Trần Exponential Backoff)
	lifecycleFactOutboxRetryMax = 30 * time.Second

	// lifecycleFactOutboxLeaseDuration must remain greater than a single Redis
	// durability fence, otherwise another pod may reclaim a record mid-publish.
	lifecycleFactOutboxLeaseDuration = 30 * time.Second
)

// lifecycleFactStreams ánh xạ loại sự kiện với Redis Stream đích tương ứng.
// Ràng buộc bảo mật: Stream không đọc trực tiếp từ database row; mọi sự kiện mới bắt buộc phải nằm trong Allowlist này.
var lifecycleFactStreams = map[string]string{
	personalWalletProvisionRequestedEventType: personalWalletProvisionRequestedStream,
}

// LifecycleFactRelay là Background Worker đảm nhiệm chuyển tiếp sự kiện vòng đời (Transactional Outbox Relay)
// từ bảng cơ sở dữ liệu PostgreSQL (`iam.lifecycle_fact_outbox_records`) sang Shared Redis Stream:
// - Đảm bảo nguyên tắc "At-Least-Once Delivery" và "Zero Event Loss": Sự kiện chỉ được đánh dấu PUBLISHED sau khi Redis xác nhận ghi đĩa bền vững (`WAITAOF`).
// - Cơ chế Đánh thức Kép (Dual Wake Mechanism): Nhận tín hiệu kích hoạt tức thời qua channel `wake` kết hợp quét định kỳ (Fallback Timer + Jitter) để tự chữa lành khi có sự cố.
// - Bảo vệ Hạ tầng (Fault Tolerance): Áp dụng Exponential Backoff khi database lỗi, dùng `FOR UPDATE SKIP LOCKED` chống tranh chấp khóa giữa nhiều Pod.
type LifecycleFactRelay struct {
	repo        iamRepoInterface.LifecycleFactOutboxRepository // Repository truy xuất bảng outbox records trong PostgreSQL
	sharedRedis *goredis.Client                                // Client kết nối tới cụm Shared Redis
	replicaAcks int                                            // Số lượng Redis Replicas tối thiểu cần xác nhận ghi đĩa thành công
	durableWait time.Duration                                  // Thời gian timeout tối đa cho lệnh WAITAOF ghi đĩa
	wake        chan struct{}                                  // Channel nhận tín hiệu đánh thức tức thì sau khi API commit DB
	cancel      context.CancelFunc                             // Hàm hủy Context để dừng background worker an toàn
	done        chan struct{}                                  // Channel báo hiệu worker đã dừng hoàn tất (Graceful Shutdown)
}

// NewLifecycleFactRelay khởi tạo một thể hiện của LifecycleFactRelay với các tham số kiểm tra ràng buộc nghiêm ngặt.
func NewLifecycleFactRelay(
	repo iamRepoInterface.LifecycleFactOutboxRepository,
	sharedRedis *goredis.Client,
	replicaAcks int,
	durableWait time.Duration,
) (*LifecycleFactRelay, error) {
	if repo == nil || sharedRedis == nil {
		return nil, errors.New("IAM lifecycle fact relay requires repository and Shared Redis")
	}
	if replicaAcks < 0 || durableWait <= 0 || durableWait+time.Second >= lifecycleFactOutboxLeaseDuration {
		return nil, errors.New("IAM lifecycle fact relay requires a durability fence that fits inside its outbox lease")
	}
	return &LifecycleFactRelay{
		repo:        repo,
		sharedRedis: sharedRedis,
		replicaAcks: replicaAcks,
		durableWait: durableWait,
		// Dung lượng buffer = 1: Cố ý gộp (coalesce) các đợt kích hoạt dồn dập (burst verify).
		// Dữ liệu sự kiện thực tế nằm bền vững trong DB; channel wake chỉ đóng vai trò là gợi ý (hint).
		wake: make(chan struct{}, 1),
		done: make(chan struct{}),
	}, nil
}

// Notify gửi tín hiệu đánh thức Relay Worker chạy ngay lập tức sau khi Transaction kích hoạt tài khoản đã COMMIT.
// Thao tác này là Non-blocking (không chặn luồng xử lý HTTP request của User).
func (r *LifecycleFactRelay) Notify() {
	if r == nil {
		return
	}
	select {
	case r.wake <- struct{}{}:
	default:
		// Nếu channel đã có sẵn tín hiệu chờ xử lý, không cần gửi thêm; vòng lặp quét DB sẽ xử lý tất cả bản ghi mới.
	}
}

// Start khởi chạy goroutine chạy ngầm của Outbox Relay.
func (r *LifecycleFactRelay) Start() {
	ctx, cancel := context.WithCancel(pkgcontext.WithOperation(context.Background(), "iam.lifecycle_fact_outbox.relay"))
	r.cancel = cancel
	go r.run(ctx)
}

// run là vòng lặp chính (Event Loop) của Relay Worker:
// - Lắng nghe tín hiệu từ `wake` channel hoặc `timer` định kỳ.
// - Xử lý quét cạn (Drain) toàn bộ bản ghi PENDING trong database.
// - Quản lý Exponential Backoff để chống Retry Storm khi PostgreSQL hoặc Redis gặp sự cố.
func (r *LifecycleFactRelay) run(ctx context.Context) {
	defer close(r.done)

	// Khởi tạo timer ban đầu với độ trễ 0s để quét ngay các bản ghi còn tồn đọng khi Pod vừa khởi động (Startup Drain).
	timer := time.NewTimer(0)
	defer timer.Stop()

	retryBackoff := lifecycleFactOutboxRetryMin
	var retryNotBefore time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-timer.C:
		}

		// Ngăn chặn tín hiệu wake liên tục phá vỡ cơ chế Exponential Backoff khi database đang gặp sự cố
		if remaining := time.Until(retryNotBefore); remaining > 0 {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(remaining)
			continue
		}

		// Thực hiện quét và phát các bản ghi outbox
		if err := r.drain(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.SysErrorCtx(ctx, "iam.lifecycle_fact_outbox.claim", err.Error())

			// Tăng thời gian chờ lũy tiến khi có lỗi (Exponential Backoff: 1s -> 2s -> 4s -> ... tối đa 30s)
			retryNotBefore = time.Now().Add(retryBackoff)
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(retryBackoff)

			retryBackoff *= 2
			if retryBackoff > lifecycleFactOutboxRetryMax {
				retryBackoff = lifecycleFactOutboxRetryMax
			}
			continue
		}

		// Reset lại trạng thái backoff khi xử lý thành công
		retryNotBefore = time.Time{}
		retryBackoff = lifecycleFactOutboxRetryMin

		// Thiết lập lại timer định kỳ với Jitter ngẫu nhiên để các Pod không quét DB cùng một thời điểm
		delay := lifecycleFactOutboxFallbackInterval + time.Duration(rand.Int64N(int64(lifecycleFactOutboxFallbackJitter)))
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(delay)
	}
}

// drain thực hiện vòng lặp quét từng đợt (Batch 50 bản ghi) từ PostgreSQL cho đến khi không còn bản ghi nào cần xử lý.
func (r *LifecycleFactRelay) drain(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		// Quét 50 bản ghi PENDING với cơ chế SELECT ... FOR UPDATE SKIP LOCKED
		events, err := r.repo.Claim(ctx, lifecycleFactOutboxClaimBatch)
		if err != nil {
			return err
		}

		// Phát từng sự kiện lên Redis Stream
		for _, event := range events {
			r.publish(ctx, event)
		}

		// Nếu số lượng bản ghi trả về nhỏ hơn kích thước batch, nghĩa là đã xử lý hết hàng đợi
		if len(events) < lifecycleFactOutboxClaimBatch {
			return nil
		}
	}
}

// publish thực hiện đóng gói, kiểm tra hợp đồng dữ liệu, phát sự kiện lên Redis Stream và xác thực hàng rào bền vững (WAITAOF):
// 1. Kiểm tra tính hợp lệ của hợp đồng Protobuf (Validate Event Contract).
// 2. Mở kết nối chuyên dụng (Dedicated Connection) trên cụm Shared Redis.
// 3. Thực thi lệnh `XADD` phát thông điệp lên Stream tương ứng.
// 4. Thực thi lệnh `WAITAOF` bảo đảm dữ liệu đã ghi xuống AOF của Master và đồng bộ sang Replica.
// 5. Cập nhật trạng thái `PUBLISHED` trong PostgreSQL nếu thành công, hoặc `FAILED` / `DEAD` nếu thất bại.
func (r *LifecycleFactRelay) publish(ctx context.Context, event iamEntity.LifecycleFactOutboxEvent) {
	stream, ok := lifecycleFactStreams[event.EventType]
	if !ok {
		_ = r.repo.MarkDead(ctx, event.ID, "unsupported lifecycle fact event type")
		return
	}

	// ============================================================================
	// XÁC MINH HỢP ĐỒNG SỰ KIỆN NGUYÊN TỬ (INLINE EVENT VALIDATION)
	// ============================================================================
	if event.EventID == uuid.Nil || event.OwnerID == uuid.Nil || (event.OwnerType != "PERSONAL" && event.OwnerType != "TENANT") {
		_ = r.repo.MarkDead(ctx, event.ID, "invalid event envelope identifiers")
		return
	}

	validContract := false
	switch event.EventType {
	case personalWalletProvisionRequestedEventType:
		wire := &iamproto.PersonalWalletProvisionRequestedV1{}
		if err := proto.Unmarshal(event.Payload, wire); err == nil {
			wireEventID, eventErr := uuid.FromBytes(wire.GetEventId())
			wireOwnerID, ownerErr := uuid.FromBytes(wire.GetOwnerId())
			_, occurredErr := time.Parse(time.RFC3339Nano, wire.GetOccurredAt())
			if eventErr == nil && ownerErr == nil && occurredErr == nil &&
				wireEventID == event.EventID && wireOwnerID == event.OwnerID &&
				event.OwnerType == "PERSONAL" && wire.GetOwnerType() == "PERSONAL" &&
				wire.GetCurrency() == "USD" && wire.GetSchemaVersion() == 1 {
				validContract = true
			}
		}

	}

	// Nếu hợp đồng dữ liệu không hợp lệ (Poison contract), đánh dấu DEAD ngay lập tức để tránh tốn tài nguyên retry vô hạn
	if !validContract {
		_ = r.repo.MarkDead(ctx, event.ID, "invalid lifecycle fact protobuf payload")
		return
	}

	// ============================================================================
	// PHÁT THÔNG ĐIỆP QUA KẾT NỐI REDIS CHUYÊN DỤNG VÀ ÁP DỤNG WAITAOF
	// ============================================================================
	// WAITAOF chỉ xác nhận các lệnh ghi trước đó trên cùng 1 kết nối TCP (Redis Connection).
	// Do đó, XADD và WAITAOF bắt buộc phải chạy trên cùng một dedicated pooled connection.
	conn := r.sharedRedis.WithTimeout(r.durableWait + time.Second).Conn()
	defer func() { _ = conn.Close() }()

	// 1. Ghi thông điệp vào Redis Stream
	if err := conn.XAdd(ctx, &goredis.XAddArgs{
		Stream: stream,
		Values: map[string]any{
			"event_id":   event.EventID.String(),
			"event_type": event.EventType,
			"payload":    event.Payload,
		},
	}).Err(); err != nil {
		_ = r.repo.MarkFailed(ctx, event.ID, err.Error())
		return
	}

	// 2. Chờ xác nhận ghi đĩa bền vững (AOF Master + Đồng bộ Replicas)
	waitCtx, cancel := context.WithTimeout(ctx, r.durableWait)
	persisted, err := conn.Do(
		waitCtx,
		"WAITAOF",
		1,
		r.replicaAcks,
		r.durableWait.Milliseconds(),
	).Slice()
	cancel()

	if err != nil || len(persisted) != 2 {
		if err == nil {
			err = errors.New("invalid WAITAOF response")
		}
		_ = r.repo.MarkFailed(ctx, event.ID, err.Error())
		return
	}

	localAOF, localOK := persisted[0].(int64)
	replicaAOF, replicaOK := persisted[1].(int64)
	if !localOK || !replicaOK || localAOF < 1 || replicaAOF < int64(r.replicaAcks) {
		// Dữ liệu đã XADD nhưng chưa đạt chính sách bền vững. Giữ outbox ở trạng thái PENDING để retry phát bù;
		// phía Cost Manager Inbox Pattern sẽ đảm nhiệm việc triệt tiêu trùng lặp (Deduplication).
		_ = r.repo.MarkFailed(
			ctx,
			event.ID,
			fmt.Sprintf(
				"Shared Redis durability fence not met: local=%v replicas=%v required=%d",
				persisted[0],
				persisted[1],
				r.replicaAcks,
			),
		)
		return
	}

	// 3. Đánh dấu sự kiện đã phát thành công hoàn tất trong PostgreSQL
	if err := r.repo.MarkPublished(ctx, event.ID); err != nil {
		logger.SysErrorCtx(ctx, "iam.lifecycle_fact_outbox.mark_published", err.Error())
	}
}

// Stop thực hiện dừng an toàn Relay Worker khi ứng dụng tắt (Graceful Shutdown).
func (r *LifecycleFactRelay) Stop() {
	if r == nil || r.cancel == nil {
		return
	}
	r.cancel()
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		logger.SysWarn("iam.lifecycle_fact_outbox.stop", "timed out waiting for lifecycle fact relay")
	}
}
