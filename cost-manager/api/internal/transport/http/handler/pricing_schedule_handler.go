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
	listService     billingSvcInterface.PricingScheduleListService
	detailService   billingSvcInterface.PricingScheduleDetailService
	metadataService billingSvcInterface.PricingScheduleMetadataService
	publishService  billingSvcInterface.PricingScheduleVersionPublishService
}

func NewPricingScheduleHandler(list billingSvcInterface.PricingScheduleListService, detail billingSvcInterface.PricingScheduleDetailService, metadata billingSvcInterface.PricingScheduleMetadataService, publish billingSvcInterface.PricingScheduleVersionPublishService) *PricingScheduleHandler {
	return &PricingScheduleHandler{listService: list, detailService: detail, metadataService: metadata, publishService: publish}
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
	schedules, total, err := h.listService.GetPricingSchedules(ctx, req.Page, req.Limit, entity.ChargeKindCode(strings.TrimSpace(req.ChargeKind)), req.Search)
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
	detail, brackets, err := h.detailService.GetPricingScheduleDetail(ctx, code)
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
	updated, err := h.metadataService.UpdatePricingScheduleMetadata(ctx, entity.PricingScheduleMetadataCommand{ScheduleCode: strings.TrimSpace(c.Param("code")), MetadataVersion: req.MetadataVersion, DisplayName: req.DisplayName})
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
	brackets := make([]entity.PricingScheduleVersionPublishBracket, len(req.Brackets))
	for i, bracket := range req.Brackets {
		parsed, err := parsePricingBracketRequest(bracket)
		if err != nil {
			apires.RespondBadRequest(c, "pricing bracket BIGINT fields must be decimal strings within int64 range")
			return
		}
		brackets[i] = parsed
	}
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	created, publishedBrackets, err := h.publishService.CreatePricingScheduleVersion(ctx, entity.PricingScheduleVersionPublishCommand{ScheduleCode: strings.TrimSpace(c.Param("code")), ExpectedLatestVersion: *req.ExpectedLatestVersion, EffectiveFrom: req.EffectiveFrom, ChangeReason: req.ChangeReason, CreatedBy: actor}, brackets)
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
	apires.RespondCreated(c, pricingScheduleVersionResponse(*created, publishedBrackets), "pricing schedule version published")
}

func pricingScheduleVersionResponse(version entity.PricingScheduleVersionPublished, publishedBrackets []entity.PricingScheduleVersionPublishBracket) gin.H {
	brackets := make([]gin.H, len(publishedBrackets))
	for i, bracket := range publishedBrackets {
		var rangeEnd any
		if bracket.RangeEndQuantity != nil {
			rangeEnd = strconv.FormatInt(*bracket.RangeEndQuantity, 10)
		}
		brackets[i] = gin.H{"id": bracket.ID, "range_start_quantity": strconv.FormatInt(bracket.RangeStartQuantity, 10), "range_end_quantity": rangeEnd, "price_numerator_micro_units": strconv.FormatInt(bracket.PriceNumeratorMicroUnits, 10), "price_denominator_quantity": strconv.FormatInt(bracket.PriceDenominatorQuantity, 10)}
	}
	var effectiveTo any
	if version.EffectiveTo != nil {
		effectiveTo = version.EffectiveTo.UTC().Format(time.RFC3339Nano)
	}
	return gin.H{"id": version.ID, "pricing_schedule_id": version.PricingScheduleID, "version_number": version.VersionNumber, "pricing_model": version.PricingModel, "status": version.Status, "effective_from": version.EffectiveFrom.UTC().Format(time.RFC3339Nano), "effective_to": effectiveTo, "checksum": version.Checksum, "brackets": brackets}
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

// The REST boundary uses decimal strings so browser JSON never rounds BIGINT
// pricing values. Parsing remains private to this publish workflow; domain and
// PostgreSQL values stay signed int64.
func parsePricingBracketRequest(bracket dto.CreateScalarBracketRequest) (entity.PricingScheduleVersionPublishBracket, error) {
	start, err := strconv.ParseInt(strings.TrimSpace(bracket.RangeStartQuantity), 10, 64)
	if err != nil {
		return entity.PricingScheduleVersionPublishBracket{}, err
	}
	var end *int64
	if bracket.RangeEndQuantity != nil {
		parsedEnd, err := strconv.ParseInt(strings.TrimSpace(*bracket.RangeEndQuantity), 10, 64)
		if err != nil {
			return entity.PricingScheduleVersionPublishBracket{}, err
		}
		end = &parsedEnd
	}
	numerator, err := strconv.ParseInt(strings.TrimSpace(bracket.PriceNumeratorMicroUnits), 10, 64)
	if err != nil {
		return entity.PricingScheduleVersionPublishBracket{}, err
	}
	denominator, err := strconv.ParseInt(strings.TrimSpace(bracket.PriceDenominatorQuantity), 10, 64)
	if err != nil {
		return entity.PricingScheduleVersionPublishBracket{}, err
	}
	return entity.PricingScheduleVersionPublishBracket{RangeStartQuantity: start, RangeEndQuantity: end, PriceNumeratorMicroUnits: numerator, PriceDenominatorQuantity: denominator}, nil
}
