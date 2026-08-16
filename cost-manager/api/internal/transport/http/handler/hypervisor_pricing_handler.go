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

type HypervisorPricingHandler struct {
	estimateService   billingSvcInterface.HypervisorEstimateService
	adjustmentService billingSvcInterface.HypervisorZoneAdjustmentPublishService
}

func NewHypervisorPricingHandler(estimate billingSvcInterface.HypervisorEstimateService, adjustment billingSvcInterface.HypervisorZoneAdjustmentPublishService) *HypervisorPricingHandler {
	return &HypervisorPricingHandler{estimateService: estimate, adjustmentService: adjustment}
}

func (h *HypervisorPricingHandler) Estimate(c *gin.Context) {
	const op = "handler.hypervisor_pricing.estimate"
	cpu, cpuErr := strconv.ParseInt(strings.TrimSpace(c.Query("cpu_cores")), 10, 64)
	memory, memoryErr := strconv.ParseInt(strings.TrimSpace(c.Query("memory_mib")), 10, 64)
	disk, diskErr := strconv.ParseInt(strings.TrimSpace(c.Query("disk_gib")), 10, 64)
	if cpuErr != nil || memoryErr != nil || diskErr != nil || cpu < 1 || cpu > 1024 || memory < 1 || memory > 4_194_304 || disk < 1 || disk > 1_048_576 {
		apires.RespondBadRequest(c, "cpu_cores, memory_mib and disk_gib must be bounded positive decimal integers")
		return
	}
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 2*time.Second)
	defer cancel()
	estimate, err := h.estimateService.EstimateHypervisor(ctx, cpu, memory, disk, zoneID)
	if err != nil {
		if errors.Is(err, billingTaxonomy.ErrInvalidArgument) || errors.Is(err, billingTaxonomy.ErrInvalidPricingBrackets) {
			apires.RespondBadRequest(c, "invalid Hypervisor estimate request")
		} else {
			logger.HandlerError(c, op, err)
			apires.RespondServiceUnavailable(c, "Hypervisor pricing is not available")
		}
		return
	}
	apires.RespondSuccess(c, gin.H{
		"cpu_cores":                             strconv.FormatInt(estimate.CPUCores, 10),
		"memory_mib":                            strconv.FormatInt(estimate.MemoryMIB, 10),
		"disk_gib":                              strconv.FormatInt(estimate.DiskGIB, 10),
		"vcpu_hourly_micro_units":               strconv.FormatInt(estimate.VCPUHourlyMicroUnits, 10),
		"memory_hourly_micro_units":             strconv.FormatInt(estimate.MemoryHourlyMicroUnits, 10),
		"disk_hourly_micro_units":               strconv.FormatInt(estimate.DiskHourlyMicroUnits, 10),
		"hourly_estimate_micro_units":           strconv.FormatInt(estimate.HourlyMicroUnits, 10),
		"monthly_730_hour_estimate_micro_units": strconv.FormatInt(estimate.Monthly730HourMicroUnits, 10),
		"currency":                              estimate.Currency,
		"vcpu_schedule_code":                    estimate.VCPUScheduleCode,
		"vcpu_schedule_id":                      estimate.VCPUScheduleID,
		"vcpu_schedule_version_id":              estimate.VCPUScheduleVersionID,
		"vcpu_version":                          estimate.VCPUVersion,
		"vcpu_checksum":                         estimate.VCPUChecksum,
		"memory_schedule_code":                  estimate.MemoryScheduleCode,
		"memory_schedule_id":                    estimate.MemoryScheduleID,
		"memory_schedule_version_id":            estimate.MemoryScheduleVersionID,
		"memory_version":                        estimate.MemoryVersion,
		"memory_checksum":                       estimate.MemoryChecksum,
		"disk_schedule_code":                    estimate.DiskScheduleCode,
		"disk_schedule_id":                      estimate.DiskScheduleID,
		"disk_schedule_version_id":              estimate.DiskScheduleVersionID,
		"disk_version":                          estimate.DiskVersion,
		"disk_checksum":                         estimate.DiskChecksum,
		"rate_adjustment_id":                    estimate.RateAdjustmentID,
		"rate_adjustment_version":               estimate.RateAdjustmentVersion,
		"rate_adjustment_checksum":              estimate.RateAdjustmentChecksum,
		"rate_adjustment_numerator":             strconv.FormatInt(estimate.RateAdjustmentNumerator, 10),
		"rate_adjustment_denominator":           strconv.FormatInt(estimate.RateAdjustmentDenominator, 10),
		"estimated_at":                          estimate.EstimatedAt.UTC().Format(time.RFC3339Nano),
	}, "Hypervisor estimate")
}

func (h *HypervisorPricingHandler) CreateZonePriceAdjustment(c *gin.Context) {
	const op = "handler.hypervisor_pricing.create_zone_adjustment"
	actor, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}
	var req dto.CreateHypervisorZonePriceAdjustmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "invalid Hypervisor Zone price adjustment payload")
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
	created, err := h.adjustmentService.CreateHypervisorZonePriceAdjustment(ctx, entity.HypervisorZoneAdjustmentPublishCommand{
		ZoneID: zoneID, ExpectedLatestVersion: req.ExpectedLatestVersion,
		EffectiveFrom: req.EffectiveFrom, ChangeReason: req.ChangeReason, CreatedBy: actor,
		MultiplierNumerator: numerator, MultiplierDenominator: denominator,
	})
	if err != nil {
		switch {
		case errors.Is(err, billingTaxonomy.ErrHypervisorZoneAdjustmentConflict):
			apires.RespondConflict(c, "HYPERVISOR_ZONE_PRICE_ADJUSTMENT_VERSION_CONFLICT")
		case errors.Is(err, billingTaxonomy.ErrInvalidArgument):
			apires.RespondBadRequest(c, "invalid Hypervisor Zone price adjustment")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "failed to publish Hypervisor Zone price adjustment")
		}
		return
	}
	apires.RespondCreated(c, gin.H{
		"id": created.ID, "zone_id": created.ZoneID, "version_number": created.VersionNumber,
		"status": created.Status, "effective_from": created.EffectiveFrom.UTC().Format(time.RFC3339Nano),
		"effective_to":           nil,
		"multiplier_numerator":   strconv.FormatInt(created.MultiplierNumerator, 10),
		"multiplier_denominator": strconv.FormatInt(created.MultiplierDenominator, 10),
		"checksum":               created.Checksum,
	}, "Hypervisor Zone price adjustment published")
}
