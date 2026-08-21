package storageHandler

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	storageEntity "controlplane/internal/storage/domain/entity"
	storageSvcInterface "controlplane/internal/storage/domain/service"
	storageTaxonomy "controlplane/internal/storage/taxonomy"
	storageDto "controlplane/internal/storage/transport/http/dto"
	apires "controlplane/pkg/apires"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// formatUsedMegabytes is the UI read contract. Durable storage keeps exact
// bytes; the HTTP boundary emits a fixed-point decimal string so JavaScript
// never has to represent a potentially unsafe integer.
func formatUsedMegabytes(bytes int64) string {
	if bytes <= 0 {
		return "0.000000"
	}
	const bytesPerMegabyte int64 = 1024 * 1024
	whole := bytes / bytesPerMegabyte
	fractionMicros := (bytes % bytesPerMegabyte) * 1_000_000 / bytesPerMegabyte
	return strconv.FormatInt(whole, 10) + "." + fmt.Sprintf("%06d", fractionMicros)
}

// [COMMENT]: PersonalBucketHandler xử lý các HTTP request quản trị Bucket của người dùng cá nhân/workspace.
type PersonalBucketHandler struct {
	personalSvc      storageSvcInterface.PersonalBucketService
	accessSessionSvc storageSvcInterface.PersonalStorageAccessSessionService
}

// [COMMENT]: NewPersonalBucketHandler khởi tạo controller xử lý các endpoint Bucket cá nhân.
func NewPersonalBucketHandler(
	personalSvc storageSvcInterface.PersonalBucketService,
	accessSessionSvc storageSvcInterface.PersonalStorageAccessSessionService,
) *PersonalBucketHandler {
	return &PersonalBucketHandler{
		personalSvc:      personalSvc,
		accessSessionSvc: accessSessionSvc,
	}
}

// [COMMENT]: Create tiếp nhận yêu cầu tạo mới bucket cá nhân trong 1 zone.
func (h *PersonalBucketHandler) Create(c *gin.Context) {
	const op = "storage.personal_bucket.create"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// 1. Trích xuất thông tin định danh trực tiếp từ context
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}

	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	// 2. Bind JSON Request Body sử dụng cấu trúc DTO
	var req storageDto.CreateBucketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "invalid body payload: "+err.Error())
		return
	}

	bucketName := strings.TrimSpace(req.Name)
	if bucketName == "" {
		apires.RespondBadRequest(c, "bucket name cannot be empty")
		return
	}

	// [COMMENT]: Thực thi nghiệp vụ qua service cá nhân
	param := &storageEntity.CreatePersonalBucket{
		Name:                 bucketName,
		WorkspaceID:          workspaceID,
		ZoneID:               zoneID,
		CapacityQuotaBytes:   req.QuotaBytes,
		UserID:               userID,
		Policy:               req.Policy,
		EncryptEnabled:       req.EncryptEnabled,
		VersioningEnabled:    req.VersioningEnabled,
		ObjectLockingEnabled: req.ObjectLockingEnabled,
		ReplicationEnabled:   req.ReplicationEnabled,
		RetentionDays:        req.RetentionDays,
		LegalHoldEnabled:     req.LegalHoldEnabled,
		Tags:                 req.Tags,
	}
	createResult, createErr := h.personalSvc.CreateBucketForPersonal(ctx, param)
	if createErr != nil {
		switch {
		case errors.Is(createErr, storageTaxonomy.ErrAlreadyExists):
			logger.HandlerWarn(c, op, createErr, "bucket name conflict: "+bucketName)
			apires.RespondConflict(c, "bucket name already exists")
		case errors.Is(createErr, storageTaxonomy.ErrInvalidBucketName):
			logger.HandlerWarn(c, op, createErr, "invalid bucket name format")
			apires.RespondBadRequest(c, "invalid bucket name format")
		case errors.Is(createErr, storageTaxonomy.ErrCommercialAdmissionDenied):
			apires.RespondServiceUnavailable(c, "STORAGE_WALLET_ADMISSION_UNAVAILABLE")
		default:
			logger.HandlerError(c, op, createErr)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondCreated(c, gin.H{
		"bucket_id":     createResult.BucketID.String(),
		"bucket_name":   createResult.BucketName,
		"credential_id": createResult.CredentialID.String(),
		"access_key":    createResult.AccessKey,
		"secret_key":    createResult.SecretKey,
		"policy":        createResult.Policy,
	}, "bucket creation initiated — save your credentials, they will not be shown again")
}

// [COMMENT]: Get truy vấn thông tin chi tiết một bucket cá nhân.
func (h *PersonalBucketHandler) Get(c *gin.Context) {
	const op = "storage.personal_bucket.get"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// Trích xuất userID để xác thực quyền sở hữu
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	idStr := c.Param("id")
	bucketID, err := uuid.Parse(idStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	bucket, getErr := h.personalSvc.GetBucket(ctx, bucketID, userID)
	if getErr != nil {
		if errors.Is(getErr, storageTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "bucket not found")
			return
		}
		logger.HandlerError(c, op, getErr)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id":                   bucket.ID.String(),
		"name":                 bucket.Name,
		"capacity_quota_bytes": bucket.CapacityQuotaBytes,
		"used_mb":              formatUsedMegabytes(bucket.UsedBytes),
		"versioning_enabled":   bucket.VersioningEnabled,
		"lifecycle_rules":      bucket.LifecycleRules,
		"created_at":           bucket.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":           bucket.UpdatedAt.UTC().Format(time.RFC3339),
	}, "get bucket details success")
}

