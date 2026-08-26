package unit

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamService "controlplane/internal/iam/service"
)

type lifecycleFactOutboxRepositoryStub struct {
	mu       sync.Mutex
	events   []iamEntity.LifecycleFactOutboxEvent
	claimed  chan struct{}
	dead     chan struct{}
	lastDead string
}

func (r *lifecycleFactOutboxRepositoryStub) Claim(context.Context, int) ([]iamEntity.LifecycleFactOutboxEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.claimed != nil {
		select {
		case r.claimed <- struct{}{}:
		default:
		}
	}
	events := r.events
	r.events = nil
	return events, nil
}

func (r *lifecycleFactOutboxRepositoryStub) MarkPublished(context.Context, int64) error {
	return nil
}

func (r *lifecycleFactOutboxRepositoryStub) MarkFailed(context.Context, int64, string) error {
	return nil
}

func (r *lifecycleFactOutboxRepositoryStub) MarkDead(_ context.Context, _ int64, message string) error {
	r.mu.Lock()
	r.lastDead = message
	r.mu.Unlock()
	if r.dead != nil {
		select {
		case r.dead <- struct{}{}:
		default:
		}
	}
	return nil
}

func newBillingRelayTestRedis(t *testing.T) *goredis.Client {
	t.Helper()
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	t.Cleanup(server.Close)
	client := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestLifecycleFactRelayRejectsInvalidDependencies(t *testing.T) {
	redisClient := newBillingRelayTestRedis(t)
	repo := &lifecycleFactOutboxRepositoryStub{}

	if _, err := iamService.NewLifecycleFactRelay(nil, redisClient, 0, time.Second); err == nil {
		t.Fatal("nil repository must fail fast")
	}
	if _, err := iamService.NewLifecycleFactRelay(repo, nil, 0, time.Second); err == nil {
		t.Fatal("nil Redis client must fail fast")
	}
	if _, err := iamService.NewLifecycleFactRelay(repo, redisClient, -1, time.Second); err == nil {
		t.Fatal("negative replica ACK count must fail fast")
	}
	if _, err := iamService.NewLifecycleFactRelay(repo, redisClient, 0, 0); err == nil {
		t.Fatal("non-positive durability wait must fail fast")
	}
	if _, err := iamService.NewLifecycleFactRelay(repo, redisClient, 0, 30*time.Second); err == nil {
		t.Fatal("durability wait must fit inside the outbox lease")
	}
}

func TestLifecycleFactRelayStartStopAndNilSafety(t *testing.T) {
	var nilRelay *iamService.LifecycleFactRelay
	nilRelay.Notify()
	nilRelay.Stop()

	repo := &lifecycleFactOutboxRepositoryStub{claimed: make(chan struct{}, 1)}
	relay, err := iamService.NewLifecycleFactRelay(repo, newBillingRelayTestRedis(t), 0, time.Second)
	if err != nil {
		t.Fatalf("new billing relay: %v", err)
	}
	relay.Notify()
	relay.Start()
	defer relay.Stop()

	select {
	case <-repo.claimed:
	case <-time.After(time.Second):
		t.Fatal("relay did not reconcile after start")
	}
}

func TestLifecycleFactRelayMarksUnsupportedEventDead(t *testing.T) {
	repo := &lifecycleFactOutboxRepositoryStub{
		events: []iamEntity.LifecycleFactOutboxEvent{{
			ID:        42,
			EventID:   uuid.New(),
			EventType: "unsupported.event",
			OwnerID:   uuid.New(),
			OwnerType: "PERSONAL",
			Payload:   []byte("invalid"),
		}},
		dead: make(chan struct{}, 1),
	}
	relay, err := iamService.NewLifecycleFactRelay(repo, newBillingRelayTestRedis(t), 0, time.Second)
	if err != nil {
		t.Fatalf("new billing relay: %v", err)
	}
	relay.Start()
	defer relay.Stop()

	select {
	case <-repo.dead:
	case <-time.After(time.Second):
		t.Fatal("unsupported event was not moved to dead state")
	}
	repo.mu.Lock()
	message := repo.lastDead
	repo.mu.Unlock()
	if message == "" {
		t.Fatal("dead event should include a reason")
	}
}
