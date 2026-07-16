package handler

import (
	domainservice "cost-manager/api/internal/domain/service"
	"cost-manager/api/pkg/apperr"
	"cost-manager/api/pkg/apires"
	"cost-manager/api/pkg/logger"
	"cost-manager/api/internal/transport/dto"

	"errors"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type SubscriptionHandler struct {
	planSvc domainservice.PlanService
}

func NewSubscriptionHandler(planSvc domainservice.PlanService) *SubscriptionHandler {
	return &SubscriptionHandler{planSvc: planSvc}
}

// GetActiveSubscription GET /api/v1/billing/subscriptions/active?owner_id=&owner_type=
func (h *SubscriptionHandler) GetActiveSubscription(c *gin.Context) {
	const op = "handler.subscription.get_active"
	ownerIDStr := c.Query("owner_id")
	ownerType := c.Query("owner_type")

	if ownerIDStr == "" || ownerType == "" {
		apires.RespondBadRequest(c, "missing owner_id or owner_type")
		return
	}
	ownerID, err := uuid.Parse(ownerIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid owner_id format")
		return
	}

	sub, err := h.planSvc.GetActiveSubscription(c.Request.Context(), ownerID, ownerType)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "failed to get subscription")
		return
	}
	if sub == nil {
		apires.RespondSuccess(c, nil, "no active subscription")
		return
	}
	apires.RespondSuccess(c, dto.ToSubscriptionResponse(*sub), "ok")
}

// Subscribe POST /api/v1/billing/subscriptions
type SubscribeRequest struct {
	OwnerID   string `json:"owner_id" binding:"required"`
	OwnerType string `json:"owner_type" binding:"required"`
	PlanID    string `json:"plan_id" binding:"required"`
}

func (h *SubscriptionHandler) Subscribe(c *gin.Context) {
	const op = "handler.subscription.subscribe"
	var req SubscribeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, err.Error())
		return
	}

	ownerID, err := uuid.Parse(req.OwnerID)
	if err != nil {
		apires.RespondBadRequest(c, "invalid owner_id format")
		return
	}
	planID, err := uuid.Parse(req.PlanID)
	if err != nil {
		apires.RespondBadRequest(c, "invalid plan_id format")
		return
	}

	sub, err := h.planSvc.Subscribe(c.Request.Context(), ownerID, req.OwnerType, planID)
	if err != nil {
		appErr, ok := apperr.As(err)
		if ok {
			switch {
			case errors.Is(appErr.Kind, apperr.ErrInsufficientFunds):
				apires.RespondBadRequest(c, "insufficient wallet balance to subscribe")
				return
			case errors.Is(appErr.Kind, apperr.ErrBadRequest):
				apires.RespondBadRequest(c, appErr.Outcome)
				return
			}
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "failed to subscribe")
		return
	}

	logger.HandlerInfo(c, op, "Successfully subscribed owner to plan: "+planID.String())
	apires.RespondCreated(c, dto.ToSubscriptionResponse(*sub), "subscription created")
}

// CancelSubscription DELETE /api/v1/billing/subscriptions/active
func (h *SubscriptionHandler) CancelSubscription(c *gin.Context) {
	const op = "handler.subscription.cancel"
	ownerIDStr := c.Query("owner_id")
	ownerType := c.Query("owner_type")

	if ownerIDStr == "" || ownerType == "" {
		apires.RespondBadRequest(c, "missing owner_id or owner_type")
		return
	}
	ownerID, err := uuid.Parse(ownerIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid owner_id format")
		return
	}

	if err := h.planSvc.CancelSubscription(c.Request.Context(), ownerID, ownerType); err != nil {
		appErr, ok := apperr.As(err)
		if ok && errors.Is(appErr.Kind, apperr.ErrBadRequest) {
			apires.RespondBadRequest(c, appErr.Outcome)
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "failed to cancel subscription")
		return
	}

	logger.HandlerInfo(c, op, "Successfully cancelled subscription for owner: "+ownerIDStr)
	apires.RespondSuccess(c, nil, "subscription cancelled")
}
