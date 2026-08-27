package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	admissionv1 "cost-manager/api/internal/genproto/billing/admission/v1"
	"cost-manager/api/pkg/logger"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

// ============================================================================
// HẰNG SỐ CẤU HÌNH THỜI GIAN & JITTER CHO RELAY (TỐI ƯU HÓA HA VÀ GIẢM TẢI DB)
// ============================================================================
const (
	// startupJitterMax là độ trễ ngẫu nhiên ban đầu (0s - 5s) khi Pod khởi động,
	// giúp 50 Pods khi deploy k8s rollout cùng lúc không đồng loạt quét DB tại giây 0.
	startupJitterMax = 5 * time.Second

	// coldFallbackInterval là chu kỳ quét dự phòng (Safety Net) khi hệ thống không có tín hiệu đánh thức (Wake).
	// Được nới rộng lên 60s để giảm tối đa tải IOPS cho PostgreSQL lúc nhàn rỗi.
	coldFallbackInterval = 60 * time.Second

	// coldFallbackJitter là khoảng thời gian ngẫu nhiên cộng thêm (0s - 30s) vào chu kỳ Cold.
	// Dao động thực tế giữa các lần quét: 60s -> 90s (làm lệch nhịp hoàn toàn giữa các Pods trong cụm HA).
	coldFallbackJitter = 30 * time.Second

	// batchDrainMinDelay và batchDrainJitter là khoảng nghỉ có Jitter giữa các đợt 100 dòng khi xả tồn đọng lớn,
	// giúp tránh chiếm dụng băng thông I/O và CPU của DB phục vụ các giao dịch người dùng.
	batchDrainMinDelay = 50 * time.Millisecond
	batchDrainJitter   = 50 * time.Millisecond

	// retryBackoffMin và retryBackoffMax giới hạn khoảng thời gian lũy tiến khi gặp sự cố mạng DB/Redis.
	retryBackoffMin = 5 * time.Second
	retryBackoffMax = 60 * time.Second
)

// walletAdmissionStreams định nghĩa 3 Redis Streams phân phối tín hiệu Admission đến từng Resource Engine:
// 1. `billing.commercial.admission.storage.changed.v1`: Mở/khóa quyền tạo và ghi Storage Bucket.
// 2. `billing.commercial.admission.hypervisor.changed.v1`: Mở/khóa quyền cấp phát và khởi chạy VM.
// 3. `billing.commercial.admission.mail.changed.v1`: Mở/khóa quyền gửi nhận Email.
var walletAdmissionStreams = [...]string{
	"billing.commercial.admission.storage.changed.v1",
	"billing.commercial.admission.hypervisor.changed.v1",
	"billing.commercial.admission.mail.changed.v1",
}

// WalletAdmissionOutboxRelay là Background Outbox Dispatcher chịu trách nhiệm:
// - Đảm bảo "At-Least-Once Delivery" và "Zero Event Loss" cho các sự kiện chuyển đổi trạng thái ví.
// - Cơ chế Đánh thức Kép (Dual Wake Mechanism): Nhận tín hiệu kích hoạt tức thời qua `Notify()` kết hợp Safety Net Cold Timer + Jitter.
// - Chống Thundering Herd: Giãn cách chu kỳ quét từ 60s đến 90s kèm Jitter giữa các batch xử lý.
// - Bảo vệ hạ tầng: Áp dụng Exponential Backoff (5s -> 60s) khi PostgreSQL hoặc Redis gặp sự cố.
type WalletAdmissionOutboxRelay struct {
	repo        billingRepoInterface.WalletAdmissionOutboxRepository
	redis       *redis.Client
	relayPolicy entity.WalletAdmissionRelayPolicy
	wake        chan struct{}
}

// NewWalletAdmissionOutboxRelay khởi tạo một instance mới của WalletAdmissionOutboxRelay.
func NewWalletAdmissionOutboxRelay(
	repo billingRepoInterface.WalletAdmissionOutboxRepository,
	sharedRedis *redis.Client,
	relayPolicy entity.WalletAdmissionRelayPolicy,
) *WalletAdmissionOutboxRelay {
	return &WalletAdmissionOutboxRelay{
		repo:        repo,
		redis:       sharedRedis,
		relayPolicy: relayPolicy,
		// Buffer = 1: Tự động gộp (coalesce) nhiều yêu cầu đánh thức dồn dập trong cùng một thời điểm.
		wake: make(chan struct{}, 1),
	}
}

// Notify gửi tín hiệu đánh thức Relay Worker chạy ngay lập tức khi có giao dịch thay đổi ví vừa COMMIT vào DB.
// Thao tác này là Non-blocking (không chặn luồng HTTP/gRPC request của User).
func (r *WalletAdmissionOutboxRelay) Notify() {
	if r == nil {
		return
	}
	select {
	case r.wake <- struct{}{}:
	default:
		// Nếu channel đã có sẵn tín hiệu chờ, không cần gửi thêm.
	}
}

