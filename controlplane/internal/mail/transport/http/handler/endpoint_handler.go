// ============================================================================
// MAIL ENDPOINT HTTP HANDLER (CONTROL PLANE ROUTING & TRANSPORT LAYER)
// ============================================================================
//
// 📜 DESIGN CONTRACT (Hợp đồng Thiết kế):
//   1. [Zone Isolation Security Contract]: Mọi tác vụ đọc/ghi/xóa tài nguyên Mail Endpoint
//      bắt buộc phải gắn liền với ZoneID cụ thể để phân lập lưu lượng và ngăn chặn leo thang đặc quyền chéo zone.
//   2. [JSON Serialization Integrity]: Tầng Transport chịu trách nhiệm deserialize đầu vào thành
//      CreateEndpointParams / UpdateEndpointParams và serialize đầu ra thành định dạng JSON chuẩn.
//      Không bao giờ trả trực tiếp cấu trúc thô của lớp Domain Entity hay Database Model.
//   3. [Fail-Safe Validation & Safe Response]: Đảm bảo tất cả các lỗi logic hoặc lỗi kết nối DB
//      đều được chuyển dịch thành mã trạng thái HTTP phù hợp (200, 201, 400, 404, 500) thông qua
//      thư viện apires của hệ thống, không để lộ raw error trace hoặc panic sập luồng.
//
// 🗄️ SOURCE OF TRUTH - SoT (Nguồn dữ liệu gốc):
//   * [SOT for Transport Validation & DTO Mapping]:
//     - File `endpoint_handler.go` là nguồn chân lý duy nhất ánh xạ các yêu cầu HTTP từ REST API
//       sang các cấu trúc tham số (Parameter Structs) của tầng nghiệp vụ `domain/service`.
//     - Mọi thiết lập ràng buộc dữ liệu (Validation Rules) như: "name không được trống",
//       "provider phải nằm trong enum smtp/sendgrid/mailgun" đều được quản lý tại đây.
//
// 🛡️ ARCHITECTURAL BOUNDARY (Ranh giới Thiết kế):
//   - Tầng Transport là biên ngoài cùng của Control Plane đón nhận các request RESTful API.
//   - Tầng Transport chỉ giao tiếp trực tiếp với các Service Interface (`mailSvcInterface.EndpointService`),
//     tuyệt đối không được gọi trực tiếp xuống lớp Repository (`mailRepoImpl`) hay kết nối trực tiếp DB/Redis.
//   - Dữ liệu trả về qua mạng phải được định dạng qua các DTO thành công hoặc thất bại chuẩn.
//
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
// @Summary Create mail endpoint
// @Description Tạo mail endpoint mới cho một Zone. Cấu hình kết nối nhạy cảm sẽ được mã hóa AES-256-GCM tự động.
// @Tags mail-endpoint
// @Accept json
// @Produce json
// @Param zone_id query string false "Zone ID (hoặc truyền trong JSON body)"
// @Param payload body mailReq.CreateEndpointRequest true "Cấu hình Endpoint"
// @Success 201 {object} map[string]interface{} "created"
// @Failure 400 {object} map[string]interface{} "invalid request"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /api/v1/mail/endpoints [post]
// @Router /admin/mail/endpoints [post]
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

	name := strings.TrimSpace(req.Name)
	if name == "" {
		logger.HandlerWarn(c, op, errors.New("empty name"), "creation aborted: name is required")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	if len(req.ConnectionConfig) == 0 {
		logger.HandlerWarn(c, op, errors.New("empty connection config"), "creation aborted: connection config is required")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	params := mailEntity.CreateEndpointParams{
		ZoneID:           req.ZoneID,
		Name:             name,
		Provider:         mailEntity.ProviderType(req.Provider),
		ConnectionConfig: req.ConnectionConfig,
	}

	err := h.svc.CreateEndpoint(ctx, params)
	if err != nil {
		if errors.Is(err, mailTaxonomy.ErrInvalidArgument) {
			apires.RespondBadRequest(c, "invalid request")
		} else {
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "failed to create mail endpoint")
		}
		return
	}

	apires.RespondCreated(c, nil, "created")
}

