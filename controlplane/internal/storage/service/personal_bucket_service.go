package storageSvcImpl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"controlplane/internal/observability"
	"controlplane/internal/security"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageSvcInterface "controlplane/internal/storage/domain/service"
	storageTaxonomy "controlplane/internal/storage/taxonomy"
	storageproto "controlplane/internal/storage/transport/proto"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: PersonalBucketServiceImpl thực thi nghiệp vụ quản trị Storage Bucket cho đối tượng Cá nhân.
type PersonalBucketSvcImpl struct {
	repo    storageRepoInterface.PersonalBucketRepo
	credSvc storageSvcInterface.PersonalCredentialService
	metrics observability.WorkflowRecorder
}

// NewPersonalBucketService wires the repository, credential service, and metrics in one constructor.
func NewPersonalBucketService(
	repo storageRepoInterface.PersonalBucketRepo,
	credSvc storageSvcInterface.PersonalCredentialService,
	metrics observability.WorkflowRecorder,
) storageSvcInterface.PersonalBucketService {
	return &PersonalBucketSvcImpl{repo: repo, credSvc: credSvc, metrics: metrics}
}

// [COMMENT]: buildPersonalBucketPolicy sinh JSON policy S3 giới hạn quyền chỉ vào bucket chỉ định.
func buildPersonalBucketPolicy(bucketName string) string {
	return fmt.Sprintf(`{
		"Version":"2012-10-17",
		"Statement":[{
			"Effect":"Allow",
			"Action":["s3:GetObject","s3:PutObject","s3:DeleteObject","s3:ListBucket"],
			"Resource":["arn:aws:s3:::%s","arn:aws:s3:::%s/*"]
		}]
	}`, bucketName, bucketName)
}

