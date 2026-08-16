package service

import (
	"context"
	"fmt"
	"time"

	billingRepoInterface "cost-manager/api/internal/domain/repo"
	admissionv1 "cost-manager/api/internal/genproto/billing/admission/v1"
	"cost-manager/api/pkg/logger"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

var walletAdmissionStreams = [...]string{
	"billing.commercial.admission.storage.changed.v1",
	"billing.commercial.admission.hypervisor.changed.v1",
	"billing.commercial.admission.mail.changed.v1",
}

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
					logger.SysError("billing.commercial.admission.relay", err.Error())
				}
				break
			}
			if len(batch) == 0 {
				break
			}
			for _, row := range batch {
				restrictionReason := ""
				if row.RestrictionReason != nil {
					restrictionReason = *row.RestrictionReason
				}
				validUntil := ""
				if row.ValidUntil != nil {
					validUntil = row.ValidUntil.UTC().Format(time.RFC3339Nano)
				}
				payload, err := proto.Marshal(&admissionv1.CommercialAdmissionChangedV1{
					EventId: row.EventID.String(), OwnerId: row.OwnerID.String(),
					OwnerType: string(row.OwnerType), PolicyVersion: row.WalletVersion, Decision: row.AdmissionMode,
					RestrictionReason: restrictionReason, EffectiveAt: row.EffectiveAt.UTC().Format(time.RFC3339Nano),
					ValidUntil: validUntil,
				})
				if err != nil {
					_ = r.repo.RecordWalletAdmissionError(ctx, row.EventID, row.ClaimToken, err.Error())
					break
				}
				published := true
				connection := r.redis.Conn()
				for _, stream := range walletAdmissionStreams {
					if err := connection.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]any{"event_id": row.EventID.String(), "payload": payload}}).Err(); err != nil {
						_ = r.repo.RecordWalletAdmissionError(ctx, row.EventID, row.ClaimToken, err.Error())
						published = false
						break
					}
				}
				if published {
					aofAcks, err := connection.Do(ctx, "WAITAOF", 1, 1, 500).Int64Slice()
					if err != nil {
						_ = r.repo.RecordWalletAdmissionError(ctx, row.EventID, row.ClaimToken, err.Error())
						published = false
					} else if len(aofAcks) != 2 || aofAcks[0] < 1 || aofAcks[1] < 1 {
						_ = r.repo.RecordWalletAdmissionError(ctx, row.EventID, row.ClaimToken, fmt.Sprintf("Redis admission durability fence not met: %v", aofAcks))
						published = false
					}
				}
				if err := connection.Close(); err != nil && published {
					_ = r.repo.RecordWalletAdmissionError(ctx, row.EventID, row.ClaimToken, err.Error())
					published = false
				}
				if !published {
					break
				}
				if err := r.repo.MarkWalletAdmissionPublished(ctx, row.EventID, row.ClaimToken); err != nil {
					logger.SysError("billing.commercial.admission.mark", fmt.Sprintf("%s: %v", row.EventID, err))
					break
				}
			}
		}
	}
}
