package hypervisorHandler

import (
	"context"
	"errors"
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

	var request hypervisorDTO.CreateVMRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apires.RespondBadRequest(c, "invalid request body")
		return
	}

	name := strings.ToLower(strings.TrimSpace(request.Name))
	if !personalVMNamePattern.MatchString(name) || strings.Contains(name, "--") {
		apires.RespondBadRequest(c, "name must be 1-63 lowercase letters, numbers or single hyphens")
		return
	}
	image := strings.ToLower(strings.TrimSpace(request.Image))
	switch image {
	case "ubuntu-24.04", "debian-12":
	default:
		apires.RespondBadRequest(c, "image is not supported")
		return
	}
	if request.CPUCores < 1 || request.CPUCores > 64 {
		apires.RespondBadRequest(c, "cpu_cores must be between 1 and 64")
		return
	}
	if request.MemoryMB < 512 || request.MemoryMB > 262144 || request.MemoryMB%256 != 0 {
		apires.RespondBadRequest(c, "memory_mb must be a multiple of 256 between 512 and 262144")
		return
	}
	if request.DiskGB < 8 || request.DiskGB > 4096 {
		apires.RespondBadRequest(c, "disk_gb must be between 8 and 4096")
		return
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
		WorkspaceID:  workspaceID,
		ZoneID:       zoneID,
		OwnerUserID:  userID,
		Name:         name,
		Image:        image,
		CPUCores:     request.CPUCores,
		MemoryMB:     request.MemoryMB,
		DiskGB:       request.DiskGB,
		SSHPublicKey: sshPublicKey,
	})
	if err != nil {
		switch {
		case errors.Is(err, hypervisorTaxonomy.ErrNameConflict):
			apires.RespondConflict(c, "a VM with this name already exists with another specification")
		case errors.Is(err, hypervisorTaxonomy.ErrProvisioningFailed):
			apires.RespondConflict(c, "the previous provisioning attempt failed; delete or repair it before reusing the name")
		case errors.Is(err, hypervisorTaxonomy.ErrScopeUnavailable):
			apires.RespondConflict(c, "the selected workspace or zone is not accepting new VM workloads")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "VM_CREATE_FAILED")
		}
		return
	}

	response := gin.H{
		"id":             result.VM.ID.String(),
		"operation_id":   result.VM.OperationID.String(),
		"name":           result.VM.Name,
		"image":          result.VM.Image,
		"cpu_cores":      result.VM.CPUCores,
		"memory_mb":      result.VM.MemoryMB,
		"disk_gb":        result.VM.DiskGB,
		"status":         result.VM.Status,
		"zone_id":        result.VM.ZoneID.String(),
		"provider_node":  result.VM.ProviderNode,
		"provider_vmid":  result.VM.ProviderVMID,
		"ipv4_address":   result.VM.IPv4Address,
		"error_code":     result.VM.ErrorCode,
		"error_message":  result.VM.ErrorMessage,
		"created_at":     result.VM.CreatedAt,
		"updated_at":     result.VM.UpdatedAt,
		"provisioned_at": result.VM.ProvisionedAt,
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
			"id":             vm.ID.String(),
			"operation_id":   vm.OperationID.String(),
			"name":           vm.Name,
			"image":          vm.Image,
			"cpu_cores":      vm.CPUCores,
			"memory_mb":      vm.MemoryMB,
			"disk_gb":        vm.DiskGB,
			"status":         vm.Status,
			"zone_id":        vm.ZoneID.String(),
			"provider_node":  vm.ProviderNode,
			"provider_vmid":  vm.ProviderVMID,
			"ipv4_address":   vm.IPv4Address,
			"error_code":     vm.ErrorCode,
			"error_message":  vm.ErrorMessage,
			"created_at":     vm.CreatedAt,
			"updated_at":     vm.UpdatedAt,
			"provisioned_at": vm.ProvisionedAt,
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
		"id":             vm.ID.String(),
		"operation_id":   vm.OperationID.String(),
		"name":           vm.Name,
		"image":          vm.Image,
		"cpu_cores":      vm.CPUCores,
		"memory_mb":      vm.MemoryMB,
		"disk_gb":        vm.DiskGB,
		"status":         vm.Status,
		"zone_id":        vm.ZoneID.String(),
		"provider_node":  vm.ProviderNode,
		"provider_vmid":  vm.ProviderVMID,
		"ipv4_address":   vm.IPv4Address,
		"error_code":     vm.ErrorCode,
		"error_message":  vm.ErrorMessage,
		"created_at":     vm.CreatedAt,
		"updated_at":     vm.UpdatedAt,
		"provisioned_at": vm.ProvisionedAt,
	}, "VM fetched")
}