func (s *PersonalBucketSvcImpl) CreateBucketForPersonal(ctx context.Context, param *storageEntity.CreatePersonalBucket) (*storageEntity.CreatedBucketResult, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()
	// [COMMENT]: Khởi tạo thực thể Bucket cá nhân từ tham số đầu vào với UUID v7
	bucketID, err := uuid.NewV7()
	if err != nil {
		return nil, apperr.Wrap(err, err, "failed_to_generate_uuid_v7")
	}

	// [COMMENT]: Sinh tên vật lý duy nhất toàn cục với prefix là 8 ký tự đầu của WorkspaceID
	physicalName := fmt.Sprintf("ws-%s-%s", param.WorkspaceID.String()[:8], param.Name)

	// [COMMENT]: Bucket không còn status field — tồn tại trong DB là đủ để xác định là active
	bucket := &storageEntity.PersonalBucket{
		ID:                   bucketID,
		Name:                 physicalName,
		CapacityQuotaBytes:   param.CapacityQuotaBytes,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
		EncryptEnabled:       param.EncryptEnabled,
		VersioningEnabled:    param.VersioningEnabled,
		ObjectLockingEnabled: param.ObjectLockingEnabled,
		ReplicationEnabled:   param.ReplicationEnabled,
		RetentionDays:        param.RetentionDays,
		LegalHoldEnabled:     param.LegalHoldEnabled,
		Tags:                 param.Tags,
	}

	// [COMMENT]: CP tự sinh Access Key và Secret Key ngẫu nhiên (chuẩn MinIO Service Account)
	accessKey, err := security.GenerateAccessKey()
	if err != nil {
		return nil, apperr.Wrap(err, err, "gen_access_key_failed")
	}
	secretKey, err := security.GenerateSecretKey()
	if err != nil {
		return nil, apperr.Wrap(err, err, "gen_secret_key_failed")
	}

	// [COMMENT]: Sử dụng custom bucket policy được truyền từ client
	// Thay thế placeholder <BUCKET_NAME> bằng tên vật lý thực tế của bucket
	policy := strings.ReplaceAll(param.Policy, "<BUCKET_NAME>", bucket.Name)

	// [COMMENT]: Validate tính hợp lệ của JSON policy
	var js map[string]interface{}
	if err := json.Unmarshal([]byte(policy), &js); err != nil {
		result, reason = observability.ResultRejected, observability.ReasonInvalidArgument
		return nil, apperr.Wrap(storageTaxonomy.ErrInvalidPolicy, err, "invalid_policy_format")
	}

	// [COMMENT]: Tạo credential entity — gắn kèm vào bucket cá nhân với UUID v7
	credID, err := uuid.NewV7()
	if err != nil {
		return nil, apperr.Wrap(err, err, "failed_to_generate_uuid_v7")
	}

	credential := &storageEntity.PersonalCredential{
		ID:        credID,
		BucketID:  bucket.ID,
		AccessKey: accessKey,
		// [COMMENT]: Lược bỏ SecretKey khỏi thực thể lưu CSDL lâu dài để tăng tính bảo mật
		Policy:    policy,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// [COMMENT]: Trích xuất Trace ID phục vụ distributed tracing
	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

	// [COMMENT]: Serialize BucketCreateSync payload kèm credential để DP provisioning MinIO Service Account
	tagsBytes, _ := json.Marshal(bucket.Tags)
	syncEvent := &storageproto.BucketCreateSync{
		Name:                 bucket.Name,
		AccessKey:            accessKey,
		SecretKey:            secretKey,
		Policy:               policy,
		EncryptEnabled:       bucket.EncryptEnabled,
		VersioningEnabled:    bucket.VersioningEnabled,
		ObjectLockingEnabled: bucket.ObjectLockingEnabled,
		ReplicationEnabled:   bucket.ReplicationEnabled,
		RetentionDays:        bucket.RetentionDays,
		LegalHoldEnabled:     bucket.LegalHoldEnabled,
		Tags:                 string(tagsBytes),
		QuotaBytes:           bucket.CapacityQuotaBytes,
	}
	payloadBytes, err := proto.Marshal(syncEvent)
	if err != nil {
		return nil, apperr.Wrap(err, err, "marshal_payload_failed")
	}

	// [COMMENT]: Tạo thực thể Outbox Record để chèn đồng thời trong DB transaction với UUID v7
	eventID, err := uuid.NewV7()
	if err != nil {
		return nil, apperr.Wrap(err, err, "failed_to_generate_uuid_v7")
	}

	outbox := &storageEntity.StorageOutboxRecord{
		EventID:     eventID,
		ZoneID:      param.ZoneID,
		JobTopic:    "storage.bucket.create",
		Payload:     payloadBytes,
		OwnerID:     param.UserID,
		OwnerType:   storageEntity.StorageOwnerTypePersonal,
		ActorUserID: &param.UserID,
		Status:      storageEntity.StorageOutboxStatusPending,

		JobVersion:           1,
		ResourceID:           bucket.ID.String(),
		ResourceName:         bucket.Name,
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 60,
	}

	// [COMMENT]: Gọi DB chèn nguyên tử (atomic) 3-way CTE: bucket + credential + outbox record
	if err := s.repo.Create(ctx, bucket, param.WorkspaceID, param.ZoneID, credential, outbox); err != nil {
		if errors.Is(err, storageTaxonomy.ErrAlreadyExists) {
			result, reason = observability.ResultRejected, observability.ReasonAlreadyExists
		} else if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return nil, apperr.Wrap(err, err, "create_failed")
	}

	// [COMMENT]: Trả về credentials ngay để HTTP handler phản hồi user — secret_key chỉ hiển thị 1 lần này
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return &storageEntity.CreatedBucketResult{
		BucketID:     bucket.ID,
		BucketName:   bucket.Name,
		CredentialID: credential.ID,
		AccessKey:    accessKey,
		SecretKey:    secretKey,
		Policy:       policy,
	}, nil
}

func (s *PersonalBucketSvcImpl) GetBucket(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID) (*storageEntity.PersonalBucket, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	bucket, err := s.repo.GetByID(ctx, bucketID, userID)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return nil, apperr.Wrap(err, err, "get_failed")
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return bucket, nil
}

func (s *PersonalBucketSvcImpl) ListBuckets(ctx context.Context, workspaceID uuid.UUID, zoneID uuid.UUID, userID uuid.UUID) ([]*storageEntity.PersonalBucket, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	// [COMMENT]: Liệt kê các bucket theo workspace_id cho luồng cá nhân có check userID và zoneID
	buckets, err := s.repo.ListByWorkspace(ctx, workspaceID, zoneID, userID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "list_failed")
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return buckets, nil
}

// [COMMENT]: ListBucketNames gọi repo để lấy danh sách tên vật lý của các bucket cá nhân
func (s *PersonalBucketSvcImpl) ListBucketNames(ctx context.Context, workspaceID uuid.UUID, zoneID uuid.UUID, userID uuid.UUID) ([]string, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	names, err := s.repo.ListNamesByWorkspace(ctx, workspaceID, zoneID, userID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "list_names_failed")
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return names, nil
}

func (s *PersonalBucketSvcImpl) UpdateBucketQuota(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID, quotaBytes int64) error {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	// [COMMENT]: 1. Lấy thông tin hiện tại của bucket để làm metadata cho outbox payload (name, current quota)
	bucket, err := s.repo.GetByID(ctx, bucketID, userID)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return apperr.Wrap(err, err, "get_failed")
	}
	// [COMMENT]: Trích xuất Trace ID phục vụ distributed tracing
	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

	// [COMMENT]: 2. Chuẩn bị proto event đồng bộ resize gửi xuống dataplane
	syncEvent := &storageproto.BucketResizeSync{
		BucketId:            bucket.ID.String(),
		Name:                bucket.Name,
		CurrentQuotaBytes:   bucket.CapacityQuotaBytes,
		RequestedQuotaBytes: quotaBytes,
	}
	payloadBytes, err := proto.Marshal(syncEvent)
	if err != nil {
		return apperr.Wrap(err, err, "marshal_payload_failed")
	}

	// [COMMENT]: 3. Khởi tạo Outbox Record để cập nhật đồng thời trong transaction
	eventID, err := uuid.NewV7()
	if err != nil {
		return apperr.Wrap(err, err, "failed_to_generate_uuid_v7")
	}

	rollbackQuotaBytes := bucket.CapacityQuotaBytes
	outbox := &storageEntity.StorageOutboxRecord{
		EventID:     eventID,
		ZoneID:      bucket.ZoneID,
		JobTopic:    "storage.bucket.resize",
		Payload:     payloadBytes,
		OwnerID:     userID,
		OwnerType:   storageEntity.StorageOwnerTypePersonal,
		ActorUserID: &userID,
		Status:      storageEntity.StorageOutboxStatusPending,

		JobVersion:           1,
		ResourceID:           bucket.ID.String(),
		ResourceName:         bucket.Name,
		RollbackQuotaBytes:   &rollbackQuotaBytes,
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 30,
	}

	// [COMMENT]: 4. Thực thi cập nhật DB và ghi outbox nguyên tử
	err = s.repo.UpdateQuota(ctx, bucketID, userID, quotaBytes, outbox)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrQuotaExceeded) || errors.Is(err, storageTaxonomy.ErrResizeLimitTooLow) {
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		} else if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return apperr.Wrap(err, err, "update_quota_failed")
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return nil
}

