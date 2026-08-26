package hypervisorSvcImpl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"time"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorRepoInterface "controlplane/internal/hypervisor/domain/repo"
	hypervisorSvcInterface "controlplane/internal/hypervisor/domain/service"
	hypervisorTaxonomy "controlplane/internal/hypervisor/taxonomy"
	hypervisorproto "controlplane/internal/hypervisor/transport/proto"
	"controlplane/internal/observability"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

const personalVMResourcePlanCacheKeyPrefix = "controlplane:hypervisor:resource-plan:v1"

// PersonalVMServiceImpl triển khai PersonalVMService quản lý vòng đời máy ảo cá nhân (Personal VM).
// Kết hợp giữa Fast-path L2 Cache (Redis) để dựng cấu hình mong muốn và Durable CTE (PostgreSQL) để thẩm định quyền thương mại và ghi Outbox.
type PersonalVMServiceImpl struct {
	repo    hypervisorRepoInterface.PersonalVMRepository
	rds     *redis.Client
	metrics observability.WorkflowRecorder
}

// NewPersonalVMService khởi tạo một instance mới của PersonalVMServiceImpl.
func NewPersonalVMService(
	repo hypervisorRepoInterface.PersonalVMRepository,
	rds *redis.Client,
	metrics observability.WorkflowRecorder,
) hypervisorSvcInterface.PersonalVMService {
	return &PersonalVMServiceImpl{
		repo:    repo,
		rds:     rds,
		metrics: metrics,
	}
}

