package storageSvcImpl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/observability"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageSvcInterface "controlplane/internal/storage/domain/service"
	storageTaxonomy "controlplane/internal/storage/taxonomy"
	storageproto "controlplane/internal/storage/transport/proto"
	"controlplane/pkg/apperr"
	"controlplane/pkg/crypto"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

// TenantBucketSvcImpl thực thi nghiệp vụ quản trị Storage Bucket cho đối tượng Doanh nghiệp.
type TenantBucketSvcImpl struct {
	repo    storageRepoInterface.TenantBucketRepo
	credSvc storageSvcInterface.TenantCredentialService
	metrics observability.WorkflowRecorder
}

// NewTenantBucketService khởi tạo instance thực thi TenantBucketService.
func NewTenantBucketService(
	repo storageRepoInterface.TenantBucketRepo,
	credSvc storageSvcInterface.TenantCredentialService,
	metrics observability.WorkflowRecorder,
) storageSvcInterface.TenantBucketService {
	return &TenantBucketSvcImpl{
		repo:    repo,
		credSvc: credSvc,
		metrics: metrics,
	}
}

// buildTenantBucketPolicy sinh chuỗi JSON policy giới hạn quyền truy cập MinIO
// chỉ vào đúng bucket chỉ định. Dùng chuẩn AWS S3 Policy format tương thích với MinIO.
func buildTenantBucketPolicy(bucketName string) string {
	return fmt.Sprintf(`{
		"Version":"2012-10-17",
		"Statement":[{
			"Effect":"Allow",
			"Action":["s3:GetObject","s3:PutObject","s3:DeleteObject","s3:ListBucket"],
			"Resource":["arn:aws:s3:::%s","arn:aws:s3:::%s/*"]
		}]
	}`, bucketName, bucketName)
}

func (s *TenantBucketSvcImpl) CreateBucketForTenant(
	ctx context.Context,
	param *storageEntity.CreateTenantBucket,
) (*storageEntity.CreatedBucketResult, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()


	bucketID, err := uuid.NewV7()
	if err != nil {
		return nil, apperr.Wrap(err, err, "failed_to_generate_uuid_v7")
	}

	physicalName := fmt.Sprintf("tn-%s-%s", param.TenantID.String()[:8], param.Name)

	bucket := &storageEntity.TenantBucket{
		ID:                 bucketID,
		Name:               physicalName,
		WorkspaceID:        param.WorkspaceID,
		ZoneID:             param.ZoneID,
		TenantID:           param.TenantID,
		CapacityQuotaBytes: param.CapacityQuotaBytes,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	accessKey, err := crypto.GenerateAccessKey()
	if err != nil {
		return nil, apperr.Wrap(err, err, "gen_access_key_failed")
	}
	secretKey, err := crypto.GenerateSecretKey()
	if err != nil {
		return nil, apperr.Wrap(err, err, "gen_secret_key_failed")
	}

	policy := buildTenantBucketPolicy(bucket.Name)

	credID, err := uuid.NewV7()
	if err != nil {
		return nil, apperr.Wrap(err, err, "failed_to_generate_uuid_v7")
	}

	credential := &storageEntity.TenantCredential{
		ID:        credID,
		BucketID:  bucket.ID,
		AccessKey: accessKey,
		Policy:    policy,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

	syncEvent := &storageproto.BucketCreateSync{
		Name:       bucket.Name,
		AccessKey:  accessKey,
		SecretKey:  secretKey,
		Policy:     policy,
		QuotaBytes: bucket.CapacityQuotaBytes,
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
		JobTopic:             "storage.bucket.create",
		Payload:              payloadBytes,
		OwnerID:              bucket.TenantID,
		OwnerType:            storageEntity.StorageOwnerTypeTenant,
		ActorUserID:          &param.UserID,
		Status:               storageEntity.StorageOutboxStatusPending,
		JobVersion:           1,
		ResourceID:           bucket.ID.String(),
		ResourceName:         bucket.Name,
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 60,
	}

	if err := s.repo.Create(ctx, bucket, credential, param.UserID, outbox); err != nil {
		if errors.Is(err, storageTaxonomy.ErrAlreadyExists) {
			result, reason = observability.ResultRejected, observability.ReasonAlreadyExists
		} else if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return nil, apperr.Wrap(err, err, "create_failed")
	}

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

func (s *TenantBucketSvcImpl) GetBucket(
	ctx context.Context,
	bucketID uuid.UUID,
	workspaceID uuid.UUID,
	tenantID uuid.UUID,
	userID uuid.UUID,
	zoneID uuid.UUID,
) (*storageEntity.TenantBucket, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	bucket, err := s.repo.GetByID(ctx, bucketID, workspaceID, tenantID, userID, zoneID)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return nil, apperr.Wrap(err, err, "get_failed")
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return bucket, nil
}

func (s *TenantBucketSvcImpl) ListBuckets(
	ctx context.Context,
	workspaceID uuid.UUID,
	tenantID uuid.UUID,
	userID uuid.UUID,
	zoneID uuid.UUID,
) ([]*storageEntity.TenantBucket, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	buckets, err := s.repo.ListByWorkspace(ctx, workspaceID, tenantID, userID, zoneID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "list_failed")
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return buckets, nil
}

func (s *TenantBucketSvcImpl) UpdateBucketQuota(
	ctx context.Context,
	param *storageEntity.UpdateTenantBucketQuota,
) error {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	bucket, err := s.repo.GetByID(ctx, param.BucketID, param.WorkspaceID, param.TenantID, param.UserID, param.ZoneID)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return apperr.Wrap(err, err, "get_failed")
	}

	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

	syncEvent := &storageproto.BucketResizeSync{
		BucketId:            bucket.ID.String(),
		Name:                bucket.Name,
		CurrentQuotaBytes:   bucket.CapacityQuotaBytes,
		RequestedQuotaBytes: param.QuotaBytes,
	}
	payloadBytes, err := proto.Marshal(syncEvent)
	if err != nil {
		return apperr.Wrap(err, err, "marshal_payload_failed")
	}

	eventID, err := uuid.NewV7()
	if err != nil {
		return apperr.Wrap(err, err, "failed_to_generate_uuid_v7")
	}

	rollbackQuotaBytes := bucket.CapacityQuotaBytes
	outbox := &storageEntity.StorageOutboxRecord{
		EventID:              eventID,
		ZoneID:               bucket.ZoneID,
		JobTopic:             "storage.bucket.resize",
		Payload:              payloadBytes,
		OwnerID:              bucket.TenantID,
		OwnerType:            storageEntity.StorageOwnerTypeTenant,
		ActorUserID:          &param.UserID,
		Status:               storageEntity.StorageOutboxStatusPending,
		JobVersion:           1,
		ResourceID:           bucket.ID.String(),
		ResourceName:         bucket.Name,
		RollbackQuotaBytes:   &rollbackQuotaBytes,
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 30,
	}

	err = s.repo.UpdateQuota(ctx, param, outbox)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrResizeLimitTooLow) {
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		} else if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return apperr.Wrap(err, err, "update_quota_failed")
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return nil
}

func (s *TenantBucketSvcImpl) UpdateBucketVersioning(
	ctx context.Context,
	param *storageEntity.UpdateTenantBucketVersioning,
) (*storageEntity.TenantBucket, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	bucket, err := s.repo.GetByID(ctx, param.BucketID, param.WorkspaceID, param.TenantID, param.UserID, param.ZoneID)
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
		VersioningEnabled: param.VersioningEnabled,
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
		OwnerID:              bucket.TenantID,
		OwnerType:            storageEntity.StorageOwnerTypeTenant,
		ActorUserID:          &param.UserID,
		Status:               storageEntity.StorageOutboxStatusPending,
		JobVersion:           1,
		ResourceID:           bucket.ID.String(),
		ResourceName:         bucket.Name,
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 30,
	}

	updatedBucket, err := s.repo.UpdateVersioning(ctx, param, outbox)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return nil, apperr.Wrap(err, err, "update_versioning_failed")
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return updatedBucket, nil
}