func (s *PersonalBucketSvcImpl) UpdateBucketVersioning(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID, versioningEnabled bool) (*storageEntity.PersonalBucket, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	bucket, err := s.repo.GetByID(ctx, bucketID, userID)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return nil, apperr.Wrap(err, err, "get_bucket_failed")
	}

	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

	syncEvent := &storageproto.BucketVersioningSync{
		BucketId:          bucket.ID.String(),
		Name:              bucket.Name,
		VersioningEnabled: versioningEnabled,
	}
	payloadBytes, err := proto.Marshal(syncEvent)
	if err != nil {
		return nil, apperr.Wrap(err, err, "marshal_payload_failed")
	}

	eventID, err := uuid.NewV7()
	if err != nil {
		return nil, apperr.Wrap(err, err, "failed_to_generate_uuid_v7")
	}

	outbox := &storageEntity.StorageOutboxRecord{
		EventID:              eventID,
		ZoneID:               bucket.ZoneID,
		JobTopic:             "storage.bucket.versioning",
		Payload:              payloadBytes,
		OwnerID:              userID,
		OwnerType:            storageEntity.StorageOwnerTypePersonal,
		ActorUserID:          &userID,
		Status:               storageEntity.StorageOutboxStatusPending,
		JobVersion:           1,
		ResourceID:           bucket.ID.String(),
		ResourceName:         bucket.Name,
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 30,
	}

	updatedBucket, err := s.repo.UpdateVersioning(ctx, bucketID, userID, versioningEnabled, outbox)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return nil, apperr.Wrap(err, err, "update_versioning_failed")
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return updatedBucket, nil
}

func (s *PersonalBucketSvcImpl) GetBucketLifecycle(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID) ([]storageEntity.BucketLifecycleRule, error) {
	bucket, err := s.repo.GetByID(ctx, bucketID, userID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "get_bucket_failed")
	}
	return bucket.LifecycleRules, nil
}

