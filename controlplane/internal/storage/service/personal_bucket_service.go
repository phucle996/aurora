package storageSvcImpl

import (
	"context"
	"fmt"
	"time"

	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageSvcInterface "controlplane/internal/storage/domain/service"
	storageproto "controlplane/internal/storage/transport/rpc/proto"
	"controlplane/pkg/apperr"
	"controlplane/pkg/crypto"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: PersonalBucketServiceImpl thực thi nghiệp vụ quản trị Storage Bucket cho đối tượng Cá nhân.
type PersonalBucketSvcImpl struct {
	repo storageRepoInterface.PersonalBucketRepo
}

// [COMMENT]: NewPersonalBucketService khởi tạo instance thực thi PersonalBucketService.
func NewPersonalBucketService(repo storageRepoInterface.PersonalBucketRepo) storageSvcInterface.PersonalBucketService {
	return &PersonalBucketSvcImpl{
		repo: repo,
	}
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
	// [COMMENT]: Khởi tạo thực thể Bucket cá nhân từ tham số đầu vào
	bucket := &storageEntity.PersonalBucket{
		ID:                 uuid.New(),
		Name:               param.Name,
		WorkspaceID:        param.WorkspaceID,
		ZoneID:             param.ZoneID,
		Status:             storageEntity.BucketStatusCreating,
		CapacityQuotaBytes: param.CapacityQuotaBytes,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
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

	// [COMMENT]: Sinh bucket policy giới hạn quyền chỉ vào đúng bucket này
	policy := buildPersonalBucketPolicy(bucket.Name)

	// [COMMENT]: Tạo credential entity — gắn kèm vào bucket cá nhân
	credential := &storageEntity.PersonalCredential{
		ID:        uuid.New(),
		BucketID:  bucket.ID,
		AccessKey: accessKey,
		SecretKey: secretKey, // TODO: AES-GCM encrypt trước khi lưu
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

	// [COMMENT]: Serialize BucketSync payload kèm credential để DP provisioning MinIO Service Account
	syncEvent := &storageproto.BucketSync{
		Id:                 bucket.ID.String(),
		Name:               bucket.Name,
		ZoneId:             bucket.ZoneID.String(),
		WorkspaceId:        bucket.WorkspaceID.String(),
		TenantId:           "", // Cá nhân không có Tenant ID
		Status:             string(bucket.Status),
		CapacityQuotaBytes: bucket.CapacityQuotaBytes,
		UpdatedAt:          bucket.UpdatedAt.UnixMilli(),
		// [COMMENT]: Thông tin credential để Dataplane tạo MinIO Service Account trong 1 job
		CredentialId: credential.ID.String(),
		AccessKey:    accessKey,
		SecretKey:    secretKey,
		Policy:       policy,
	}
	payloadBytes, err := proto.Marshal(syncEvent)
	if err != nil {
		return nil, apperr.Wrap(err, err, "marshal_payload_failed")
	}

	// [COMMENT]: Tạo thực thể Outbox Record để chèn đồng thời trong DB transaction
	outbox := &storageEntity.StorageOutboxRecord{
		EventID:              uuid.New(),
		RoutingScope:         "zone:" + bucket.ZoneID.String(),
		JobTopic:             "storage.bucket.create",
		Payload:              payloadBytes,
		UserID:               param.UserID.String(),
		Status:               storageEntity.StorageOutboxStatusPending,
		JobVersion:           1,
		ResourceID:           bucket.ID.String(),
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 60,
	}

	// [COMMENT]: Gọi DB chèn nguyên tử (atomic) 3-way CTE: bucket + credential + outbox record
	if err := s.repo.Create(ctx, bucket, credential, outbox); err != nil {
		return nil, apperr.Wrap(err, err, "create_failed")
	}

	// [COMMENT]: Trả về credentials ngay để HTTP handler phản hồi user — secret_key chỉ hiển thị 1 lần này
	return &storageEntity.CreatedBucketResult{
		BucketID:     bucket.ID,
		BucketName:   bucket.Name,
		CredentialID: credential.ID,
		AccessKey:    accessKey,
		SecretKey:    secretKey,
		Policy:       policy,
	}, nil
}

func (s *PersonalBucketSvcImpl) GetBucket(ctx context.Context, bucketID uuid.UUID) (*storageEntity.PersonalBucket, error) {
	bucket, err := s.repo.GetByID(ctx, bucketID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "get_failed")
	}
	return bucket, nil
}

func (s *PersonalBucketSvcImpl) ListBuckets(ctx context.Context, workspaceID uuid.UUID) ([]*storageEntity.PersonalBucket, error) {
	// [COMMENT]: Liệt kê các bucket theo workspace_id cho luồng cá nhân
	buckets, err := s.repo.ListByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "list_failed")
	}
	return buckets, nil
}

func (s *PersonalBucketSvcImpl) UpdateBucketQuota(ctx context.Context, bucketID uuid.UUID, quotaBytes int64) error {
	err := s.repo.UpdateQuota(ctx, bucketID, quotaBytes)
	if err != nil {
		return apperr.Wrap(err, err, "update_quota_failed")
	}
	return nil
}

func (s *PersonalBucketSvcImpl) SuspendBucket(ctx context.Context, bucketID uuid.UUID) error {
	err := s.repo.UpdateStatus(ctx, bucketID, storageEntity.BucketStatusSuspended)
	if err != nil {
		return apperr.Wrap(err, err, "suspend_failed")
	}
	return nil
}

func (s *PersonalBucketSvcImpl) ResumeBucket(ctx context.Context, bucketID uuid.UUID) error {
	err := s.repo.UpdateStatus(ctx, bucketID, storageEntity.BucketStatusActive)
	if err != nil {
		return apperr.Wrap(err, err, "resume_failed")
	}
	return nil
}

func (s *PersonalBucketSvcImpl) DeleteBucket(ctx context.Context, bucketID uuid.UUID) error {
	err := s.repo.Delete(ctx, bucketID)
	if err != nil {
		return apperr.Wrap(err, err, "delete_failed")
	}
	return nil
}
