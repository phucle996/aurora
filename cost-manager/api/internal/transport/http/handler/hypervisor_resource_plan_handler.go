package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingSvcInterface "cost-manager/api/internal/domain/service"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"cost-manager/api/internal/transport/http/dto"
	"cost-manager/api/pkg/apires"
	"cost-manager/api/pkg/logger"
	"cost-manager/api/pkg/pkgcontext"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var hypervisorResourcePlanDecimal = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)

var hypervisorResourcePlanCode = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

type HypervisorResourcePlanHandler struct {
	service billingSvcInterface.HypervisorResourcePlanService
}

func NewHypervisorResourcePlanHandler(service billingSvcInterface.HypervisorResourcePlanService) *HypervisorResourcePlanHandler {
	return &HypervisorResourcePlanHandler{service: service}
}

func (h *HypervisorResourcePlanHandler) ListEffective(c *gin.Context) {
	const op = "handler.hypervisor_resource_plan.list_effective"
	limit := 100
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			apires.RespondBadRequest(c, "limit must be an integer between 1 and 100")
			return
		}
		limit = parsed
	}
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 3*time.Second)
	defer cancel()
	plans, hasMore, err := h.service.ListEffectiveHypervisorResourcePlans(ctx, entity.HypervisorResourcePlanListQuery{At: time.Now().UTC(), Limit: limit})
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondServiceUnavailable(c, "HYPERVISOR_RESOURCE_PLAN_CATALOG_UNAVAILABLE")
		return
	}
	items := make([]gin.H, len(plans))
	for index, plan := range plans {
		var effectiveTo any
		if plan.EffectiveTo != nil {
			effectiveTo = plan.EffectiveTo.UTC().Format(time.RFC3339Nano)
		}
		items[index] = gin.H{
			"plan_id": plan.PlanID.String(), "revision_id": plan.RevisionID.String(), "revision_number": strconv.FormatInt(plan.RevisionNumber, 10),
			"code": plan.Code, "display_name": plan.DisplayName, "description": plan.Description, "billing_model": plan.BillingModel,
			"cpu_cores": strconv.FormatInt(plan.CPUCores, 10), "memory_mib": strconv.FormatInt(plan.MemoryMIB, 10), "boot_disk_gib": strconv.FormatInt(plan.BootDiskGIB, 10),
			"content_sha256": plan.ContentSHA256, "effective_from": plan.EffectiveFrom.UTC().Format(time.RFC3339Nano), "effective_to": effectiveTo,
		}
	}
	apires.RespondSuccess(c, gin.H{"plans": items, "has_more": hasMore, "observed_at": time.Now().UTC().Format(time.RFC3339Nano)}, "Hypervisor resource plans")
}

func (h *HypervisorResourcePlanHandler) Create(c *gin.Context) {
	const op = "handler.hypervisor_resource_plan.create"
	actor, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	var request dto.CreateHypervisorResourcePlanRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		apires.RespondBadRequest(c, "invalid Hypervisor resource plan payload")
		return
	}
	request.EffectiveFrom = request.EffectiveFrom.UTC().Truncate(time.Microsecond)
	if err := decoder.Decode(new(any)); err != io.EOF || request.EffectiveFrom.IsZero() {
		apires.RespondBadRequest(c, "effective_from is required and exactly one JSON object is allowed")
		return
	}
	request.DisplayName = strings.TrimSpace(request.DisplayName)
	request.Description = strings.TrimSpace(request.Description)
	request.ChangeReason = strings.TrimSpace(request.ChangeReason)
	if request.DisplayName == "" || len(request.DisplayName) > 256 || request.ChangeReason == "" || len(request.ChangeReason) > 2000 {
		apires.RespondBadRequest(c, "display_name and change_reason are required and bounded")
		return
	}
	code := strings.ToLower(strings.TrimSpace(request.Code))
	cpu, cpuErr := strconv.ParseInt(strings.TrimSpace(request.CPUCores), 10, 64)
	memory, memoryErr := strconv.ParseInt(strings.TrimSpace(request.MemoryMIB), 10, 64)
	disk, diskErr := strconv.ParseInt(strings.TrimSpace(request.BootDiskGIB), 10, 64)
	if !hypervisorResourcePlanCode.MatchString(code) || cpuErr != nil || memoryErr != nil || diskErr != nil ||
		!hypervisorResourcePlanDecimal.MatchString(request.CPUCores) || !hypervisorResourcePlanDecimal.MatchString(request.MemoryMIB) || !hypervisorResourcePlanDecimal.MatchString(request.BootDiskGIB) {
		apires.RespondBadRequest(c, "code and resource limits must be valid decimal strings")
		return
	}
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	plan, err := h.service.CreateHypervisorResourcePlan(ctx, entity.CreateHypervisorResourcePlanCommand{Code: code, DisplayName: request.DisplayName, Description: request.Description, CPUCores: cpu, MemoryMIB: memory, BootDiskGIB: disk, EffectiveFrom: request.EffectiveFrom, ChangeReason: request.ChangeReason, CreatedBy: actor})
	if err != nil {
		switch {
		case errors.Is(err, billingTaxonomy.ErrInvalidArgument):
			apires.RespondBadRequest(c, "invalid Hypervisor resource plan")
		case errors.Is(err, billingTaxonomy.ErrHypervisorResourcePlanConflict):
			apires.RespondConflict(c, "HYPERVISOR_RESOURCE_PLAN_CONFLICT")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "HYPERVISOR_RESOURCE_PLAN_CREATE_FAILED")
		}
		return
	}
	h.service.NotifyHypervisorResourcePlanOutbox()
	apires.RespondCreated(c, gin.H{"plan_id": plan.PlanID.String(), "revision_id": plan.RevisionID.String(), "revision_number": strconv.FormatInt(plan.RevisionNumber, 10), "code": plan.Code, "display_name": plan.DisplayName, "description": plan.Description, "billing_model": plan.BillingModel, "cpu_cores": strconv.FormatInt(plan.CPUCores, 10), "memory_mib": strconv.FormatInt(plan.MemoryMIB, 10), "boot_disk_gib": strconv.FormatInt(plan.BootDiskGIB, 10), "content_sha256": plan.ContentSHA256, "effective_from": plan.EffectiveFrom.UTC().Format(time.RFC3339Nano), "effective_to": nil}, "Hypervisor resource plan created")
}

