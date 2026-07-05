package handler

import (
	"net/http"

	response "controlplane/pkg/apires"
	storageSvc "controlplane/internal/storage/domain/service"

	"github.com/gin-gonic/gin"
)

// [COMMENT]: BucketHandler tiếp nhận và điều phối các HTTP REST request quản trị Bucket.
type BucketHandler struct {
	svc storageSvc.BucketService
}

// [COMMENT]: NewBucketHandler khởi tạo controller xử lý các endpoint Bucket.
func NewBucketHandler(svc storageSvc.BucketService) *BucketHandler {
	return &BucketHandler{
		svc: svc,
	}
}

func (h *BucketHandler) Create(c *gin.Context) {
	// [COMMENT]: SKELETON - Chưa cài đặt logic HTTP handler.
	c.JSON(http.StatusNotImplemented, response.APIResponse{Error: "not_implemented", Message: "API Create not implemented"})
}

func (h *BucketHandler) Get(c *gin.Context) {
	// [COMMENT]: SKELETON - Chưa cài đặt logic HTTP handler.
	c.JSON(http.StatusNotImplemented, response.APIResponse{Error: "not_implemented", Message: "API Get not implemented"})
}

func (h *BucketHandler) List(c *gin.Context) {
	// [COMMENT]: SKELETON - Chưa cài đặt logic HTTP handler.
	c.JSON(http.StatusNotImplemented, response.APIResponse{Error: "not_implemented", Message: "API List not implemented"})
}

func (h *BucketHandler) UpdateQuota(c *gin.Context) {
	// [COMMENT]: SKELETON - Chưa cài đặt logic HTTP handler.
	c.JSON(http.StatusNotImplemented, response.APIResponse{Error: "not_implemented", Message: "API UpdateQuota not implemented"})
}

func (h *BucketHandler) Suspend(c *gin.Context) {
	// [COMMENT]: SKELETON - Chưa cài đặt logic HTTP handler.
	c.JSON(http.StatusNotImplemented, response.APIResponse{Error: "not_implemented", Message: "API Suspend not implemented"})
}

func (h *BucketHandler) Resume(c *gin.Context) {
	// [COMMENT]: SKELETON - Chưa cài đặt logic HTTP handler.
	c.JSON(http.StatusNotImplemented, response.APIResponse{Error: "not_implemented", Message: "API Resume not implemented"})
}

func (h *BucketHandler) Delete(c *gin.Context) {
	// [COMMENT]: SKELETON - Chưa cài đặt logic HTTP handler.
	c.JSON(http.StatusNotImplemented, response.APIResponse{Error: "not_implemented", Message: "API Delete not implemented"})
}
