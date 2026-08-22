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

type StoragePricingHandler struct {
	service billingSvcInterface.StoragePricingService
}

func NewStoragePricingHandler(service billingSvcInterface.StoragePricingService) *StoragePricingHandler {
	return &StoragePricingHandler{service: service}
}

func (h *StoragePricingHandler) ListZonePriceAdjustments(c *gin.Context) {
	const op = "handler.storage_pricing.list_zone_adjustments"
	limit := 100
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			apires.RespondBadRequest(c, "limit must be an integer between 1 and 100")
			return
		}
		limit = parsed
	}
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	result, err := h.service.ListStorageZonePriceAdjustments(ctx, entity.StorageZoneAdjustmentListQuery{ZoneID: zoneID, Limit: limit})
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "failed to retrieve Storage Zone price adjustments")
		return
	}
	items := make([]gin.H, len(result.Items))
	for index, item := range result.Items {
		var effectiveTo *string
		if item.EffectiveTo != nil {
			formatted := item.EffectiveTo.UTC().Format(time.RFC3339Nano)
			effectiveTo = &formatted
		}
		items[index] = gin.H{
			"id": item.ID, "zone_id": item.ZoneID, "version_number": item.VersionNumber,
			"status": item.Status, "effective_from": item.EffectiveFrom.UTC().Format(time.RFC3339Nano),
			"effective_to": effectiveTo, "multiplier_numerator": strconv.FormatInt(item.MultiplierNumerator, 10),
			"multiplier_denominator": strconv.FormatInt(item.MultiplierDenominator, 10), "checksum": item.Checksum,
			"change_reason": item.ChangeReason, "created_by": item.CreatedBy,
			"created_at": item.CreatedAt.UTC().Format(time.RFC3339Nano), "is_latest": item.IsLatest,
			"is_effective": item.IsEffective,
		}
	}
	apires.RespondSuccess(c, gin.H{
		"zone_id": result.ZoneID, "adjustments": items, "has_more": result.HasMore,
		"observed_at": result.ObservedAt.UTC().Format(time.RFC3339Nano),
	}, "Storage Zone price adjustments")
}

func (h *StoragePricingHandler) Estimate(c *gin.Context) {
	const op = "handler.storage_pricing.estimate"
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
	apires.RespondSuccess(c, gin.H{"capacity_bytes": strconv.FormatInt(estimate.CapacityBytes, 10), "hourly_estimate_micro_units": strconv.FormatInt(estimate.HourlyMicroUnits, 10), "currency": estimate.Currency, "pricing_schedule_code": estimate.PricingScheduleCode, "pricing_schedule_id": estimate.PricingScheduleID, "pricing_schedule_version_id": estimate.PricingScheduleVersionID, "pricing_version": estimate.PricingVersion, "pricing_checksum": estimate.PricingChecksum, "pricing_effective_from": estimate.PricingEffectiveFrom, "rate_adjustment_id": estimate.RateAdjustmentID, "rate_adjustment_version": estimate.RateAdjustmentVersion, "rate_adjustment_checksum": estimate.RateAdjustmentChecksum, "rate_adjustment_numerator": strconv.FormatInt(estimate.RateAdjustmentNumerator, 10), "rate_adjustment_denominator": strconv.FormatInt(estimate.RateAdjustmentDenominator, 10), "estimated_at": estimate.EstimatedAt}, "storage estimate")
}

