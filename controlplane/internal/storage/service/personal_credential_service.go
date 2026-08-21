package storageSvcImpl

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

// [COMMENT]: PersonalCredentialSvcImpl thực thi nghiệp vụ quản lý tài khoản keys của MinIO cho cá nhân.
type PersonalCredentialSvcImpl struct {
	repo       storageRepoInterface.PersonalCredentialRepo
	bucketRepo storageRepoInterface.PersonalBucketRepo
	metrics    observability.WorkflowRecorder
}

// [COMMENT]: NewPersonalCredentialService tạo mới instance thực thi PersonalCredentialService.
func NewPersonalCredentialService(
	repo storageRepoInterface.PersonalCredentialRepo,
	bucketRepo storageRepoInterface.PersonalBucketRepo,
	metrics observability.WorkflowRecorder,
) storageSvcInterface.PersonalCredentialService {
	return &PersonalCredentialSvcImpl{
		repo:       repo,
		bucketRepo: bucketRepo,
		metrics:    metrics,
	}
}

func (s *PersonalCredentialSvcImpl) CreateCredential(ctx context.Context, param *storageEntity.CreatePersonalCredential) (*storageEntity.CreatedPersonalCredential, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	// [COMMENT]: Kiểm tra an toàn: Đảm bảo policy JSON chỉ cho phép truy cập vào đúng bucketName được truyền
	if !validatePolicyBucketName(param.Policy, param.BucketName) {
		result, reason = observability.ResultRejected, observability.ReasonInvalidArgument
		return nil, apperr.Wrap(storageTaxonomy.ErrInvalidPolicy, storageTaxonomy.ErrInvalidPolicy, "policy_violates_bucket_boundary")
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

	// [COMMENT]: Điền các trường thông tin credential được sinh vào param
	param.ID = uuid.New()
	param.AccessKey = accessKey

	// [COMMENT]: Khởi tạo thực thể CreatedPersonalCredential chứa raw Secret Key phản hồi cho Client
	createdCred := &storageEntity.CreatedPersonalCredential{
		ID:        param.ID,
		AccessKey: accessKey,
		SecretKey: rawSecretKey,
		Policy:    param.Policy,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// [COMMENT]: Trích xuất Trace ID phục vụ distributed tracing
	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

	// [COMMENT]: Gửi bản nháp dạng raw Secret Key qua CDC Outbox sang Dataplane (không kèm BucketId và Status)
	syncEvent := &storageproto.CredentialSync{
		Id:        param.ID.String(),
		AccessKey: accessKey,
		SecretKey: rawSecretKey,
		Policy:    param.Policy,
	}
	payloadBytes, err := proto.Marshal(syncEvent)
	if err != nil {
		return nil, apperr.Wrap(err, err, "marshal_payload_failed")
	}

	outbox := &storageEntity.StorageOutboxRecord{
		EventID:     uuid.New(),
		ZoneID:      param.ZoneID,
		JobTopic:    "storage.credential.create",
		Payload:     payloadBytes,
		OwnerID:     param.UserID,
		OwnerType:   storageEntity.StorageOwnerTypePersonal,
		ActorUserID: &param.UserID,
		Status:      storageEntity.StorageOutboxStatusPending,

		JobVersion:           1,
		ResourceID:           param.ID.String(),
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 60,
	}

	// [COMMENT]: Thực thi chèn đồng thời Credential và Outbox record với xác thực chéo scope
	bucketID, err := s.repo.Create(ctx, param, outbox)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrAlreadyExists) {
			result, reason = observability.ResultRejected, observability.ReasonAlreadyExists
		} else if errors.Is(err, storageTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return nil, apperr.Wrap(err, err, "create_failed")
	}

	createdCred.BucketID = bucketID
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return createdCred, nil
}

func (s *PersonalCredentialSvcImpl) ListCredentials(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID) ([]*storageEntity.PersonalCredentialListItem, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	// [COMMENT]: Validate bucket ownership using GetByID check
	bucket, err := s.bucketRepo.GetByID(ctx, bucketID, userID)
	if err != nil || bucket == nil {
		result, reason = observability.ResultRejected, observability.ReasonNotFound
		return nil, apperr.Wrap(storageTaxonomy.ErrNotFound, storageTaxonomy.ErrNotFound, "bucket_not_found")
	}

	// [COMMENT]: Gọi repo lấy trực tiếp danh sách thực thể rút gọn PersonalCredentialListItem
	creds, err := s.repo.ListByBucket(ctx, bucketID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "list_failed")
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return creds, nil
}

func (s *PersonalCredentialSvcImpl) DeleteCredential(ctx context.Context, param *storageEntity.DeletePersonalCredential) error {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	// [COMMENT]: Trích xuất Trace ID phục vụ distributed tracing
	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

	// [COMMENT]: Tạo sự kiện Outbox đồng bộ xóa (deleted) tài khoản trên MinIO.
	// Chỉ cần access_key để MinIO xác định user và derive policy_name = "policy-{access_key}".
	// Không cần policy JSON — Dataplane tự tính policy_name từ access_key khi xóa.
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
		OwnerID:     param.UserID,
		OwnerType:   storageEntity.StorageOwnerTypePersonal,
		ActorUserID: &param.UserID,
		Status:      storageEntity.StorageOutboxStatusPending,

		JobVersion:           1,
		ResourceID:           param.CredentialID.String(),
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 30,
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

func (s *PersonalCredentialSvcImpl) ListAccessKeys(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID) ([]string, error) {
	return s.repo.ListAccessKeys(ctx, bucketID, userID)
}

// [COMMENT]: PolicyStatement và PolicyDoc dùng để parse cấu trúc JSON Policy từ Client
type PolicyStatement struct {
	Effect   string `json:"Effect"`
	Resource any    `json:"Resource"`
}

type PolicyDoc struct {
	Statement []PolicyStatement `json:"Statement"`
}

// [COMMENT]: validatePolicyBucketName xác thực chéo mọi Resource trong Allow statements phải thuộc về bucketName
func validatePolicyBucketName(policyJSON string, bucketName string) bool {
	var doc PolicyDoc
	if err := json.Unmarshal([]byte(policyJSON), &doc); err != nil {
		return false
	}
	allowedPrefix := "arn:aws:s3:::" + bucketName
	for _, stmt := range doc.Statement {
		if stmt.Effect == "Allow" && stmt.Resource != nil {
			var resources []string
			switch r := stmt.Resource.(type) {
			case string:
				resources = []string{r}
			case []any:
				for _, item := range r {
					if str, ok := item.(string); ok {
						resources = append(resources, str)
					}
				}
			}
			for _, res := range resources {
				// Phải khớp chính xác bucketName hoặc là sub-path (bắt đầu bằng bucketName/)
				if res != allowedPrefix && !strings.HasPrefix(res, allowedPrefix+"/") {
					return false
				}
			}
		}
	}
	return true
}