// Get godoc
// @Summary Get mail endpoint
// @Description Lấy thông tin chi tiết mail endpoint theo ID thuộc Zone. Cấu hình nhạy cảm sẽ được giải mã trong suốt.
// @Tags mail-endpoint
// @Produce json
// @Param zone_id query string true "Zone ID"
// @Param id path string true "Endpoint ID"
// @Success 200 {object} map[string]interface{} "ok"
// @Failure 400 {object} map[string]interface{} "invalid request"
// @Failure 404 {object} map[string]interface{} "not found"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /api/v1/mail/endpoints/{id} [get]
// @Router /admin/mail/endpoints/{id} [get]
func (h *EndpointHandler) Get(c *gin.Context) {
	const op = "mail.endpoint.get"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	zoneUUID, err := uuid.Parse(strings.TrimSpace(c.Query("zone_id")))
	if err != nil {
		logger.HandlerWarn(c, op, err, "retrieval aborted: zone_id is not a valid UUID")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	uuidID, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil {
		logger.HandlerWarn(c, op, err, "retrieval aborted: endpoint id is not a valid UUID")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	endpoint, err := h.svc.GetEndpoint(ctx, zoneUUID, uuidID)
	if err != nil {
		if errors.Is(err, mailTaxonomy.ErrEndpointNotFound) {
			apires.RespondNotFound(c, "mail endpoint not found")
		} else {
			logger.HandlerWarn(c, op, err, "target mail endpoint database query failed")
			apires.RespondInternalError(c, "failed to get mail endpoint")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id":                endpoint.ID.String(),
		"zone_id":           endpoint.ZoneID.String(),
		"name":              endpoint.Name,
		"provider":          endpoint.Provider,
		"connection_config": endpoint.ConnectionConfig,
		"is_active":         endpoint.IsActive,
		"created_at":        formatTimePtr(endpoint.CreatedAt),
		"updated_at":        formatTimePtr(endpoint.UpdatedAt),
	}, "ok")
}

// List godoc
// @Summary List mail endpoints
// @Description Trả về danh sách tất cả các mail endpoints được định nghĩa trong Zone cụ thể.
// @Tags mail-endpoint
// @Produce json
// @Param zone_id query string true "Zone ID"
// @Success 200 {object} map[string]interface{} "ok"
// @Failure 400 {object} map[string]interface{} "invalid request"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /api/v1/mail/endpoints [get]
// @Router /admin/mail/endpoints [get]
func (h *EndpointHandler) List(c *gin.Context) {
	const op = "mail.endpoint.list"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	zoneUUID, err := uuid.Parse(strings.TrimSpace(c.Query("zone_id")))
	if err != nil {
		logger.HandlerWarn(c, op, err, "retrieval aborted: zone_id is not a valid UUID or missing zone_id")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	endpoints, err := h.svc.ListEndpoints(ctx, zoneUUID)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "failed to list mail endpoints")
		return
	}

	items := make([]gin.H, 0, len(endpoints))
	for _, ep := range endpoints {
		items = append(items, gin.H{
			"id":                ep.ID.String(),
			"zone_id":           ep.ZoneID.String(),
			"name":              ep.Name,
			"provider":          ep.Provider,
			"connection_config": ep.ConnectionConfig,
			"is_active":         ep.IsActive,
		})
	}

	apires.RespondSuccess(c, items, "ok")
}

// Update godoc
// @Summary Update mail endpoint
// @Description Cập nhật thông tin chi tiết và Connection Config của một mail endpoint theo ID.
// @Tags mail-endpoint
// @Accept json
// @Produce json
// @Param zone_id query string true "Zone ID"
// @Param id path string true "Endpoint ID"
// @Param payload body mailReq.UpdateEndpointRequest true "Cấu hình cập nhật"
// @Success 200 {object} map[string]interface{} "updated"
// @Failure 400 {object} map[string]interface{} "invalid request"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /api/v1/mail/endpoints/{id} [patch]
// @Router /admin/mail/endpoints/{id} [patch]
func (h *EndpointHandler) Update(c *gin.Context) {
	const op = "mail.endpoint.update"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	zoneIDStr := strings.TrimSpace(c.Query("zone_id"))
	if zoneIDStr == "" {
		logger.HandlerWarn(c, op, errors.New("missing zone_id"), "updating aborted: zone_id is required")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	zoneUUID, err := uuid.Parse(zoneIDStr)
	if err != nil {
		logger.HandlerWarn(c, op, err, "updating aborted: zone_id is not a valid UUID")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

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

	name := strings.TrimSpace(req.Name)
	if name == "" {
		logger.HandlerWarn(c, op, errors.New("empty name"), "updating aborted: name is required")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	if len(req.ConnectionConfig) == 0 {
		logger.HandlerWarn(c, op, errors.New("empty connection config"), "updating aborted: connection config is required")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	params := mailEntity.UpdateEndpointParams{
		ZoneID:           zoneUUID,
		ID:               uuidID,
		Name:             name,
		ConnectionConfig: req.ConnectionConfig,
		IsActive:         req.IsActive,
	}

	updated, err := h.svc.UpdateEndpoint(ctx, params)
	if err != nil {
		switch {
		case errors.Is(err, mailTaxonomy.ErrEndpointNotFound):
			apires.RespondNotFound(c, "mail endpoint not found")
		case errors.Is(err, mailTaxonomy.ErrInvalidArgument):
			apires.RespondBadRequest(c, "invalid request")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "failed to update mail endpoint")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id":                updated.ID.String(),
		"zone_id":           updated.ZoneID.String(),
		"name":              updated.Name,
		"provider":          updated.Provider,
		"connection_config": updated.ConnectionConfig,
		"is_active":         updated.IsActive,
		"created_at":        formatTimePtr(updated.CreatedAt),
		"updated_at":        formatTimePtr(updated.UpdatedAt),
	}, "updated")
}

// Delete godoc
// @Summary Delete mail endpoint
// @Description Xóa vĩnh viễn mail endpoint khỏi Zone theo ID.
// @Tags mail-endpoint
// @Produce json
// @Param zone_id query string true "Zone ID"
// @Param id path string true "Endpoint ID"
// @Success 204 {string} string "No Content"
// @Failure 400 {object} map[string]interface{} "invalid request"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /api/v1/mail/endpoints/{id} [delete]
// @Router /admin/mail/endpoints/{id} [delete]
func (h *EndpointHandler) Delete(c *gin.Context) {
	const op = "mail.endpoint.delete"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	zoneIDStr := strings.TrimSpace(c.Query("zone_id"))
	if zoneIDStr == "" {
		logger.HandlerWarn(c, op, errors.New("missing zone_id"), "deletion aborted: zone_id is required")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	zoneUUID, err := uuid.Parse(zoneIDStr)
	if err != nil {
		logger.HandlerWarn(c, op, err, "deletion aborted: zone_id is not a valid UUID")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

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

	if err := h.svc.DeleteEndpoint(ctx, zoneUUID, uuidID); err != nil {
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
// @Summary Test saved mail endpoint connection
// @Description Thực hiện bắt tay mạng (handshake) và xác thực đầy đủ với Endpoint đã lưu.
// @Tags mail-endpoint
// @Produce json
// @Param zone_id query string true "Zone ID"
// @Param id path string true "Endpoint ID"
// @Success 200 {object} map[string]interface{} "ok"
// @Failure 400 {object} map[string]interface{} "invalid request"
// @Failure 404 {object} map[string]interface{} "not found"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /admin/mail/endpoints/{id}/test-connect [post]
func (h *EndpointHandler) TestConnection(c *gin.Context) {
	const op = "mail.endpoint.test_connection"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	zoneIDStr := strings.TrimSpace(c.Query("zone_id"))
	if zoneIDStr == "" {
		logger.HandlerWarn(c, op, errors.New("missing zone_id"), "testing connection aborted: zone_id query param is required")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	zoneUUID, err := uuid.Parse(zoneIDStr)
	if err != nil {
		logger.HandlerWarn(c, op, err, "testing connection aborted: zone_id is not a valid UUID")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

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

	err = h.svc.TestConnection(ctx, zoneUUID, uuidID)
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
// @Summary Test transient endpoint connection config
// @Description Thực hiện chạy thử kết nối sử dụng cấu hình thô chưa lưu.
// @Tags mail-endpoint
// @Accept json
// @Produce json
// @Param payload body mailReq.CreateEndpointRequest true "Cấu hình Endpoint tạm thời"
// @Success 200 {object} map[string]interface{} "ok"
// @Failure 400 {object} map[string]interface{} "invalid request"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /admin/mail/endpoints/try-connect [post]
func (h *EndpointHandler) TestConnectionRaw(c *gin.Context) {
	const op = "mail.endpoint.test_connection_raw"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	var req mailReq.CreateEndpointRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.HandlerWarn(c, op, err, "binding CreateEndpointRequest for raw test failed")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	err := h.svc.TestConnectionRaw(ctx, mailEntity.ProviderType(req.Provider), req.ConnectionConfig)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondBadRequest(c, "Connection failed")
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
