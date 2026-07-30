package apires

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// 400 Bad Request
func RespondBadRequest(c *gin.Context, msg string) {
	c.JSON(http.StatusBadRequest, APIResponse{
		Error:   "bad request",
		Message: msg,
	})
}

// RespondBadRequestWithCode preserves a stable workflow taxonomy while keeping
// the shared HTTP envelope. The caller still owns which validation branch maps
// to the code; this package does not infer business errors.
func RespondBadRequestWithCode(c *gin.Context, code, msg string) {
	c.JSON(http.StatusBadRequest, APIResponse{
		Error:   code,
		Message: msg,
	})
}

// 401 Unauthorized
func RespondUnauthorized(c *gin.Context, msg string) {
	c.JSON(http.StatusUnauthorized, APIResponse{
		Error:   "unauthorized",
		Message: msg,
	})
}

// 403 Forbidden
func RespondForbidden(c *gin.Context, msg string) {
	c.JSON(http.StatusForbidden, APIResponse{
		Error:   "forbidden",
		Message: msg,
	})
}

// 404 Not Found
func RespondNotFound(c *gin.Context, msg string) {
	c.JSON(http.StatusNotFound, APIResponse{
		Error:   "not found",
		Message: msg,
	})
}

func RespondNotFoundWithCode(c *gin.Context, code, msg string) {
	c.JSON(http.StatusNotFound, APIResponse{
		Error:   code,
		Message: msg,
	})
}

// 500 Internal Server Error
func RespondInternalError(c *gin.Context, err string) {
	c.JSON(http.StatusInternalServerError, APIResponse{
		Error:   err,
		Message: "internal server error",
	})
}

// 409 Conflict
func RespondConflict(c *gin.Context, msg string) {
	c.JSON(http.StatusConflict, APIResponse{
		Error:   "conflict",
		Message: msg,
	})
}

func RespondConflictWithCode(c *gin.Context, code, msg string) {
	c.JSON(http.StatusConflict, APIResponse{
		Error:   code,
		Message: msg,
	})
}

// 422 Unprocessable Entity
func RespondUnprocessableEntity(c *gin.Context, code, msg string) {
	c.JSON(http.StatusUnprocessableEntity, APIResponse{
		Error:   code,
		Message: msg,
	})
}

// 503 Service Unavailable
func RespondServiceUnavailable(c *gin.Context, err string) {
	c.JSON(http.StatusServiceUnavailable, APIResponse{
		Error:   err,
		Message: "Service Unavailable",
	})
}

func RespondServiceUnavailableWithCode(c *gin.Context, code, msg string) {
	c.JSON(http.StatusServiceUnavailable, APIResponse{
		Error:   code,
		Message: msg,
	})
}

// 429 Too Many Requests
func RespondTooManyRequests(c *gin.Context, msg string) {
	c.JSON(http.StatusTooManyRequests, APIResponse{
		Error:   "too many requests",
		Message: msg,
	})
}
