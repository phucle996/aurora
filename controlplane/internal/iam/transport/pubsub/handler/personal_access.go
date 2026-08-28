package pubsubHandler

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	iamSvcInterface "controlplane/internal/iam/domain/service"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

const (
	personalAccessRequestChannel = "iam.personal.access.resolve"
	personalAccessReplyPrefix    = "iam.personal.access.reply."
)

// PersonalAccessRedisHandler resolves the durable platform role before ACR
// removes a tenant scope. It is deliberately separate from tenant access so a
// tenant membership can never be reused as personal authority.
type PersonalAccessRedisHandler struct {
	sharedRedis *goredis.Client
	service     iamSvcInterface.PersonalRbacService
	cancel      context.CancelFunc
	pubsub      *goredis.PubSub
	loopWG      sync.WaitGroup
	workWG      sync.WaitGroup
	slots       chan struct{}
}

func NewPersonalAccessRedisHandler(sharedRedis *goredis.Client, service iamSvcInterface.PersonalRbacService) (*PersonalAccessRedisHandler, error) {
	if sharedRedis == nil || service == nil {
		return nil, errors.New("personal access Redis handler requires Shared Redis and platform RBAC service")
	}
	return &PersonalAccessRedisHandler{sharedRedis: sharedRedis, service: service, slots: make(chan struct{}, 32)}, nil
}

func (h *PersonalAccessRedisHandler) Start() error {
	ctx, cancel := context.WithCancel(pkgcontext.WithOperation(context.Background(), "iam.personal_access.subscribe"))
	pubsub := h.sharedRedis.Subscribe(ctx, personalAccessRequestChannel)
	if _, err := pubsub.Receive(ctx); err != nil {
		cancel()
		_ = pubsub.Close()
		return fmt.Errorf("subscribe personal access request channel: %w", err)
	}
	h.cancel, h.pubsub = cancel, pubsub
	h.loopWG.Add(1)
	go func() {
		defer h.loopWG.Done()
		channel := pubsub.Channel(goredis.WithChannelSize(256))
		for {
			select {
			case <-ctx.Done():
				return
			case message, ok := <-channel:
				if !ok {
					return
				}
				select {
				case h.slots <- struct{}{}:
					h.workWG.Add(1)
					go func(payload []byte) {
						defer h.workWG.Done()
						defer func() { <-h.slots }()
						h.resolve(payload)
					}(append([]byte(nil), message.Payload...))
				default:
				}
			}
		}
	}()
	return nil
}

func (h *PersonalAccessRedisHandler) resolve(payload []byte) {
	if len(payload) != 32 {
		return
	}
	requestID, requestErr := uuid.FromBytes(payload[:16])
	userID, userErr := uuid.FromBytes(payload[16:])
	if requestErr != nil || userErr != nil || requestID == uuid.Nil || userID == uuid.Nil {
		return
	}
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(context.Background(), "iam.personal_access.resolve"), 700*time.Millisecond)
	defer cancel()
	lockKey := "iam:personal_access:dispatch:" + requestID.String()
	lockToken := uuid.NewString()
	acquired, err := h.sharedRedis.SetNX(ctx, lockKey, lockToken, 2*time.Second).Result()
	if err != nil || !acquired {
		return
	}
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 300*time.Millisecond)
		defer releaseCancel()
		_ = h.sharedRedis.Eval(releaseCtx, `if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) end return 0`, []string{lockKey}, lockToken).Err()
	}()

	response := []byte{0}
	roleLevel, resolveErr := h.service.ResolvePersonalRoleLevel(ctx, userID)
	if resolveErr == nil && roleLevel >= 0 {
		response = make([]byte, 5)
		response[0] = 1
		binary.BigEndian.PutUint32(response[1:], uint32(roleLevel))
	}
	if err := h.sharedRedis.Publish(context.WithoutCancel(ctx), personalAccessReplyPrefix+requestID.String(), response).Err(); err != nil {
		logger.SysErrorCtx(ctx, "redis.PersonalAccess", "Failed to publish personal access response")
	}
}

func (h *PersonalAccessRedisHandler) Stop() {
	if h.cancel != nil {
		h.cancel()
	}
	if h.pubsub != nil {
		_ = h.pubsub.Close()
	}
	h.loopWG.Wait()
	h.workWG.Wait()
}
