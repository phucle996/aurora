// ============================================================================
// MAIL ENDPOINT HTTP HANDLER (CONTROL PLANE ROUTING & TRANSPORT LAYER)
// ============================================================================

package mailHandler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	mailEntity "controlplane/internal/mail/domain/entity"
	mailSvcInterface "controlplane/internal/mail/domain/service"
	mailSvcImpl "controlplane/internal/mail/service"
	mailTaxonomy "controlplane/internal/mail/taxonomy"
	mailReq "controlplane/internal/mail/transport/http/dto/req"
	"controlplane/pkg/apires"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type EndpointHandler struct {
	svc mailSvcInterface.EndpointService
}

func NewEndpointHandler(svc mailSvcInterface.EndpointService) *EndpointHandler {
	return &EndpointHandler{svc: svc}
}

func (h *EndpointHandler) requestContext(c *gin.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	code := strings.TrimSpace(c.GetHeader("X-Zone-Code"))
	return mailSvcImpl.WithZoneCode(ctx, code), cancel
}

// Create godoc
func (h *EndpointHandler) Create(c *gin.Context) {
	const op = "mail.endpoint.create"
	ctx, cancel := h.requestContext(c, 5*time.Second)
	defer cancel()

	var req mailReq.CreateEndpointRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.HandlerWarn(c, op, err, "binding CreateEndpointRequest failed due to payload schema mismatch")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	var caCert, clientCert, clientKey string
	if req.CACertPEM != nil {
		caCert = strings.TrimSpace(*req.CACertPEM)
	}
	if req.ClientCertPEM != nil {
		clientCert = strings.TrimSpace(*req.ClientCertPEM)
	}
	if req.ClientKeyPEM != nil {
		clientKey = strings.TrimSpace(*req.ClientKeyPEM)
	}

	params := mailEntity.CreateEndpointParams{
		Name:           strings.TrimSpace(req.Name),
		Host:           strings.TrimSpace(req.Host),
		Port:           req.Port,
		Username:       strings.TrimSpace(req.Username),
		Password:       req.Password,
		TLSMode:        strings.TrimSpace(req.TLSMode),
		Status:         strings.TrimSpace(req.Status),
		MaxConnections: req.MaxConnections,
		Priority:       req.Priority,
		Weight:         req.Weight,
		CACertPEM:      caCert,
		ClientCertPEM:  clientCert,
		ClientKeyPEM:   clientKey,
	}

	err := h.svc.CreateEndpoint(ctx, params)
	if err != nil {
		if errors.Is(err, mailTaxonomy.ErrInvalidArgument) {
			apires.RespondBadRequest(c, err.Error())
		} else {
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "failed to create mail endpoint")
		}
		return
	}

	apires.RespondCreated(c, nil, "created")
}

