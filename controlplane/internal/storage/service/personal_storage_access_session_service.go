package storageSvcImpl

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"controlplane/internal/observability"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageSvcInterface "controlplane/internal/storage/domain/service"
	storageproto "controlplane/internal/storage/transport/proto"
	"controlplane/pkg/apperr"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

type personalStorageAccessSessionService struct {
	repo    storageRepoInterface.PersonalStorageAccessSessionRepository
	metrics observability.WorkflowRecorder
}

func NewPersonalStorageAccessSessionService(repo storageRepoInterface.PersonalStorageAccessSessionRepository, metrics observability.WorkflowRecorder) storageSvcInterface.PersonalStorageAccessSessionService {
	return &personalStorageAccessSessionService{repo: repo, metrics: metrics}
}

func (s *personalStorageAccessSessionService) CreatePersonalStorageAccessSession(ctx context.Context, command *storageEntity.StorageAccessSession) error {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	if command == nil || command.AccessSessionID == uuid.Nil || command.ResourceID == uuid.Nil || command.ActorID == uuid.Nil || command.WorkspaceID == uuid.Nil || command.ZoneID == uuid.Nil {
		result, reason = observability.ResultRejected, observability.ReasonInvalidArgument
		return apperr.Wrap(fmt.Errorf("access session identity is incomplete"), nil, "invalid_access_session")
	}
	if command.ExpiresAtUnixSeconds <= uint64(time.Now().Unix()) {
		result, reason = observability.ResultRejected, observability.ReasonInvalidArgument
		return apperr.Wrap(fmt.Errorf("access session expiry is in the past"), nil, "invalid_access_session_expiry")
	}
	bucketName, err := s.repo.GetPersonalStorageAccessSessionTarget(ctx, command.ResourceID, command.WorkspaceID, command.ActorID, command.ZoneID)
	if err != nil {
		return apperr.Wrap(err, err, "access_session_target_not_found")
	}
	command.BucketName = bucketName
	command.BindingHash = fmt.Sprintf("%x", sha256.Sum256([]byte(command.AccessSessionID.String()+":"+command.ActorID.String()+":"+uuid.New().String())))

	preparePayload, err := proto.Marshal(&storageproto.StorageAccessPrepareRequest{
		AccessSessionId:      command.AccessSessionID.String(),
		BindingHash:          command.BindingHash,
		ActorId:              command.ActorID.String(),
		ResourceId:           command.ResourceID.String(),
		BucketName:           command.BucketName,
		WorkspaceId:          command.WorkspaceID.String(),
		ZoneId:               command.ZoneID.String(),
		Actions:              append([]string(nil), command.Actions...),
		KeyPrefix:            command.KeyPrefix,
		ExpiresAtUnixSeconds: command.ExpiresAtUnixSeconds,
		PolicyRevision:       command.PolicyRevision,
	})
	if err != nil {
		return apperr.Wrap(err, err, "marshal_access_session_command_failed")
	}
	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		id := spanCtx.TraceID()
		traceID = id[:]
	}
	outbox := &storageEntity.StorageOutboxRecord{
		EventID:              command.AccessSessionID,
		ZoneID:               command.ZoneID,
		JobTopic:             "storage.access.prepare",
		Payload:              preparePayload,
		OwnerID:              command.ActorID,
		OwnerType:            storageEntity.StorageOwnerTypePersonal,
		ActorUserID:          &command.ActorID,
		Status:               storageEntity.StorageOutboxStatusPending,
		JobVersion:           1,
		ResourceID:           command.ResourceID.String(),
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 30,
	}
	if err := s.repo.CreatePersonalStorageAccessSession(ctx, command, outbox); err != nil {
		return apperr.Wrap(err, err, "create_access_session_command_failed")
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return nil
}

func (s *personalStorageAccessSessionService) GetPersonalStorageAccessSessionStatus(ctx context.Context, accessSessionID, resourceID, workspaceID, actorID, zoneID uuid.UUID) (*storageEntity.StorageAccessSessionStatus, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()
	if accessSessionID == uuid.Nil || resourceID == uuid.Nil || workspaceID == uuid.Nil || actorID == uuid.Nil || zoneID == uuid.Nil {
		result, reason = observability.ResultRejected, observability.ReasonInvalidArgument
		return nil, apperr.Wrap(fmt.Errorf("access session status identity is incomplete"), nil, "invalid_access_session_status")
	}
	status, err := s.repo.GetPersonalStorageAccessSessionStatus(ctx, accessSessionID, resourceID, workspaceID, actorID, zoneID)
	if err != nil {
		return nil, apperr.Wrap(err, err, "access_session_status_not_found")
	}
	switch status.State {
	case string(storageEntity.StorageOutboxStatusPending), string(storageEntity.StorageOutboxStatusProcessing):
		status.State = "PENDING"
	case string(storageEntity.StorageOutboxStatusSucceeded):
		status.State = "ACTIVE"
	case string(storageEntity.StorageOutboxStatusFailed):
		status.State = "FAILED"
	default:
		return nil, apperr.Wrap(fmt.Errorf("unsupported access session status %q", status.State), nil, "invalid_access_session_status")
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return status, nil
}