// Run bắt đầu vòng lặp điều phối outbox với cơ chế Dual Wake (Wake-Cold) và Jitter.
func (r *WalletAdmissionOutboxRelay) Run(ctx context.Context) {
	// Khởi tạo timer ban đầu với startup jitter ngẫu nhiên để phân tán các Pods khi khởi động
	initialDelay := time.Duration(rand.Int64N(int64(startupJitterMax)))
	timer := time.NewTimer(initialDelay)
	defer timer.Stop()

	retryBackoff := retryBackoffMin
	var retryNotBefore time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-timer.C:
		}

		// Nếu đang trong thời gian phạt lỗi (Backoff), tiếp tục chờ cho đến khi hết hạn phạt
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

		// Thực hiện xả cạn các bản ghi Outbox đang chờ xử lý trong DB
		if err := r.drain(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.SysError("billing.commercial.admission.relay", err.Error())

			// Tăng thời gian chờ phạt lũy tiến (Exponential Backoff: 5s -> 10s -> 20s -> ... -> 60s)
			retryNotBefore = time.Now().Add(retryBackoff)
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(retryBackoff)

			retryBackoff *= 2
			if retryBackoff > retryBackoffMax {
				retryBackoff = retryBackoffMax
			}
			continue
		}

		// Xử lý thành công -> Reset trạng thái phạt
		retryNotBefore = time.Time{}
		retryBackoff = retryBackoffMin

		// Thiết lập lại Cold Safety Net Timer với Full Jitter (60s + 0..30s)
		coldDelay := coldFallbackInterval + time.Duration(rand.Int64N(int64(coldFallbackJitter)))
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(coldDelay)
	}
}

// drain thực hiện vòng lặp quét từng batch (100 rows) từ PostgreSQL cho đến khi không còn bản ghi nào cần xử lý.
func (r *WalletAdmissionOutboxRelay) drain(ctx context.Context) error {
	claimToken := uuid.New()
	for {
		// 1. Claim batch 100 rows chưa publish bằng CTE FOR UPDATE SKIP LOCKED
		batch, err := r.repo.ClaimUnpublishedWalletAdmissionBatch(ctx, 100, claimToken)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			// Đã xử lý hết sạch các bản ghi tồn đọng
			return nil
		}

		// 2. Phát tán từng bản ghi trong batch
		for _, row := range batch {
			if err := r.publishRow(ctx, row); err != nil {
				return err
			}
		}

		// 3. Nếu còn bản ghi để tiếp tục batch sau, nghỉ một khoảng ngắn có Jitter (50ms - 100ms) để nhường I/O cho DB
		if len(batch) == 100 {
			pause := batchDrainMinDelay + time.Duration(rand.Int64N(int64(batchDrainJitter)))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(pause):
			}
		}
	}
}

// publishRow thực hiện tuần tự: Serialization -> XADD 3 Streams -> WAITAOF -> Mark Published.
func (r *WalletAdmissionOutboxRelay) publishRow(ctx context.Context, row *entity.WalletAdmissionOutboxRow) error {
	restrictionReason := ""
	if row.RestrictionReason != nil {
		restrictionReason = *row.RestrictionReason
	}
	validUntil := ""
	if row.ValidUntil != nil {
		validUntil = row.ValidUntil.UTC().Format(time.RFC3339Nano)
	}

	// 1. Đóng gói Protobuf Envelope
	payload, err := proto.Marshal(&admissionv1.CommercialAdmissionChangedV1{
		EventId:           row.EventID.String(),
		OwnerId:           row.OwnerID.String(),
		OwnerType:         string(row.OwnerType),
		PolicyVersion:     row.WalletVersion,
		Decision:          row.AdmissionMode,
		RestrictionReason: restrictionReason,
		EffectiveAt:       row.EffectiveAt.UTC().Format(time.RFC3339Nano),
		ValidUntil:        validUntil,
	})
	if err != nil {
		_ = r.repo.RecordWalletAdmissionError(ctx, row.EventID, row.ClaimToken, err.Error())
		return err
	}

	// 2. Mở kết nối chuyên dụng để thực thi XADD và WAITAOF trên cùng 1 pipeline connection
	connection := r.redis.Conn()
	defer connection.Close()

	// 3. XADD đồng thời vào 3 stream của Storage, Hypervisor, Mail
	for _, stream := range walletAdmissionStreams {
		if addErr := connection.XAdd(ctx, &redis.XAddArgs{
			Stream: stream,
			Values: map[string]any{
				"event_id": row.EventID.String(),
				"payload":  payload,
			},
		}).Err(); addErr != nil {
			_ = r.repo.RecordWalletAdmissionError(ctx, row.EventID, row.ClaimToken, addErr.Error())
			return addErr
		}
	}

	// 4. Redis AOF Durability Fence: local AOF is mandatory; replica acknowledgements follow this workflow's deployment policy.
	aofAcks, aofErr := connection.Do(ctx, "WAITAOF", 1, r.relayPolicy.ReplicaAcks, r.relayPolicy.DurableWait.Milliseconds()).Int64Slice()
	if aofErr != nil {
		_ = r.repo.RecordWalletAdmissionError(ctx, row.EventID, row.ClaimToken, aofErr.Error())
		return aofErr
	}
	if len(aofAcks) != 2 || aofAcks[0] < 1 || aofAcks[1] < int64(r.relayPolicy.ReplicaAcks) {
		durabilityErr := fmt.Sprintf("Redis admission durability fence not met: %v", aofAcks)
		_ = r.repo.RecordWalletAdmissionError(ctx, row.EventID, row.ClaimToken, durabilityErr)
		return errors.New(durabilityErr)
	}

	// 5. Đánh dấu bản ghi đã publish thành công trong PostgreSQL
	if markErr := r.repo.MarkWalletAdmissionPublished(ctx, row.EventID, row.ClaimToken); markErr != nil {
		logger.SysError("billing.commercial.admission.mark", fmt.Sprintf("%s: %v", row.EventID, markErr))
		return markErr
	}

	return nil
}
