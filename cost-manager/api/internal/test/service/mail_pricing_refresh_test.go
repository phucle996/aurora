package service_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	"cost-manager/api/internal/service"
)

type mailPricingRefreshRepo struct {
	billingRepoInterface.MailPricingRepository
	observed chan time.Time
}

func (r mailPricingRefreshRepo) GetActiveMailPricingSnapshot(context.Context, entity.ChargeKindCode, time.Time) (*entity.MailPricingSnapshot, error) {
	r.observed <- time.Now()
	return nil, errors.New("pricing temporarily unavailable")
}

func TestMailPricingRefreshRetriesWithoutWaitingForL2TTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		observed := make(chan time.Time, 2)
		pricing := service.NewMailPricingService(mailPricingRefreshRepo{observed: observed}, nil)
		done := make(chan struct{})
		go func() {
			defer close(done)
			pricing.RunPricingSnapshotRefresh(ctx)
		}()
		first, second := <-observed, <-observed
		if elapsed := second.Sub(first); elapsed != 15*time.Second {
			t.Errorf("refresh retry took %s, want 15s independently of cache TTL", elapsed)
		}
		cancel()
		<-done
	})
}
