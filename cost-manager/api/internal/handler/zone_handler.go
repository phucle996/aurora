package handler

import (
	"errors"

	"cost-manager/api/internal/domain/service"
	"cost-manager/api/pkg/apperr"
	"cost-manager/api/pkg/apires"
	"cost-manager/api/pkg/pkgcontext"
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
	ctx := pkgcontext.WithOperation(c.Request.Context(), op)
	list, err := h.zoneSvc.ListZones(ctx)
	if err != nil {
		appErr, ok := apperr.As(err)
		if ok && errors.Is(appErr.Kind, apperr.ErrBadRequest) {
			apires.RespondBadRequest(c, appErr.Outcome)
		} else {
			apires.RespondInternalError(c, op+": "+err.Error())
		}
		return
	}

	res := make([]gin.H, len(list))
	for i, z := range list {
		res[i] = gin.H{
			"id":     z.ID,
			"code":   z.Code,
			"name":   z.Name,
			"status": z.Status,
		}
	}

	apires.RespondSuccess(c, res, "ok")
}
