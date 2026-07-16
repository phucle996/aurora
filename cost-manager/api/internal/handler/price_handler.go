package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"cost-manager/api/internal/domain/entity"
	"cost-manager/api/internal/domain/service"
	"cost-manager/api/internal/transport/dto"
	"cost-manager/api/pkg/apires"
	"cost-manager/api/pkg/apperr"
	"cost-manager/api/pkg/logger"
	"cost-manager/api/pkg/pkgcontext"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type PriceHandler struct {
	billingSvc service.BillingService
}

func NewPriceHandler(billingSvc service.BillingService) *PriceHandler {
	return &PriceHandler{billingSvc: billingSvc}
}

func (h *PriceHandler) ListPrices(c *gin.Context) {
	const op = "handler.price.list_prices"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	list, err := h.billingSvc.ListPrices(ctx)
	if err != nil {
		logger.HandlerError(c, op, err)
		appErr, ok := apperr.As(err)
		if ok {
			if errors.Is(appErr.Kind, apperr.ErrPriceNotFound) {
				apires.RespondNotFound(c, appErr.Outcome)
				return
			}
			if errors.Is(appErr.Kind, apperr.ErrBadRequest) {
				apires.RespondBadRequest(c, appErr.Outcome)
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "failed to list prices",
		})
		return
	}

	res := make([]gin.H, len(list))
	for i, p := range list {
		res[i] = gin.H{
			"id":             p.ID,
			"service_type":   p.ServiceType,
			"metric_type":    p.MetricType,
			"zone_code":      p.ZoneCode,
			"unit":           p.Unit,
			"unit_price":     p.UnitPrice,
			"currency":       p.Currency,
			"tier":           p.Tier,
			"free_quota":     p.FreeQuota,
			"effective_from": p.EffectiveFrom,
			"effective_to":   p.EffectiveTo,
			"created_at":     p.CreatedAt,
		}
	}

	apires.RespondSuccess(c, res, "ok")
}

func (h *PriceHandler) SavePrice(c *gin.Context) {
	const op = "handler.price.save_price"
	var req dto.SavePriceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, err.Error())
		return
	}

	var priceID uuid.UUID
	if req.ID != "" {
		var err error
		priceID, err = uuid.Parse(req.ID)
		if err != nil {
			apires.RespondBadRequest(c, "invalid ID format")
			return
		}
	}

	effFrom := req.EffectiveFrom
	if effFrom.IsZero() {
		effFrom = time.Now()
	}

	p := &entity.Price{
		ID:            priceID,
		ServiceType:   entity.ServiceType(req.ServiceType),
		MetricType:    entity.MetricType(req.MetricType),
		ZoneCode:      req.ZoneCode,
		Unit:          entity.UnitType(req.Unit),
		UnitPrice:     req.UnitPrice,
		Currency:      req.Currency,
		Tier:          entity.TierType(req.Tier),
		FreeQuota:     req.FreeQuota,
		EffectiveFrom: effFrom,
		EffectiveTo:   req.EffectiveTo,
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	err := h.billingSvc.CreateOrUpdatePrice(ctx, p)
	if err != nil {
		logger.HandlerError(c, op, err)
		appErr, ok := apperr.As(err)
		if ok {
			if errors.Is(appErr.Kind, apperr.ErrPriceNotFound) {
				apires.RespondNotFound(c, appErr.Outcome)
				return
			}
			if errors.Is(appErr.Kind, apperr.ErrBadRequest) {
				apires.RespondBadRequest(c, appErr.Outcome)
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "failed to save price",
		})
		return
	}

	logger.HandlerInfo(c, op, "Successfully saved price rate configuration")
	apires.RespondCreated(c, gin.H{
		"id":             p.ID,
		"service_type":   p.ServiceType,
		"metric_type":    p.MetricType,
		"zone_code":      p.ZoneCode,
		"unit":           p.Unit,
		"unit_price":     p.UnitPrice,
		"currency":       p.Currency,
		"tier":           p.Tier,
		"free_quota":     p.FreeQuota,
		"effective_from": p.EffectiveFrom,
		"effective_to":   p.EffectiveTo,
		"created_at":     p.CreatedAt,
	}, "price saved")
}
