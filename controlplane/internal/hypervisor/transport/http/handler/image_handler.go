package hypervisorHandler

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorSvcInterface "controlplane/internal/hypervisor/domain/service"
	hypervisorTaxonomy "controlplane/internal/hypervisor/taxonomy"
	hypervisorDTO "controlplane/internal/hypervisor/transport/http/dto"
	"controlplane/pkg/apires"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var imageCodePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)
var imageReleasePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,31}$`)

type ImageHandler struct {
	service hypervisorSvcInterface.ImageService
}

func NewImageHandler(service hypervisorSvcInterface.ImageService) *ImageHandler {
	return &ImageHandler{service: service}
}

func (h *ImageHandler) RegisterMetadata(c *gin.Context) {
	const op = "hypervisor.image.register_metadata"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 10*time.Second)
	defer cancel()

	zoneID, err := uuid.Parse(strings.TrimSpace(c.Param("zone_id")))
	if err != nil {
		apires.RespondBadRequest(c, "zone_id is invalid")
		return
	}
	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	if actor == "" {
		actor = strings.TrimSpace(c.GetHeader("X-User-ID"))
	}
	if actor == "" || len(actor) > 128 {
		apires.RespondBadRequest(c, "verified admin identity is missing")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
	var request hypervisorDTO.RegisterImageMetadataRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		apires.RespondBadRequest(c, "invalid request body")
		return
	}
	name := strings.TrimSpace(request.Name)
	if name == "" || len(name) > 512 {
		apires.RespondBadRequest(c, "name must contain between 1 and 512 characters")
		return
	}
	code := strings.ToLower(strings.TrimSpace(request.Code))
	if !imageCodePattern.MatchString(code) {
		apires.RespondBadRequest(c, "code must be 1-128 letters, numbers, dots, underscores or hyphens")
		return
	}
	distribution := strings.ToLower(strings.TrimSpace(request.Distribution))
	if !imageCodePattern.MatchString(distribution) {
		apires.RespondBadRequest(c, "distribution is invalid")
		return
	}
	release := strings.TrimSpace(request.Release)
	if !imageReleasePattern.MatchString(release) {
		apires.RespondBadRequest(c, "release is invalid")
		return
	}
	if request.Revision < 1 {
		apires.RespondBadRequest(c, "revision must be positive")
		return
	}
	architecture := strings.ToLower(strings.TrimSpace(request.Architecture))
	if architecture != "x86_64" && architecture != "aarch64" {
		apires.RespondBadRequest(c, "architecture must be x86_64 or aarch64")
		return
	}
	format := strings.ToLower(strings.TrimSpace(request.Format))
	if format != "qcow2" && format != "raw" {
		apires.RespondBadRequest(c, "format must be qcow2 or raw")
		return
	}
	if request.SizeBytes < 1 || request.SizeBytes > 1<<40 {
		apires.RespondBadRequest(c, "size_bytes must be between 1 byte and 1 TiB")
		return
	}
	checksum, err := hex.DecodeString(strings.ToLower(strings.TrimSpace(request.SHA256)))
	if err != nil || len(checksum) != 32 {
		apires.RespondBadRequest(c, "sha256 must be 64 hexadecimal characters")
		return
	}

	image, err := h.service.RegisterImageMetadata(ctx, &hypervisorEntity.RegisterImageMetadata{
		ZoneID:       zoneID,
		Name:         name,
		Code:         code,
		Distribution: distribution,
		Release:      release,
		Revision:     request.Revision,
		Architecture: architecture,
		Format:       format,
		SizeBytes:    request.SizeBytes,
		SHA256:       checksum,
		CreatedBy:    actor,
	})
	if err != nil {
		switch {
		case errors.Is(err, hypervisorTaxonomy.ErrImageConflict):
			apires.RespondConflict(c, "this image code and revision already exist in the zone")
		case errors.Is(err, hypervisorTaxonomy.ErrScopeUnavailable):
			apires.RespondConflict(c, "the selected zone is not accepting hypervisor images")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "IMAGE_UPLOAD_CREATE_FAILED")
		}
		return
	}
	apires.RespondCreated(c, gin.H{
		"id":           image.ID.String(),
		"zone_id":      image.ZoneID.String(),
		"name":         image.Name,
		"code":         image.Code,
		"distribution": image.Distribution,
		"release":      image.Release,
		"revision":     image.Revision,
		"architecture": image.Architecture,
		"format":       image.Format,
		"size_bytes":   image.SizeBytes,
		"sha256":       hex.EncodeToString(image.SHA256),
		"state":        image.State,
		"import_path": "/admin/hypervisor/zones/" + image.ZoneID.String() +
			"/images/" + image.ID.String() + "/import",
		"created_at": image.CreatedAt,
	}, "image upload registered")
}

func (h *ImageHandler) ListAdmin(c *gin.Context) {
	const op = "hypervisor.image.list_admin"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	zoneID, err := uuid.Parse(strings.TrimSpace(c.Param("zone_id")))
	if err != nil {
		apires.RespondBadRequest(c, "zone_id is invalid")
		return
	}
	limit := int32(100)
	if rawLimit := strings.TrimSpace(c.Query("limit")); rawLimit != "" {
		parsed, err := strconv.ParseInt(rawLimit, 10, 32)
		if err != nil || parsed < 1 || parsed > 200 {
			apires.RespondBadRequest(c, "limit must be between 1 and 200")
			return
		}
		limit = int32(parsed)
	}
	images, err := h.service.ListAdmin(ctx, zoneID, limit)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "IMAGE_LIST_FAILED")
		return
	}
	rows := make([]gin.H, 0, len(images))
	for _, image := range images {
		rows = append(rows, gin.H{
			"id":                     image.ID.String(),
			"zone_id":                image.ZoneID.String(),
			"name":                   image.Name,
			"code":                   image.Code,
			"distribution":           image.Distribution,
			"release":                image.Release,
			"revision":               image.Revision,
			"architecture":           image.Architecture,
			"format":                 image.Format,
			"size_bytes":             image.SizeBytes,
			"sha256":                 hex.EncodeToString(image.SHA256),
			"state":                  image.State,
			"provider_template_vmid": image.ProviderTemplateVMID,
			"error_code":             image.ErrorCode,
			"error_message":          image.ErrorMessage,
			"created_at":             image.CreatedAt,
			"updated_at":             image.UpdatedAt,
			"available_at":           image.AvailableAt,
		})
	}
	apires.RespondSuccess(c, gin.H{"images": rows}, "images fetched")
}

func (h *ImageHandler) ListCatalog(c *gin.Context) {
	const op = "hypervisor.image.list_catalog"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}
	images, err := h.service.ListCatalog(ctx, zoneID)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "IMAGE_CATALOG_FAILED")
		return
	}
	rows := make([]gin.H, 0, len(images))
	for _, image := range images {
		// User catalog deliberately excludes object keys, provider topology and
		// upload metadata. The browser only selects an immutable image ID.
		rows = append(rows, gin.H{
			"id":           image.ID.String(),
			"zone_id":      image.ZoneID.String(),
			"name":         image.Name,
			"code":         image.Code,
			"distribution": image.Distribution,
			"release":      image.Release,
			"revision":     image.Revision,
			"architecture": image.Architecture,
			"format":       image.Format,
			"size_bytes":   image.SizeBytes,
		})
	}
	apires.RespondSuccess(c, gin.H{"images": rows}, "image catalog fetched")
}

func (h *ImageHandler) BeginImport(c *gin.Context) {
	const op = "hypervisor.image.begin_import"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 10*time.Second)
	defer cancel()

	zoneID, err := uuid.Parse(strings.TrimSpace(c.Param("zone_id")))
	if err != nil {
		apires.RespondBadRequest(c, "zone_id is invalid")
		return
	}
	imageID, err := uuid.Parse(strings.TrimSpace(c.Param("image_id")))
	if err != nil {
		apires.RespondBadRequest(c, "image_id is invalid")
		return
	}
	image, err := h.service.BeginImport(ctx, &hypervisorEntity.ImageImportRequest{
		ImageID: imageID,
		ZoneID:  zoneID,
	})
	if err != nil {
		switch {
		case errors.Is(err, hypervisorTaxonomy.ErrImageNotFound):
			apires.RespondNotFound(c, "image was not found")
		case errors.Is(err, hypervisorTaxonomy.ErrImageStateConflict):
			apires.RespondConflict(c, "image is already being imported or cannot be imported")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "IMAGE_IMPORT_FAILED")
		}
		return
	}
	apires.RespondAccepted(c, gin.H{
		"id":       image.ID.String(),
		"zone_id":  image.ZoneID.String(),
		"revision": image.Revision,
		"state":    image.State,
	}, "image import accepted")
}

func (h *ImageHandler) BeginDelete(c *gin.Context) {
	const op = "hypervisor.image.begin_delete"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 10*time.Second)
	defer cancel()

	zoneID, err := uuid.Parse(strings.TrimSpace(c.Param("zone_id")))
	if err != nil {
		apires.RespondBadRequest(c, "zone_id is invalid")
		return
	}
	imageID, err := uuid.Parse(strings.TrimSpace(c.Param("image_id")))
	if err != nil {
		apires.RespondBadRequest(c, "image_id is invalid")
		return
	}
	image, err := h.service.BeginDelete(ctx, &hypervisorEntity.ImageDeleteRequest{
		ImageID: imageID,
		ZoneID:  zoneID,
	})
	if err != nil {
		switch {
		case errors.Is(err, hypervisorTaxonomy.ErrImageNotFound):
			apires.RespondNotFound(c, "image was not found")
		case errors.Is(err, hypervisorTaxonomy.ErrImageStateConflict):
			apires.RespondConflict(c, "image is busy or cannot be deleted")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "IMAGE_DELETE_FAILED")
		}
		return
	}
	apires.RespondAccepted(c, gin.H{
		"id":       image.ID.String(),
		"zone_id":  image.ZoneID.String(),
		"revision": image.Revision,
		"state":    image.State,
	}, "image deletion accepted")
}
