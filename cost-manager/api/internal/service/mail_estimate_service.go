package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"cost-manager/api/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

const (
	mailPricingReadinessStream = "billing.pricing.mail.rateability.changed.v1"
	mailPricingReadinessTTL    = 45 * time.Second
)

type mailPricingReadinessPayload struct {
	SchemaVersion int      `json:"schema_version"`
	Ready         bool     `json:"ready"`
	Missing       []string `json:"missing"`
	ObservedAt    string   `json:"observed_at"`
	ValidUntil    string   `json:"valid_until"`
	Fingerprint   string   `json:"fingerprint"`
}

type mailEstimateService struct {
	adjustmentRepo billingRepoInterface.MailZoneAdjustmentRepository
	pricingCache   *pricingCache
}

func NewMailEstimateService(snapshotRepo billingRepoInterface.PricingSnapshotRepository, adjustmentRepo billingRepoInterface.MailZoneAdjustmentRepository, redisClient *goredis.Client) *mailEstimateService {
	return &mailEstimateService{adjustmentRepo: adjustmentRepo, pricingCache: &pricingCache{repo: snapshotRepo, redisClient: redisClient, l1: make(map[string]pricingCacheItem)}}
}

func (s *mailEstimateService) EstimateMail(ctx context.Context, recipientQuantity int64, zoneID uuid.UUID) (*entity.MailEstimate, error) {
	if recipientQuantity < 1 || recipientQuantity > 1_000_000_000 || zoneID == uuid.Nil {
		return nil, billingTaxonomy.ErrInvalidArgument
	}
	now := time.Now().UTC()
	snapshot, err := s.pricingCache.get(ctx, entity.ChargeKindMailAcceptedRecipient, now)
	if err != nil {
		return nil, err
	}
	if snapshot.ModuleCode != "mail" || snapshot.RawInputUnit != "RECIPIENT" {
		return nil, fmt.Errorf("Mail pricing snapshot contract mismatch")
	}
	adjustment, err := s.adjustmentRepo.GetActiveMailZonePriceAdjustment(ctx, zoneID, now)
	if err != nil {
		return nil, err
	}
	numerator, denominator := int64(1), int64(1)
	if adjustment != nil {
		if mailZoneAdjustmentChecksum(adjustment.ZoneID, adjustment.VersionNumber, adjustment.EffectiveFrom, adjustment.MultiplierNumerator, adjustment.MultiplierDenominator) != adjustment.Checksum {
			return nil, fmt.Errorf("Mail Zone price adjustment checksum mismatch")
		}
		numerator, denominator = adjustment.MultiplierNumerator, adjustment.MultiplierDenominator
	}
	estimate, err := mailAcceptedRecipientCharge(uint64(recipientQuantity), snapshot.Brackets, numerator, denominator)
	if err != nil {
		return nil, err
	}
	var adjustmentID *uuid.UUID
	var adjustmentVersion *int
	var adjustmentChecksum *string
	if adjustment != nil {
		adjustmentID = &adjustment.ID
		adjustmentVersion = &adjustment.VersionNumber
		adjustmentChecksum = &adjustment.Checksum
	}
	return &entity.MailEstimate{
		RecipientQuantity: recipientQuantity, EstimateMicroUnits: estimate, Currency: snapshot.Currency,
		PricingScheduleCode: snapshot.Code, PricingScheduleID: snapshot.PricingScheduleID,
		PricingScheduleVersionID: snapshot.VersionID, PricingVersion: snapshot.VersionNumber,
		PricingChecksum: snapshot.Checksum, PricingEffectiveFrom: snapshot.EffectiveFrom,
		RateAdjustmentID: adjustmentID, RateAdjustmentVersion: adjustmentVersion,
		RateAdjustmentChecksum: adjustmentChecksum, RateAdjustmentNumerator: numerator,
		RateAdjustmentDenominator: denominator, EstimatedAt: now,
	}, nil
}

func (s *mailEstimateService) RunPricingCacheInvalidation(ctx context.Context) {
	s.pricingCache.runInvalidation(ctx)
}

