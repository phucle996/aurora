package storageSvcImpl

import (
	"context"
	"time"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageSvcInterface "controlplane/internal/storage/domain/service"
	storageTaxonomy "controlplane/internal/storage/taxonomy"
	storageproto "controlplane/internal/storage/transport/rpc/proto"
	"controlplane/pkg/apperr"
	"controlplane/pkg/crypto"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: TenantCredentialSvcImpl thực thi nghiệp vụ quản lý tài khoản keys của MinIO cho doanh nghiệp.
type TenantCredentialSvcImpl struct {
	repo       storageRepoInterface.TenantCredentialRepo
	bucketRepo storageRepoInterface.TenantBucketRepo
	masterKey  string
}

// [COMMENT]: NewTenantCredentialService tạo mới instance thực thi TenantCredentialService.
func NewTenantCredentialService(
	repo storageRepoInterface.TenantCredentialRepo,
	bucketRepo storageRepoInterface.TenantBucketRepo,
	masterKey string,
) storageSvcInterface.TenantCredentialService {
	return &TenantCredentialSvcImpl{
		repo:       repo,
		bucketRepo: bucketRepo,
		masterKey:  masterKey,
	}
}

func (s *TenantCredentialSvcImpl) CreateCredential(ctx context.Context, param *storageEntity.CreateTenantCredential) (*storageEntity.TenantCredential, error) {
	// [COMMENT]: Kiểm tra sự tồn tại của Bucket liên kết (Entity Existence Check)
	bucket, err := s.bucketRepo.GetByID(ctx, param.BucketID)
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

	// [COMMENT]: Mã hóa đối xứng Secret Key bằng Master Key trước khi lưu DB
	encryptedSecret, err := crypto.Encrypt(rawSecretKey, s.masterKey)
	if err != nil {
		return nil, apperr.Wrap(err, err, "encrypt_secret_failed")
	}

	cred := &storageEntity.TenantCredential{
		ID:        uuid.New(),
		BucketID:  param.BucketID,
		AccessKey: accessKey,
		SecretKey: encryptedSecret,
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

	// [COMMENT]: Trả lại thực thể chứa rawSecretKey cho Handler hiển thị duy nhất một lần cho User
	cred.SecretKey = rawSecretKey
	return cred, nil
}

func (s *TenantCredentialSvcImpl) GetCredential(ctx context.Context, credID uuid.UUID) (*storageEntity.TenantCredential, error) {
	cred, err := s.repo.GetByID(ctx, credID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "get_failed")
	}
	if cred == nil {
		return nil, apperr.Wrap(storageTaxonomy.ErrNotFound, storageTaxonomy.ErrNotFound, "credential_not_found")
	}

	// [COMMENT]: Trả về thông tin credential, giữ nguyên SecretKey đã mã hóa (hoặc giải mã nếu cần, ở đây giữ nguyên bảo mật)
	return cred, nil
}

func (s *TenantCredentialSvcImpl) ListCredentials(ctx context.Context, bucketID uuid.UUID) ([]*storageEntity.TenantCredential, error) {
	creds, err := s.repo.ListByBucket(ctx, bucketID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "list_failed")
	}
	return creds, nil
}

func (s *TenantCredentialSvcImpl) RevokeCredential(ctx context.Context, credID uuid.UUID, userID uuid.UUID) error {
	cred, err := s.repo.GetByID(ctx, credID)
	if err != nil {
		return apperr.Wrap(err, err, "get_failed")
	}
	if cred == nil {
		return apperr.Wrap(storageTaxonomy.ErrNotFound, storageTaxonomy.ErrNotFound, "credential_not_found")
	}

	// [COMMENT]: Tìm bucket liên kết để xác định ZoneID định tuyến Outbox
	bucket, err := s.bucketRepo.GetByID(ctx, cred.BucketID)
	if err != nil {
		return apperr.Wrap(err, err, "get_bucket_failed")
	}
	if bucket == nil {
		return apperr.Wrap(storageTaxonomy.ErrNotFound, storageTaxonomy.ErrNotFound, "bucket_not_found")
	}

	// [COMMENT]: Trích xuất Trace ID phục vụ distributed tracing
	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

	// [COMMENT]: Tạo sự kiện Outbox đồng bộ thu hồi (revoked) tài khoản trên MinIO
	syncEvent := &storageproto.CredentialSync{
		Id:        cred.ID.String(),
		BucketId:  cred.BucketID.String(),
		AccessKey: cred.AccessKey,
		SecretKey: "",
		Policy:    cred.Policy,
		Status:    "revoked",
	}
	payloadBytes, err := proto.Marshal(syncEvent)
	if err != nil {
		return apperr.Wrap(err, err, "marshal_payload_failed")
	}

	outbox := &storageEntity.StorageOutboxRecord{
		EventID:              uuid.New(),
		RoutingScope:         "zone:" + bucket.ZoneID.String(),
		JobTopic:             "storage.credential.revoke",
		Payload:              payloadBytes,
		UserID:               userID.String(),
		Status:               storageEntity.StorageOutboxStatusPending,
		JobVersion:           1,
		ResourceID:           cred.ID.String(),
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 60,
	}

	// [COMMENT]: Thực thi xóa cứng Credential khỏi DB Controlplane và chèn Outbox event nguyên tử
	if err := s.repo.Delete(ctx, credID, outbox); err != nil {
		return apperr.Wrap(err, err, "delete_failed")
	}

	return nil
}
