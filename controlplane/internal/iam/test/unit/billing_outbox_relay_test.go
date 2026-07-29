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

type billingOutboxRepositoryStub struct {
	mu       sync.Mutex
	events   []iamEntity.BillingOutboxEvent
	claimed  chan struct{}
	dead     chan struct{}
	lastDead string
}

func (r *billingOutboxRepositoryStub) Claim(context.Context, int) ([]iamEntity.BillingOutboxEvent, error) {
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

func (r *billingOutboxRepositoryStub) MarkPublished(context.Context, int64) error {
	return nil
}

func (r *billingOutboxRepositoryStub) MarkFailed(context.Context, int64, string) error {
	return nil
}

func (r *billingOutboxRepositoryStub) MarkDead(_ context.Context, _ int64, message string) error {
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

func TestBillingOutboxRelayRejectsInvalidDependencies(t *testing.T) {
	redisClient := newBillingRelayTestRedis(t)
	repo := &billingOutboxRepositoryStub{}

	if _, err := iamService.NewBillingOutboxRelay(nil, redisClient, 0, time.Second); err == nil {
		t.Fatal("nil repository must fail fast")
	}
	if _, err := iamService.NewBillingOutboxRelay(repo, nil, 0, time.Second); err == nil {
		t.Fatal("nil Redis client must fail fast")
	}
	if _, err := iamService.NewBillingOutboxRelay(repo, redisClient, -1, time.Second); err == nil {
		t.Fatal("negative replica ACK count must fail fast")
	}
	if _, err := iamService.NewBillingOutboxRelay(repo, redisClient, 0, 0); err == nil {
		t.Fatal("non-positive durability wait must fail fast")
	}
}

func TestBillingOutboxRelayStartStopAndNilSafety(t *testing.T) {
	var nilRelay *iamService.BillingOutboxRelay
	nilRelay.Notify()
	nilRelay.Stop()

	repo := &billingOutboxRepositoryStub{claimed: make(chan struct{}, 1)}
	relay, err := iamService.NewBillingOutboxRelay(repo, newBillingRelayTestRedis(t), 0, time.Second)
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

func TestBillingOutboxRelayMarksUnsupportedEventDead(t *testing.T) {
	repo := &billingOutboxRepositoryStub{
		events: []iamEntity.BillingOutboxEvent{{
			ID:        42,
			EventID:   uuid.New(),
			EventType: "unsupported.event",
			OwnerID:   uuid.New(),
			OwnerType: "PERSONAL",
			Payload:   []byte("invalid"),
		}},
		dead: make(chan struct{}, 1),
	}
	relay, err := iamService.NewBillingOutboxRelay(repo, newBillingRelayTestRedis(t), 0, time.Second)
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