// Get godoc
func (h *EndpointHandler) Get(c *gin.Context) {
	const op = "mail.endpoint.get"
	ctx, cancel := h.requestContext(c, 5*time.Second)
	defer cancel()

	uuidID, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil {
		logger.HandlerWarn(c, op, err, "retrieval aborted: endpoint id is not a valid UUID")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	endpoint, err := h.svc.GetEndpoint(ctx, uuid.Nil, uuidID)
	if err != nil {
		if errors.Is(err, mailTaxonomy.ErrEndpointNotFound) {
			apires.RespondNotFound(c, "mail endpoint not found")
		} else {
			logger.HandlerWarn(c, op, err, "target mail endpoint database query failed")
			apires.RespondInternalError(c, "failed to get mail endpoint")
		}
		return
	}

	response := gin.H{
		"id":              endpoint.ID.String(),
		"zone_id":         endpoint.ZoneID.String(),
		"name":            endpoint.Name,
		"provider":        "smtp", // Tương thích với FE, luôn trả về "smtp"
		"host":            endpoint.Host,
		"port":            endpoint.Port,
		"username":        endpoint.Username,
		"tls_mode":        endpoint.TLSMode,
		"status":          endpoint.Status,
		"max_connections": endpoint.MaxConnections,
		"priority":        endpoint.Priority,
		"weight":          endpoint.Weight,
		"ca_cert_pem":     endpoint.CACertPEM,
		"client_cert_pem": endpoint.ClientCertPEM,
		"is_active":       endpoint.IsActive,
		"created_at":      formatTimePtr(endpoint.CreatedAt),
		"updated_at":      formatTimePtr(endpoint.UpdatedAt),
	}

	apires.RespondSuccess(c, response, "ok")
}

// List godoc
func (h *EndpointHandler) List(c *gin.Context) {
	const op = "mail.endpoint.list"
	ctx, cancel := h.requestContext(c, 5*time.Second)
	defer cancel()

	endpoints, err := h.svc.ListEndpoints(ctx, uuid.Nil)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "failed to list mail endpoints")
		return
	}

	items := make([]gin.H, 0, len(endpoints))
	for _, ep := range endpoints {
		item := gin.H{
			"id":              ep.ID.String(),
			"zone_id":         ep.ZoneID.String(),
			"name":            ep.Name,
			"provider":        "smtp", // Luôn trả về "smtp" cho FE
			"host":            ep.Host,
			"port":            ep.Port,
			"username":        ep.Username,
			"tls_mode":        ep.TLSMode,
			"status":          ep.Status,
			"max_connections": ep.MaxConnections,
			"priority":        ep.Priority,
			"weight":          ep.Weight,
			"ca_cert_pem":     ep.CACertPEM,
			"client_cert_pem": ep.ClientCertPEM,
			"is_active":       ep.IsActive,
			"created_at":      formatTimePtr(ep.CreatedAt),
			"updated_at":      formatTimePtr(ep.UpdatedAt),
		}
		items = append(items, item)
	}

	apires.RespondSuccess(c, items, "ok")
}

// Update godoc
func (h *EndpointHandler) Update(c *gin.Context) {
	const op = "mail.endpoint.update"
	ctx, cancel := h.requestContext(c, 5*time.Second)
	defer cancel()

	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		logger.HandlerWarn(c, op, errors.New("missing endpoint id"), "updating aborted: endpoint id in path params is required")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	uuidID, err := uuid.Parse(id)
	if err != nil {
		logger.HandlerWarn(c, op, err, "updating aborted: endpoint id is not a valid UUID")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	var req mailReq.UpdateEndpointRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.HandlerWarn(c, op, err, "binding UpdateEndpointRequest failed due to payload mismatch")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	var caCert, clientCert, clientKey string
	if req.CACertPEM != nil {
		caCert = strings.TrimSpace(*req.CACertPEM)
	}
	if req.ClientCertPEM != nil {
		clientCert = strings.TrimSpace(*req.ClientCertPEM)
	}
	if req.ClientKeyPEM != nil {
		clientKey = strings.TrimSpace(*req.ClientKeyPEM)
	}

	params := mailEntity.UpdateEndpointParams{
		ID:             uuidID,
		Name:           strings.TrimSpace(req.Name),
		Host:           strings.TrimSpace(req.Host),
		Port:           req.Port,
		Username:       strings.TrimSpace(req.Username),
		Password:       req.Password,
		TLSMode:        strings.TrimSpace(req.TLSMode),
		Status:         strings.TrimSpace(req.Status),
		MaxConnections: req.MaxConnections,
		Priority:       req.Priority,
		Weight:         req.Weight,
		CACertPEM:      caCert,
		ClientCertPEM:  clientCert,
		ClientKeyPEM:   clientKey,
		IsActive:       req.IsActive,
	}

	updated, err := h.svc.UpdateEndpoint(ctx, params)
	if err != nil {
		switch {
		case errors.Is(err, mailTaxonomy.ErrEndpointNotFound):
			apires.RespondNotFound(c, "mail endpoint not found")
		case errors.Is(err, mailTaxonomy.ErrInvalidArgument):
			apires.RespondBadRequest(c, err.Error())
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "failed to update mail endpoint")
		}
		return
	}

	response := gin.H{
		"id":              updated.ID.String(),
		"zone_id":         updated.ZoneID.String(),
		"name":            updated.Name,
		"provider":        "smtp", // Trả về "smtp" cho FE
		"host":            updated.Host,
		"port":            updated.Port,
		"username":        updated.Username,
		"tls_mode":        updated.TLSMode,
		"status":          updated.Status,
		"max_connections": updated.MaxConnections,
		"priority":        updated.Priority,
		"weight":          updated.Weight,
		"ca_cert_pem":     updated.CACertPEM,
		"client_cert_pem": updated.ClientCertPEM,
		"is_active":       updated.IsActive,
		"created_at":      formatTimePtr(updated.CreatedAt),
		"updated_at":      formatTimePtr(updated.UpdatedAt),
	}

	apires.RespondSuccess(c, response, "updated")
}

