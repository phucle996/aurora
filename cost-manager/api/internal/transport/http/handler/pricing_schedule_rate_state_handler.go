package handler

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	billingSvcInterface "cost-manager/api/internal/domain/service"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"cost-manager/api/pkg/apires"
	"cost-manager/api/pkg/logger"
	"cost-manager/api/pkg/pkgcontext"
	"github.com/gin-gonic/gin"
)

type PricingScheduleRateStateHandler struct {
	service billingSvcInterface.PricingScheduleRateStateService
}

func NewPricingScheduleRateStateHandler(service billingSvcInterface.PricingScheduleRateStateService) *PricingScheduleRateStateHandler {
	return &PricingScheduleRateStateHandler{service: service}
}

func (h *PricingScheduleRateStateHandler) GetPricingScheduleRateState(c *gin.Context) {
	const op = "handler.pricing_schedule_rate_state.get"
	code := strings.TrimSpace(c.Param("code"))
	if code == "" {
		apires.RespondBadRequest(c, "invalid pricing schedule code")
		return
	}
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	rows, err := h.service.GetPricingScheduleRateState(ctx, code)
	if err != nil {
		if errors.Is(err, billingTaxonomy.ErrPricingScheduleNotFound) {
			apires.RespondNotFound(c, "PRICING_SCHEDULE_NOT_FOUND")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "failed to retrieve pricing schedule rate state")
		return
	}

	first := rows[0]
	versions := map[string]gin.H{}
	for _, row := range rows {
		if row.VersionRole == nil || row.VersionID == nil || row.VersionNumber == nil || row.VersionStatus == nil || row.EffectiveFrom == nil || row.Checksum == nil || row.ChangeReason == nil {
			continue
		}
		version, exists := versions[*row.VersionRole]
		if !exists {
			var effectiveTo any
			if row.EffectiveTo != nil {
				effectiveTo = row.EffectiveTo.UTC().Format(time.RFC3339Nano)
			}
			version = gin.H{
				"id":                  row.VersionID,
				"pricing_schedule_id": row.ScheduleID,
				"version_number":      row.VersionNumber,
				"pricing_model":       row.PricingModel,
				"status":              row.VersionStatus,
				"effective_from":      row.EffectiveFrom.UTC().Format(time.RFC3339Nano),
				"effective_to":        effectiveTo,
				"checksum":            row.Checksum,
				"change_reason":       row.ChangeReason,
				"brackets":            []gin.H{},
			}
		}
		if row.BracketID != nil && row.RangeStartQuantity != nil && row.PriceNumerator != nil && row.PriceDenominator != nil {
			brackets := version["brackets"].([]gin.H)
			var rangeEnd any
			if row.RangeEndQuantity != nil {
				rangeEnd = strconv.FormatInt(*row.RangeEndQuantity, 10)
			}
			version["brackets"] = append(brackets, gin.H{
				"id":                          row.BracketID,
				"range_start_quantity":        strconv.FormatInt(*row.RangeStartQuantity, 10),
				"range_end_quantity":          rangeEnd,
				"price_numerator_micro_units": strconv.FormatInt(*row.PriceNumerator, 10),
				"price_denominator_quantity":  strconv.FormatInt(*row.PriceDenominator, 10),
			})
		}
		versions[*row.VersionRole] = version
	}
	var effectiveVersion, nextScheduledVersion any
	if version, ok := versions["EFFECTIVE"]; ok {
		effectiveVersion = version
	}
	if version, ok := versions["NEXT_SCHEDULED"]; ok {
		nextScheduledVersion = version
	}
	var latestVersionNumber any
	if first.LatestVersionNumber != nil {
		latestVersionNumber = *first.LatestVersionNumber
	}
	apires.RespondSuccess(c, gin.H{
		"id":                     first.ScheduleID,
		"code":                   first.Code,
		"display_name":           first.DisplayName,
		"charge_kind_code":       first.ChargeKindCode,
		"pricing_model":          first.PricingModel,
		"currency":               first.Currency,
		"metadata_version":       first.MetadataVersion,
		"observed_at":            first.ObservedAt.UTC().Format(time.RFC3339Nano),
		"latest_version_number":  latestVersionNumber,
		"effective_version":      effectiveVersion,
		"next_scheduled_version": nextScheduledVersion,
	}, "pricing schedule rate state")
}
