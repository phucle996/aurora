package storageSvcImpl

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"controlplane/internal/observability"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageSvcInterface "controlplane/internal/storage/domain/service"
	storageTaxonomy "controlplane/internal/storage/taxonomy"
	storageproto "controlplane/internal/storage/transport/rpc/proto"
	"controlplane/pkg/apperr"
	"controlplane/pkg/crypto"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: PersonalBucketServiceImpl thực thi nghiệp vụ quản trị Storage Bucket cho đối tượng Cá nhân.
type PersonalBucketSvcImpl struct {
	repo    storageRepoInterface.PersonalBucketRepo
	authRds *goredis.Client
	metrics observability.WorkflowRecorder
}

// NewPersonalBucketService wires the repository and the dedicated
// Security-State Redis in one constructor. Shared L2 Redis is intentionally
// not accepted here: an access session is an authz projection, not a cache.
func NewPersonalBucketService(
	repo storageRepoInterface.PersonalBucketRepo,
	authRds *goredis.Client,
	metrics observability.WorkflowRecorder,
) storageSvcInterface.PersonalBucketService {
	return &PersonalBucketSvcImpl{repo: repo, authRds: authRds, metrics: metrics}
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
	accessKey, err := crypto.GenerateAccessKey()
	if err != nil {
		return nil, apperr.Wrap(err, err, "gen_access_key_failed")
	}
	secretKey, err := crypto.GenerateSecretKey()
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

func (s *PersonalBucketSvcImpl) DeleteBucket(ctx context.Context, param *storageEntity.DeletePersonalBucket) error {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	// [COMMENT]: 1. Lấy danh sách access keys của toàn bộ credentials liên kết để xóa sạch trên MinIO
	accessKeys, err := s.repo.ListAccessKeys(ctx, param.BucketID, param.UserID)
	if err != nil {
		return apperr.Wrap(err, err, "list_credentials_failed")
	}

	// [COMMENT]: Trích xuất Trace ID phục vụ tracing
	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

	// [COMMENT]: 2. Khởi tạo và mã hóa payload protobuf BucketDeleteSync sử dụng tên từ param input
	syncEvent := &storageproto.BucketDeleteSync{
		Name:       param.BucketName,
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

	// [COMMENT]: 3. Cấu hình outbox record với zone_id từ param input
	outbox := &storageEntity.StorageOutboxRecord{
		EventID:     eventID,
		ZoneID:      param.ZoneID,
		JobTopic:    "storage.bucket.delete",
		Payload:     payloadBytes,
		OwnerID:     param.UserID,
		OwnerType:   storageEntity.StorageOwnerTypePersonal,
		ActorUserID: &param.UserID,
		Status:      storageEntity.StorageOutboxStatusPending,

		JobVersion:           1,
		ResourceID:           param.BucketID.String(),
		ResourceName:         param.BucketName,
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

func (s *PersonalBucketSvcImpl) CreateStorageAccessSession(ctx context.Context, param *storageEntity.StorageAccessSession) error {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	if param == nil || param.AccessSessionID == uuid.Nil || param.ResourceID == uuid.Nil || param.ActorID == uuid.Nil {
		result, reason = observability.ResultRejected, observability.ReasonInvalidArgument
		return apperr.Wrap(fmt.Errorf("access session identity is incomplete"), nil, "invalid_access_session")
	}
	if param.ExpiresAtUnixSeconds <= uint64(time.Now().Unix()) {
		result, reason = observability.ResultRejected, observability.ReasonInvalidArgument
		return apperr.Wrap(fmt.Errorf("access session expiry is in the past"), nil, "invalid_access_session_expiry")
	}

	// The random binding is retained only as a digest. The client receives the
	// opaque session id, while ACR binds it to the authenticated Trinity actor.
	param.BindingHash = fmt.Sprintf("%x", sha256.Sum256([]byte(param.AccessSessionID.String()+":"+param.ActorID.String()+":"+uuid.New().String())))
	// Auth-State Redis stores a versioned protobuf projection. The domain entity
	// remains tag-free; this binary contract is the only Go/Rust wire boundary.
	encoded, err := proto.Marshal(&storageproto.StorageAccessRecord{
		SchemaVersion:        1,
		AccessSessionId:      param.AccessSessionID.String(),
		BindingHash:          param.BindingHash,
		ActorId:              param.ActorID.String(),
		ResourceId:           param.ResourceID.String(),
		BucketName:           param.BucketName,
		WorkspaceId:          param.WorkspaceID.String(),
		ZoneId:               param.ZoneID.String(),
		Actions:              append([]string(nil), param.Actions...),
		KeyPrefix:            param.KeyPrefix,
		ExpiresAtUnixSeconds: param.ExpiresAtUnixSeconds,
		PolicyRevision:       param.PolicyRevision,
	})
	if err != nil {
		return apperr.Wrap(err, err, "marshal_access_record_failed")
	}
	ttl := time.Until(time.Unix(int64(param.ExpiresAtUnixSeconds), 0))
	if ttl <= 0 {
		result, reason = observability.ResultRejected, observability.ReasonInvalidArgument
		return apperr.Wrap(fmt.Errorf("access session ttl is not positive"), nil, "invalid_access_session_expiry")
	}
	// The opaque UUID is the lookup handle; the random digest stays inside the
	// value and is copied to the Zone for assertion/record equality checks.
	key := "storage_access:{" + param.AccessSessionID.String() + "}"

	reqProto := &storageproto.StorageAccessPrepareRequest{
		AccessSessionId:      param.AccessSessionID.String(),
		BindingHash:          param.BindingHash,
		ActorId:              param.ActorID.String(),
		ResourceId:           param.ResourceID.String(),
		BucketName:           param.BucketName,
		WorkspaceId:          param.WorkspaceID.String(),
		ZoneId:               param.ZoneID.String(),
		Actions:              append([]string(nil), param.Actions...),
		KeyPrefix:            param.KeyPrefix,
		ExpiresAtUnixSeconds: param.ExpiresAtUnixSeconds,
		PolicyRevision:       param.PolicyRevision,
	}
	payloadBytes, err := proto.Marshal(reqProto)
	if err != nil {
		return apperr.Wrap(err, err, "marshal_access_session_command_failed")
	}
	traceID := []byte(nil)
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}
	outbox := &storageEntity.StorageOutboxRecord{
		EventID:              param.AccessSessionID,
		ZoneID:               param.ZoneID,
		JobTopic:             "storage.access.prepare",
		Payload:              payloadBytes,
		OwnerID:              param.ActorID,
		OwnerType:            storageEntity.StorageOwnerTypePersonal,
		ActorUserID:          &param.ActorID,
		Status:               storageEntity.StorageOutboxStatusPending,
		JobVersion:           1,
		ResourceID:           param.ResourceID.String(),
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 30,
	}
	// The PostgreSQL outbox is the first durability boundary. A crash before
	// the following Redis write may prepare an unusable Zone record, but can
	// never authorize a request because ACR has no Central Access Record.
	if err := s.repo.CreateAccessPrepare(ctx, param, outbox); err != nil {
		return apperr.Wrap(err, err, "create_access_session_command_failed")
	}
	if err := s.authRds.Set(ctx, key, encoded, ttl).Err(); err != nil {
		result, reason = observability.ResultFailure, observability.ReasonUnavailable
		return apperr.Wrap(err, err, "persist_access_session_failed")
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return nil
}
