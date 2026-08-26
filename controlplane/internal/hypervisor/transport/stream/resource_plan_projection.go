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
	// hypervisorResourcePlanStreamName là tên Redis Stream chứa các sự kiện thay đổi cấu hình gói tài nguyên VM (Resource Plan) từ Cost Manager.
	hypervisorResourcePlanStreamName = "billing.hypervisor.resource-plan.changed.v1"

	// hypervisorResourcePlanStreamGroup là tên Consumer Group riêng biệt cho Hypervisor Controlplane.
	hypervisorResourcePlanStreamGroup = "controlplane-hypervisor-resource-plan-v1"

	// resourcePlanClaimMinIdle là thời gian tối thiểu một message bị pending trước khi được AutoClaim thu hồi xử lý lại.
	resourcePlanClaimMinIdle = 30 * time.Second

	// resourcePlanReadBatchSize là số lượng bản ghi tối đa đọc trong mỗi chu kỳ.
	resourcePlanReadBatchSize = 64

	// resourcePlanReadBlockTimeout là thời gian block tối đa khi chờ đợi message mới từ Redis Stream.
	resourcePlanReadBlockTimeout = 5 * time.Second

	// resourcePlanMaxPayloadBytes là dung lượng tối đa cho phép của một protobuf payload (64KB).
	resourcePlanMaxPayloadBytes = 64 * 1024
)

// ResourcePlanProjectionConsumer tiêu thụ luồng sự kiện phiên bản cấu hình gói tài nguyên máy ảo (Resource Plan Revision)
// được phát hành từ Cost Manager và cập nhật vào bảng chiếu cục bộ của Hypervisor Controlplane.
type ResourcePlanProjectionConsumer struct {
	rds     *goredis.Client
	service hypervisorSvcInterface.HypervisorResourcePlanProjectionService
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// NewResourcePlanProjectionConsumer khởi tạo một instance mới của ResourcePlanProjectionConsumer.
func NewResourcePlanProjectionConsumer(
	rds *goredis.Client,
	service hypervisorSvcInterface.HypervisorResourcePlanProjectionService,
) *ResourcePlanProjectionConsumer {
	return &ResourcePlanProjectionConsumer{
		rds:     rds,
		service: service,
		cancel:  func() {},
	}
}

// Start khởi tạo Consumer Group trên Redis Stream và bắt đầu vòng lặp tiêu thụ sự kiện trong goroutine riêng.
func (c *ResourcePlanProjectionConsumer) Start() error {
	ctx, cancel := context.WithCancel(context.Background())

	// Khởi tạo Consumer Group bắt đầu từ offset "0" để không bỏ lỡ cấu hình gói tài nguyên ban đầu.
	err := c.rds.XGroupCreateMkStream(
		ctx,
		hypervisorResourcePlanStreamName,
		hypervisorResourcePlanStreamGroup,
		"0",
	).Err()

	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		cancel()
		return fmt.Errorf("create Hypervisor resource plan consumer group: %w", err)
	}

	c.cancel = cancel
	c.wg.Add(2)

	go func() {
		defer c.wg.Done()
		c.run(ctx)
	}()
	go func() {
		defer c.wg.Done()
		c.runCacheRefresh(ctx)
	}()

	return nil
}