// Create thực hiện quy trình khởi tạo máy ảo cá nhân:
// 1. Đọc và thẩm định cấu hình gói tài nguyên từ Redis L2 Cache (Fast-path).
// 2. Tra cứu Image khả dụng trong Zone.
// 3. Tính toán spec_hash chống xung đột cấu hình.
// 4. Đóng gói Protobuf payload và ghi Outbox qua giao dịch CTE nguyên tử.
func (s *PersonalVMServiceImpl) Create(
	ctx context.Context,
	input *hypervisorEntity.CreatePersonalVM,
) (out *hypervisorEntity.PersonalVMCreateResult, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, hypervisorTaxonomy.ErrNameConflict):
			result, reason = observability.ResultRejected, observability.ReasonAlreadyExists
		case errors.Is(err, hypervisorTaxonomy.ErrImageNotFound), errors.Is(err, hypervisorTaxonomy.ErrNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, hypervisorTaxonomy.ErrImageStateConflict), errors.Is(err, hypervisorTaxonomy.ErrScopeUnavailable):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		case errors.Is(err, hypervisorTaxonomy.ErrCommercialAdmissionDenied):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		case errors.Is(err, hypervisorTaxonomy.ErrResourcePlanCacheUnavailable):
			result, reason = observability.ResultFailure, observability.ReasonUnavailable
		case errors.Is(err, hypervisorTaxonomy.ErrResourcePlanUnavailable):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	// [COMMENT]: 1. Đọc cấu hình Resource Plan từ Redis L2 Cache (Fast-path)

	cacheKey := personalVMResourcePlanCacheKeyPrefix + ":" + input.ResourcePlanID.String() + ":" + input.ResourcePlanRevisionID.String()
	payload, err := s.rds.Get(ctx, cacheKey).Bytes()
	if err != nil {
		return nil, hypervisorTaxonomy.ErrResourcePlanCacheUnavailable
	}

	var resourcePlan hypervisorproto.EffectiveHypervisorResourcePlanV1
	if err := proto.Unmarshal(payload, &resourcePlan); err != nil {
		return nil, hypervisorTaxonomy.ErrResourcePlanCacheUnavailable
	}

	// [COMMENT]: 2. Xác thực tính toàn vẹn và hợp lệ của Resource Plan trong Cache
	planID, planErr := uuid.FromBytes(resourcePlan.PlanId)
	revisionID, revisionErr := uuid.FromBytes(resourcePlan.RevisionId)
	effectiveFrom, effectiveFromErr := time.Parse(time.RFC3339Nano, resourcePlan.EffectiveFrom)
	if planErr != nil || revisionErr != nil || effectiveFromErr != nil {
		return nil, hypervisorTaxonomy.ErrResourcePlanCacheUnavailable
	}

	var effectiveTo time.Time
	if resourcePlan.EffectiveTo != "" {
		var effectiveToErr error
		effectiveTo, effectiveToErr = time.Parse(time.RFC3339Nano, resourcePlan.EffectiveTo)
		if effectiveToErr != nil {
			return nil, hypervisorTaxonomy.ErrResourcePlanCacheUnavailable
		}
	}

	now := time.Now().UTC()
	if resourcePlan.SchemaVersion != 1 ||
		planID != input.ResourcePlanID ||
		revisionID != input.ResourcePlanRevisionID ||
		resourcePlan.RevisionNumber == 0 ||
		resourcePlan.RevisionNumber > 9_223_372_036_854_775_807 ||
		resourcePlan.BillingModel != "LIMIT_HOURLY" ||
		resourcePlan.State != "ACTIVE" ||
		resourcePlan.CpuCores == 0 || resourcePlan.CpuCores > 1024 ||
		resourcePlan.MemoryMib == 0 || resourcePlan.MemoryMib > 4_194_304 ||
		resourcePlan.BootDiskGib == 0 || resourcePlan.BootDiskGib > 1_048_576 ||
		len(resourcePlan.ContentSha256) != sha256.Size ||
		effectiveFrom.UTC().After(now) ||
		(resourcePlan.EffectiveTo != "" && !effectiveTo.UTC().After(now)) {
		return nil, hypervisorTaxonomy.ErrResourcePlanCacheUnavailable
	}

	// [COMMENT]: 3. Kiểm tra cờ cho phép tạo mới máy ảo (CREATE)
	allowedCreate := false
	for _, operation := range resourcePlan.AllowedOperations {
		if operation == "CREATE" {
			allowedCreate = true
			break
		}
	}
	if !allowedCreate {
		return nil, hypervisorTaxonomy.ErrResourcePlanCacheUnavailable
	}

	// [COMMENT]: 4. Tính toán dung lượng ổ đĩa và cấu hình đĩa bổ sung (Additional Disks)
	cpuCores := int32(resourcePlan.CpuCores)
	memoryMB := int64(resourcePlan.MemoryMib)
	bootDiskGB := int64(resourcePlan.BootDiskGib)

	additionalDiskSizes := make([]int64, 0, len(input.AdditionalDisks))
	totalDiskGB := bootDiskGB
	protoDisks := make([]*hypervisorproto.VmCreateAdditionalDiskV1, 0, len(input.AdditionalDisks))
	for _, disk := range input.AdditionalDisks {
		totalDiskGB += disk.SizeGB
		if totalDiskGB > 65536 {
			return nil, hypervisorTaxonomy.ErrResourcePlanUnavailable
		}
		additionalDiskSizes = append(additionalDiskSizes, disk.SizeGB)
		protoDisks = append(protoDisks, &hypervisorproto.VmCreateAdditionalDiskV1{
			DiskIndex: uint32(disk.DiskIndex),
			SizeGb:    uint64(disk.SizeGB),
		})
	}

	// [COMMENT]: 5. Tra cứu Image khả dụng trong Zone
	image, err := s.repo.GetAvailableImage(ctx, input.ImageID, input.ZoneID)
	if err != nil {
		return nil, err
	}

	// [COMMENT]: 6. Sinh UUIDv7 cho VMID và OperationID
	vmID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	operationID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}

	// [COMMENT]: 7. Tính toán mã băm thông số kỹ thuật (SpecHash) nhị phân cố định
	spec := sha256.New()
	spec.Write(image.ID[:])
	var number [8]byte
	binary.BigEndian.PutUint64(number[:], uint64(image.Revision))
	spec.Write(number[:])
	spec.Write(image.SHA256)
	binary.BigEndian.PutUint64(number[:], uint64(cpuCores))
	spec.Write(number[:])
	binary.BigEndian.PutUint64(number[:], uint64(memoryMB))
	spec.Write(number[:])
	binary.BigEndian.PutUint64(number[:], uint64(bootDiskGB))
	spec.Write(number[:])
	spec.Write(planID[:])
	spec.Write(revisionID[:])
	binary.BigEndian.PutUint64(number[:], resourcePlan.RevisionNumber)
	spec.Write(number[:])
	spec.Write(resourcePlan.ContentSha256)
	for _, disk := range input.AdditionalDisks {
		binary.BigEndian.PutUint64(number[:], uint64(disk.DiskIndex))
		spec.Write(number[:])
		binary.BigEndian.PutUint64(number[:], uint64(disk.SizeGB))
		spec.Write(number[:])
	}
	spec.Write([]byte(input.SSHPublicKey))
	specHash := spec.Sum(nil)

	providerName := "aurora-" + vmID.String()

	// [COMMENT]: 8. Khởi tạo thực thể PersonalVM
	vm := &hypervisorEntity.PersonalVM{
		ID:                         vmID,
		WorkspaceID:                input.WorkspaceID,
		ZoneID:                     input.ZoneID,
		OwnerUserID:                input.OwnerUserID,
		Name:                       input.Name,
		Image:                      image.Name,
		ImageID:                    &image.ID,
		ImageRevision:              &image.Revision,
		ImageSHA256:                image.SHA256,
		ResourcePlanID:             planID,
		ResourcePlanRevisionID:     revisionID,
		ResourcePlanRevisionNumber: int64(resourcePlan.RevisionNumber),
		ResourcePlanContentSHA256:  resourcePlan.ContentSha256,
		CPUCores:                   cpuCores,
		MemoryMB:                   memoryMB,
		BootDiskGB:                 bootDiskGB,
		DiskGB:                     totalDiskGB,
		AdditionalDiskSizesGB:      additionalDiskSizes,
		SSHPublicKey:               input.SSHPublicKey,
		SpecHash:                   specHash,
		Status:                     hypervisorEntity.VMStatusProvisioning,
		OperationID:                operationID,
		ProviderName:               providerName,
		CreatedAt:                  now,
		UpdatedAt:                  now,
	}

	// [COMMENT]: 9. Đóng gói Protobuf payload cho job tạo máy ảo trên Dataplane
	jobPayload, err := proto.Marshal(&hypervisorproto.VmCreateV1{
		SchemaVersion:              1,
		VmId:                       vmID[:],
		ProviderName:               providerName,
		ImageId:                    image.ID[:],
		CpuCores:                   uint32(cpuCores),
		MemoryMb:                   uint64(memoryMB),
		DiskGb:                     uint64(bootDiskGB),
		SshPublicKey:               input.SSHPublicKey,
		ConfigHash:                 specHash,
		ImageRevision:              uint64(image.Revision),
		ImageSha256:                image.SHA256,
		ProviderTemplateVmid:       uint64(*image.ProviderTemplateVMID),
		ResourcePlanId:             planID[:],
		ResourcePlanRevisionId:     revisionID[:],
		ResourcePlanRevisionNumber: resourcePlan.RevisionNumber,
		ResourcePlanContentSha256:  resourcePlan.ContentSha256,
		AdditionalDisks:            protoDisks,
	})
	if err != nil {
		return nil, err
	}

	// [COMMENT]: 10. Trích xuất TraceID và tạo Outbox Record
	var traceID []byte
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		id := spanContext.TraceID()
		traceID = id[:]
	}

	outbox := &hypervisorEntity.HypervisorOutboxRecord{
		EventID:              operationID,
		ZoneID:               input.ZoneID,
		JobTopic:             "hypervisor.vm.create",
		Payload:              jobPayload,
		ActorUserID:          &input.OwnerUserID,
		OwnerID:              input.OwnerUserID,
		OwnerType:            "PERSONAL",
		Status:               "PENDING",
		JobVersion:           1,
		ResourceID:           vmID.String(),
		ResourceName:         input.Name,
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		IdleSeconds:          600,
	}

	// [COMMENT]: 11. Thực thi CTE nguyên tử chèn VM và ghi Outbox
	result, err := s.repo.CreateOrGet(ctx, vm, outbox)
	if err != nil {
		return nil, err
	}

	// [COMMENT]: 12. Kiểm tra tính lũy quyền (Idempotency): Nếu VM đã tồn tại nhưng khác cấu hình SpecHash -> Trả về lỗi xung đột tên
	if !result.Created && !bytes.Equal(result.VM.SpecHash, specHash) {
		return nil, hypervisorTaxonomy.ErrNameConflict
	}

	return result, nil
}

