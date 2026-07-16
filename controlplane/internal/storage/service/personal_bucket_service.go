package storageSvcImpl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	// [COMMENT]: Khởi tạo thực thể Bucket cá nhân từ tham số đầu vào với UUID v7
	bucketID, err := uuid.NewV7()
	if err != nil {
		return nil, apperr.Wrap(err, err, "failed_to_generate_uuid_v7")
	}

	// [COMMENT]: Sinh tên vật lý duy nhất toàn cục với prefix là 8 ký tự đầu của WorkspaceID
	physicalName := fmt.Sprintf("ws-%s-%s", param.WorkspaceID.String()[:8], param.Name)

	// [COMMENT]: Bucket không còn status field — tồn tại trong DB là đủ để xác định là active
	bucket := &storageEntity.PersonalBucket{
		ID:                 bucketID,
		Name:               physicalName,
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

	// [COMMENT]: Sử dụng custom bucket policy được truyền từ client
	// Thay thế placeholder <BUCKET_NAME> bằng tên vật lý thực tế của bucket
	policy := strings.ReplaceAll(param.Policy, "<BUCKET_NAME>", bucket.Name)

	// [COMMENT]: Validate tính hợp lệ của JSON policy
	var js map[string]interface{}
	if err := json.Unmarshal([]byte(policy), &js); err != nil {
		return nil, apperr.Wrap(storageTaxonomy.ErrInvalidPolicy, err, "invalid_policy_format")
	}

	// [COMMENT]: Tạo credential entity — gắn kèm vào bucket cá nhân với UUID v7
	credID, err := uuid.NewV7()
	if err != nil {
		return nil, apperr.Wrap(err, err, "failed_to_generate_uuid_v7")
	}

	credential := &storageEntity.PersonalCredential{
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

	// [COMMENT]: Serialize BucketSync payload kèm credential để DP provisioning MinIO Service Account
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
		RoutingScope:         "zone:" + param.ZoneID.String(),
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
	if err := s.repo.Create(ctx, bucket, param.WorkspaceID, param.ZoneID, credential, outbox); err != nil {
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

func (s *PersonalBucketSvcImpl) GetBucket(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID) (*storageEntity.PersonalBucket, error) {
	bucket, err := s.repo.GetByID(ctx, bucketID, userID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "get_failed")
	}
	return bucket, nil
}

func (s *PersonalBucketSvcImpl) ListBuckets(ctx context.Context, workspaceID uuid.UUID, zoneID uuid.UUID, userID uuid.UUID) ([]*storageEntity.PersonalBucket, error) {
	// [COMMENT]: Liệt kê các bucket theo workspace_id cho luồng cá nhân có check userID và zoneID
	buckets, err := s.repo.ListByWorkspace(ctx, workspaceID, zoneID, userID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "list_failed")
	}
	return buckets, nil
}

// [COMMENT]: ListBucketNames gọi repo để lấy danh sách tên vật lý của các bucket cá nhân
func (s *PersonalBucketSvcImpl) ListBucketNames(ctx context.Context, workspaceID uuid.UUID, zoneID uuid.UUID, userID uuid.UUID) ([]string, error) {
	names, err := s.repo.ListNamesByWorkspace(ctx, workspaceID, zoneID, userID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "list_names_failed")
	}
	return names, nil
}

func (s *PersonalBucketSvcImpl) UpdateBucketQuota(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID, quotaBytes int64) error {
	// [COMMENT]: 1. Lấy thông tin hiện tại của bucket để làm metadata cho outbox payload (name, current quota)
	bucket, err := s.repo.GetByID(ctx, bucketID, userID)
	if err != nil {
		return apperr.Wrap(err, err, "get_failed")
	}

	// [COMMENT]: Trích xuất Trace ID phục vụ distributed tracing
	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

	// [COMMENT]: 2. Chuẩn bị proto event đồng bộ resize gửi xuống dataplane
	syncEvent := &storageproto.BucketResizeSync{
		BucketId:            bucket.ID.String(),
		Name:                bucket.Name,
		CurrentQuotaBytes:   bucket.CapacityQuotaBytes,
		RequestedQuotaBytes: quotaBytes,
	}
	payloadBytes, err := proto.Marshal(syncEvent)
	if err != nil {
		return apperr.Wrap(err, err, "marshal_payload_failed")
	}

	// [COMMENT]: 3. Khởi tạo Outbox Record để cập nhật đồng thời trong transaction
	eventID, err := uuid.NewV7()
	if err != nil {
		return apperr.Wrap(err, err, "failed_to_generate_uuid_v7")
	}

	outbox := &storageEntity.StorageOutboxRecord{
		EventID:              eventID,
		RoutingScope:         "zone:" + bucket.ZoneID.String(),
		JobTopic:             "storage.bucket.resize",
		Payload:              payloadBytes,
		UserID:               userID.String(),
		Status:               storageEntity.StorageOutboxStatusPending,
		JobVersion:           1,
		ResourceID:           bucket.ID.String(),
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 30,
	}

	// [COMMENT]: 4. Thực thi cập nhật DB và ghi outbox nguyên tử
	err = s.repo.UpdateQuota(ctx, bucketID, userID, quotaBytes, outbox)
	if err != nil {
		return apperr.Wrap(err, err, "update_quota_failed")
	}
	return nil
}

