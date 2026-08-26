package hypervisorStream

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorSvcInterface "controlplane/internal/hypervisor/domain/service"
	hypervisorProto "controlplane/internal/hypervisor/transport/proto"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const (
	// hypervisorCommercialAdmissionStream là Redis Stream nhận sự kiện thay đổi quyền hạn thương mại từ Cost Manager.
	hypervisorCommercialAdmissionStream = "billing.commercial.admission.hypervisor.changed.v1"

	// hypervisorCommercialAdmissionGroup là tên Consumer Group của phân hệ Hypervisor Controlplane.
	hypervisorCommercialAdmissionGroup = "controlplane-hypervisor-commercial-admission-v1"

	// claimMinIdleTime là ngưỡng thời gian tối thiểu một message bị pending trước khi được AutoClaim thu hồi xử lý lại.
	claimMinIdleTime = 30 * time.Second

	// readBatchSize là số lượng bản ghi tối đa đọc trong mỗi chu kỳ XReadGroup / XAutoClaim.
	readBatchSize = 64

	// readBlockTimeout là thời gian block tối đa khi chờ đợi message mới từ Redis Stream.
	readBlockTimeout = 5 * time.Second

	// maxPayloadSizeBytes là dung lượng tối đa cho phép của một protobuf payload (64KB).
	maxPayloadSizeBytes = 64 * 1024
)

// CommercialAdmissionProjectionConsumer lắng nghe và đồng bộ trạng thái quyền hạn thương mại (Commercial Admission)
// của các chủ sở hữu tài nguyên (Personal / Tenant) vào cơ sở dữ liệu nội bộ của Hypervisor.
type CommercialAdmissionProjectionConsumer struct {
	rds     *goredis.Client
	service hypervisorSvcInterface.CommercialAdmissionProjectionService
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// NewCommercialAdmissionProjectionConsumer khởi tạo một instance mới của CommercialAdmissionProjectionConsumer.
func NewCommercialAdmissionProjectionConsumer(
	rds *goredis.Client,
	service hypervisorSvcInterface.CommercialAdmissionProjectionService,
) *CommercialAdmissionProjectionConsumer {
	return &CommercialAdmissionProjectionConsumer{
		rds:     rds,
		service: service,
		cancel:  func() {},
	}
}

// Start khởi tạo Consumer Group trên Redis Stream (nếu chưa có) và khởi chạy background worker goroutine.
func (c *CommercialAdmissionProjectionConsumer) Start() error {
	ctx, cancel := context.WithCancel(context.Background())

	// Tạo Consumer Group từ vị trí ban đầu "0" để không bỏ sót bất kỳ message nào.
	// Bỏ qua lỗi BUSYGROUP nếu group đã tồn tại từ các lần chạy trước đó.
	err := c.rds.XGroupCreateMkStream(
		ctx,
		hypervisorCommercialAdmissionStream,
		hypervisorCommercialAdmissionGroup,
		"0",
	).Err()

	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		cancel()
		return fmt.Errorf("create Hypervisor commercial admission consumer group: %w", err)
	}

	c.cancel = cancel
	c.wg.Add(1)

	go func() {
		defer c.wg.Done()
		c.run(ctx)
	}()

	return nil
}

// Stop dừng consumer worker một cách an toàn và chờ goroutine hoàn tất xử lý.
func (c *CommercialAdmissionProjectionConsumer) Stop() {
	c.cancel()
	c.wg.Wait()
}