// List truy vấn danh sách máy ảo cá nhân trong Workspace và Zone của User.
func (s *PersonalVMServiceImpl) List(
	ctx context.Context,
	workspaceID uuid.UUID,
	zoneID uuid.UUID,
	ownerUserID uuid.UUID,
	limit int32,
) (out []*hypervisorEntity.PersonalVM, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	return s.repo.List(ctx, workspaceID, zoneID, ownerUserID, limit)
}

// Get truy vấn chi tiết máy ảo cá nhân theo VMID.
func (s *PersonalVMServiceImpl) Get(
	ctx context.Context,
	vmID uuid.UUID,
	workspaceID uuid.UUID,
	zoneID uuid.UUID,
	ownerUserID uuid.UUID,
) (out *hypervisorEntity.PersonalVM, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, hypervisorTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	return s.repo.Get(ctx, vmID, workspaceID, zoneID, ownerUserID)
}

// Delete kích hoạt quy trình xóa máy ảo cá nhân và gửi job hủy tài nguyên Proxmox sang Dataplane.
func (s *PersonalVMServiceImpl) Delete(
	ctx context.Context,
	vmID uuid.UUID,
	workspaceID uuid.UUID,
	zoneID uuid.UUID,
	ownerUserID uuid.UUID,
) (out *hypervisorEntity.PersonalVMDeleteResult, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, hypervisorTaxonomy.ErrNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, hypervisorTaxonomy.ErrVMStateConflict):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	var traceID []byte
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		id := spanContext.TraceID()
		traceID = id[:]
	}

	return s.repo.BeginDelete(ctx, vmID, workspaceID, zoneID, ownerUserID, traceID)
}
