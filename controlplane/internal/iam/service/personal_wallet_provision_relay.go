package iamSvcImpl

import (
	"context"
	"fmt"
	"time"

	iamRepoInterface "controlplane/internal/iam/domain/repo"
	"controlplane/pkg/logger"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const personalWalletProvisionSubject = "billing.wallet.personal.provision.requested.v1"

type PersonalWalletProvisionRelay struct {
	repo   iamRepoInterface.PersonalWalletProvisionOutboxRepository
	js     jetstream.JetStream
	cancel context.CancelFunc
	done   chan struct{}
}

func NewPersonalWalletProvisionRelay(repo iamRepoInterface.PersonalWalletProvisionOutboxRepository, nc *nats.Conn) (*PersonalWalletProvisionRelay, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name: "BILLING_WALLET_PROVISION", Subjects: []string{personalWalletProvisionSubject},
		Storage: jetstream.FileStorage, Retention: jetstream.LimitsPolicy, MaxAge: 30 * 24 * time.Hour,
	})
	if err != nil {
		return nil, fmt.Errorf("iam account relay: ensure stream: %w", err)
	}
	_ = stream
	return &PersonalWalletProvisionRelay{repo: repo, js: js, done: make(chan struct{})}, nil
}

func (r *PersonalWalletProvisionRelay) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	go r.run(ctx)
}

func (r *PersonalWalletProvisionRelay) run(ctx context.Context) {
	defer close(r.done)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	cleanupTicker := time.NewTicker(time.Hour)
	defer cleanupTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			events, err := r.repo.Claim(ctx, 50)
			if err != nil {
				logger.SysError("iam.account_outbox.claim", err.Error())
				continue
			}
			for _, event := range events {
				// [COMMENT]: PubAck là ranh giới xác nhận broker đã lưu event bền vững.
				_, publishErr := r.js.Publish(ctx, personalWalletProvisionSubject, event.Payload, jetstream.WithMsgID(event.EventID.String()))
				if publishErr != nil {
					_ = r.repo.MarkFailed(ctx, event.ID, publishErr.Error())
					continue
				}
				if err := r.repo.MarkPublished(ctx, event.ID); err != nil {
					// JetStream dedup theo Msg-Id làm retry sau lease an toàn.
					logger.SysError("iam.account_outbox.mark_published", err.Error())
				}
			}
		case <-cleanupTicker.C:
			// [COMMENT]: Xóa theo batch nhỏ để tránh lock/IO spike trên PostgreSQL HA.
			if _, err := r.repo.CleanupPublished(ctx, 1000); err != nil {
				logger.SysError("iam.account_outbox.cleanup", err.Error())
			}
		}
	}
}

func (r *PersonalWalletProvisionRelay) Stop() {
	if r == nil || r.cancel == nil {
		return
	}
	r.cancel()
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
	}
}