// run duy trì vòng lặp tiêu thụ message từ Redis Stream với cơ chế AutoClaim để tự động phục hồi khi Pod bị crash.
func (c *CommercialAdmissionProjectionConsumer) run(ctx context.Context) {
	consumerID := "hypervisor-admission-" + uuid.NewString()

	for ctx.Err() == nil {
		// 1. Phục hồi các messages bị pending quá lâu do các worker khác gặp sự cố (Crash Recovery via XAutoClaim)
		claimedMessages, _, claimErr := c.rds.XAutoClaim(ctx, &goredis.XAutoClaimArgs{
			Stream:   hypervisorCommercialAdmissionStream,
			Group:    hypervisorCommercialAdmissionGroup,
			Consumer: consumerID,
			MinIdle:  claimMinIdleTime,
			Start:    "0-0",
			Count:    readBatchSize,
		}).Result()

		if claimErr == nil && len(claimedMessages) > 0 {
			c.processBatch(ctx, claimedMessages)
			continue
		}

		// 2. Lắng nghe các messages mới phát sinh trong Stream (XReadGroup với id ">")
		streams, err := c.rds.XReadGroup(ctx, &goredis.XReadGroupArgs{
			Group:    hypervisorCommercialAdmissionGroup,
			Consumer: consumerID,
			Streams:  []string{hypervisorCommercialAdmissionStream, ">"},
			Count:    readBatchSize,
			Block:    readBlockTimeout,
		}).Result()

		if err != nil {
			if errors.Is(err, goredis.Nil) || ctx.Err() != nil {
				continue
			}
			logger.SysWarn("hypervisor.commercial_admission.read", err.Error())
			continue
		}

		// 3. Xử lý toàn bộ messages nhận được từ stream
		for _, stream := range streams {
			c.processBatch(ctx, stream.Messages)
		}
	}
}

// processBatch lặp qua từng message trong batch để giải mã, xác thực và thực thi projection.
func (c *CommercialAdmissionProjectionConsumer) processBatch(ctx context.Context, messages []goredis.XMessage) {
	for _, message := range messages {
		c.processMessage(ctx, message)
	}
}