func (h *HypervisorResourcePlanHandler) PublishRevision(c *gin.Context) {
	const op = "handler.hypervisor_resource_plan.publish_revision"
	actor, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	planID, err := uuid.Parse(strings.TrimSpace(c.Param("plan_id")))
	if err != nil || planID == uuid.Nil {
		apires.RespondBadRequest(c, "plan_id is invalid")
		return
	}
	var request dto.PublishHypervisorResourcePlanRevisionRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		apires.RespondBadRequest(c, "invalid Hypervisor resource plan revision payload")
		return
	}
	request.EffectiveFrom = request.EffectiveFrom.UTC().Truncate(time.Microsecond)
	if err := decoder.Decode(new(any)); err != io.EOF || request.EffectiveFrom.IsZero() {
		apires.RespondBadRequest(c, "effective_from is required and exactly one JSON object is allowed")
		return
	}
	request.ChangeReason = strings.TrimSpace(request.ChangeReason)
	if request.ChangeReason == "" || len(request.ChangeReason) > 2000 {
		apires.RespondBadRequest(c, "change_reason is required and bounded")
		return
	}
	expected, expectedErr := strconv.ParseInt(strings.TrimSpace(request.ExpectedLatestRevision), 10, 64)
	cpu, cpuErr := strconv.ParseInt(strings.TrimSpace(request.CPUCores), 10, 64)
	memory, memoryErr := strconv.ParseInt(strings.TrimSpace(request.MemoryMIB), 10, 64)
	disk, diskErr := strconv.ParseInt(strings.TrimSpace(request.BootDiskGIB), 10, 64)
	if expectedErr != nil || expected < 1 || expected == math.MaxInt64 || !hypervisorResourcePlanDecimal.MatchString(request.ExpectedLatestRevision) || cpuErr != nil || memoryErr != nil || diskErr != nil ||
		!hypervisorResourcePlanDecimal.MatchString(request.CPUCores) || !hypervisorResourcePlanDecimal.MatchString(request.MemoryMIB) || !hypervisorResourcePlanDecimal.MatchString(request.BootDiskGIB) {
		apires.RespondBadRequest(c, "revision and resource limits must be valid decimal strings")
		return
	}
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	plan, err := h.service.PublishHypervisorResourcePlanRevision(ctx, entity.PublishHypervisorResourcePlanRevisionCommand{PlanID: planID, ExpectedLatestRevision: expected, CPUCores: cpu, MemoryMIB: memory, BootDiskGIB: disk, EffectiveFrom: request.EffectiveFrom, ChangeReason: request.ChangeReason, CreatedBy: actor})
	if err != nil {
		switch {
		case errors.Is(err, billingTaxonomy.ErrInvalidArgument):
			apires.RespondBadRequest(c, "invalid Hypervisor resource plan revision")
		case errors.Is(err, billingTaxonomy.ErrHypervisorResourcePlanNotFound):
			apires.RespondNotFound(c, "HYPERVISOR_RESOURCE_PLAN_NOT_FOUND")
		case errors.Is(err, billingTaxonomy.ErrHypervisorResourcePlanConflict):
			apires.RespondConflict(c, "HYPERVISOR_RESOURCE_PLAN_REVISION_CONFLICT")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "HYPERVISOR_RESOURCE_PLAN_PUBLISH_FAILED")
		}
		return
	}
	h.service.NotifyHypervisorResourcePlanOutbox()
	apires.RespondCreated(c, gin.H{"plan_id": plan.PlanID.String(), "revision_id": plan.RevisionID.String(), "revision_number": strconv.FormatInt(plan.RevisionNumber, 10), "code": plan.Code, "display_name": plan.DisplayName, "description": plan.Description, "billing_model": plan.BillingModel, "cpu_cores": strconv.FormatInt(plan.CPUCores, 10), "memory_mib": strconv.FormatInt(plan.MemoryMIB, 10), "boot_disk_gib": strconv.FormatInt(plan.BootDiskGIB, 10), "content_sha256": plan.ContentSHA256, "effective_from": plan.EffectiveFrom.UTC().Format(time.RFC3339Nano), "effective_to": nil}, "Hypervisor resource plan revision published")
}

