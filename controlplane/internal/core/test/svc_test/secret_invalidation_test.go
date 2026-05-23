package svc_test

import (
	"context"
	"testing"
	"time"

	coreCache "controlplane/internal/core/cache"
	coreEntity "controlplane/internal/core/domain/entity"

	goredis "github.com/redis/go-redis/v9"
)

type fakeInvalidationProvider struct {
	invalidated []string
}

func (f *fakeInvalidationProvider) GetPrimary(ctx context.Context, familyCode string) (*coreEntity.RuntimeSecret, error) {
	return nil, nil
}
func (f *fakeInvalidationProvider) GetCandidates(ctx context.Context, familyCode string) ([]coreEntity.RuntimeSecret, error) {
	return nil, nil
}
func (f *fakeInvalidationProvider) Warm(ctx context.Context, familyCode string) error { return nil }
func (f *fakeInvalidationProvider) Invalidate(familyCode string) {
	f.invalidated = append(f.invalidated, familyCode)
}

func TestRedisSecretInvalidationBusWithoutRedisStillInvalidatesLocal(t *testing.T) {
	provider := &fakeInvalidationProvider{}
	bus := coreCache.NewRedisSecretInvalidationBus(nil, provider, "node-1")
	if err := bus.InvalidateFamily(context.Background(), "access_token", "rotate"); err != nil {
		t.Fatalf("InvalidateFamily() error = %v", err)
	}
	if len(provider.invalidated) != 1 || provider.invalidated[0] != "access_token" {
		t.Fatalf("local invalidations = %#v, want access_token", provider.invalidated)
	}
}

func TestRedisSecretInvalidationBusListenContextCancel(t *testing.T) {
	provider := &fakeInvalidationProvider{}
	bus := coreCache.NewRedisSecretInvalidationBus(goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:0"}), provider, "node-1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := bus.Listen(ctx); err == nil {
		t.Fatal("Listen() error = nil, want context error")
	}
	_ = time.Second
}
