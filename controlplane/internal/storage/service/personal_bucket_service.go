package storageSvcImpl

import (
	"context"
	"time"

	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageSvcInterface "controlplane/internal/storage/domain/service"
	storageproto "controlplane/internal/storage/transport/rpc/proto"
	"controlplane/pkg/apperr"

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

func (s *PersonalBucketSvcImpl) CreateBucketForPersonal(ctx context.Context, param *storageEntity.CreatePersonalBucket) error {
	// [COMMENT]: Khởi tạo thực thể Bucket cá nhân từ tham số đầu vào
	bucket := &storageEntity.PersonalBucket{
		ID:                 uuid.New(),
		Name:               param.Name,
		WorkspaceID:        param.WorkspaceID,
		ZoneID:             param.ZoneID,
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
		TenantId:           "", // Cá nhân không có Tenant ID
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
