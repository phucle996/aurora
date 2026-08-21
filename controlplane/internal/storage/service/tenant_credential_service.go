package storageSvcImpl

import (
	"context"
	"errors"
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

// TenantCredentialSvcImpl thực thi nghiệp vụ quản lý tài khoản keys của MinIO cho doanh nghiệp.
type TenantCredentialSvcImpl struct {
	repo       storageRepoInterface.TenantCredentialRepo
	bucketRepo storageRepoInterface.TenantBucketRepo
	metrics    observability.WorkflowRecorder
}

// NewTenantCredentialService tạo mới instance thực thi TenantCredentialService.
func NewTenantCredentialService(
	repo storageRepoInterface.TenantCredentialRepo,
	bucketRepo storageRepoInterface.TenantBucketRepo,
	metrics observability.WorkflowRecorder,
) storageSvcInterface.TenantCredentialService {
	return &TenantCredentialSvcImpl{
		repo:       repo,
		bucketRepo: bucketRepo,
		metrics:    metrics,
	}
}

func (s *TenantCredentialSvcImpl) CreateCredential(
	ctx context.Context,
	param *storageEntity.CreateTenantCredential,
) (*storageEntity.CreatedTenantCredential, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	bucket, err := s.bucketRepo.GetByID(ctx, param.BucketID, param.WorkspaceID, param.TenantID, param.UserID, param.ZoneID)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return nil, apperr.Wrap(err, err, "get_bucket_failed")
	}

	accessKey, err := crypto.GenerateAccessKey()
	if err != nil {
		return nil, apperr.Wrap(err, err, "generate_access_key_failed")
	}
	rawSecretKey, err := crypto.GenerateSecretKey()
	if err != nil {
		return nil, apperr.Wrap(err, err, "generate_secret_key_failed")
	}

	policy := param.Policy
	if policy == "" {
		policy = buildTenantBucketPolicy(bucket.Name)
	}

	credID, err := uuid.NewV7()
	if err != nil {
		return nil, apperr.Wrap(err, err, "failed_to_generate_uuid_v7")
	}

	cred := &storageEntity.TenantCredential{
		ID:        credID,
		BucketID:  param.BucketID,
		AccessKey: accessKey,
		Policy:    policy,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	createdCred := &storageEntity.CreatedTenantCredential{
		ID:        cred.ID,
		BucketID:  cred.BucketID,
		AccessKey: cred.AccessKey,
		SecretKey: rawSecretKey,
		Policy:    cred.Policy,
		CreatedAt: cred.CreatedAt,
		UpdatedAt: cred.UpdatedAt,
	}

	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

	syncEvent := &storageproto.CredentialSync{
		Id:        cred.ID.String(),
		AccessKey: cred.AccessKey,
		SecretKey: rawSecretKey,
		Policy:    cred.Policy,
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
		JobTopic:             "storage.credential.create",
		Payload:              payloadBytes,
		OwnerID:              bucket.TenantID,
		OwnerType:            storageEntity.StorageOwnerTypeTenant,
		ActorUserID:          &param.UserID,
		Status:               storageEntity.StorageOutboxStatusPending,
		JobVersion:           1,
		ResourceID:           cred.ID.String(),
		ResourceName:         bucket.Name,
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 60,
	}

	if err := s.repo.Create(ctx, cred, param.WorkspaceID, param.TenantID, param.UserID, param.ZoneID, outbox); err != nil {
		if errors.Is(err, storageTaxonomy.ErrAlreadyExists) {
			result, reason = observability.ResultRejected, observability.ReasonAlreadyExists
		} else if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return nil, apperr.Wrap(err, err, "create_failed")
	}

	result, reason = observability.ResultSuccess, observability.ReasonNone
	return createdCred, nil
}

func (s *TenantCredentialSvcImpl) ListCredentials(
	ctx context.Context,
	bucketID uuid.UUID,
	workspaceID uuid.UUID,
	tenantID uuid.UUID,
	userID uuid.UUID,
	zoneID uuid.UUID,
) ([]*storageEntity.TenantCredential, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	creds, err := s.repo.ListByBucket(ctx, bucketID, workspaceID, tenantID, userID, zoneID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "list_failed")
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return creds, nil
}

func (s *TenantCredentialSvcImpl) DeleteCredential(
	ctx context.Context,
	param *storageEntity.DeleteTenantCredential,
) error {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	cred, err := s.repo.GetByID(ctx, param.CredentialID, param.BucketID, param.WorkspaceID, param.TenantID, param.UserID, param.ZoneID)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return apperr.Wrap(err, err, "get_credential_failed")
	}

	param.AccessKey = cred.AccessKey

	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

	syncEvent := &storageproto.CredentialSync{
		Id:        param.CredentialID.String(),
		AccessKey: param.AccessKey,
		Policy:    "",
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
		ZoneID:               param.ZoneID,
		JobTopic:             "storage.credential.delete",
		Payload:              payloadBytes,
		OwnerID:              param.TenantID,
		OwnerType:            storageEntity.StorageOwnerTypeTenant,
		ActorUserID:          &param.UserID,
		Status:               storageEntity.StorageOutboxStatusPending,
		JobVersion:           1,
		ResourceID:           param.CredentialID.String(),
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 30,
	}

	err = s.repo.Delete(ctx, param, outbox)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return apperr.Wrap(err, err, "delete_failed")
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return nil
}

func (s *TenantCredentialSvcImpl) ListAccessKeys(
	ctx context.Context,
	bucketID uuid.UUID,
	workspaceID uuid.UUID,
	tenantID uuid.UUID,
	userID uuid.UUID,
	zoneID uuid.UUID,
) ([]string, error) {
	return s.repo.ListAccessKeys(ctx, bucketID, workspaceID, tenantID, userID, zoneID)
}