// [COMMENT]: List trả về danh sách các bucket thuộc workspace cá nhân hiện tại.
func (h *PersonalBucketHandler) List(c *gin.Context) {
	const op = "storage.personal_bucket.list"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// Trích xuất userID để xác thực quyền sở hữu
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}

	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	list, listErr := h.personalSvc.ListBuckets(ctx, workspaceID, zoneID, userID)
	if listErr != nil {
		logger.HandlerError(c, op, listErr)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	resList := make([]gin.H, len(list))
	for i, b := range list {
		resList[i] = gin.H{
			"id":                   b.ID.String(),
			"name":                 b.Name,
			"capacity_quota_bytes": b.CapacityQuotaBytes,
			"used_mb":              formatUsedMegabytes(b.UsedBytes),
			"versioning_enabled":   b.VersioningEnabled,
			"lifecycle_rules":      b.LifecycleRules,
			"created_at":           b.CreatedAt.UTC().Format(time.RFC3339),
			"updated_at":           b.UpdatedAt.UTC().Format(time.RFC3339),
		}
	}

	apires.RespondSuccess(c, resList, "list buckets success")
}

// [COMMENT]: ListNames chỉ trả về mảng danh sách tên vật lý của các bucket cá nhân (truy vấn nhẹ).
func (h *PersonalBucketHandler) ListNames(c *gin.Context) {
	const op = "storage.personal_bucket.list_names"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// Trích xuất userID để xác thực quyền sở hữu
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}

	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	names, listErr := h.personalSvc.ListBucketNames(ctx, workspaceID, zoneID, userID)
	if listErr != nil {
		logger.HandlerError(c, op, listErr)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	apires.RespondSuccess(c, names, "list bucket names success")
}

// [COMMENT]: UpdateQuota điều chỉnh dung lượng tối đa của bucket cá nhân.
func (h *PersonalBucketHandler) UpdateQuota(c *gin.Context) {
	const op = "storage.personal_bucket.update_quota"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// Trích xuất userID để xác thực quyền sở hữu
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	idStr := c.Param("id")
	bucketID, err := uuid.Parse(idStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	var req storageDto.UpdateQuotaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "invalid body payload: "+err.Error())
		return
	}

	updateErr := h.personalSvc.UpdateBucketQuota(ctx, bucketID, userID, req.QuotaBytes)
	if updateErr != nil {
		if errors.Is(updateErr, storageTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "bucket not found")
			return
		}
		if errors.Is(updateErr, storageTaxonomy.ErrResizeLimitTooLow) {
			apires.RespondBadRequest(c, "requested quota must leave at least 1GB of free space above current usage")
			return
		}
		if errors.Is(updateErr, storageTaxonomy.ErrCommercialAdmissionDenied) {
			apires.RespondServiceUnavailable(c, "STORAGE_WALLET_ADMISSION_UNAVAILABLE")
			return
		}
		logger.HandlerError(c, op, updateErr)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	apires.RespondSuccess(c, nil, "bucket quota updated")
}

// [COMMENT]: UpdateVersioning cập nhật trạng thái versioning cho bucket cá nhân.
func (h *PersonalBucketHandler) UpdateVersioning(c *gin.Context) {
	const op = "storage.personal_bucket.update_versioning"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	idStr := c.Param("id")
	bucketID, err := uuid.Parse(idStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	var req storageDto.UpdateBucketVersioningRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "invalid request body")
		return
	}

	updatedBucket, updateErr := h.personalSvc.UpdateBucketVersioning(ctx, bucketID, userID, req.VersioningEnabled)
	if updateErr != nil {
		if errors.Is(updateErr, storageTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "bucket not found")
			return
		}
		if errors.Is(updateErr, storageTaxonomy.ErrCommercialAdmissionDenied) {
			apires.RespondServiceUnavailable(c, "STORAGE_WALLET_ADMISSION_UNAVAILABLE")
			return
		}
		logger.HandlerError(c, op, updateErr)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id":                 updatedBucket.ID.String(),
		"name":               updatedBucket.Name,
		"versioning_enabled": updatedBucket.VersioningEnabled,
	}, "bucket versioning updated")
}

