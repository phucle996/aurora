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

type MailPricingHandler struct {
	estimateService       billingSvcInterface.MailEstimateService
	adjustmentService     billingSvcInterface.MailZoneAdjustmentPublishService
	adjustmentListService billingSvcInterface.MailZoneAdjustmentListService
}

func NewMailPricingHandler(estimate billingSvcInterface.MailEstimateService, adjustment billingSvcInterface.MailZoneAdjustmentPublishService, adjustmentList billingSvcInterface.MailZoneAdjustmentListService) *MailPricingHandler {
	return &MailPricingHandler{estimateService: estimate, adjustmentService: adjustment, adjustmentListService: adjustmentList}
}

func (h *MailPricingHandler) ListZonePriceAdjustments(c *gin.Context) {
	const op = "handler.mail_pricing.list_zone_adjustments"
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
	result, err := h.adjustmentListService.ListMailZonePriceAdjustments(ctx, entity.MailZoneAdjustmentListQuery{ZoneID: zoneID, Limit: limit})
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "failed to retrieve Mail Zone price adjustments")
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
	}, "Mail Zone price adjustments")
}

func (h *MailPricingHandler) Estimate(c *gin.Context) {
	const op = "handler.mail_pricing.estimate"
	quantity, err := strconv.ParseInt(strings.TrimSpace(c.Query("recipient_quantity")), 10, 64)
	if err != nil || quantity < 1 || quantity > 1_000_000_000 {
		apires.RespondBadRequest(c, "recipient_quantity must be a positive decimal integer no larger than 1000000000")
		return
	}
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 2*time.Second)
	defer cancel()
	estimate, err := h.estimateService.EstimateMail(ctx, quantity, zoneID)
	if err != nil {
		if errors.Is(err, billingTaxonomy.ErrInvalidArgument) || errors.Is(err, billingTaxonomy.ErrInvalidPricingBrackets) {
			apires.RespondBadRequest(c, "invalid Mail estimate request")
		} else {
			logger.HandlerError(c, op, err)
			apires.RespondServiceUnavailable(c, "Mail pricing is not available")
		}
		return
	}
	apires.RespondSuccess(c, gin.H{
		"recipient_quantity":          strconv.FormatInt(estimate.RecipientQuantity, 10),
		"estimate_micro_units":        strconv.FormatInt(estimate.EstimateMicroUnits, 10),
		"currency":                    estimate.Currency,
		"pricing_schedule_code":       estimate.PricingScheduleCode,
		"pricing_schedule_id":         estimate.PricingScheduleID,
		"pricing_schedule_version_id": estimate.PricingScheduleVersionID,
		"pricing_version":             estimate.PricingVersion,
		"pricing_checksum":            estimate.PricingChecksum,
		"pricing_effective_from":      estimate.PricingEffectiveFrom.UTC().Format(time.RFC3339Nano),
		"rate_adjustment_id":          estimate.RateAdjustmentID,
		"rate_adjustment_version":     estimate.RateAdjustmentVersion,
		"rate_adjustment_checksum":    estimate.RateAdjustmentChecksum,
		"rate_adjustment_numerator":   strconv.FormatInt(estimate.RateAdjustmentNumerator, 10),
		"rate_adjustment_denominator": strconv.FormatInt(estimate.RateAdjustmentDenominator, 10),
		"estimated_at":                estimate.EstimatedAt.UTC().Format(time.RFC3339Nano),
	}, "Mail accepted-recipient estimate")
}

func (h *MailPricingHandler) CreateZonePriceAdjustment(c *gin.Context) {
	const op = "handler.mail_pricing.create_zone_adjustment"
	actor, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}
	var req dto.CreateMailZonePriceAdjustmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "invalid Mail Zone price adjustment payload")
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
	created, err := h.adjustmentService.CreateMailZonePriceAdjustment(ctx, entity.MailZoneAdjustmentPublishCommand{ZoneID: zoneID, ExpectedLatestVersion: req.ExpectedLatestVersion, EffectiveFrom: req.EffectiveFrom, ChangeReason: req.ChangeReason, CreatedBy: actor, MultiplierNumerator: numerator, MultiplierDenominator: denominator})
	if err != nil {
		switch {
		case errors.Is(err, billingTaxonomy.ErrMailZoneAdjustmentConflict):
			apires.RespondConflict(c, "MAIL_ZONE_PRICE_ADJUSTMENT_VERSION_CONFLICT")
		case errors.Is(err, billingTaxonomy.ErrInvalidArgument):
			apires.RespondBadRequest(c, "invalid Mail Zone price adjustment")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "failed to publish Mail Zone price adjustment")
		}
		return
	}
	apires.RespondCreated(c, gin.H{
		"id": created.ID, "zone_id": created.ZoneID, "version_number": created.VersionNumber,
		"status": created.Status, "effective_from": created.EffectiveFrom.UTC().Format(time.RFC3339Nano),
		"effective_to": nil, "multiplier_numerator": strconv.FormatInt(created.MultiplierNumerator, 10),
		"multiplier_denominator": strconv.FormatInt(created.MultiplierDenominator, 10), "checksum": created.Checksum,
	}, "Mail Zone price adjustment published")
}