func (s *PersonalBucketSvcImpl) UpdateBucketLifecycle(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID, rules []storageEntity.BucketLifecycleRule) (*storageEntity.PersonalBucket, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	bucket, err := s.repo.GetByID(ctx, bucketID, userID)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return nil, apperr.Wrap(err, err, "get_bucket_failed")
	}

	// Invariant check: if any rule has noncurrent_version_expiration_days > 0, bucket must have versioning enabled
	for _, rule := range rules {
		if rule.NoncurrentVersionExpirationDays > 0 && !bucket.VersioningEnabled {
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
			return nil, apperr.Wrap(storageTaxonomy.ErrVersioningRequired, storageTaxonomy.ErrVersioningRequired, "versioning_required_for_noncurrent_expiration")
		}
	}

	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

	protoRules := make([]*storageproto.LifecycleRuleSync, len(rules))
	for i, r := range rules {
		protoRules[i] = &storageproto.LifecycleRuleSync{
			Id:                                 r.ID,
			Enabled:                            r.Enabled,
			Prefix:                             r.Prefix,
			ExpirationDays:                     int32(r.ExpirationDays),
			NoncurrentVersionExpirationDays:    int32(r.NoncurrentVersionExpirationDays),
			AbortIncompleteMultipartUploadDays: int32(r.AbortIncompleteMultipartUploadDays),
		}
	}

	syncEvent := &storageproto.BucketLifecycleSync{
		BucketId: bucket.ID.String(),
		Name:     bucket.Name,
		Rules:    protoRules,
	}
	payloadBytes, err := proto.Marshal(syncEvent)
	if err != nil {
		return nil, apperr.Wrap(err, err, "marshal_payload_failed")
	}

	eventID, err := uuid.NewV7()
	if err != nil {
		return nil, apperr.Wrap(err, err, "failed_to_generate_uuid_v7")
	}

	outbox := &storageEntity.StorageOutboxRecord{
		EventID:              eventID,
		ZoneID:               bucket.ZoneID,
		JobTopic:             "storage.bucket.lifecycle",
		Payload:              payloadBytes,
		OwnerID:              userID,
		OwnerType:            storageEntity.StorageOwnerTypePersonal,
		ActorUserID:          &userID,
		Status:               storageEntity.StorageOutboxStatusPending,
		JobVersion:           1,
		ResourceID:           bucket.ID.String(),
		ResourceName:         bucket.Name,
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 30,
	}

	updatedBucket, err := s.repo.UpdateLifecycle(ctx, bucketID, userID, rules, outbox)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		} else if errors.Is(err, storageTaxonomy.ErrVersioningRequired) {
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		}
		return nil, apperr.Wrap(err, err, "update_lifecycle_failed")
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return updatedBucket, nil
}

func (s *PersonalBucketSvcImpl) DeleteBucket(ctx context.Context, param *storageEntity.DeletePersonalBucket) error {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	// The bucket name is server-owned durable data, never caller input. It is
	// resolved under the personal owner fence before it enters the Zone command.
	bucket, err := s.repo.GetByID(ctx, param.BucketID, param.UserID)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return apperr.Wrap(err, err, "get_bucket_failed")
	}

	// [COMMENT]: 1. Lấy danh sách access keys của toàn bộ credentials liên kết để xóa sạch trên MinIO
	accessKeys, err := s.credSvc.ListAccessKeys(ctx, param.BucketID, param.UserID)
	if err != nil {
		return apperr.Wrap(err, err, "list_credentials_failed")
	}

	// [COMMENT]: Trích xuất Trace ID phục vụ tracing
	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

	// [COMMENT]: 2. Khởi tạo và mã hóa payload protobuf BucketDeleteSync từ durable bucket name
	syncEvent := &storageproto.BucketDeleteSync{
		Name:       bucket.Name,
		AccessKeys: accessKeys,
	}
	payloadBytes, err := proto.Marshal(syncEvent)
	if err != nil {
		return apperr.Wrap(err, err, "marshal_payload_failed")
	}

	eventID, err := uuid.NewV7()
	if err != nil {
		return apperr.Wrap(err, err, "failed_to_generate_uuid_v7")
	}

	// [COMMENT]: 3. Cấu hình outbox record với zone_id từ durable bucket
	outbox := &storageEntity.StorageOutboxRecord{
		EventID:     eventID,
		ZoneID:      bucket.ZoneID,
		JobTopic:    "storage.bucket.delete",
		Payload:     payloadBytes,
		OwnerID:     param.UserID,
		OwnerType:   storageEntity.StorageOwnerTypePersonal,
		ActorUserID: &param.UserID,
		Status:      storageEntity.StorageOutboxStatusPending,

		JobVersion:           1,
		ResourceID:           param.BucketID.String(),
		ResourceName:         bucket.Name,
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 30,
	}

	// [COMMENT]: 4. Thực thi xóa cứng DB và ghi outbox nguyên tử
	err = s.repo.Delete(ctx, param.BucketID, param.UserID, outbox)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return apperr.Wrap(err, err, "delete_failed")
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return nil
}
