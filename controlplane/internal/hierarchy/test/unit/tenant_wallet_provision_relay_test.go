package unit

import (
	"testing"
	"time"

	hierarchyRepoInterface "controlplane/internal/hierarchy/domain/repo"
	hierarchySvcImpl "controlplane/internal/hierarchy/service"

	goredis "github.com/redis/go-redis/v9"
)

type tenantWalletProvisionRelayRepoStub struct {
	hierarchyRepoInterface.TenantRepository
}

func TestTenantWalletProvisionRelayRejectsDurabilityDeadlineAtLease(t *testing.T) {
	redisClient := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	defer redisClient.Close()

	relay, err := hierarchySvcImpl.NewTenantWalletProvisionRelay(
		&tenantWalletProvisionRelayRepoStub{},
		redisClient,
		1,
		30*time.Second,
	)
	if err == nil || relay != nil {
		t.Fatalf("expected durable wait at lease duration to be rejected, relay=%v err=%v", relay, err)
	}
}
