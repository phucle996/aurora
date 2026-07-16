package handler

import (
	"errors"
	"net/http"
	"time"

	"cost-manager/api/internal/domain/entity"
	"cost-manager/api/internal/domain/service"
	"cost-manager/api/pkg/apperr"
	"cost-manager/api/pkg/apires"
	"cost-manager/api/pkg/logger"
	"cost-manager/api/internal/transport/dto"
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
	list, err := h.billingSvc.ListPrices(c.Request.Context())
	if err != nil {
		h.handleError(c, op, err)
		return
	}

	apires.RespondSuccess(c, dto.ToPriceListResponse(list), "ok")
}

type SavePriceRequest struct {
	ID            string     `json:"id"`
	ServiceType   string     `json:"service_type" binding:"required"`
	ZoneCode      string     `json:"zone_code" binding:"required"`
	UnitPrice     float64    `json:"unit_price" binding:"required,gte=0"`
	Currency      string     `json:"currency" binding:"required"`
	Tier          string     `json:"tier" binding:"required"`
	EffectiveFrom time.Time  `json:"effective_from"`
	EffectiveTo   *time.Time `json:"effective_to"`
}

func (h *PriceHandler) SavePrice(c *gin.Context) {
	const op = "handler.price.save_price"
	var req SavePriceRequest
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
		ServiceType:   req.ServiceType,
		ZoneCode:      req.ZoneCode,
		UnitPrice:     req.UnitPrice,
		Currency:      req.Currency,
		Tier:          req.Tier,
		EffectiveFrom: effFrom,
		EffectiveTo:   req.EffectiveTo,
	}

	err := h.billingSvc.CreateOrUpdatePrice(c.Request.Context(), p)
	if err != nil {
		h.handleError(c, op, err)
		return
	}

	logger.HandlerInfo(c, op, "Successfully saved price rate configuration")
	apires.RespondCreated(c, dto.ToPriceResponse(*p), "price saved")
}

func (h *PriceHandler) handleError(c *gin.Context, op string, err error) {
	logger.HandlerError(c, op, err)

	appErr, ok := apperr.As(err)
	if !ok {
		apires.RespondInternalError(c, "internal_server_error")
		return
	}

	if errors.Is(appErr.Kind, apperr.ErrPriceNotFound) {
		apires.RespondNotFound(c, appErr.Outcome)
	} else if errors.Is(appErr.Kind, apperr.ErrBadRequest) {
		apires.RespondBadRequest(c, appErr.Outcome)
	} else {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   appErr.Kind.Error(),
			"outcome": appErr.Outcome,
		})
	}
}
