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

var personalVMNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,61}[a-z0-9]$|^[a-z]$`)

type PersonalVMHandler struct {
	service hypervisorSvcInterface.PersonalVMService
}

func NewPersonalVMHandler(
	service hypervisorSvcInterface.PersonalVMService,
) *PersonalVMHandler {
	return &PersonalVMHandler{service: service}
}

func (h *PersonalVMHandler) Create(c *gin.Context) {
	const op = "hypervisor.personal_vm.create"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

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

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
	var request hypervisorDTO.CreateVMRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		apires.RespondBadRequest(c, "invalid request body")
		return
	}

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
	profileCode := strings.ToLower(strings.TrimSpace(request.ResourceProfileCode))
	bootDiskGB := int64(0)
	switch profileCode {
	case "basic":
		bootDiskGB = 32
	case "standard":
		bootDiskGB = 64
	case "performance":
		bootDiskGB = 128
	default:
		apires.RespondBadRequest(c, "resource_profile_code must be basic, standard or performance")
		return
	}
	if len(request.AdditionalDisks) > 15 {
		apires.RespondBadRequest(c, "additional_disks supports at most 15 disks")
		return
	}
	additionalDisks := make([]hypervisorEntity.PersonalVMCreateAdditionalDisk, 0, len(request.AdditionalDisks))
	totalDiskGB := bootDiskGB
	for index, disk := range request.AdditionalDisks {
		if disk.SizeGB < 8 || disk.SizeGB > 4096 || totalDiskGB+disk.SizeGB > 65536 {
			apires.RespondBadRequest(c, "each additional disk must be 8-4096 GiB and total disk must not exceed 65536 GiB")
			return
		}
		totalDiskGB += disk.SizeGB
		additionalDisks = append(additionalDisks, hypervisorEntity.PersonalVMCreateAdditionalDisk{DiskIndex: int32(index + 1), SizeGB: disk.SizeGB})
	}
	sshPublicKey := strings.TrimSpace(request.SSHPublicKey)
	if len(sshPublicKey) > 16384 ||
		(!strings.HasPrefix(sshPublicKey, "ssh-ed25519 ") &&
			!strings.HasPrefix(sshPublicKey, "ssh-rsa ") &&
			!strings.HasPrefix(sshPublicKey, "ecdsa-sha2-")) {
		apires.RespondBadRequest(c, "ssh_public_key is invalid")
		return
	}

	result, err := h.service.Create(ctx, &hypervisorEntity.CreatePersonalVM{
		WorkspaceID:         workspaceID,
		ZoneID:              zoneID,
		OwnerUserID:         userID,
		Name:                name,
		ImageID:             imageID,
		ResourceProfileCode: profileCode,
		AdditionalDisks:     additionalDisks,
		SSHPublicKey:        sshPublicKey,
	})
	if err != nil {
		switch {
		case errors.Is(err, hypervisorTaxonomy.ErrInvalidResourceProfile):
			apires.RespondBadRequest(c, "resource profile is invalid")
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

	response := gin.H{
		"id":                       result.VM.ID.String(),
		"operation_id":             result.VM.OperationID.String(),
		"name":                     result.VM.Name,
		"image":                    result.VM.Image,
		"image_id":                 result.VM.ImageID,
		"image_revision":           result.VM.ImageRevision,
		"resource_profile_code":    result.VM.ResourceProfileCode,
		"cpu_cores":                result.VM.CPUCores,
		"memory_mb":                result.VM.MemoryMB,
		"boot_disk_gb":             result.VM.BootDiskGB,
		"disk_gb":                  result.VM.DiskGB,
		"additional_disk_sizes_gb": result.VM.AdditionalDiskSizesGB,
		"status":                   result.VM.Status,
		"zone_id":                  result.VM.ZoneID.String(),
		"provider_vmid":            result.VM.ProviderVMID,
		"ipv4_address":             result.VM.IPv4Address,
		"created_at":               result.VM.CreatedAt,
		"updated_at":               result.VM.UpdatedAt,
		"provisioned_at":           result.VM.ProvisionedAt,
	}
	if result.Created || result.VM.Status == hypervisorEntity.VMStatusProvisioning {
		apires.RespondAccepted(c, response, "VM provisioning accepted")
		return
	}
	apires.RespondSuccess(c, response, "VM already exists")
}

func (h *PersonalVMHandler) List(c *gin.Context) {
	const op = "hypervisor.personal_vm.list"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

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

	limit := int32(50)
	if rawLimit := strings.TrimSpace(c.Query("limit")); rawLimit != "" {
		value, err := strconv.ParseInt(rawLimit, 10, 32)
		if err != nil || value < 1 || value > 100 {
			apires.RespondBadRequest(c, "limit must be between 1 and 100")
			return
		}
		limit = int32(value)
	}

	vms, err := h.service.List(ctx, workspaceID, zoneID, userID, limit)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "VM_LIST_FAILED")
		return
	}

	rows := make([]gin.H, 0, len(vms))
	for _, vm := range vms {
		rows = append(rows, gin.H{
			"id":                       vm.ID.String(),
			"operation_id":             vm.OperationID.String(),
			"name":                     vm.Name,
			"image":                    vm.Image,
			"image_id":                 vm.ImageID,
			"image_revision":           vm.ImageRevision,
			"resource_profile_code":    vm.ResourceProfileCode,
			"cpu_cores":                vm.CPUCores,
			"memory_mb":                vm.MemoryMB,
			"boot_disk_gb":             vm.BootDiskGB,
			"disk_gb":                  vm.DiskGB,
			"additional_disk_sizes_gb": vm.AdditionalDiskSizesGB,
			"status":                   vm.Status,
			"zone_id":                  vm.ZoneID.String(),
			"provider_vmid":            vm.ProviderVMID,
			"ipv4_address":             vm.IPv4Address,
			"created_at":               vm.CreatedAt,
			"updated_at":               vm.UpdatedAt,
			"provisioned_at":           vm.ProvisionedAt,
		})
	}
	apires.RespondSuccess(c, gin.H{"vms": rows}, "VMs fetched")
}

func (h *PersonalVMHandler) Get(c *gin.Context) {
	const op = "hypervisor.personal_vm.get"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}
	vmID, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil {
		apires.RespondBadRequest(c, "VM id is invalid")
		return
	}

	vm, err := h.service.Get(ctx, vmID, workspaceID, userID)
	if err != nil {
		if errors.Is(err, hypervisorTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "VM was not found")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "VM_GET_FAILED")
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id":                       vm.ID.String(),
		"operation_id":             vm.OperationID.String(),
		"name":                     vm.Name,
		"image":                    vm.Image,
		"image_id":                 vm.ImageID,
		"image_revision":           vm.ImageRevision,
		"resource_profile_code":    vm.ResourceProfileCode,
		"cpu_cores":                vm.CPUCores,
		"memory_mb":                vm.MemoryMB,
		"boot_disk_gb":             vm.BootDiskGB,
		"disk_gb":                  vm.DiskGB,
		"additional_disk_sizes_gb": vm.AdditionalDiskSizesGB,
		"status":                   vm.Status,
		"zone_id":                  vm.ZoneID.String(),
		"provider_vmid":            vm.ProviderVMID,
		"ipv4_address":             vm.IPv4Address,
		"created_at":               vm.CreatedAt,
		"updated_at":               vm.UpdatedAt,
		"provisioned_at":           vm.ProvisionedAt,
	}, "VM fetched")
}

func (h *PersonalVMHandler) Delete(c *gin.Context) {
	const op = "hypervisor.personal_vm.delete"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

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
	vmID, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil {
		apires.RespondBadRequest(c, "VM id is invalid")
		return
	}

	result, err := h.service.Delete(ctx, vmID, workspaceID, zoneID, userID)
	if err != nil {
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

	apires.RespondAccepted(c, gin.H{
		"id": result.VMID.String(), "operation_id": result.OperationID.String(), "status": result.Status,
	}, "VM deletion accepted")
}
