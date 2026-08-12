package service

import (
	"context"
	"fmt"
	"time"

	billingRepoInterface "cost-manager/api/internal/domain/repo"
	walletv1 "cost-manager/api/internal/genproto/billing/wallet/v1"
	"cost-manager/api/pkg/logger"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const walletAdmissionStream = "billing.wallet.admission.changed.v1"
const maxWalletAdmissionTargets = 10_000

// WalletAdmissionOutboxRelay delivers committed financial admission transitions
// to module-owned projection consumers. Redis is transport only; PostgreSQL
// outbox rows remain replay/reconciliation authority.
type WalletAdmissionOutboxRelay struct {
	repo  billingRepoInterface.WalletAdmissionOutboxRepository
	redis *redis.Client
}

func NewWalletAdmissionOutboxRelay(repo billingRepoInterface.WalletAdmissionOutboxRepository, sharedRedis *redis.Client) *WalletAdmissionOutboxRelay {
	return &WalletAdmissionOutboxRelay{repo: repo, redis: sharedRedis}
}

func (r *WalletAdmissionOutboxRelay) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		claimToken := uuid.New()
		for {
			batch, err := r.repo.ClaimUnpublishedWalletAdmissionBatch(ctx, 100, claimToken)
			if err != nil {
				if ctx.Err() == nil {
					logger.SysError("billing.wallet.admission.relay", err.Error())
				}
				break
			}
			if len(batch) == 0 {
				break
			}
			for _, row := range batch {
				targets, targetErr := r.repo.ListActiveStorageAdmissionTargets(ctx, row.OwnerID, row.OwnerType)
				if targetErr != nil {
					_ = r.repo.RecordWalletAdmissionError(ctx, row.EventID, row.ClaimToken, targetErr.Error())
					break
				}
				if len(targets) > maxWalletAdmissionTargets {
					err := fmt.Errorf("wallet admission target fanout exceeds %d", maxWalletAdmissionTargets)
					_ = r.repo.RecordWalletAdmissionError(ctx, row.EventID, row.ClaimToken, err.Error())
					break
				}
				restrictionReason := ""
				if row.RestrictionReason != nil {
					restrictionReason = *row.RestrictionReason
				}
				validUntil := ""
				if row.ValidUntil != nil {
					validUntil = row.ValidUntil.UTC().Format(time.RFC3339Nano)
				}
				wireTargets := make([]*walletv1.StorageAdmissionTargetV1, 0, len(targets))
				for _, target := range targets {
					wireTargets = append(wireTargets, &walletv1.StorageAdmissionTargetV1{
						ResourceId: target.ResourceID.String(), ResourceName: target.ResourceName, ZoneId: target.ZoneID.String(),
					})
				}
				payload, err := proto.Marshal(&walletv1.WalletAdmissionChangedV1{
					EventId: row.EventID.String(), WalletId: row.WalletID.String(), OwnerId: row.OwnerID.String(),
					OwnerType: string(row.OwnerType), WalletVersion: row.WalletVersion, AdmissionMode: row.AdmissionMode,
					RestrictionReason: restrictionReason, EffectiveAt: row.EffectiveAt.UTC().Format(time.RFC3339Nano),
					ValidUntil:     validUntil,
					StorageTargets: wireTargets,
				})
				if err != nil {
					_ = r.repo.RecordWalletAdmissionError(ctx, row.EventID, row.ClaimToken, err.Error())
					break
				}
				if err := r.redis.XAdd(ctx, &redis.XAddArgs{Stream: walletAdmissionStream, Values: map[string]any{"event_id": row.EventID.String(), "payload": payload}}).Err(); err != nil {
					_ = r.repo.RecordWalletAdmissionError(ctx, row.EventID, row.ClaimToken, err.Error())
					break
				}
				if err := r.redis.Do(ctx, "WAITAOF", 1, 1, 500).Err(); err != nil {
					_ = r.repo.RecordWalletAdmissionError(ctx, row.EventID, row.ClaimToken, err.Error())
					break
				}
				if err := r.repo.MarkWalletAdmissionPublished(ctx, row.EventID, row.ClaimToken); err != nil {
					logger.SysError("billing.wallet.admission.mark", fmt.Sprintf("%s: %v", row.EventID, err))
					break
				}
			}
		}
	}
}