// Delete godoc
func (h *EndpointHandler) Delete(c *gin.Context) {
	const op = "mail.endpoint.delete"
	ctx, cancel := h.requestContext(c, 5*time.Second)
	defer cancel()

	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		logger.HandlerWarn(c, op, errors.New("missing endpoint id"), "deletion aborted: endpoint id in path params is required")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	uuidID, err := uuid.Parse(id)
	if err != nil {
		logger.HandlerWarn(c, op, err, "deletion aborted: endpoint id is not a valid UUID")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	if err := h.svc.DeleteEndpoint(ctx, uuid.Nil, uuidID); err != nil {
		if errors.Is(err, mailTaxonomy.ErrEndpointNotFound) {
			apires.RespondNotFound(c, "mail endpoint not found")
		} else {
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "failed to delete mail endpoint")
		}
		return
	}

	c.Status(http.StatusNoContent)
}

// TestConnection godoc
func (h *EndpointHandler) TestConnection(c *gin.Context) {
	const op = "mail.endpoint.test_connection"
	ctx, cancel := h.requestContext(c, 10*time.Second)
	defer cancel()

	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		logger.HandlerWarn(c, op, errors.New("missing endpoint id"), "testing connection aborted: endpoint id in path params is required")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	uuidID, err := uuid.Parse(id)
	if err != nil {
		logger.HandlerWarn(c, op, err, "testing connection aborted: endpoint id is not a valid UUID")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	err = h.svc.TestConnection(ctx, uuid.Nil, uuidID)
	if err != nil {
		if errors.Is(err, mailTaxonomy.ErrEndpointNotFound) {
			apires.RespondNotFound(c, "mail endpoint not found")
		} else {
			logger.HandlerError(c, op, err)
			apires.RespondBadRequest(c, "Connection failed")
		}
		return
	}

	apires.RespondSuccess(c, nil, "Connection successful")
}

// TestConnectionRaw godoc
func (h *EndpointHandler) TestConnectionRaw(c *gin.Context) {
	const op = "mail.endpoint.test_connection_raw"
	ctx, cancel := h.requestContext(c, 10*time.Second)
	defer cancel()

	var req mailReq.CreateEndpointRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.HandlerWarn(c, op, err, "binding CreateEndpointRequest for raw test failed")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	var caCert, clientCert, clientKey string
	if req.CACertPEM != nil {
		caCert = strings.TrimSpace(*req.CACertPEM)
	}
	if req.ClientCertPEM != nil {
		clientCert = strings.TrimSpace(*req.ClientCertPEM)
	}
	if req.ClientKeyPEM != nil {
		clientKey = strings.TrimSpace(*req.ClientKeyPEM)
	}

	params := mailEntity.CreateEndpointParams{
		Name:           strings.TrimSpace(req.Name),
		Host:           strings.TrimSpace(req.Host),
		Port:           req.Port,
		Username:       strings.TrimSpace(req.Username),
		Password:       req.Password,
		TLSMode:        strings.TrimSpace(req.TLSMode),
		Status:         strings.TrimSpace(req.Status),
		MaxConnections: req.MaxConnections,
		Priority:       req.Priority,
		Weight:         req.Weight,
		CACertPEM:      caCert,
		ClientCertPEM:  clientCert,
		ClientKeyPEM:   clientKey,
	}

	err := h.svc.TestConnectionRaw(ctx, params)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondBadRequest(c, err.Error())
		return
	}

	apires.RespondSuccess(c, nil, "Connection successful")
}

func formatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}