// processMessage giải mã Protobuf, kiểm tra tính toàn vẹn và đẩy vào Domain Service.
func (c *CommercialAdmissionProjectionConsumer) processMessage(ctx context.Context, message goredis.XMessage) {
	// 1. Trích xuất event_id từ Redis Stream envelope
	var envelopeEventID string
	switch value := message.Values["event_id"].(type) {
	case string:
		envelopeEventID = value
	case []byte:
		envelopeEventID = string(value)
	}

	// 2. Trích xuất protobuf payload thô
	var payload []byte
	switch value := message.Values["payload"].(type) {
	case string:
		payload = []byte(value)
	case []byte:
		payload = value
	}

	// 3. Kiểm tra payload cơ bản và giải mã Protobuf
	if len(payload) == 0 || len(payload) > maxPayloadSizeBytes {
		logger.SysWarn("hypervisor.commercial_admission.invalid", "commercial admission payload size invalid")
		c.ack(ctx, message.ID)
		return
	}

	var event hypervisorProto.CommercialAdmissionChangedV1
	if err := proto.Unmarshal(payload, &event); err != nil {
		logger.SysWarn("hypervisor.commercial_admission.invalid", fmt.Sprintf("failed to unmarshal proto: %v", err))
		c.ack(ctx, message.ID)
		return
	}

	// 4. Xác thực tính hợp lệ của các định danh UUID
	envelopeID, envelopeErr := uuid.Parse(envelopeEventID)
	if envelopeErr != nil || envelopeID == uuid.Nil {
		logger.SysWarn("hypervisor.commercial_admission.invalid", "envelope event_id is invalid")
		c.ack(ctx, message.ID)
		return
	}

	eventID, eventErr := uuid.Parse(event.EventId)
	if eventErr != nil || eventID == uuid.Nil {
		logger.SysWarn("hypervisor.commercial_admission.invalid", "proto event_id is invalid")
		c.ack(ctx, message.ID)
		return
	}

	if envelopeID != eventID {
		logger.SysWarn("hypervisor.commercial_admission.invalid", "envelope event_id does not match proto event_id")
		c.ack(ctx, message.ID)
		return
	}

	ownerID, ownerErr := uuid.Parse(event.OwnerId)
	if ownerErr != nil || ownerID == uuid.Nil {
		logger.SysWarn("hypervisor.commercial_admission.invalid", "owner_id is invalid")
		c.ack(ctx, message.ID)
		return
	}

	// 5. Kiểm tra các ràng buộc kỹ thuật và bất biến nghiệp vụ (Business Invariant Checks)
	// - PolicyVersion > 0
	// - OwnerType phải là PERSONAL hoặc TENANT
	// - Decision phải là ALLOW hoặc SUSPEND_BILLABLE
	// - Decision == ALLOW: RestrictionReason bắt buộc phải rỗng
	// - Decision == SUSPEND_BILLABLE: RestrictionReason bắt buộc không được rỗng
	if event.PolicyVersion <= 0 ||
		(event.OwnerType != "PERSONAL" && event.OwnerType != "TENANT") ||
		(event.Decision != "ALLOW" && event.Decision != "SUSPEND_BILLABLE") ||
		(event.Decision == "ALLOW" && event.RestrictionReason != "") ||
		(event.Decision == "SUSPEND_BILLABLE" && strings.TrimSpace(event.RestrictionReason) == "") {
		logger.SysWarn("hypervisor.commercial_admission.invalid", "commercial admission business invariants invalid")
		c.ack(ctx, message.ID)
		return
	}

	// 6. Chuyển đổi và chuẩn hóa mốc thời gian sang UTC
	effectiveAt, effectiveErr := time.Parse(time.RFC3339Nano, event.EffectiveAt)
	if effectiveErr != nil {
		logger.SysWarn("hypervisor.commercial_admission.invalid", "effective_at timestamp is invalid")
		c.ack(ctx, message.ID)
		return
	}
	effectiveAt = effectiveAt.UTC()

	var validUntil *time.Time
	if event.ValidUntil != "" {
		parsed, validUntilErr := time.Parse(time.RFC3339Nano, event.ValidUntil)
		if validUntilErr != nil {
			logger.SysWarn("hypervisor.commercial_admission.invalid", "valid_until timestamp is invalid")
			c.ack(ctx, message.ID)
			return
		}
		utcTime := parsed.UTC()
		validUntil = &utcTime
	}

	// ValidUntil phải sau EffectiveAt nếu được thiết lập
	if validUntil != nil && !validUntil.After(effectiveAt) {
		logger.SysWarn("hypervisor.commercial_admission.invalid", "valid_until must be after effective_at")
		c.ack(ctx, message.ID)
		return
	}

	// 7. Thực thi cập nhật quyền hạn vào Hypervisor Service
	err := c.service.Apply(ctx, &hypervisorEntity.CommercialAdmissionProjectionCommand{
		EventID:           eventID,
		OwnerID:           ownerID,
		OwnerType:         event.OwnerType,
		PolicyVersion:     event.PolicyVersion,
		Decision:          event.Decision,
		RestrictionReason: event.RestrictionReason,
		EffectiveAt:       effectiveAt,
		ValidUntil:        validUntil,
	})

	if err != nil {
		// Lỗi tạm thời (DB timeout, connection error) -> Không ACK để chu kỳ sau hoặc AutoClaim thử lại
		logger.SysError("hypervisor.commercial_admission.apply", fmt.Sprintf("transient error applying projection: %v", err))
		return
	}

	// 8. Xử lý thành công -> Xác nhận và dọn dẹp message khỏi Stream
	c.ack(ctx, message.ID)
}

// ack thực hiện pipeline XACK và XDEL nguyên tử để xác nhận hoàn tất và giải phóng bộ nhớ RAM trên Redis.
func (c *CommercialAdmissionProjectionConsumer) ack(ctx context.Context, messageID string) {
	_, _ = c.rds.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
		pipe.XAck(ctx, hypervisorCommercialAdmissionStream, hypervisorCommercialAdmissionGroup, messageID)
		pipe.XDel(ctx, hypervisorCommercialAdmissionStream, messageID)
		return nil
	})
}
