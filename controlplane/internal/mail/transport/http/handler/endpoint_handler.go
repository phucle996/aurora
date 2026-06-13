// ============================================================================
// MAIL ENDPOINT HTTP HANDLER (CONTROL PLANE ROUTING & TRANSPORT LAYER)
// ============================================================================

package mailHandler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	mailEntity "controlplane/internal/mail/domain/entity"
	mailSvcInterface "controlplane/internal/mail/domain/service"
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

// Create godoc
func (h *EndpointHandler) Create(c *gin.Context) {
	const op = "mail.endpoint.create"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
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
		TLSMode:        mailEntity.TLSMode(strings.TrimSpace(req.TLSMode)),
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
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	uuidID, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil {
		logger.HandlerWarn(c, op, err, "retrieval aborted: endpoint id is not a valid UUID")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	endpoint, err := h.svc.GetEndpoint(ctx, uuidID)
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
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	cursor := strings.TrimSpace(c.Query("cursor"))
	limitStr := strings.TrimSpace(c.Query("limit"))
	limit := 20
	if limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}
	if limit > 100 {
		limit = 100
	}

	endpoints, nextCursor, err := h.svc.ListEndpoints(ctx, cursor, limit)
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

	apires.RespondSuccess(c, gin.H{
		"items":       items,
		"next_cursor": nextCursor,
	}, "ok")
}

// Update godoc
func (h *EndpointHandler) Update(c *gin.Context) {
	const op = "mail.endpoint.update"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)

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
		TLSMode:        mailEntity.TLSMode(strings.TrimSpace(req.TLSMode)),
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
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)

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

	if err := h.svc.DeleteEndpoint(ctx, uuidID); err != nil {
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
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)

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

	err = h.svc.TestConnection(ctx, uuidID)
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

// TryConnect godoc
func (h *EndpointHandler) TryConnect(c *gin.Context) {
	const op = "mail.endpoint.try_connect"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	var req mailReq.TestConnectionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.HandlerWarn(c, op, err, "binding TestConnectionRequest for raw test failed")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	var caCertPtr, clientCertPtr, clientKeyPtr *string
	if req.CACertPEM != nil {
		trimmed := strings.TrimSpace(*req.CACertPEM)
		caCertPtr = &trimmed
	}
	if req.ClientCertPEM != nil {
		trimmed := strings.TrimSpace(*req.ClientCertPEM)
		clientCertPtr = &trimmed
	}
	if req.ClientKeyPEM != nil {
		trimmed := strings.TrimSpace(*req.ClientKeyPEM)
		clientKeyPtr = &trimmed
	}

	testReq := mailEntity.TestConnection{
		Host:          strings.TrimSpace(req.Host),
		Port:          req.Port,
		Username:      strings.TrimSpace(req.Username),
		Password:      req.Password,
		TLSMode:       mailEntity.TLSMode(strings.TrimSpace(req.TLSMode)),
		CACertPEM:     caCertPtr,
		ClientCertPEM: clientCertPtr,
		ClientKeyPEM:  clientKeyPtr,
	}

	err := h.svc.TestConnectionRaw(ctx, testReq)
	if err != nil {
		if errors.Is(err, mailTaxonomy.ErrInvalidArgument) {
			logger.HandlerWarn(c, op, err, "TestConnectionRaw failed: invalid argument")
			apires.RespondBadRequest(c, "Invalid request")
		} else {
			// fail do 1 vấn đề nào đó khác ở layer bên dưới
			// cái này không phải là không connect đc mà do 1 nguyên nhân nào khác.
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "Failed to test connection")
		}
		return
	}

	// thành công thì vào outbox rồi chờ thông báo - async workflow
	apires.RespondSuccess(c, nil, "Connection successful")
}

func formatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}