func (h *HypervisorResourcePlanHandler) ListAdmin(c *gin.Context) {
	query := entity.HypervisorResourcePlanAdminQuery{Limit: 50, At: time.Now().UTC()}
	if raw, ok := c.GetQuery("limit"); ok {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			apires.RespondBadRequest(c, "limit must be 1..100")
			return
		}
		query.Limit = limit
	}
	if raw, ok := c.GetQuery("after"); ok {
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			apires.RespondBadRequest(c, "after must be a non-nil plan UUID")
			return
		}
		query.After = id
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	plans, more, err := h.service.ListPlans(ctx, query)
	if err != nil {
		logger.HandlerError(c, "hypervisor_resource_plan.list", err)
		apires.RespondServiceUnavailable(c, "HYPERVISOR_RESOURCE_PLAN_CATALOG_UNAVAILABLE")
		return
	}
	items := make([]gin.H, 0, len(plans))
	for _, plan := range plans {
		items = append(items, gin.H{"plan_id": plan.PlanID.String(), "code": plan.Code, "display_name": plan.DisplayName, "description": plan.Description, "state": plan.State, "latest_revision_number": strconv.FormatInt(plan.LatestRevisionNumber, 10), "effective_revision_number": strconv.FormatInt(plan.EffectiveRevisionNumber, 10)})
	}
	next := ""
	if more {
		next = plans[len(plans)-1].PlanID.String()
	}
	apires.RespondSuccess(c, gin.H{"plans": items, "next_cursor": next, "observed_at": query.At.Format(time.RFC3339Nano)}, "Hypervisor resource plans")
}
func (h *HypervisorResourcePlanHandler) ListRevisions(c *gin.Context) {
	id, err := uuid.Parse(c.Param("plan_id"))
	if err != nil || id == uuid.Nil {
		apires.RespondBadRequest(c, "plan_id must be a non-nil UUID")
		return
	}
	query := entity.HypervisorResourcePlanHistoryQuery{PlanID: id, Limit: 50, At: time.Now().UTC()}
	if raw, ok := c.GetQuery("limit"); ok {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			apires.RespondBadRequest(c, "limit must be 1..100")
			return
		}
		query.Limit = limit
	}
	if raw, ok := c.GetQuery("before"); ok {
		before, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || before < 1 || !hypervisorResourcePlanDecimal.MatchString(raw) {
			apires.RespondBadRequest(c, "before must be a positive BIGINT decimal string")
			return
		}
		query.Before = before
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	revisions, more, err := h.service.ListRevisions(ctx, query)
	if err != nil {
		logger.HandlerError(c, "hypervisor_resource_plan.history", err)
		apires.RespondServiceUnavailable(c, "HYPERVISOR_RESOURCE_PLAN_CATALOG_UNAVAILABLE")
		return
	}
	items := make([]gin.H, 0, len(revisions))
	for _, revision := range revisions {
		var effectiveTo any
		if !revision.EffectiveTo.IsZero() {
			effectiveTo = revision.EffectiveTo.UTC().Format(time.RFC3339Nano)
		}
		items = append(items, gin.H{"plan_id": revision.PlanID.String(), "revision_id": revision.RevisionID.String(), "revision_number": strconv.FormatInt(revision.RevisionNumber, 10), "cpu_cores": strconv.FormatInt(revision.CPUCores, 10), "memory_mib": strconv.FormatInt(revision.MemoryMIB, 10), "boot_disk_gib": strconv.FormatInt(revision.BootDiskGIB, 10), "effective_from": revision.EffectiveFrom.UTC().Format(time.RFC3339Nano), "effective_to": effectiveTo, "state": revision.State, "change_reason": revision.ChangeReason, "is_latest": revision.IsLatest, "is_effective": revision.IsEffective})
	}
	next := ""
	if more {
		next = strconv.FormatInt(revisions[len(revisions)-1].RevisionNumber, 10)
	}
	apires.RespondSuccess(c, gin.H{"revisions": items, "next_cursor": next, "observed_at": query.At.Format(time.RFC3339Nano)}, "Hypervisor resource plan revisions")
}