func (s *TenantBucketSvcImpl) GetBucketLifecycle(
	ctx context.Context,
	bucketID uuid.UUID,
	workspaceID uuid.UUID,
	tenantID uuid.UUID,
	userID uuid.UUID,
	zoneID uuid.UUID,
) ([]storageEntity.BucketLifecycleRule, error) {
	bucket, err := s.repo.GetByID(ctx, bucketID, workspaceID, tenantID, userID, zoneID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "get_bucket_failed")
	}
	return bucket.LifecycleRules, nil
}

func (s *TenantBucketSvcImpl) UpdateBucketLifecycle(
	ctx context.Context,
	param *storageEntity.UpdateTenantBucketLifecycle,
) (*storageEntity.TenantBucket, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	bucket, err := s.repo.GetByID(ctx, param.BucketID, param.WorkspaceID, param.TenantID, param.UserID, param.ZoneID)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return nil, apperr.Wrap(err, err, "get_bucket_failed")
	}

	// Invariant: Noncurrent version expiration requires versioning to be enabled
	for _, rule := range param.Rules {
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

	protoRules := make([]*storageproto.LifecycleRuleSync, len(param.Rules))
	for i, r := range param.Rules {
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
		OwnerID:              bucket.TenantID,
		OwnerType:            storageEntity.StorageOwnerTypeTenant,
		ActorUserID:          &param.UserID,
		Status:               storageEntity.StorageOutboxStatusPending,
		JobVersion:           1,
		ResourceID:           bucket.ID.String(),
		ResourceName:         bucket.Name,
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 30,
	}

	updatedBucket, err := s.repo.UpdateLifecycle(ctx, param, outbox)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return nil, apperr.Wrap(err, err, "update_lifecycle_failed")
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return updatedBucket, nil
}

func (s *TenantBucketSvcImpl) DeleteBucket(
	ctx context.Context,
	param *storageEntity.DeleteTenantBucket,
) error {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	bucket, err := s.repo.GetByID(ctx, param.BucketID, param.WorkspaceID, param.TenantID, param.UserID, param.ZoneID)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return apperr.Wrap(err, err, "get_bucket_failed")
	}

	accessKeys, err := s.credSvc.ListAccessKeys(ctx, param.BucketID, param.WorkspaceID, param.TenantID, param.UserID, param.ZoneID)
	if err != nil {
		return apperr.Wrap(err, err, "list_credentials_failed")
	}

	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

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

	outbox := &storageEntity.StorageOutboxRecord{
		EventID:              eventID,
		ZoneID:               bucket.ZoneID,
		JobTopic:             "storage.bucket.delete",
		Payload:              payloadBytes,
		OwnerID:              bucket.TenantID,
		OwnerType:            storageEntity.StorageOwnerTypeTenant,
		ActorUserID:          &param.UserID,
		Status:               storageEntity.StorageOutboxStatusPending,
		JobVersion:           1,
		ResourceID:           bucket.ID.String(),
		ResourceName:         bucket.Name,
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 30,
	}

	err = s.repo.Delete(ctx, param.BucketID, param.WorkspaceID, param.TenantID, param.UserID, param.ZoneID, outbox)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return apperr.Wrap(err, err, "delete_failed")
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return nil
}
