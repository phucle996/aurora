package storageSvcImpl

import (
	"context"
	"fmt"

	"controlplane/internal/config"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageSvcInterface "controlplane/internal/storage/domain/service"
	storageproto "controlplane/internal/storage/transport/rpc/proto"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: PersonalObjectServiceImpl thực thi logic cho service thao tác objects.
type PersonalObjectServiceImpl struct {
	objectRepo storageRepoInterface.PersonalObjectRepo
	bucketRepo storageRepoInterface.PersonalBucketRepo
	cfg        *config.Config
}

// [COMMENT]: NewPersonalObjectService khởi tạo service quản lý đối tượng.
func NewPersonalObjectService(
	objectRepo storageRepoInterface.PersonalObjectRepo,
	bucketRepo storageRepoInterface.PersonalBucketRepo,
	cfg *config.Config,
) storageSvcInterface.PersonalObjectService {
	return &PersonalObjectServiceImpl{
		objectRepo: objectRepo,
		bucketRepo: bucketRepo,
		cfg:        cfg,
	}
}

// [COMMENT]: RegisterObjectPresign sinh event_id, đóng gói payload Protobuf và chèn outbox record.
func (s *PersonalObjectServiceImpl) RegisterObjectPresign(ctx context.Context, param *storageEntity.RequestObjectPresignParam) (uuid.UUID, error) {
	// [COMMENT]: 1. Sinh event_id dưới dạng UUIDv7
	eventID, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("storage service: failed to generate event_id: %w", err)
	}

	var payloadBytes []byte

	// [COMMENT]: 2. Đóng gói payload Protobuf ObjectPresignRequest

	msg := &storageproto.ObjectPresignRequest{
		BucketName:  param.BucketName,
		Key:         param.Key,
		Action:      string(param.Action),
		ContentType: param.ContentType,
	}

	payloadBytes, err = proto.Marshal(msg)
	if err != nil {
		return uuid.Nil, fmt.Errorf("storage service: marshal proto failed: %w", err)
	}

	// [COMMENT]: 3. Khởi tạo Outbox Record thực thể
	outbox := &storageEntity.StorageOutboxRecord{
		EventID:              eventID,
		RoutingScope:         fmt.Sprintf("zone:%s", param.ZoneID.String()),
		JobTopic:             "storage.object.presign",
		Payload:              payloadBytes,
		UserID:               param.UserID.String(),
		Status:               storageEntity.StorageOutboxStatusPending,
		JobVersion:           1,
		PayloadSchemaVersion: 1,
		ResourceID:           param.BucketID.String(),
	}

	// [COMMENT]: 4. Gọi repo chèn bản ghi vào database (Repo thực thi CTE IDOR check)
	if err := s.objectRepo.CreateObjectPresign(ctx, param, outbox); err != nil {
		return uuid.Nil, err
	}

	return eventID, nil
}