// [COMMENT]: GetLifecycle lấy cấu hình lifecycle rules của bucket.
func (h *PersonalBucketHandler) GetLifecycle(c *gin.Context) {
	const op = "storage.personal_bucket.get_lifecycle"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	idStr := c.Param("id")
	bucketID, err := uuid.Parse(idStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	rules, getErr := h.personalSvc.GetBucketLifecycle(ctx, bucketID, userID)
	if getErr != nil {
		if errors.Is(getErr, storageTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "bucket not found")
			return
		}
		logger.HandlerError(c, op, getErr)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	apires.RespondSuccess(c, gin.H{"rules": rules}, "get bucket lifecycle success")
}

// [COMMENT]: UpdateLifecycle cập nhật cấu hình lifecycle rules cho bucket.
func (h *PersonalBucketHandler) UpdateLifecycle(c *gin.Context) {
	const op = "storage.personal_bucket.update_lifecycle"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	idStr := c.Param("id")
	bucketID, err := uuid.Parse(idStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	var req storageDto.UpdateBucketLifecycleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "invalid request body")
		return
	}

	if len(req.Rules) > 100 {
		apires.RespondBadRequest(c, "too many lifecycle rules (max 100)")
		return
	}

	domainRules := make([]storageEntity.BucketLifecycleRule, len(req.Rules))
	ruleIDs := make(map[string]bool)
	for i, r := range req.Rules {
		r.ID = strings.TrimSpace(r.ID)
		if r.ID == "" || len(r.ID) > 64 {
			apires.RespondBadRequest(c, "rule id must be non-empty and at most 64 characters")
			return
		}
		if ruleIDs[r.ID] {
			apires.RespondBadRequest(c, fmt.Sprintf("duplicate rule id: %s", r.ID))
			return
		}
		ruleIDs[r.ID] = true

		if r.ExpirationDays < 0 || r.NoncurrentVersionExpirationDays < 0 || r.AbortIncompleteMultipartUploadDays < 0 {
			apires.RespondBadRequest(c, "days parameters in lifecycle rules must be non-negative")
			return
		}

		domainRules[i] = storageEntity.BucketLifecycleRule{
			ID:                                 r.ID,
			Enabled:                            r.Enabled,
			Prefix:                             r.Prefix,
			ExpirationDays:                     r.ExpirationDays,
			NoncurrentVersionExpirationDays:    r.NoncurrentVersionExpirationDays,
			AbortIncompleteMultipartUploadDays: r.AbortIncompleteMultipartUploadDays,
		}
	}

	updatedBucket, updateErr := h.personalSvc.UpdateBucketLifecycle(ctx, bucketID, userID, domainRules)
	if updateErr != nil {
		if errors.Is(updateErr, storageTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "bucket not found")
			return
		}
		if errors.Is(updateErr, storageTaxonomy.ErrVersioningRequired) {
			apires.RespondBadRequest(c, "noncurrent version expiration requires bucket versioning to be enabled")
			return
		}
		if errors.Is(updateErr, storageTaxonomy.ErrCommercialAdmissionDenied) {
			apires.RespondServiceUnavailable(c, "STORAGE_WALLET_ADMISSION_UNAVAILABLE")
			return
		}
		logger.HandlerError(c, op, updateErr)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id":              updatedBucket.ID.String(),
		"name":            updatedBucket.Name,
		"lifecycle_rules": updatedBucket.LifecycleRules,
	}, "bucket lifecycle updated")
}

// [COMMENT]: Delete khởi động tiến trình xóa hoàn toàn bucket cá nhân.
func (h *PersonalBucketHandler) Delete(c *gin.Context) {
	const op = "storage.personal_bucket.delete"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Trích xuất userID, workspaceID, zoneID từ request context
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}

	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	idStr := c.Param("id")
	bucketID, err := uuid.Parse(idStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	// [COMMENT]: Lấy bucket name vật lý từ URL query parameter
	bucketName := strings.TrimSpace(c.Query("name"))
	if bucketName == "" {
		apires.RespondBadRequest(c, "missing bucket name query parameter")
		return
	}

	// [COMMENT]: Khởi tạo thực thể tham số xóa
	param := &storageEntity.DeletePersonalBucket{
		BucketID:    bucketID,
		BucketName:  bucketName,
		WorkspaceID: workspaceID,
		ZoneID:      zoneID,
		UserID:      userID,
	}

	deleteErr := h.personalSvc.DeleteBucket(ctx, param)
	if deleteErr != nil {
		if errors.Is(deleteErr, storageTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "bucket not found")
			return
		}
		logger.HandlerError(c, op, deleteErr)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	apires.RespondSuccess(c, nil, "bucket deletion initiated")
}

