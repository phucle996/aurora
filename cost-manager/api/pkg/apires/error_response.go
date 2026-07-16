package apires

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RespondBadRequest trả về 400 Bad Request
func RespondBadRequest(c *gin.Context, msg string) {
	c.JSON(http.StatusBadRequest, APIResponse{
		Error:   "bad_request",
		Message: msg,
	})
}

// RespondUnauthorized trả về 401 Unauthorized
func RespondUnauthorized(c *gin.Context, msg string) {
	c.JSON(http.StatusUnauthorized, APIResponse{
		Error:   "unauthorized",
		Message: msg,
	})
}

// RespondForbidden trả về 403 Forbidden
func RespondForbidden(c *gin.Context, msg string) {
	c.JSON(http.StatusForbidden, APIResponse{
		Error:   "forbidden",
		Message: msg,
	})
}

// RespondNotFound trả về 404 Not Found
func RespondNotFound(c *gin.Context, msg string) {
	c.JSON(http.StatusNotFound, APIResponse{
		Error:   "not_found",
		Message: msg,
	})
}

// RespondConflict trả về 409 Conflict
func RespondConflict(c *gin.Context, msg string) {
	c.JSON(http.StatusConflict, APIResponse{
		Error:   "conflict",
		Message: msg,
	})
}

// RespondInternalError trả về 500 Internal Server Error
func RespondInternalError(c *gin.Context, msg string) {
	c.JSON(http.StatusInternalServerError, APIResponse{
		Error:   "internal_server_error",
		Message: msg,
	})
}

// RespondServiceUnavailable trả về 503 Service Unavailable
func RespondServiceUnavailable(c *gin.Context, msg string) {
	c.JSON(http.StatusServiceUnavailable, APIResponse{
		Error:   "service_unavailable",
		Message: msg,
	})
}

// RespondTooManyRequests trả về 429 Too Many Requests
func RespondTooManyRequests(c *gin.Context, msg string) {
	c.JSON(http.StatusTooManyRequests, APIResponse{
		Error:   "too_many_requests",
		Message: msg,
	})
}
