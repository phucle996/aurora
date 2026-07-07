package storageSvcImpl

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageSvcInterface "controlplane/internal/storage/domain/service"
	storageproto "controlplane/internal/storage/transport/rpc/proto"
	"controlplane/pkg/apperr"
	"controlplane/pkg/crypto"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: TenantBucketSvcImpl thực thi nghiệp vụ quản trị Storage Bucket cho đối tượng Doanh nghiệp.
type TenantBucketSvcImpl struct {
	repo storageRepoInterface.TenantBucketRepo
}

// [COMMENT]: NewTenantBucketService khởi tạo instance thực thi TenantBucketService.
func NewTenantBucketService(repo storageRepoInterface.TenantBucketRepo) storageSvcInterface.TenantBucketService {
	return &TenantBucketSvcImpl{
		repo: repo,
	}
}

// [COMMENT]: buildTenantBucketPolicy sinh chuỗi JSON policy giới hạn quyền truy cập MinIO
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

func (s *TenantBucketSvcImpl) CreateBucketForTenant(ctx context.Context, param *storageEntity.CreateTenantBucket) (*storageEntity.CreatedBucketResult, error) {
	// [COMMENT]: Khởi tạo thực thể Bucket doanh nghiệp từ tham số đầu vào
	bucket := &storageEntity.TenantBucket{
		ID:                 uuid.New(),
		Name:               param.Name,
		WorkspaceID:        param.WorkspaceID,
		ZoneID:             param.ZoneID,
		TenantID:           param.TenantID,
		Status:             storageEntity.BucketStatusCreating, // provisioning = creating
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
	policy := buildTenantBucketPolicy(bucket.Name)

	// [COMMENT]: Tạo credential entity — secret_key lưu DB dưới dạng plain text (cần thêm encryption sau)
	credential := &storageEntity.TenantCredential{
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

	// [COMMENT]: Serialize BucketSync payload kèm thông tin credential để DP provisioning MinIO Service Account
	syncEvent := &storageproto.BucketSync{
		Id:                 bucket.ID.String(),
		Name:               bucket.Name,
		ZoneId:             bucket.ZoneID.String(),
		WorkspaceId:        bucket.WorkspaceID.String(),
		TenantId:           bucket.TenantID.String(),
		Status:             string(bucket.Status),
		CapacityQuotaBytes: bucket.CapacityQuotaBytes,
		UpdatedAt:          bucket.UpdatedAt.UnixMilli(),
		// [COMMENT]: Thông tin credential để Dataplane tạo MinIO Service Account ngay trong 1 job
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

func (s *TenantBucketSvcImpl) GetBucket(ctx context.Context, bucketID uuid.UUID) (*storageEntity.TenantBucket, error) {
	bucket, err := s.repo.GetByID(ctx, bucketID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "get_failed")
	}
	return bucket, nil
}

func (s *TenantBucketSvcImpl) ListBuckets(ctx context.Context, tenantID uuid.UUID, zoneID uuid.UUID) ([]*storageEntity.TenantBucket, error) {
	buckets, err := s.repo.ListByTenantAndZone(ctx, tenantID, zoneID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "list_failed")
	}
	return buckets, nil
}

func (s *TenantBucketSvcImpl) UpdateBucketQuota(ctx context.Context, bucketID uuid.UUID, quotaBytes int64) error {
	err := s.repo.UpdateQuota(ctx, bucketID, quotaBytes)
	if err != nil {
		return apperr.Wrap(err, err, "update_quota_failed")
	}
	return nil
}

func (s *TenantBucketSvcImpl) SuspendBucket(ctx context.Context, bucketID uuid.UUID) error {
	err := s.repo.UpdateStatus(ctx, bucketID, storageEntity.BucketStatusSuspended)
	if err != nil {
		return apperr.Wrap(err, err, "suspend_failed")
	}
	return nil
}

func (s *TenantBucketSvcImpl) ResumeBucket(ctx context.Context, bucketID uuid.UUID) error {
	err := s.repo.UpdateStatus(ctx, bucketID, storageEntity.BucketStatusActive)
	if err != nil {
		return apperr.Wrap(err, err, "resume_failed")
	}
	return nil
}

func (s *TenantBucketSvcImpl) DeleteBucket(ctx context.Context, bucketID uuid.UUID) error {
	err := s.repo.Delete(ctx, bucketID)
	if err != nil {
		return apperr.Wrap(err, err, "delete_failed")
	}
	return nil
}