func (s *PersonalBucketSvcImpl) DeleteBucket(ctx context.Context, param *storageEntity.DeletePersonalBucket) error {
	// [COMMENT]: 1. Lấy danh sách access keys của toàn bộ credentials liên kết để xóa sạch trên MinIO
	accessKeys, err := s.repo.ListAccessKeys(ctx, param.BucketID, param.UserID)
	if err != nil {
		return apperr.Wrap(err, err, "list_credentials_failed")
	}

	// [COMMENT]: Trích xuất Trace ID phục vụ tracing
	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

	// [COMMENT]: 2. Khởi tạo và mã hóa payload protobuf BucketDeleteSync sử dụng tên từ param input
	syncEvent := &storageproto.BucketDeleteSync{
		Name:       param.BucketName,
		AccessKeys: accessKeys,
	}
	payloadBytes, err := proto.Marshal(syncEvent)
	if err != nil {
		return apperr.Wrap(err, err, "marshal_payload_failed")
	}

	eventID, err := uuid.NewV7()
	if err != nil {
		return apperr.Wrap(err, err, "failed_to_generate_uuid_v7")
	}

	// [COMMENT]: 3. Cấu hình outbox record với zone_id từ param input
	outbox := &storageEntity.StorageOutboxRecord{
		EventID:              eventID,
		RoutingScope:         "zone:" + param.ZoneID.String(),
		JobTopic:             "storage.bucket.delete",
		Payload:              payloadBytes,
		UserID:               param.UserID.String(),
		Status:               storageEntity.StorageOutboxStatusPending,
		JobVersion:           1,
		ResourceID:           param.BucketID.String(),
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 30,
	}

	// [COMMENT]: 4. Thực thi xóa cứng DB và ghi outbox nguyên tử
	err = s.repo.Delete(ctx, param.BucketID, param.UserID, outbox)
	if err != nil {
		return apperr.Wrap(err, err, "delete_failed")
	}
	return nil
}

// [COMMENT]: RequestSts thực thi nghiệp vụ yêu cầu cấp STS token cho bucket cá nhân, tạo Outbox Record.
func (s *PersonalBucketSvcImpl) RequestSts(ctx context.Context, param *storageEntity.RequestBucketSts) (uuid.UUID, error) {
	// 1. SELECT GetByID để lấy bucket_name vật lý và kiểm định nhanh sự tồn tại
	bucket, err := s.repo.GetByID(ctx, param.BucketID, param.UserID)
	if err != nil {
		return uuid.Nil, apperr.Wrap(err, err, "get_failed")
	}

	// Cập nhật tên bucket vật lý vào param
	param.BucketName = bucket.Name

	// Trích xuất Trace ID phục vụ distributed tracing
	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

	// 2. Tạo event ID dạng UUIDv7
	eventID, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, apperr.Wrap(err, err, "failed_to_generate_uuid_v7")
	}

	// 3. Marshal ObjectStsRequest protobuf
	reqProto := &storageproto.ObjectStsRequest{
		BucketName:      bucket.Name,
		DurationSeconds: param.DurationSeconds,
	}
	payloadBytes, err := proto.Marshal(reqProto)
	if err != nil {
		return uuid.Nil, apperr.Wrap(err, err, "marshal_payload_failed")
	}

	// 4. Tạo Outbox Record với topic "storage.object.sts"
	outbox := &storageEntity.StorageOutboxRecord{
		EventID:              eventID,
		RoutingScope:         "zone:" + param.ZoneID.String(),
		JobTopic:             "storage.object.sts",
		Payload:              payloadBytes,
		UserID:               param.UserID.String(),
		Status:               storageEntity.StorageOutboxStatusPending,
		JobVersion:           1,
		ResourceID:           param.BucketID.String(),
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 30,
	}

	// 5. Lưu vào CSDL
	err = s.repo.CreateSts(ctx, param, outbox)
	if err != nil {
		return uuid.Nil, apperr.Wrap(err, err, "create_sts_failed")
	}

	return eventID, nil
}
