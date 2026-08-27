package hypervisorHandler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorSvcInterface "controlplane/internal/hypervisor/domain/service"
	hypervisorTaxonomy "controlplane/internal/hypervisor/taxonomy"
	hypervisorDTO "controlplane/internal/hypervisor/transport/http/dto"
	"controlplane/pkg/apires"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// [COMMENT]: personalVMNamePattern quy định định dạng tên máy ảo hợp lệ: 1-63 ký tự chữ thường, số hoặc dấu gạch đơn, bắt đầu bằng chữ cái.
var personalVMNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,61}[a-z0-9]$|^[a-z]$`)

// [COMMENT]: PersonalVMHandler xử lý các HTTP request quản trị máy ảo (VM) ở phạm vi cá nhân (Personal scope).
type PersonalVMHandler struct {
	service hypervisorSvcInterface.PersonalVMService
}

// [COMMENT]: NewPersonalVMHandler khởi tạo handler với dependency service quản lý Personal VM.
func NewPersonalVMHandler(
	service hypervisorSvcInterface.PersonalVMService,
) *PersonalVMHandler {
	return &PersonalVMHandler{service: service}
}

// [COMMENT]: Create tiếp nhận yêu cầu khởi tạo máy ảo cá nhân mới trong Zone chỉ định.
func (h *PersonalVMHandler) Create(c *gin.Context) {
	const op = "hypervisor.personal_vm.create"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// 1. Trích xuất bộ 3 định danh tin cậy (trusted context) do ACR đã xác thực và inject
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	// 2. Stream decode Request Body có giới hạn 64KB để phòng chống tấn công DoS và kiểm tra Unknown Fields
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
	var request hypervisorDTO.CreateVMRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		apires.RespondBadRequest(c, "invalid request body")
		return
	}

	// 3. Transport chỉ xác thực syntax/boundary. Cost owns resource-plan limits;
	// the service resolves the selected immutable revision from its local projection.
	name := strings.ToLower(strings.TrimSpace(request.Name))
	if !personalVMNamePattern.MatchString(name) || strings.Contains(name, "--") {
		apires.RespondBadRequest(c, "name must be 1-63 lowercase letters, numbers or single hyphens")
		return
	}
	imageID, err := uuid.Parse(strings.TrimSpace(request.ImageID))
	if err != nil {
		apires.RespondBadRequest(c, "image_id is invalid")
		return
	}
	resourcePlanID, err := uuid.Parse(strings.TrimSpace(request.ResourcePlanID))
	if err != nil || resourcePlanID == uuid.Nil {
		apires.RespondBadRequest(c, "resource_plan_id is invalid")
		return
	}
	resourcePlanRevisionID, err := uuid.Parse(strings.TrimSpace(request.ResourcePlanRevisionID))
	if err != nil || resourcePlanRevisionID == uuid.Nil {
		apires.RespondBadRequest(c, "resource_plan_revision_id is invalid")
		return
	}
	if len(request.AdditionalDisks) > 15 {
		apires.RespondBadRequest(c, "additional_disks supports at most 15 disks")
		return
	}
	additionalDisks := make([]hypervisorEntity.PersonalVMCreateAdditionalDisk, 0, len(request.AdditionalDisks))
	for index, disk := range request.AdditionalDisks {
		sizeGB, parseErr := strconv.ParseInt(strings.TrimSpace(disk.SizeGB), 10, 64)
		if parseErr != nil || sizeGB < 8 || sizeGB > 4096 {
			apires.RespondBadRequest(c, "each additional disk must be 8-4096 GiB")
			return
		}
		additionalDisks = append(additionalDisks, hypervisorEntity.PersonalVMCreateAdditionalDisk{DiskIndex: int32(index + 1), SizeGB: sizeGB})
	}
	sshPublicKey := strings.TrimSpace(request.SSHPublicKey)
	if len(sshPublicKey) > 16384 ||
		(!strings.HasPrefix(sshPublicKey, "ssh-ed25519 ") &&
			!strings.HasPrefix(sshPublicKey, "ssh-rsa ") &&
			!strings.HasPrefix(sshPublicKey, "ecdsa-sha2-")) {
		apires.RespondBadRequest(c, "ssh_public_key is invalid")
		return
	}

	// 4. Ủy quyền thực thi nghiệp vụ cho PersonalVMService
	result, err := h.service.Create(ctx, &hypervisorEntity.CreatePersonalVM{
		WorkspaceID:            workspaceID,
		ZoneID:                 zoneID,
		OwnerUserID:            userID,
		Name:                   name,
		ImageID:                imageID,
		ResourcePlanID:         resourcePlanID,
		ResourcePlanRevisionID: resourcePlanRevisionID,
		AdditionalDisks:        additionalDisks,
		SSHPublicKey:           sshPublicKey,
	})
	if err != nil {
		// 5. Chuẩn hóa mã lỗi trả về cho client theo đúng Domain Taxonomy
		switch {
		case errors.Is(err, hypervisorTaxonomy.ErrResourcePlanCacheUnavailable):
			apires.RespondServiceUnavailable(c, "HYPERVISOR_RESOURCE_PLAN_CACHE_UNAVAILABLE")
		case errors.Is(err, hypervisorTaxonomy.ErrResourcePlanUnavailable):
			apires.RespondConflict(c, "the selected resource plan is no longer available")
		case errors.Is(err, hypervisorTaxonomy.ErrNameConflict):
			apires.RespondConflict(c, "a VM with this name already exists with another specification")
		case errors.Is(err, hypervisorTaxonomy.ErrScopeUnavailable):
			apires.RespondConflict(c, "the selected workspace or zone is not accepting new VM workloads")
		case errors.Is(err, hypervisorTaxonomy.ErrImageNotFound):
			apires.RespondConflict(c, "the selected image is no longer available in this zone")
		case errors.Is(err, hypervisorTaxonomy.ErrCommercialAdmissionDenied):
			apires.RespondServiceUnavailable(c, "HYPERVISOR_WALLET_ADMISSION_UNAVAILABLE")
		case errors.Is(err, hypervisorTaxonomy.ErrPricingUnavailable):
			apires.RespondServiceUnavailable(c, "HYPERVISOR_PRICING_UNAVAILABLE")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "VM_CREATE_FAILED")
		}
		return
	}

	// 6. BIGINT is always rendered as a decimal string at the browser boundary.
	imageRevision := ""
	if result.VM.ImageRevision != nil {
		imageRevision = strconv.FormatInt(*result.VM.ImageRevision, 10)
	}
	providerVMID := ""
	if result.VM.ProviderVMID != nil {
		providerVMID = strconv.FormatInt(*result.VM.ProviderVMID, 10)
	}
	additionalDiskSizesGB := make([]string, 0, len(result.VM.AdditionalDiskSizesGB))
	for _, sizeGB := range result.VM.AdditionalDiskSizesGB {
		additionalDiskSizesGB = append(additionalDiskSizesGB, strconv.FormatInt(sizeGB, 10))
	}

	// 7. Định dạng payload phản hồi: 202 Accepted nếu tạo mới/đang provisioning, 200 OK nếu VM đã tồn tại (idempotent)
	response := gin.H{
		"id":                            result.VM.ID.String(),
		"operation_id":                  result.VM.OperationID.String(),
		"name":                          result.VM.Name,
		"image":                         result.VM.Image,
		"image_id":                      result.VM.ImageID,
		"image_revision":                imageRevision,
		"resource_plan_id":              result.VM.ResourcePlanID.String(),
		"resource_plan_revision_id":     result.VM.ResourcePlanRevisionID.String(),
		"resource_plan_revision_number": strconv.FormatInt(result.VM.ResourcePlanRevisionNumber, 10),
		"cpu_cores":                     result.VM.CPUCores,
		"memory_mb":                     strconv.FormatInt(result.VM.MemoryMB, 10),
		"boot_disk_gb":                  strconv.FormatInt(result.VM.BootDiskGB, 10),
		"disk_gb":                       strconv.FormatInt(result.VM.DiskGB, 10),
		"additional_disk_sizes_gb":      additionalDiskSizesGB,
		"status":                        result.VM.Status,
		"zone_id":                       result.VM.ZoneID.String(),
		"provider_vmid":                 providerVMID,
		"ipv4_address":                  result.VM.IPv4Address,
		"created_at":                    result.VM.CreatedAt,
		"updated_at":                    result.VM.UpdatedAt,
		"provisioned_at":                result.VM.ProvisionedAt,
	}
	if result.Created || result.VM.Status == hypervisorEntity.VMStatusProvisioning {
		apires.RespondAccepted(c, response, "VM provisioning accepted")
		return
	}
	apires.RespondSuccess(c, response, "VM already exists")
}

// [COMMENT]: List truy vấn danh sách máy ảo thuộc về Personal Workspace và Zone của người dùng.
func (h *PersonalVMHandler) List(c *gin.Context) {
	const op = "hypervisor.personal_vm.list"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// 1. Trích xuất bộ 3 định danh tin cậy từ Context
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	// 2. Phân tích và giới hạn tham số phân trang limit (mặc định 50, tối đa 100)
	limit := int32(50)
	if rawLimit := strings.TrimSpace(c.Query("limit")); rawLimit != "" {
		value, err := strconv.ParseInt(rawLimit, 10, 32)
		if err != nil || value < 1 || value > 100 {
			apires.RespondBadRequest(c, "limit must be between 1 and 100")
			return
		}
		limit = int32(value)
	}

	// 3. Gọi service truy vấn danh sách VM
	vms, err := h.service.List(ctx, workspaceID, zoneID, userID, limit)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "VM_LIST_FAILED")
		return
	}

	// 4. Format dữ liệu trả về cho client
	rows := make([]gin.H, 0, len(vms))
	for _, vm := range vms {
		imageRevision := ""
		if vm.ImageRevision != nil {
			imageRevision = strconv.FormatInt(*vm.ImageRevision, 10)
		}
		providerVMID := ""
		if vm.ProviderVMID != nil {
			providerVMID = strconv.FormatInt(*vm.ProviderVMID, 10)
		}
		additionalDiskSizesGB := make([]string, 0, len(vm.AdditionalDiskSizesGB))
		for _, sizeGB := range vm.AdditionalDiskSizesGB {
			additionalDiskSizesGB = append(additionalDiskSizesGB, strconv.FormatInt(sizeGB, 10))
		}
		rows = append(rows, gin.H{
			"id":                            vm.ID.String(),
			"operation_id":                  vm.OperationID.String(),
			"name":                          vm.Name,
			"image":                         vm.Image,
			"image_id":                      vm.ImageID,
			"image_revision":                imageRevision,
			"resource_plan_id":              vm.ResourcePlanID.String(),
			"resource_plan_revision_id":     vm.ResourcePlanRevisionID.String(),
			"resource_plan_revision_number": strconv.FormatInt(vm.ResourcePlanRevisionNumber, 10),
			"cpu_cores":                     vm.CPUCores,
			"memory_mb":                     strconv.FormatInt(vm.MemoryMB, 10),
			"boot_disk_gb":                  strconv.FormatInt(vm.BootDiskGB, 10),
			"disk_gb":                       strconv.FormatInt(vm.DiskGB, 10),
			"additional_disk_sizes_gb":      additionalDiskSizesGB,
			"status":                        vm.Status,
			"zone_id":                       vm.ZoneID.String(),
			"provider_vmid":                 providerVMID,
			"ipv4_address":                  vm.IPv4Address,
			"created_at":                    vm.CreatedAt,
			"updated_at":                    vm.UpdatedAt,
			"provisioned_at":                vm.ProvisionedAt,
		})
	}
	apires.RespondSuccess(c, gin.H{"vms": rows}, "VMs fetched")
}

// [COMMENT]: Get truy vấn thông tin chi tiết của một máy ảo theo VM ID.
func (h *PersonalVMHandler) Get(c *gin.Context) {
	const op = "hypervisor.personal_vm.get"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// 1. Trích xuất bộ 3 định danh tin cậy từ Context
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	// 2. Parse VM ID từ URL path parameter
	vmID, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil {
		apires.RespondBadRequest(c, "VM id is invalid")
		return
	}

	// 3. Gọi service lấy thông tin chi tiết VM
	vm, err := h.service.Get(ctx, vmID, workspaceID, zoneID, userID)
	if err != nil {
		if errors.Is(err, hypervisorTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "VM was not found")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "VM_GET_FAILED")
		return
	}

	// 4. BIGINT is always rendered as a decimal string at the browser boundary.
	imageRevision := ""
	if vm.ImageRevision != nil {
		imageRevision = strconv.FormatInt(*vm.ImageRevision, 10)
	}
	providerVMID := ""
	if vm.ProviderVMID != nil {
		providerVMID = strconv.FormatInt(*vm.ProviderVMID, 10)
	}
	additionalDiskSizesGB := make([]string, 0, len(vm.AdditionalDiskSizesGB))
	for _, sizeGB := range vm.AdditionalDiskSizesGB {
		additionalDiskSizesGB = append(additionalDiskSizesGB, strconv.FormatInt(sizeGB, 10))
	}

	// 5. Trả về thông tin chi tiết máy ảo
	apires.RespondSuccess(c, gin.H{
		"id":                            vm.ID.String(),
		"operation_id":                  vm.OperationID.String(),
		"name":                          vm.Name,
		"image":                         vm.Image,
		"image_id":                      vm.ImageID,
		"image_revision":                imageRevision,
		"resource_plan_id":              vm.ResourcePlanID.String(),
		"resource_plan_revision_id":     vm.ResourcePlanRevisionID.String(),
		"resource_plan_revision_number": strconv.FormatInt(vm.ResourcePlanRevisionNumber, 10),
		"cpu_cores":                     vm.CPUCores,
		"memory_mb":                     strconv.FormatInt(vm.MemoryMB, 10),
		"boot_disk_gb":                  strconv.FormatInt(vm.BootDiskGB, 10),
		"disk_gb":                       strconv.FormatInt(vm.DiskGB, 10),
		"additional_disk_sizes_gb":      additionalDiskSizesGB,
		"status":                        vm.Status,
		"zone_id":                       vm.ZoneID.String(),
		"provider_vmid":                 providerVMID,
		"ipv4_address":                  vm.IPv4Address,
		"created_at":                    vm.CreatedAt,
		"updated_at":                    vm.UpdatedAt,
		"provisioned_at":                vm.ProvisionedAt,
	}, "VM fetched")
}

// [COMMENT]: Delete tiếp nhận yêu cầu xóa máy ảo cá nhân bất đồng bộ qua Outbox.
func (h *PersonalVMHandler) Delete(c *gin.Context) {
	const op = "hypervisor.personal_vm.delete"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// 1. Trích xuất bộ 3 định danh tin cậy từ Context
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	// 2. Parse VM ID từ URL path parameter
	vmID, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil {
		apires.RespondBadRequest(c, "VM id is invalid")
		return
	}

	// 3. Gọi service thực hiện đánh dấu DELETING và phát sinh outbox command
	result, err := h.service.Delete(ctx, vmID, workspaceID, zoneID, userID)
	if err != nil {
		// 4. Xử lý lỗi phân loại theo Domain Taxonomy
		switch {
		case errors.Is(err, hypervisorTaxonomy.ErrNotFound):
			apires.RespondNotFound(c, "VM was not found")
		case errors.Is(err, hypervisorTaxonomy.ErrVMStateConflict):
			apires.RespondConflict(c, "VM is not ready for deletion")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "VM_DELETE_FAILED")
		}
		return
	}

	// 5. Trả về 202 Accepted kèm thông tin operation ID
	apires.RespondAccepted(c, gin.H{
		"id": result.VMID.String(), "operation_id": result.OperationID.String(), "status": result.Status,
	}, "VM deletion accepted")
}
