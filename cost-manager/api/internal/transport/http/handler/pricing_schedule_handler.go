package handler

import (
	"context"
	"errors"
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
)

type PricingScheduleHandler struct {
	service billingSvcInterface.PricingScheduleService
}

func NewPricingScheduleHandler(service billingSvcInterface.PricingScheduleService) *PricingScheduleHandler {
	return &PricingScheduleHandler{service: service}
}

func (h *PricingScheduleHandler) ListPricingSchedules(c *gin.Context) {
	const op = "handler.pricing_schedule.list"
	var req dto.ListPricingSchedulesRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		apires.RespondBadRequest(c, "invalid pricing schedule query")
		return
	}
	if req.Page < 1 {
		req.Page = 1
	}
	if req.Limit < 1 || req.Limit > 100 {
		req.Limit = 20
	}
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	schedules, total, err := h.service.GetPricingSchedules(ctx, req.Page, req.Limit, entity.ChargeKindCode(strings.TrimSpace(req.ChargeKind)), req.Search)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "failed to retrieve pricing schedules")
		return
	}
	items := make([]gin.H, len(schedules))
	for i, schedule := range schedules {
		items[i] = gin.H{"id": schedule.ID, "code": schedule.Code, "display_name": schedule.DisplayName, "charge_kind_code": schedule.ChargeKindCode, "pricing_model": schedule.PricingModel, "scope_type": schedule.ScopeType, "zone_id": schedule.ZoneID, "currency": schedule.Currency, "metadata_version": schedule.MetadataVersion, "status": schedule.Status, "created_at": schedule.CreatedAt, "updated_at": schedule.UpdatedAt}
	}
	apires.RespondSuccess(c, gin.H{"pricing_schedules": items, "pagination": gin.H{"page": req.Page, "limit": req.Limit, "total": total}}, "pricing schedules")
}

func (h *PricingScheduleHandler) GetPricingScheduleDetail(c *gin.Context) {
	const op = "handler.pricing_schedule.detail"
	code := strings.TrimSpace(c.Param("code"))
	if code == "" {
		apires.RespondBadRequest(c, "invalid pricing schedule code")
		return
	}
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	detail, err := h.service.GetPricingScheduleDetail(ctx, code)
	if err != nil {
		if errors.Is(err, billingTaxonomy.ErrPricingScheduleNotFound) {
			apires.RespondNotFound(c, "PRICING_SCHEDULE_NOT_FOUND")
		} else {
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "failed to retrieve pricing schedule")
		}
		return
	}
	apires.RespondSuccess(c, gin.H{"id": detail.Schedule.ID, "code": detail.Schedule.Code, "display_name": detail.Schedule.DisplayName, "charge_kind_code": detail.Schedule.ChargeKindCode, "pricing_model": detail.Schedule.PricingModel, "scope_type": detail.Schedule.ScopeType, "zone_id": detail.Schedule.ZoneID, "currency": detail.Schedule.Currency, "metadata_version": detail.Schedule.MetadataVersion, "latest_version": pricingScheduleVersionResponse(detail.LatestVersion)}, "pricing schedule")
}

func (h *PricingScheduleHandler) EstimateStorage(c *gin.Context) {
	const op = "handler.pricing_schedule.estimate_storage"
	capacity, err := strconv.ParseInt(strings.TrimSpace(c.Query("capacity_bytes")), 10, 64)
	if err != nil || capacity <= 0 || capacity > 1<<60 {
		apires.RespondBadRequest(c, "capacity_bytes must be a positive integer no larger than 1<<60")
		return
	}
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 2*time.Second)
	defer cancel()
	estimate, err := h.service.EstimateStorage(ctx, capacity, zoneID)
	if err != nil {
		if errors.Is(err, billingTaxonomy.ErrInvalidArgument) || errors.Is(err, billingTaxonomy.ErrInvalidPricingBrackets) {
			apires.RespondBadRequest(c, "invalid storage estimate request")
		} else {
			logger.HandlerError(c, op, err)
			apires.RespondServiceUnavailable(c, "storage pricing is not available")
		}
		return
	}
	apires.RespondSuccess(c, gin.H{"capacity_bytes": strconv.FormatInt(estimate.CapacityBytes, 10), "hourly_estimate_micro_units": strconv.FormatInt(estimate.HourlyMicroUnits, 10), "currency": estimate.Currency, "pricing_schedule_code": estimate.PricingScheduleCode, "pricing_schedule_id": estimate.PricingScheduleID, "pricing_schedule_version_id": estimate.PricingScheduleVersionID, "pricing_version": estimate.PricingVersion, "pricing_checksum": estimate.PricingChecksum, "pricing_effective_from": estimate.PricingEffectiveFrom, "estimated_at": estimate.EstimatedAt}, "storage estimate")
}

