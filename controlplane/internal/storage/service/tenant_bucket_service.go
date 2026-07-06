package storageSvcImpl

import (
	"context"
	"time"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageSvcInterface "controlplane/internal/storage/domain/service"
	storageproto "controlplane/internal/storage/transport/rpc/proto"
	"controlplane/pkg/apperr"
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

func (s *TenantBucketSvcImpl) CreateBucketForTenant(ctx context.Context, param *storageEntity.CreateTenantBucket) error {
	// [COMMENT]: Khởi tạo thực thể Bucket doanh nghiệp từ tham số đầu vào
	bucket := &storageEntity.TenantBucket{
		ID:                 uuid.New(),
		Name:               param.Name,
		WorkspaceID:        param.WorkspaceID,
		ZoneID:             param.ZoneID,
		TenantID:           param.TenantID,
		Status:             storageEntity.BucketStatusActive,
		CapacityQuotaBytes: param.CapacityQuotaBytes,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	// [COMMENT]: Trích xuất Trace ID phục vụ distributed tracing
	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

	// [COMMENT]: Serialize thông điệp đồng bộ hóa Bucket bằng Protobuf (nhị phân)
	syncEvent := &storageproto.BucketSync{
		Id:                 bucket.ID.String(),
		Name:               bucket.Name,
		ZoneId:             bucket.ZoneID.String(),
		WorkspaceId:        bucket.WorkspaceID.String(),
		TenantId:           bucket.TenantID.String(),
		Status:             string(bucket.Status),
		CapacityQuotaBytes: bucket.CapacityQuotaBytes,
		UpdatedAt:          bucket.UpdatedAt.UnixMilli(),
	}
	payloadBytes, err := proto.Marshal(syncEvent)
	if err != nil {
		return apperr.Wrap(err, err, "marshal_payload_failed")
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

	// [COMMENT]: Gọi DB chèn nguyên tử (atomic) metadata bucket và outbox record
	if err := s.repo.Create(ctx, bucket, outbox); err != nil {
		return apperr.Wrap(err, err, "create_failed")
	}

	return nil
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
