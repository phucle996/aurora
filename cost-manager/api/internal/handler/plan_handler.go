package handler

import (
	"errors"

	domainservice "cost-manager/api/internal/domain/service"
	"cost-manager/api/internal/domain/entity"
	"cost-manager/api/pkg/apperr"
	"cost-manager/api/pkg/apires"
	"cost-manager/api/pkg/logger"
	"cost-manager/api/internal/transport/dto"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type PlanHandler struct {
	planSvc domainservice.PlanService
}

func NewPlanHandler(planSvc domainservice.PlanService) *PlanHandler {
	return &PlanHandler{planSvc: planSvc}
}

// ListPlans GET /api/v1/billing/plans
func (h *PlanHandler) ListPlans(c *gin.Context) {
	const op = "handler.plan.list_plans"
	plans, err := h.planSvc.ListPlans(c.Request.Context())
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "failed to list plans")
		return
	}
	logger.HandlerInfo(c, op, "Successfully listed plans")
	apires.RespondSuccess(c, dto.ToPlanListResponse(plans), "ok")
}

// GetPlan GET /api/v1/billing/plans/:id
func (h *PlanHandler) GetPlan(c *gin.Context) {
	const op = "handler.plan.get_plan"
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		apires.RespondBadRequest(c, "invalid plan id format")
		return
	}
	plan, err := h.planSvc.GetPlan(c.Request.Context(), id)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondNotFound(c, "plan not found")
		return
	}
	apires.RespondSuccess(c, dto.ToPlanResponse(*plan), "ok")
}

// CreatePlan POST /api/v1/billing/plans
type CreatePlanRequest struct {
	Name         string              `json:"name" binding:"required"`
	Code         string              `json:"code" binding:"required"`
	ServiceType  string              `json:"service_type" binding:"required"`
	ZoneCode     string              `json:"zone_code"`
	MonthlyPrice float64             `json:"monthly_price" binding:"required,gte=0"`
	Currency     string              `json:"currency"`
	Description  string              `json:"description"`
	Metrics      []CreateMetricInput `json:"metrics" binding:"required,min=1"`
}

type CreateMetricInput struct {
	MetricType string  `json:"metric_type" binding:"required"`
	Quota      float64 `json:"quota" binding:"required,gte=0"`
	Unit       string  `json:"unit" binding:"required"`
}

func (h *PlanHandler) CreatePlan(c *gin.Context) {
	const op = "handler.plan.create_plan"
	var req CreatePlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, err.Error())
		return
	}

	currency := req.Currency
	if currency == "" {
		currency = "VND"
	}
	zoneCode := req.ZoneCode
	if zoneCode == "" {
		zoneCode = "global"
	}

	// Map request → domain entity
	plan := &entity.Plan{
		Name:         req.Name,
		Code:         req.Code,
		ServiceType:  entity.ServiceType(req.ServiceType),
		ZoneCode:     zoneCode,
		MonthlyPrice: req.MonthlyPrice,
		Currency:     currency,
		Description:  req.Description,
		Status:       entity.PlanActive,
	}
	for _, m := range req.Metrics {
		plan.Metrics = append(plan.Metrics, entity.PlanMetric{
			MetricType: entity.MetricType(m.MetricType),
			Quota:      m.Quota,
			Unit:       entity.UnitType(m.Unit),
		})
	}

	if err := h.planSvc.CreatePlan(c.Request.Context(), plan); err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "failed to create plan")
		return
	}

	logger.HandlerInfo(c, op, "Successfully created plan: "+plan.Code)
	apires.RespondCreated(c, dto.ToPlanResponse(*plan), "plan created")
}

// UpdatePlanStatus PATCH /api/v1/billing/plans/:id/status
type UpdateStatusRequest struct {
	Status string `json:"status" binding:"required,oneof=ACTIVE DEPRECATED"`
}

func (h *PlanHandler) UpdatePlanStatus(c *gin.Context) {
	const op = "handler.plan.update_status"
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		apires.RespondBadRequest(c, "invalid plan id format")
		return
	}
	var req UpdateStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, err.Error())
		return
	}
	if err := h.planSvc.UpdatePlanStatus(c.Request.Context(), id, req.Status); err != nil {
		appErr, ok := apperr.As(err)
		if ok && errors.Is(appErr.Kind, apperr.ErrPriceNotFound) {
			apires.RespondNotFound(c, "plan not found")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "failed to update plan status")
		return
	}
	logger.HandlerInfo(c, op, "Updated plan status to "+req.Status)
	apires.RespondSuccess(c, nil, "status updated")
}
