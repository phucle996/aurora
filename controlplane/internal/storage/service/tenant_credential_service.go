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
	storageproto "controlplane/internal/storage/transport/rpc/proto"
	"controlplane/pkg/apperr"
	"controlplane/pkg/crypto"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: TenantCredentialSvcImpl thực thi nghiệp vụ quản lý tài khoản keys của MinIO cho doanh nghiệp.
type TenantCredentialSvcImpl struct {
	repo       storageRepoInterface.TenantCredentialRepo
	bucketRepo storageRepoInterface.TenantBucketRepo
	admission  storageRepoInterface.WalletAdmissionRepo
	metrics    observability.WorkflowRecorder
}

// [COMMENT]: NewTenantCredentialService tạo mới instance thực thi TenantCredentialService.
func NewTenantCredentialService(
	repo storageRepoInterface.TenantCredentialRepo,
	bucketRepo storageRepoInterface.TenantBucketRepo,
	admission storageRepoInterface.WalletAdmissionRepo,
	metrics observability.WorkflowRecorder,
) storageSvcInterface.TenantCredentialService {
	return &TenantCredentialSvcImpl{
		repo:       repo,
		bucketRepo: bucketRepo,
		admission:  admission,
		metrics:    metrics,
	}
}

func (s *TenantCredentialSvcImpl) CreateCredential(ctx context.Context, param *storageEntity.CreateTenantCredential) (*storageEntity.CreatedTenantCredential, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	// [COMMENT]: Kiểm tra sự tồn tại của Bucket liên kết (Entity Existence Check)
	bucket, err := s.bucketRepo.GetByID(ctx, param.BucketID)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return nil, apperr.Wrap(err, err, "get_bucket_failed")
	}
	if bucket == nil {
		result, reason = observability.ResultRejected, observability.ReasonNotFound
		return nil, apperr.Wrap(storageTaxonomy.ErrNotFound, storageTaxonomy.ErrNotFound, "bucket_not_found")
	}
	if err := s.admission.RequireOwnerAdmission(ctx, bucket.TenantID.String(), string(storageEntity.StorageOwnerTypeTenant)); err != nil {
		result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		return nil, apperr.Wrap(err, err, "wallet_admission_denied")
	}

	// [COMMENT]: Sinh ngẫu nhiên cặp Access Key và Secret Key
	accessKey, err := crypto.GenerateAccessKey()
	if err != nil {
		return nil, apperr.Wrap(err, err, "generate_access_key_failed")
	}
	rawSecretKey, err := crypto.GenerateSecretKey()
	if err != nil {
		return nil, apperr.Wrap(err, err, "generate_secret_key_failed")
	}

	// [COMMENT]: Khởi tạo thực thể TenantCredential (không chứa SecretKey) để lưu xuống DB
	cred := &storageEntity.TenantCredential{
		ID:        uuid.New(),
		BucketID:  param.BucketID,
		AccessKey: accessKey,
		Policy:    param.Policy,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// [COMMENT]: Khởi tạo thực thể CreatedTenantCredential chứa raw Secret Key phản hồi cho Client
	createdCred := &storageEntity.CreatedTenantCredential{
		ID:        cred.ID,
		BucketID:  cred.BucketID,
		AccessKey: cred.AccessKey,
		SecretKey: rawSecretKey,
		Policy:    cred.Policy,
		CreatedAt: cred.CreatedAt,
		UpdatedAt: cred.UpdatedAt,
	}

	// [COMMENT]: Trích xuất Trace ID phục vụ distributed tracing
	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

	// [COMMENT]: Gửi bản nháp dạng raw Secret Key qua CDC Outbox sang Dataplane
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

	outbox := &storageEntity.StorageOutboxRecord{
		EventID:     uuid.New(),
		ZoneID:      bucket.ZoneID,
		JobTopic:    "storage.credential.create",
		Payload:     payloadBytes,
		OwnerID:     bucket.TenantID,
		OwnerType:   storageEntity.StorageOwnerTypeTenant,
		ActorUserID: &param.UserID,
		Status:      storageEntity.StorageOutboxStatusPending,

		JobVersion:           1,
		ResourceID:           cred.ID.String(),
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 60,
	}

	// [COMMENT]: Thực thi chèn đồng thời Credential và Outbox record
	if err := s.repo.Create(ctx, cred, outbox); err != nil {
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

func (s *TenantCredentialSvcImpl) ListCredentials(ctx context.Context, bucketID uuid.UUID) ([]*storageEntity.TenantCredential, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	creds, err := s.repo.ListByBucket(ctx, bucketID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "list_failed")
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return creds, nil
}

func (s *TenantCredentialSvcImpl) DeleteCredential(ctx context.Context, param *storageEntity.DeleteTenantCredential) error {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	// [COMMENT]: Lấy thông tin bucket để trích xuất TenantID làm OwnerID cho Outbox record
	bucket, err := s.bucketRepo.GetByID(ctx, param.BucketID)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return apperr.Wrap(err, err, "get_bucket_failed")
	}

	// [COMMENT]: Trích xuất Trace ID phục vụ distributed tracing
	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

	// [COMMENT]: Tạo sự kiện Outbox đồng bộ xóa (deleted) tài khoản trên MinIO.
	// Chỉ cần access_key để MinIO xác định user và tự derive policy_name = "policy-{access_key}".
	syncEvent := &storageproto.CredentialSync{
		Id:        param.CredentialID.String(),
		AccessKey: param.AccessKey,
		SecretKey: "",
		Policy:    "",
	}
	payloadBytes, err := proto.Marshal(syncEvent)
	if err != nil {
		return apperr.Wrap(err, err, "marshal_payload_failed")
	}

	// [COMMENT]: ZoneID được bind từ request context đã xác minh; outbox không tự suy luận lại route.
	outbox := &storageEntity.StorageOutboxRecord{
		EventID:     uuid.New(),
		ZoneID:      param.ZoneID,
		JobTopic:    "storage.credential.delete",
		Payload:     payloadBytes,
		OwnerID:     bucket.TenantID,
		OwnerType:   storageEntity.StorageOwnerTypeTenant,
		ActorUserID: &param.UserID,
		Status:      storageEntity.StorageOutboxStatusPending,

		JobVersion:           1,
		ResourceID:           param.CredentialID.String(),
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 60,
	}

	// [COMMENT]: Thực thi xóa cứng Credential khỏi DB và chèn Outbox event nguyên tử.
	// CTE tự validate ownership chain trước khi ghi immutable zone_id.
	if err := s.repo.Delete(ctx, param, outbox); err != nil {
		if errors.Is(err, storageTaxonomy.ErrCredentialNotFound) || errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return apperr.Wrap(err, err, "delete_failed")
	}

	result, reason = observability.ResultSuccess, observability.ReasonNone
	return nil
}
