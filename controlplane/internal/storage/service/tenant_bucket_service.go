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
	// [COMMENT]: Khởi tạo thực thể Bucket doanh nghiệp từ tham số đầu vào với UUID v7
	bucketID, err := uuid.NewV7()
	if err != nil {
		return nil, apperr.Wrap(err, err, "failed_to_generate_uuid_v7")
	}

	// [COMMENT]: Sinh tên vật lý duy nhất toàn cục với prefix là 8 ký tự đầu của TenantID
	physicalName := fmt.Sprintf("tn-%s-%s", param.TenantID.String()[:8], param.Name)

	// [COMMENT]: Bucket không còn status field — tồn tại trong DB là đủ để xác định là active
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

	// [COMMENT]: Tạo credential entity — gắn kèm vào bucket doanh nghiệp với UUID v7
	credID, err := uuid.NewV7()
	if err != nil {
		return nil, apperr.Wrap(err, err, "failed_to_generate_uuid_v7")
	}

	credential := &storageEntity.TenantCredential{
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

	// [COMMENT]: Serialize BucketSync payload kèm thông tin credential để DP provisioning MinIO Service Account
	syncEvent := &storageproto.BucketSync{
		Name:      bucket.Name,
		AccessKey: accessKey,
		SecretKey: secretKey,
		Policy:    policy,
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
		EventID:              eventID,
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



func (s *TenantBucketSvcImpl) DeleteBucket(ctx context.Context, bucketID uuid.UUID) error {
	err := s.repo.Delete(ctx, bucketID)
	if err != nil {
		return apperr.Wrap(err, err, "delete_failed")
	}
	return nil
}


