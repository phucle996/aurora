package apires

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RespondSuccess trả về 200 OK kèm data và message
func RespondSuccess(c *gin.Context, data any, message string) {
	c.JSON(http.StatusOK, APIResponse{
		Message: message,
		Data:    data,
	})
}

// RespondCreated trả về 201 Created
func RespondCreated(c *gin.Context, data any, message string) {
	c.JSON(http.StatusCreated, APIResponse{
		Message: message,
		Data:    data,
	})
}

// RespondAccepted trả về 202 Accepted
func RespondAccepted(c *gin.Context, data any, message string) {
	c.JSON(http.StatusAccepted, APIResponse{
		Message: message,
		Data:    data,
	})
}
