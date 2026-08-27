package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingSvcInterface "cost-manager/api/internal/domain/service"
	hypervisorresourceplanv1 "cost-manager/api/internal/genproto/billing/hypervisor/v1"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"cost-manager/api/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const (
	hypervisorResourcePlanStream = "billing.hypervisor.resource-plan.changed.v1"
)

type hypervisorResourcePlanService struct {
	repo        billingRepoInterface.HypervisorResourcePlanRepository
	redisClient goredis.UniversalClient
	relayPolicy entity.HypervisorResourcePlanRelayPolicy
	wake        chan struct{}
}

func NewHypervisorResourcePlanService(repo billingRepoInterface.HypervisorResourcePlanRepository, redisClient goredis.UniversalClient, policy entity.HypervisorResourcePlanRelayPolicy) billingSvcInterface.HypervisorResourcePlanService {
	return &hypervisorResourcePlanService{repo: repo, redisClient: redisClient, relayPolicy: policy, wake: make(chan struct{}, 1)}
}

func (s *hypervisorResourcePlanService) CreateHypervisorResourcePlan(ctx context.Context, command entity.CreateHypervisorResourcePlanCommand) (*entity.HypervisorResourcePlanRevision, error) {
	if command.CPUCores < 1 || command.CPUCores > 1024 || command.MemoryMIB < 1 || command.MemoryMIB > 4_194_304 || command.BootDiskGIB < 1 || command.BootDiskGIB > 65_536 {
		return nil, billingTaxonomy.ErrInvalidArgument
	}
	planID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("Hypervisor resource plan service: generate plan id: %w", err)
	}
	revisionID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("Hypervisor resource plan service: generate revision id: %w", err)
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("Hypervisor resource plan service: generate event id: %w", err)
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("hypervisor-resource-plan/v1\n%s\n%d\n%d\n%d\n%s", command.Code, command.CPUCores, command.MemoryMIB, command.BootDiskGIB, command.EffectiveFrom.Format(time.RFC3339Nano))))
	command.PlanID = planID
	command.RevisionID = revisionID
	command.EventID = eventID
	command.ContentSHA256 = hex.EncodeToString(hash[:])
	payload, err := proto.Marshal(&hypervisorresourceplanv1.EffectiveHypervisorResourcePlanV1{
		SchemaVersion: 1, EventId: eventID.String(), PlanId: planID[:], RevisionId: revisionID[:], RevisionNumber: 1,
		Code: command.Code, DisplayName: command.DisplayName, Description: command.Description, BillingModel: "LIMIT_HOURLY",
		CpuCores: uint64(command.CPUCores), MemoryMib: uint64(command.MemoryMIB), BootDiskGib: uint64(command.BootDiskGIB), ContentSha256: hash[:],
		EffectiveFrom: command.EffectiveFrom.Format(time.RFC3339Nano), AllowedOperations: []string{"CREATE"}, State: "ACTIVE", OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil, fmt.Errorf("Hypervisor resource plan service: marshal create event: %w", err)
	}
	command.OutboxPayload = payload
	return s.repo.CreateHypervisorResourcePlan(ctx, command)
}

func (s *hypervisorResourcePlanService) PublishHypervisorResourcePlanRevision(ctx context.Context, command entity.PublishHypervisorResourcePlanRevisionCommand) (*entity.HypervisorResourcePlanRevision, error) {
	if command.CPUCores < 1 || command.CPUCores > 1024 || command.MemoryMIB < 1 || command.MemoryMIB > 4_194_304 || command.BootDiskGIB < 1 || command.BootDiskGIB > 65_536 {
		return nil, billingTaxonomy.ErrInvalidArgument
	}
	identity, err := s.repo.GetHypervisorResourcePlanIdentity(ctx, command.PlanID)
	if err != nil {
		return nil, err
	}
	revisionID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("Hypervisor resource plan service: generate revision id: %w", err)
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("Hypervisor resource plan service: generate event id: %w", err)
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("hypervisor-resource-plan/v1\n%s\n%d\n%d\n%d\n%s", identity.Code, command.CPUCores, command.MemoryMIB, command.BootDiskGIB, command.EffectiveFrom.Format(time.RFC3339Nano))))
	command.RevisionID = revisionID
	command.EventID = eventID
	command.ContentSHA256 = hex.EncodeToString(hash[:])
	payload, err := proto.Marshal(&hypervisorresourceplanv1.EffectiveHypervisorResourcePlanV1{
		SchemaVersion: 1, EventId: eventID.String(), PlanId: command.PlanID[:], RevisionId: revisionID[:], RevisionNumber: uint64(command.ExpectedLatestRevision + 1),
		Code: identity.Code, DisplayName: identity.DisplayName, Description: identity.Description, BillingModel: "LIMIT_HOURLY",
		CpuCores: uint64(command.CPUCores), MemoryMib: uint64(command.MemoryMIB), BootDiskGib: uint64(command.BootDiskGIB), ContentSha256: hash[:],
		EffectiveFrom: command.EffectiveFrom.Format(time.RFC3339Nano), AllowedOperations: []string{"CREATE"}, State: "ACTIVE", OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil, fmt.Errorf("Hypervisor resource plan service: marshal revision event: %w", err)
	}
	command.OutboxPayload = payload
	return s.repo.PublishHypervisorResourcePlanRevision(ctx, command)
}

