package storageSvcImpl

import (
	"context"
	"time"

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

// [COMMENT]: PersonalCredentialSvcImpl thực thi nghiệp vụ quản lý tài khoản keys của MinIO cho cá nhân.
type PersonalCredentialSvcImpl struct {
	repo       storageRepoInterface.PersonalCredentialRepo
	bucketRepo storageRepoInterface.PersonalBucketRepo
	masterKey  string
}

// [COMMENT]: NewPersonalCredentialService tạo mới instance thực thi PersonalCredentialService.
func NewPersonalCredentialService(
	repo storageRepoInterface.PersonalCredentialRepo,
	bucketRepo storageRepoInterface.PersonalBucketRepo,
	masterKey string,
) storageSvcInterface.PersonalCredentialService {
	return &PersonalCredentialSvcImpl{
		repo:       repo,
		bucketRepo: bucketRepo,
		masterKey:  masterKey,
	}
}

func (s *PersonalCredentialSvcImpl) CreateCredential(ctx context.Context, param *storageEntity.CreatePersonalCredential) (*storageEntity.CreatedPersonalCredential, error) {
	// [COMMENT]: Kiểm tra sự tồn tại của Bucket liên kết (Entity Existence Check)
	bucket, err := s.bucketRepo.GetByID(ctx, param.BucketID, param.UserID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "get_bucket_failed")
	}
	if bucket == nil {
		return nil, apperr.Wrap(storageTaxonomy.ErrNotFound, storageTaxonomy.ErrNotFound, "bucket_not_found")
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

	// [COMMENT]: Khởi tạo thực thể PersonalCredential (không chứa SecretKey) để lưu xuống DB
	cred := &storageEntity.PersonalCredential{
		ID:        uuid.New(),
		BucketID:  param.BucketID,
		AccessKey: accessKey,
		Policy:    param.Policy,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// [COMMENT]: Khởi tạo thực thể CreatedPersonalCredential chứa raw Secret Key phản hồi cho Client
	createdCred := &storageEntity.CreatedPersonalCredential{
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
		BucketId:  cred.BucketID.String(),
		AccessKey: cred.AccessKey,
		SecretKey: rawSecretKey,
		Policy:    cred.Policy,
		Status:    "active",
	}
	payloadBytes, err := proto.Marshal(syncEvent)
	if err != nil {
		return nil, apperr.Wrap(err, err, "marshal_payload_failed")
	}

	outbox := &storageEntity.StorageOutboxRecord{
		EventID:              uuid.New(),
		RoutingScope:         "zone:" + bucket.ZoneID.String(),
		JobTopic:             "storage.credential.create",
		Payload:              payloadBytes,
		UserID:               param.UserID.String(),
		Status:               storageEntity.StorageOutboxStatusPending,
		JobVersion:           1,
		ResourceID:           cred.ID.String(),
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 60,
	}

	// [COMMENT]: Thực thi chèn đồng thời Credential và Outbox record
	if err := s.repo.Create(ctx, cred, outbox); err != nil {
		return nil, apperr.Wrap(err, err, "create_failed")
	}

	return createdCred, nil
}

func (s *PersonalCredentialSvcImpl) GetCredential(ctx context.Context, credID uuid.UUID, userID uuid.UUID) (*storageEntity.PersonalCredential, error) {
	cred, err := s.repo.GetByID(ctx, credID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "get_failed")
	}
	if cred == nil {
		return nil, apperr.Wrap(storageTaxonomy.ErrNotFound, storageTaxonomy.ErrNotFound, "credential_not_found")
	}

	// [COMMENT]: Validate bucket ownership using GetByID check
	bucket, err := s.bucketRepo.GetByID(ctx, cred.BucketID, userID)
	if err != nil || bucket == nil {
		return nil, apperr.Wrap(storageTaxonomy.ErrNotFound, storageTaxonomy.ErrNotFound, "bucket_not_found")
	}

	return cred, nil
}

func (s *PersonalCredentialSvcImpl) ListCredentials(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID) ([]*storageEntity.PersonalCredentialListItem, error) {
	// [COMMENT]: Validate bucket ownership using GetByID check
	bucket, err := s.bucketRepo.GetByID(ctx, bucketID, userID)
	if err != nil || bucket == nil {
		return nil, apperr.Wrap(storageTaxonomy.ErrNotFound, storageTaxonomy.ErrNotFound, "bucket_not_found")
	}

	// [COMMENT]: Gọi repo lấy trực tiếp danh sách thực thể rút gọn PersonalCredentialListItem
	creds, err := s.repo.ListByBucket(ctx, bucketID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "list_failed")
	}
	return creds, nil
}

func (s *PersonalCredentialSvcImpl) DeleteCredential(ctx context.Context, param *storageEntity.DeletePersonalCredential) error {
	// [COMMENT]: Chỉ lấy thông tin credential để build proto payload (access_key, policy).
	// Toàn bộ việc validate quyền sở hữu (workspace → user → bucket → credential) sẽ do CTE trong repo đảm nhiệm nguyên tử.
	cred, err := s.repo.GetByID(ctx, param.CredentialID)
	if err != nil {
		return apperr.Wrap(err, err, "get_failed")
	}
	if cred == nil {
		return apperr.Wrap(storageTaxonomy.ErrNotFound, storageTaxonomy.ErrNotFound, "credential_not_found")
	}

	// [COMMENT]: Trích xuất Trace ID phục vụ distributed tracing
	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

	// [COMMENT]: Tạo sự kiện Outbox đồng bộ xóa (deleted) tài khoản trên MinIO
	syncEvent := &storageproto.CredentialSync{
		Id:        cred.ID.String(),
		BucketId:  cred.BucketID.String(),
		AccessKey: cred.AccessKey,
		SecretKey: "",
		Policy:    cred.Policy,
		Status:    "deleted",
	}
	payloadBytes, err := proto.Marshal(syncEvent)
	if err != nil {
		return apperr.Wrap(err, err, "marshal_payload_failed")
	}

	// [COMMENT]: RoutingScope được resolve trực tiếp từ zone_id trong context — không cần JOIN DB hay để trống.
	outbox := &storageEntity.StorageOutboxRecord{
		EventID:              uuid.New(),
		RoutingScope:         "zone:" + param.ZoneID.String(),
		JobTopic:             "storage.credential.delete",
		Payload:              payloadBytes,
		UserID:               param.UserID.String(),
		Status:               storageEntity.StorageOutboxStatusPending,
		JobVersion:           1,
		ResourceID:           cred.ID.String(),
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 60,
	}

	// [COMMENT]: Thực thi xóa cứng Credential khỏi DB và chèn Outbox event nguyên tử.
	// CTE tự validate scope chain và tự tính routing_scope từ zone_id.
	if err := s.repo.Delete(ctx, param, outbox); err != nil {
		return apperr.Wrap(err, err, "delete_failed")
	}

	return nil
}