func (c *ResourcePlanProjectionConsumer) runCacheRefresh(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if err := c.service.RefreshCache(ctx); err != nil && ctx.Err() == nil {
			logger.SysWarn("hypervisor.resource_plan.cache.refresh", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Stop dừng consumer an toàn và chờ background goroutine hoàn tất xử lý.
func (c *ResourcePlanProjectionConsumer) Stop() {
	c.cancel()
	c.wg.Wait()
}

// run duy trì vòng lặp tiêu thụ sự kiện với 2 giai đoạn: AutoClaim phục hồi sự cố và XReadGroup đọc message mới.
func (c *ResourcePlanProjectionConsumer) run(ctx context.Context) {
	consumerID := "hypervisor-resource-plan-" + uuid.NewString()

	for ctx.Err() == nil {
		// 1. Giai đoạn phục hồi sự cố (Crash Recovery): AutoClaim các message bị treo quá 30 giây trong PEL
		claimed, _, claimErr := c.rds.XAutoClaim(ctx, &goredis.XAutoClaimArgs{
			Stream:   hypervisorResourcePlanStreamName,
			Group:    hypervisorResourcePlanStreamGroup,
			Consumer: consumerID,
			MinIdle:  resourcePlanClaimMinIdle,
			Start:    "0-0",
			Count:    resourcePlanReadBatchSize,
		}).Result()

		if claimErr == nil && len(claimed) > 0 {
			c.processBatch(ctx, claimed)
			continue
		}

		// 2. Giai đoạn đọc thông thường: Lắng nghe các messages mới phát sinh trong Stream
		streams, err := c.rds.XReadGroup(ctx, &goredis.XReadGroupArgs{
			Group:    hypervisorResourcePlanStreamGroup,
			Consumer: consumerID,
			Streams:  []string{hypervisorResourcePlanStreamName, ">"},
			Count:    resourcePlanReadBatchSize,
			Block:    resourcePlanReadBlockTimeout,
		}).Result()

		if err != nil {
			if !errors.Is(err, goredis.Nil) && ctx.Err() == nil {
				logger.SysWarn("hypervisor.resource_plan.read", err.Error())
			}
			continue
		}

		for _, stream := range streams {
			c.processBatch(ctx, stream.Messages)
		}
	}
}

// processBatch xử lý danh sách các message nhận được trong batch.
func (c *ResourcePlanProjectionConsumer) processBatch(ctx context.Context, messages []goredis.XMessage) {
	for _, message := range messages {
		c.processMessage(ctx, message)
	}
}

// processMessage giải mã Protobuf, kiểm tra tính toàn vẹn và cập nhật vào Domain Service.
func (c *ResourcePlanProjectionConsumer) processMessage(ctx context.Context, message goredis.XMessage) {
	// 1. Trích xuất event_id từ envelope
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

	// 3. Kiểm tra kích thước payload
	if len(payload) == 0 || len(payload) > resourcePlanMaxPayloadBytes {
		logger.SysWarn("hypervisor.resource_plan.invalid", "resource plan payload size invalid")
		c.ack(ctx, message.ID)
		return
	}

	// 4. Giải mã Protobuf payload
	var event hypervisorProto.EffectiveHypervisorResourcePlanV1
	if err := proto.Unmarshal(payload, &event); err != nil {
		logger.SysWarn("hypervisor.resource_plan.invalid", fmt.Sprintf("resource plan payload is not protobuf: %v", err))
		c.ack(ctx, message.ID)
		return
	}

	// 5. Xác thực các định danh UUID
	envelopeID, envelopeErr := uuid.Parse(envelopeEventID)
	eventID, eventErr := uuid.Parse(event.EventId)
	planID, planErr := uuid.FromBytes(event.PlanId)
	revisionID, revisionErr := uuid.FromBytes(event.RevisionId)

	if envelopeErr != nil || eventErr != nil || planErr != nil || revisionErr != nil ||
		envelopeID == uuid.Nil || eventID == uuid.Nil || planID == uuid.Nil || revisionID == uuid.Nil ||
		envelopeID != eventID {
		logger.SysWarn("hypervisor.resource_plan.invalid", "resource plan envelope or UUID fields are invalid")
		c.ack(ctx, message.ID)
		return
	}

	// 6. Kiểm tra các ràng buộc kỹ thuật, nghiệp vụ và giới hạn biên phần cứng (Hardware Boundaries)
	// - SchemaVersion: Bắt buộc là 1
	// - RevisionNumber: Phải lớn hơn 0
	// - ContentSha256: Đúng 32 bytes (SHA-256 binary)
	// - BillingModel: Bắt buộc là "LIMIT_HOURLY"
	// - Code và DisplayName: Không được để trống
	// - vCPU: 1 - 1024 cores
	// - Memory: 1 MiB - 4 TiB (4,194,304 MiB)
	// - BootDisk: 1 GiB - 1 PiB (1,048,576 GiB)
	if event.SchemaVersion != 1 ||
		event.RevisionNumber == 0 ||
		len(event.ContentSha256) != 32 ||
		event.BillingModel != "LIMIT_HOURLY" ||
		(event.State != "ACTIVE" && event.State != "RETIRED") ||
		strings.TrimSpace(event.Code) == "" ||
		strings.TrimSpace(event.DisplayName) == "" ||
		event.CpuCores == 0 || event.CpuCores > 1024 ||
		event.MemoryMib == 0 || event.MemoryMib > 4_194_304 ||
		event.BootDiskGib == 0 || event.BootDiskGib > 1_048_576 {
		logger.SysWarn("hypervisor.resource_plan.invalid", "resource plan hardware or contract bounds invalid")
		c.ack(ctx, message.ID)
		return
	}

	// 7. Chuyển đổi và chuẩn hóa mốc thời gian sang UTC
	effectiveFrom, effectiveErr := time.Parse(time.RFC3339Nano, event.EffectiveFrom)
	if effectiveErr != nil {
		logger.SysWarn("hypervisor.resource_plan.invalid", "resource plan effective_from is invalid")
		c.ack(ctx, message.ID)
		return
	}

	var effectiveTo *time.Time
	if event.EffectiveTo != "" {
		parsed, err := time.Parse(time.RFC3339Nano, event.EffectiveTo)
		if err != nil {
			logger.SysWarn("hypervisor.resource_plan.invalid", "resource plan effective_to is invalid")
			c.ack(ctx, message.ID)
			return
		}
		utcValue := parsed.UTC()
		effectiveTo = &utcValue
	}

	// Kiểm tra tính hợp lệ của khoảng thời gian (EffectiveTo phải sau EffectiveFrom)
	if effectiveTo != nil && !effectiveTo.After(effectiveFrom) {
		logger.SysWarn("hypervisor.resource_plan.invalid", "resource plan effective_to must be after effective_from")
		c.ack(ctx, message.ID)
		return
	}

	// 8. Chuyển allowed_operations thành policy flat của Hypervisor projection.
	// An inactive plan or a revision without CREATE is a valid Cost decision;
	// it must reach the durable CTE instead of being silently dropped.
	allowedCreate := false
	for _, operation := range event.AllowedOperations {
		if operation == "CREATE" {
			allowedCreate = true
			break
		}
	}
	// 9. Thực thi lệnh cập nhật bản chiếu gói tài nguyên vào Service
	err := c.service.Apply(ctx, &hypervisorEntity.HypervisorResourcePlanProjectionCommand{
		EventID:        eventID,
		PlanID:         planID,
		RevisionID:     revisionID,
		RevisionNumber: int64(event.RevisionNumber),
		Code:           event.Code,
		DisplayName:    event.DisplayName,
		Description:    event.Description,
		BillingModel:   event.BillingModel,
		CPUCores:       int32(event.CpuCores),
		MemoryMIB:      int64(event.MemoryMib),
		BootDiskGIB:    int64(event.BootDiskGib),
		ContentSHA256:  event.ContentSha256,
		EffectiveFrom:  effectiveFrom.UTC(),
		EffectiveTo:    effectiveTo,
		State:          event.State,
		AllowedCreate:  allowedCreate,
	})

	if err != nil {
		// Lỗi hệ thống tạm thời (DB connection / timeout) -> Không ACK để tự động thử lại
		logger.SysError("hypervisor.resource_plan.apply", fmt.Sprintf("transient error applying resource plan: %v", err))
		return
	}

	if err := c.service.RefreshCache(ctx); err != nil && ctx.Err() == nil {
		logger.SysWarn("hypervisor.resource_plan.cache.refresh", err.Error())
	}

	// 10. Hoàn tất thành công -> Xác nhận và dọn dẹp message khỏi Stream
	c.ack(ctx, message.ID)
}

// ack thực thi atomic pipelined XACK và XDEL để giải phóng RAM trên Redis.
func (c *ResourcePlanProjectionConsumer) ack(ctx context.Context, messageID string) {
	_, _ = c.rds.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
		pipe.XAck(ctx, hypervisorResourcePlanStreamName, hypervisorResourcePlanStreamGroup, messageID)
		pipe.XDel(ctx, hypervisorResourcePlanStreamName, messageID)
		return nil
	})
}
