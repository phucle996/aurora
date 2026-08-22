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
		items[i] = gin.H{"id": schedule.ID, "code": schedule.Code, "display_name": schedule.DisplayName, "charge_kind_code": schedule.ChargeKindCode, "pricing_model": schedule.PricingModel, "currency": schedule.Currency, "metadata_version": schedule.MetadataVersion, "status": schedule.Status, "created_at": schedule.CreatedAt, "updated_at": schedule.UpdatedAt}
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
	detail, brackets, err := h.service.GetPricingScheduleDetail(ctx, code)
	if err != nil {
		if errors.Is(err, billingTaxonomy.ErrPricingScheduleNotFound) {
			apires.RespondNotFound(c, "PRICING_SCHEDULE_NOT_FOUND")
		} else {
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "failed to retrieve pricing schedule")
		}
		return
	}
	var latestVersion any
	if detail.HasLatestVersion {
		latestVersion = pricingScheduleDetailVersionResponse(*detail, brackets)
	}
	apires.RespondSuccess(c, gin.H{"id": detail.ID, "code": detail.Code, "display_name": detail.DisplayName, "charge_kind_code": detail.ChargeKindCode, "pricing_model": detail.PricingModel, "currency": detail.Currency, "metadata_version": detail.MetadataVersion, "latest_version": latestVersion}, "pricing schedule")
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
	updated, err := h.service.UpdatePricingScheduleMetadata(ctx, entity.PricingScheduleMetadataCommand{ScheduleCode: strings.TrimSpace(c.Param("code")), MetadataVersion: req.MetadataVersion, DisplayName: req.DisplayName})
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

func pricingScheduleDetailVersionResponse(detail entity.PricingScheduleDetail, detailBrackets []entity.PricingScheduleDetailBracket) gin.H {
	brackets := make([]gin.H, len(detailBrackets))
	for index, bracket := range detailBrackets {
		var rangeEnd any
		if bracket.RangeEndQuantity != nil {
			rangeEnd = strconv.FormatInt(*bracket.RangeEndQuantity, 10)
		}
		brackets[index] = gin.H{"id": bracket.ID, "range_start_quantity": strconv.FormatInt(bracket.RangeStartQuantity, 10), "range_end_quantity": rangeEnd, "price_numerator_micro_units": strconv.FormatInt(bracket.PriceNumeratorMicroUnits, 10), "price_denominator_quantity": strconv.FormatInt(bracket.PriceDenominatorQuantity, 10)}
	}
	var effectiveTo any
	if detail.LatestEffectiveTo != nil {
		effectiveTo = detail.LatestEffectiveTo.UTC().Format(time.RFC3339Nano)
	}
	return gin.H{"id": detail.LatestVersionID, "pricing_schedule_id": detail.ID, "version_number": detail.LatestVersionNumber, "pricing_model": detail.LatestVersionPricingModel, "status": detail.LatestVersionStatus, "effective_from": detail.LatestEffectiveFrom.UTC().Format(time.RFC3339Nano), "effective_to": effectiveTo, "checksum": detail.LatestChecksum, "brackets": brackets}
}