func (s *mailEstimateService) RunPricingReadinessProjection(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		now := time.Now().UTC()
		payload := s.mailPricingReadiness(ctx, now)
		if encoded, err := json.Marshal(payload); err != nil {
			logger.SysError("billing.mail.pricing_readiness.encode", err.Error())
		} else if err := s.pricingCache.redisClient.XAdd(ctx, &goredis.XAddArgs{
			Stream: mailPricingReadinessStream,
			Values: map[string]any{"payload": encoded},
		}).Err(); err != nil && ctx.Err() == nil {
			logger.SysError("billing.mail.pricing_readiness.publish", err.Error())
		} else if err := s.pricingCache.redisClient.Do(ctx, "WAITAOF", 1, 1, 500).Err(); err != nil && ctx.Err() == nil {
			logger.SysError("billing.mail.pricing_readiness.durability", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *mailEstimateService) mailPricingReadiness(ctx context.Context, now time.Time) mailPricingReadinessPayload {
	missing := make([]string, 0, 1)
	fingerprint := sha256.New()
	snapshot, err := s.pricingCache.get(ctx, entity.ChargeKindMailAcceptedRecipient, now)
	if err != nil || snapshot.ModuleCode != "mail" || snapshot.RawInputUnit != "RECIPIENT" {
		missing = append(missing, string(entity.ChargeKindMailAcceptedRecipient))
	} else {
		_, _ = fingerprint.Write([]byte(entity.ChargeKindMailAcceptedRecipient))
		_, _ = fingerprint.Write(snapshot.VersionID[:])
		_, _ = fingerprint.Write([]byte(snapshot.Checksum))
	}
	return mailPricingReadinessPayload{SchemaVersion: 1, Ready: len(missing) == 0, Missing: missing, ObservedAt: now.Format(time.RFC3339Nano), ValidUntil: now.Add(mailPricingReadinessTTL).Format(time.RFC3339Nano), Fingerprint: fmt.Sprintf("%x", fingerprint.Sum(nil))}
}

func mailAcceptedRecipientCharge(quantity uint64, brackets []entity.PricingSnapshotBracket, adjustmentNumerator, adjustmentDenominator int64) (int64, error) {
	if len(brackets) == 0 || adjustmentNumerator < 0 || adjustmentDenominator <= 0 {
		return 0, billingTaxonomy.ErrInvalidPricingBrackets
	}
	total := new(big.Rat)
	for _, bracket := range brackets {
		if bracket.RangeStartQuantity < 0 || bracket.PriceNumeratorMicroUnits < 0 || bracket.PriceDenominatorQuantity <= 0 {
			return 0, billingTaxonomy.ErrInvalidPricingBrackets
		}
		start := uint64(bracket.RangeStartQuantity)
		if quantity <= start {
			break
		}
		upper := quantity
		if bracket.RangeEndQuantity != nil {
			if *bracket.RangeEndQuantity <= bracket.RangeStartQuantity {
				return 0, billingTaxonomy.ErrInvalidPricingBrackets
			}
			if uint64(*bracket.RangeEndQuantity) < upper {
				upper = uint64(*bracket.RangeEndQuantity)
			}
		}
		if upper > start {
			units := new(big.Int).SetUint64(upper - start)
			price := new(big.Int).Mul(units, big.NewInt(bracket.PriceNumeratorMicroUnits))
			total.Add(total, new(big.Rat).SetFrac(price, big.NewInt(bracket.PriceDenominatorQuantity)))
		}
	}
	total.Mul(total, new(big.Rat).SetFrac(big.NewInt(adjustmentNumerator), big.NewInt(adjustmentDenominator)))
	ceil := new(big.Int).Quo(total.Num(), total.Denom())
	if new(big.Int).Mod(total.Num(), total.Denom()).Sign() != 0 {
		ceil.Add(ceil, big.NewInt(1))
	}
	if !ceil.IsInt64() {
		return 0, fmt.Errorf("Mail pricing charge exceeds BIGINT")
	}
	return ceil.Int64(), nil
}