func (s *hypervisorResourcePlanService) ListEffectiveHypervisorResourcePlans(ctx context.Context, query entity.HypervisorResourcePlanListQuery) ([]entity.HypervisorResourcePlanRevision, bool, error) {
	return s.repo.ListEffectiveHypervisorResourcePlans(ctx, query)
}

func (s *hypervisorResourcePlanService) NotifyHypervisorResourcePlanOutbox() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *hypervisorResourcePlanService) RunHypervisorResourcePlanOutboxRelay(ctx context.Context) {
	poll := time.NewTimer(0)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-poll.C:
			poll.Reset(30*time.Second + time.Duration(rand.IntN(10))*time.Second)
		}
		for ctx.Err() == nil {
			claimToken := uuid.New()
			rows, err := s.repo.ClaimHypervisorResourcePlanOutbox(ctx, claimToken, time.Now().UTC().Add(30*time.Second), 1)
			if err != nil {
				logger.SysError("billing.hypervisor.resource_plan.outbox.claim", err.Error())
				break
			}
			if len(rows) == 0 {
				break
			}
			for _, row := range rows {
				publishCtx, cancel := context.WithTimeout(ctx, s.relayPolicy.DurableWait+2*time.Second)
				var primary *goredis.Client
				var publishErr error
				switch client := s.redisClient.(type) {
				case *goredis.Client:
					primary = client
				case *goredis.ClusterClient:
					primary, publishErr = client.MasterForKey(publishCtx, hypervisorResourcePlanStream)
				default:
					publishErr = fmt.Errorf("unsupported resource plan Redis client")
				}
				if publishErr == nil {
					// A non-transactional pipeline puts XADD and WAITAOF on the same
					// physical connection. A retry replays both, never WAIT alone.
					conn := primary.WithTimeout(s.relayPolicy.DurableWait + time.Second).Conn()
					var persisted *goredis.Cmd
					_, publishErr = conn.Pipelined(publishCtx, func(pipe goredis.Pipeliner) error {
						pipe.XAdd(publishCtx, &goredis.XAddArgs{Stream: hypervisorResourcePlanStream, Values: map[string]any{"event_id": row.EventID.String(), "payload": row.Payload}})
						persisted = pipe.Do(publishCtx, "WAITAOF", 1, s.relayPolicy.ReplicaAcks, s.relayPolicy.DurableWait.Milliseconds())
						return nil
					})
					if publishErr == nil {
						acks, err := persisted.Int64Slice()
						if err != nil {
							publishErr = err
						} else if len(acks) != 2 || acks[0] < 1 || acks[1] < int64(s.relayPolicy.ReplicaAcks) {
							publishErr = fmt.Errorf("resource plan durability threshold was not reached")
						}
					}
					_ = conn.Close()
				}
				if publishErr != nil {
					if client, ok := s.redisClient.(*goredis.ClusterClient); ok {
						client.ReloadState(ctx)
					}
				}
				cancel()
				if publishErr != nil {
					backoff := 1 << min(row.RetryCount, 6)
					availableAt := time.Now().UTC().Add(time.Duration(backoff+rand.IntN(backoff+1)) * time.Second)
					if err := s.repo.RetryHypervisorResourcePlanOutbox(ctx, row.ID, row.ClaimToken, publishErr.Error(), availableAt); err != nil && ctx.Err() == nil {
						logger.SysError("billing.hypervisor.resource_plan.outbox.retry", err.Error())
					}
					continue
				}
				if err := s.repo.MarkHypervisorResourcePlanOutboxPublished(ctx, row.ID, row.ClaimToken); err != nil && ctx.Err() == nil {
					logger.SysError("billing.hypervisor.resource_plan.outbox.publish", err.Error())
				}
			}
		}
	}
}

func (s *hypervisorResourcePlanService) ListPlans(ctx context.Context, query entity.HypervisorResourcePlanAdminQuery) ([]entity.HypervisorResourcePlanAdminItem, bool, error) {
	return s.repo.ListPlans(ctx, query)
}
func (s *hypervisorResourcePlanService) ListRevisions(ctx context.Context, query entity.HypervisorResourcePlanHistoryQuery) ([]entity.HypervisorResourcePlanHistoryItem, bool, error) {
	return s.repo.ListRevisions(ctx, query)
}