func (h *StoragePricingHandler) CreateBasePriceVersion(c *gin.Context) {
	const op = "handler.storage_pricing.create_base_version"
	actor, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	var req dto.CreateStorageBasePriceVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "invalid Storage base price version payload")
		return
	}
	expectedLatestVersion, err := strconv.ParseInt(req.ExpectedLatestVersion.String(), 10, 32)
	if err != nil || expectedLatestVersion < 0 {
		apires.RespondBadRequest(c, "expected_latest_version must be a non-negative integer")
		return
	}
	brackets := make([]entity.StorageBasePricePublishBracket, len(req.Brackets))
	for index, bracket := range req.Brackets {
		start, err := strconv.ParseInt(strings.TrimSpace(bracket.RangeStartQuantity), 10, 64)
		if err != nil {
			apires.RespondBadRequest(c, "Storage price bracket BIGINT fields must be decimal strings within int64 range")
			return
		}
		var end *int64
		if bracket.RangeEndQuantity != nil {
			parsedEnd, err := strconv.ParseInt(strings.TrimSpace(*bracket.RangeEndQuantity), 10, 64)
			if err != nil {
				apires.RespondBadRequest(c, "Storage price bracket BIGINT fields must be decimal strings within int64 range")
				return
			}
			end = &parsedEnd
		}
		numerator, numeratorErr := strconv.ParseInt(strings.TrimSpace(bracket.PriceNumeratorMicroUnits), 10, 64)
		denominator, denominatorErr := strconv.ParseInt(strings.TrimSpace(bracket.PriceDenominatorQuantity), 10, 64)
		if numeratorErr != nil || denominatorErr != nil {
			apires.RespondBadRequest(c, "Storage price bracket BIGINT fields must be decimal strings within int64 range")
			return
		}
		brackets[index] = entity.StorageBasePricePublishBracket{
			RangeStartQuantity: start, RangeEndQuantity: end,
			PriceNumeratorMicroUnits: numerator, PriceDenominatorQuantity: denominator,
		}
	}
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	published, publishedBrackets, err := h.service.CreateStorageBasePriceVersion(ctx, entity.StorageBasePricePublishCommand{
		ScheduleCode: strings.TrimSpace(c.Param("code")), ExpectedLatestVersion: int(expectedLatestVersion),
		EffectiveFrom: req.EffectiveFrom, ChangeReason: req.ChangeReason, CreatedBy: actor,
	}, brackets)
	if err != nil {
		switch {
		case errors.Is(err, billingTaxonomy.ErrPricingScheduleNotFound):
			apires.RespondNotFound(c, "STORAGE_PRICING_SCHEDULE_NOT_FOUND")
		case errors.Is(err, billingTaxonomy.ErrPricingScheduleVersionConflict), errors.Is(err, billingTaxonomy.ErrPricingScheduleEffectiveConflict):
			apires.RespondConflict(c, "STORAGE_PRICING_SCHEDULE_VERSION_CONFLICT")
		case errors.Is(err, billingTaxonomy.ErrInvalidArgument), errors.Is(err, billingTaxonomy.ErrInvalidPricingBrackets):
			apires.RespondBadRequest(c, "invalid Storage base price version")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "failed to publish Storage base price version")
		}
		return
	}
	responseBrackets := make([]gin.H, len(publishedBrackets))
	for index, bracket := range publishedBrackets {
		var rangeEnd any
		if bracket.RangeEndQuantity != nil {
			rangeEnd = strconv.FormatInt(*bracket.RangeEndQuantity, 10)
		}
		responseBrackets[index] = gin.H{
			"id": bracket.ID, "range_start_quantity": strconv.FormatInt(bracket.RangeStartQuantity, 10),
			"range_end_quantity":          rangeEnd,
			"price_numerator_micro_units": strconv.FormatInt(bracket.PriceNumeratorMicroUnits, 10),
			"price_denominator_quantity":  strconv.FormatInt(bracket.PriceDenominatorQuantity, 10),
		}
	}
	var effectiveTo any
	if published.EffectiveTo != nil {
		effectiveTo = published.EffectiveTo.UTC().Format(time.RFC3339Nano)
	}
	apires.RespondCreated(c, gin.H{
		"id": published.ID, "pricing_schedule_id": published.PricingScheduleID,
		"charge_kind_code": published.ChargeKindCode, "version_number": published.VersionNumber,
		"pricing_model": published.PricingModel, "status": published.Status,
		"effective_from": published.EffectiveFrom.UTC().Format(time.RFC3339Nano),
		"effective_to":   effectiveTo, "checksum": published.Checksum, "brackets": responseBrackets,
	}, "Storage base price version published")
}

func (h *StoragePricingHandler) CreateZonePriceAdjustment(c *gin.Context) {
	const op = "handler.storage_pricing.create_zone_adjustment"
	actor, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}
	var req dto.CreateStorageZonePriceAdjustmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "invalid Storage Zone price adjustment payload")
		return
	}
	expectedLatestVersion, err := strconv.ParseInt(req.ExpectedLatestVersion.String(), 10, 32)
	if err != nil || expectedLatestVersion < 0 {
		apires.RespondBadRequest(c, "expected_latest_version must be a non-negative integer")
		return
	}
	numerator, numeratorErr := strconv.ParseInt(strings.TrimSpace(req.MultiplierNumerator), 10, 64)
	denominator, denominatorErr := strconv.ParseInt(strings.TrimSpace(req.MultiplierDenominator), 10, 64)
	if numeratorErr != nil || denominatorErr != nil {
		apires.RespondBadRequest(c, "multiplier BIGINT fields must be decimal strings within int64 range")
		return
	}
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	created, err := h.service.CreateStorageZonePriceAdjustment(ctx, entity.StorageZoneAdjustmentPublishCommand{
		ZoneID: zoneID, ExpectedLatestVersion: int(expectedLatestVersion),
		EffectiveFrom: req.EffectiveFrom, ChangeReason: req.ChangeReason, CreatedBy: actor,
		MultiplierNumerator: numerator, MultiplierDenominator: denominator,
	})
	if err != nil {
		switch {
		case errors.Is(err, billingTaxonomy.ErrStorageZoneAdjustmentConflict):
			apires.RespondConflict(c, "STORAGE_ZONE_PRICE_ADJUSTMENT_VERSION_CONFLICT")
		case errors.Is(err, billingTaxonomy.ErrInvalidArgument):
			apires.RespondBadRequest(c, "invalid Storage Zone price adjustment")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "failed to publish Storage Zone price adjustment")
		}
		return
	}
	apires.RespondCreated(c, gin.H{
		"id": created.ID, "zone_id": created.ZoneID, "version_number": created.VersionNumber,
		"status": created.Status, "effective_from": created.EffectiveFrom.UTC().Format(time.RFC3339Nano),
		"effective_to": nil, "multiplier_numerator": strconv.FormatInt(created.MultiplierNumerator, 10),
		"multiplier_denominator": strconv.FormatInt(created.MultiplierDenominator, 10), "checksum": created.Checksum,
	}, "Storage Zone price adjustment published")
}
