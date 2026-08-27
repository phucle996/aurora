package hypervisorSvcImpl

import (
	"context"
	"fmt"
	"time"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorRepoInterface "controlplane/internal/hypervisor/domain/repo"
	hypervisorSvcInterface "controlplane/internal/hypervisor/domain/service"
	hypervisorProto "controlplane/internal/hypervisor/transport/proto"

	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const (
	// hypervisorResourcePlanCacheKeyPrefix là tiền tố key Redis lưu cache các cấu hình Resource Plan Revision có hiệu lực.
	hypervisorResourcePlanCacheKeyPrefix = "controlplane:hypervisor:resource-plan:v1"

	// hypervisorResourcePlanCacheTTL là thời gian tồn tại của mỗi bản ghi cache (45 giây),
	// được làm mới định kỳ (mỗi 15 giây) bởi background consumer để đảm bảo cache luôn ấm.
	hypervisorResourcePlanCacheTTL = 45 * time.Second
)

// hypervisorResourcePlanProjectionService quản lý việc lưu trữ bản chiếu (projection) gói tài nguyên
// từ Billing/Cost Manager và đồng bộ vào Redis L2 Cache.
type hypervisorResourcePlanProjectionService struct {
	repo hypervisorRepoInterface.HypervisorResourcePlanProjectionRepository
	rds  *redis.Client
}

// NewHypervisorResourcePlanProjectionService khởi tạo một instance mới của HypervisorResourcePlanProjectionService.
func NewHypervisorResourcePlanProjectionService(
	repo hypervisorRepoInterface.HypervisorResourcePlanProjectionRepository,
	rds *redis.Client,
) hypervisorSvcInterface.HypervisorResourcePlanProjectionService {
	return &hypervisorResourcePlanProjectionService{
		repo: repo,
		rds:  rds,
	}
}

// Apply lưu trữ bản ghi Resource Plan Revision đã được tầng transport xác thực vào database projection.
func (s *hypervisorResourcePlanProjectionService) Apply(
	ctx context.Context,
	command *hypervisorEntity.HypervisorResourcePlanProjectionCommand,
) error {
	return s.repo.Insert(ctx, &hypervisorEntity.HypervisorResourcePlanProjection{
		PlanID:         command.PlanID,
		RevisionID:     command.RevisionID,
		RevisionNumber: command.RevisionNumber,
		Code:           command.Code,
		DisplayName:    command.DisplayName,
		Description:    command.Description,
		BillingModel:   command.BillingModel,
		CPUCores:       command.CPUCores,
		MemoryMIB:      command.MemoryMIB,
		BootDiskGIB:    command.BootDiskGIB,
		ContentSHA256:  command.ContentSHA256,
		EffectiveFrom:  command.EffectiveFrom,
		EffectiveTo:    command.EffectiveTo,
		State:          command.State,
		AllowCreate:    command.AllowedCreate,
		SourceEventID:  command.EventID,
	})
}

// RefreshCache tải toàn bộ các Resource Plan Revision đang có hiệu lực từ database,
// đóng gói dưới dạng Protobuf và ghi đè vào Redis L2 Cache để phục vụ cho các flow tạo VM (fast-path).
func (s *hypervisorResourcePlanProjectionService) RefreshCache(ctx context.Context) error {
	if s.rds == nil {
		return fmt.Errorf("Hypervisor resource plan projection cache: Redis client is unavailable")
	}

	// 1. Lấy danh sách các bản ghi cấu hình đang có hiệu lực từ repository
	projections, err := s.repo.ListEffective(ctx)
	if err != nil {
		return err
	}

	// 2. Duyệt qua từng cấu hình, serialize sang Protobuf và cập nhật vào Redis
	for _, projection := range projections {
		effectiveTo := ""
		if projection.EffectiveTo != nil {
			effectiveTo = projection.EffectiveTo.UTC().Format(time.RFC3339Nano)
		}

		allowedOperations := make([]string, 0, 1)
		if projection.AllowCreate {
			allowedOperations = append(allowedOperations, "CREATE")
		}

		payload, err := proto.Marshal(&hypervisorProto.EffectiveHypervisorResourcePlanV1{
			SchemaVersion:     1,
			EventId:           projection.SourceEventID.String(),
			PlanId:            projection.PlanID[:],
			RevisionId:        projection.RevisionID[:],
			RevisionNumber:    uint64(projection.RevisionNumber),
			Code:              projection.Code,
			DisplayName:       projection.DisplayName,
			Description:       projection.Description,
			BillingModel:      projection.BillingModel,
			CpuCores:          uint64(projection.CPUCores),
			MemoryMib:         uint64(projection.MemoryMIB),
			BootDiskGib:       uint64(projection.BootDiskGIB),
			ContentSha256:     projection.ContentSHA256,
			EffectiveFrom:     projection.EffectiveFrom.UTC().Format(time.RFC3339Nano),
			EffectiveTo:       effectiveTo,
			AllowedOperations: allowedOperations,
			State:             projection.State,
			OccurredAt:        time.Now().UTC().Format(time.RFC3339Nano),
		})
		if err != nil {
			return fmt.Errorf("Hypervisor resource plan projection cache: marshal revision: %w", err)
		}

		// 3. Ghi vào Redis với key định danh duy nhất theo PlanID và RevisionID
		cacheKey := fmt.Sprintf("%s:%s:%s", hypervisorResourcePlanCacheKeyPrefix, projection.PlanID, projection.RevisionID)
		if err := s.rds.Set(ctx, cacheKey, payload, hypervisorResourcePlanCacheTTL).Err(); err != nil {
			return fmt.Errorf("Hypervisor resource plan projection cache: write revision: %w", err)
		}
	}

	return nil
}
