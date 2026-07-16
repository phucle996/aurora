package handler

import (
	"errors"

	"cost-manager/api/internal/domain/service"
	"cost-manager/api/pkg/apperr"
	"cost-manager/api/pkg/apires"
	"cost-manager/api/internal/transport/dto"
	"github.com/gin-gonic/gin"
)

type ZoneHandler struct {
	zoneSvc service.ZoneService
}

func NewZoneHandler(zoneSvc service.ZoneService) *ZoneHandler {
	return &ZoneHandler{zoneSvc: zoneSvc}
}

func (h *ZoneHandler) ListZones(c *gin.Context) {
	const op = "handler.zone.list_zones"
	list, err := h.zoneSvc.ListZones(c.Request.Context())
	if err != nil {
		appErr, ok := apperr.As(err)
		if ok && errors.Is(appErr.Kind, apperr.ErrBadRequest) {
			apires.RespondBadRequest(c, appErr.Outcome)
		} else {
			apires.RespondInternalError(c, op+": "+err.Error())
		}
		return
	}

	apires.RespondSuccess(c, dto.ToZoneListResponse(list), "ok")
}