// CreateAccessSession starts the metadata-only access flow. The returned id is
// an opaque handle, not a credential; Envoy/ACR must still authenticate the
// Trinity session on every Gateway request.
func (h *PersonalBucketHandler) CreateAccessSession(c *gin.Context) {
	const op = "storage.personal_bucket.create_access_session"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}
	bucketID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}
	var req storageDto.RequestStorageAccessRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "invalid body payload: "+err.Error())
		return
	}
	duration := req.DurationSeconds
	if duration < 60 || duration > 3600 {
		apires.RespondBadRequest(c, "duration_seconds must be between 60 and 3600")
		return
	}
	actions := req.Actions
	if len(actions) == 0 {
		actions = []string{"ListBucket", "GetObject", "PutObject", "DeleteObject", "GetObjectTagging", "PutObjectTagging"}
	}
	allowed := map[string]struct{}{"ListBucket": {}, "GetObject": {}, "PutObject": {}, "DeleteObject": {}, "GetObjectTagging": {}, "PutObjectTagging": {}}
	seen := make(map[string]struct{}, len(actions))
	uniqueActions := make([]string, 0, len(actions))
	for _, action := range actions {
		if _, valid := allowed[action]; !valid {
			apires.RespondBadRequest(c, "unsupported storage action")
			return
		}
		if _, duplicate := seen[action]; duplicate {
			continue
		}
		seen[action] = struct{}{}
		uniqueActions = append(uniqueActions, action)
	}
	actions = uniqueActions
	keyPrefix := strings.TrimSpace(req.KeyPrefix)
	if len(keyPrefix) > 256 || strings.ContainsAny(keyPrefix, "\r\n") {
		apires.RespondBadRequest(c, "invalid key_prefix")
		return
	}
	accessSessionID, err := uuid.NewV7()
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
		return
	}
	// Authorization is checked by the repository in the same transaction that
	// inserts the preparation outbox; do not trust a bucket id from the client.
	session := &storageEntity.StorageAccessSession{
		AccessSessionID:      accessSessionID,
		ActorID:              userID,
		ResourceID:           bucketID,
		WorkspaceID:          workspaceID,
		ZoneID:               zoneID,
		Actions:              actions,
		KeyPrefix:            keyPrefix,
		ExpiresAtUnixSeconds: uint64(time.Now().Add(time.Duration(duration) * time.Second).Unix()),
		PolicyRevision:       1,
	}
	if err := h.accessSessionSvc.CreatePersonalStorageAccessSession(ctx, session); err != nil {
		if errors.Is(err, storageTaxonomy.ErrCommercialAdmissionDenied) {
			apires.RespondServiceUnavailable(c, "STORAGE_WALLET_ADMISSION_UNAVAILABLE")
			return
		}
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "bucket not found")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
		return
	}
	apires.RespondAccepted(c, gin.H{
		"access_session_id": accessSessionID.String(),
		"zone_id":           zoneID.String(),
		"bucket_id":         bucketID.String(),
		"expires_at":        time.Unix(int64(session.ExpiresAtUnixSeconds), 0).UTC().Format(time.RFC3339),
		"gateway_path":      "/zone-control/v1/storage/",
	}, "storage access session is being prepared")
}

func (h *PersonalBucketHandler) GetAccessSessionStatus(c *gin.Context) {
	const op = "storage.personal_bucket.get_access_session_status"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}
	bucketID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}
	accessSessionID, err := uuid.Parse(c.Param("access_session_id"))
	if err != nil {
		apires.RespondBadRequest(c, "invalid access session id format")
		return
	}
	status, err := h.accessSessionSvc.GetPersonalStorageAccessSessionStatus(ctx, accessSessionID, bucketID, workspaceID, userID, zoneID)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "storage access session not found")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
		return
	}
	response := gin.H{
		"access_session_id": accessSessionID.String(),
		"bucket_id":         bucketID.String(),
		"status":            status.State,
	}
	if status.CompletedAt != nil {
		response["completed_at"] = status.CompletedAt.UTC().Format(time.RFC3339)
	}
	if status.State == "FAILED" && status.ErrorCode != nil {
		response["error_code"] = *status.ErrorCode
	}
	apires.RespondSuccess(c, response, "get storage access session status success")
}