func (h *PricingScheduleHandler) UpdatePricingScheduleMetadata(c *gin.Context) {
	const op = "handler.pricing_schedule.update_metadata"
	actor, ok := pkgcontext.GetUserID(c, op)
	_ = actor
	if !ok {
		return
	}
	var req dto.UpdatePricingScheduleMetadataRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "invalid pricing schedule metadata payload")
		return
	}
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	updated, err := h.service.UpdatePricingScheduleMetadata(ctx, entity.PricingScheduleMetadataUpdate{ScheduleCode: strings.TrimSpace(c.Param("code")), MetadataVersion: req.MetadataVersion, DisplayName: req.DisplayName})
	if err != nil {
		switch {
		case errors.Is(err, billingTaxonomy.ErrPricingScheduleNotFound):
			apires.RespondNotFound(c, "PRICING_SCHEDULE_NOT_FOUND")
		case errors.Is(err, billingTaxonomy.ErrPricingScheduleMetadataConflict):
			apires.RespondConflict(c, "PRICING_SCHEDULE_METADATA_CONFLICT")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "failed to update pricing schedule")
		}
		return
	}
	apires.RespondSuccess(c, gin.H{"id": updated.ID, "code": updated.Code, "display_name": updated.DisplayName, "metadata_version": updated.MetadataVersion, "updated_at": updated.UpdatedAt}, "pricing schedule metadata updated")
}

func (h *PricingScheduleHandler) CreatePricingScheduleVersion(c *gin.Context) {
	const op = "handler.pricing_schedule.create_version"
	actor, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	var req dto.CreatePricingScheduleVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "invalid pricing schedule version payload")
		return
	}
	brackets := make([]entity.ScalarBracketInput, len(req.Brackets))
	for i, bracket := range req.Brackets {
		brackets[i] = entity.ScalarBracketInput{RangeStartQuantity: bracket.RangeStartQuantity, RangeEndQuantity: bracket.RangeEndQuantity, PriceNumeratorMicroUnits: bracket.PriceNumeratorMicroUnits, PriceDenominatorQuantity: bracket.PriceDenominatorQuantity}
	}
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	created, err := h.service.CreatePricingScheduleVersion(ctx, entity.PricingScheduleVersionCreate{ScheduleCode: strings.TrimSpace(c.Param("code")), ExpectedLatestVersion: req.ExpectedLatestVersion, EffectiveFrom: req.EffectiveFrom, ChangeReason: req.ChangeReason, CreatedBy: actor, Brackets: brackets})
	if err != nil {
		switch {
		case errors.Is(err, billingTaxonomy.ErrPricingScheduleNotFound):
			apires.RespondNotFound(c, "PRICING_SCHEDULE_NOT_FOUND")
		case errors.Is(err, billingTaxonomy.ErrPricingScheduleVersionConflict), errors.Is(err, billingTaxonomy.ErrPricingScheduleEffectiveConflict):
			apires.RespondConflict(c, "PRICING_SCHEDULE_VERSION_CONFLICT")
		case errors.Is(err, billingTaxonomy.ErrInvalidArgument), errors.Is(err, billingTaxonomy.ErrInvalidPricingBrackets):
			apires.RespondBadRequest(c, "invalid pricing schedule version")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "failed to publish pricing schedule version")
		}
		return
	}
	apires.RespondCreated(c, pricingScheduleVersionResponse(*created), "pricing schedule version published")
}

func pricingScheduleVersionResponse(version entity.PricingScheduleVersion) gin.H {
	brackets := make([]gin.H, len(version.Brackets))
	for i, bracket := range version.Brackets {
		brackets[i] = gin.H{"id": bracket.ID, "range_start_quantity": bracket.RangeStartQuantity, "range_end_quantity": bracket.RangeEndQuantity, "price_numerator_micro_units": bracket.PriceNumeratorMicroUnits, "price_denominator_quantity": bracket.PriceDenominatorQuantity}
	}
	return gin.H{"id": version.ID, "pricing_schedule_id": version.PricingScheduleID, "version_number": version.VersionNumber, "pricing_model": version.PricingModel, "status": version.Status, "effective_from": version.EffectiveFrom, "effective_to": version.EffectiveTo, "checksum": version.Checksum, "brackets": brackets}
}
