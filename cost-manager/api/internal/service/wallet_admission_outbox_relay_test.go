package service

import (
	"context"
	"net"
	"testing"
	"time"

	"cost-manager/api/internal/domain/entity"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type walletAdmissionRelayRepoStub struct {
	marked bool
}

func (s *walletAdmissionRelayRepoStub) ClaimUnpublishedWalletAdmissionBatch(context.Context, int, uuid.UUID) ([]*entity.WalletAdmissionOutboxRow, error) {
	return nil, nil
}

func (s *walletAdmissionRelayRepoStub) MarkWalletAdmissionPublished(context.Context, uuid.UUID, uuid.UUID) error {
	s.marked = true
	return nil
}

func (s *walletAdmissionRelayRepoStub) RecordWalletAdmissionError(context.Context, uuid.UUID, uuid.UUID, string) error {
	return nil
}

type walletAdmissionRedisHook struct {
	waitArgs []interface{}
}

func (h *walletAdmissionRedisHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return next(ctx, network, addr)
	}
}

func (h *walletAdmissionRedisHook) ProcessHook(_ redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		switch cmd.Name() {
		case "xadd":
			cmd.(*redis.StringCmd).SetVal("1-0")
		case "waitaof":
			h.waitArgs = append([]interface{}(nil), cmd.Args()...)
			cmd.(*redis.Cmd).SetVal([]interface{}{int64(1), int64(0)})
		}
		return nil
	}
}

func (h *walletAdmissionRedisHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		return next(ctx, cmds)
	}
}

func TestWalletAdmissionRelayUsesWorkflowDurabilityPolicy(t *testing.T) {
	row := &entity.WalletAdmissionOutboxRow{
		EventID:       uuid.New(),
		OwnerID:       uuid.New(),
		OwnerType:     entity.OwnerType("PERSONAL"),
		WalletVersion: 1,
		AdmissionMode: "SUSPEND_BILLABLE",
		EffectiveAt:   time.Now().UTC(),
		ClaimToken:    uuid.New(),
	}

	t.Run("standalone accepts local AOF without replica acknowledgement", func(t *testing.T) {
		repo := &walletAdmissionRelayRepoStub{}
		hook := &walletAdmissionRedisHook{}
		client := redis.NewClient(&redis.Options{Addr: "unused:6379"})
		client.AddHook(hook)
		t.Cleanup(func() { _ = client.Close() })

		relay := NewWalletAdmissionOutboxRelay(repo, client, entity.WalletAdmissionRelayPolicy{
			ReplicaAcks: 0,
			DurableWait: 2 * time.Second,
		})
		if err := relay.publishRow(context.Background(), row); err != nil {
			t.Fatalf("publishRow() error = %v", err)
		}
		if !repo.marked {
			t.Fatal("published row was not marked")
		}
		if len(hook.waitArgs) != 4 || hook.waitArgs[1] != 1 || hook.waitArgs[2] != 0 || hook.waitArgs[3] != int64(2000) {
			t.Fatalf("WAITAOF args = %#v, want [waitaof 1 0 2000]", hook.waitArgs)
		}
	})

	t.Run("replicated policy rejects missing replica acknowledgement", func(t *testing.T) {
		repo := &walletAdmissionRelayRepoStub{}
		hook := &walletAdmissionRedisHook{}
		client := redis.NewClient(&redis.Options{Addr: "unused:6379"})
		client.AddHook(hook)
		t.Cleanup(func() { _ = client.Close() })

		relay := NewWalletAdmissionOutboxRelay(repo, client, entity.WalletAdmissionRelayPolicy{
			ReplicaAcks: 1,
			DurableWait: time.Second,
		})
		if err := relay.publishRow(context.Background(), row); err == nil {
			t.Fatal("publishRow() error = nil, want durability fence error")
		}
		if repo.marked {
			t.Fatal("row was marked without the required replica acknowledgement")
		}
	})
}
