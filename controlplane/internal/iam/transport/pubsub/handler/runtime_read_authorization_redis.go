package pubsubHandler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamproto "controlplane/internal/iam/transport/proto"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const (
	runtimeReadAuthorizationRequestChannel = "iam.authorization.runtime.get"
	runtimeReadAuthorizationReplyPrefix    = "iam.authorization.runtime.reply."
)

// RuntimeReadAuthorizationRedisHandler is the IAM-owned decision boundary
// used before ACR mints a short-lived Zone runtime-read assertion. It returns
// only an allow/deny decision; no role, permission catalog or credential is
// copied into the assertion or exposed to the browser.
type RuntimeReadAuthorizationRedisHandler struct {
	sharedRedis *goredis.Client
	personal    iamSvcInterface.PersonalRuntimeReadAuthorizationService
	tenant      iamSvcInterface.TenantRuntimeReadAuthorizationService
	cancel      context.CancelFunc
	pubsub      *goredis.PubSub
	loopWG      sync.WaitGroup
	workWG      sync.WaitGroup
	slots       chan struct{}
}

func NewRuntimeReadAuthorizationRedisHandler(
	sharedRedis *goredis.Client,
	personal iamSvcInterface.PersonalRuntimeReadAuthorizationService,
	tenant iamSvcInterface.TenantRuntimeReadAuthorizationService,
) (*RuntimeReadAuthorizationRedisHandler, error) {
	if sharedRedis == nil || personal == nil || tenant == nil {
		return nil, errors.New("runtime-read authorization Redis handler requires Shared Redis and both owner authorization services")
	}
	return &RuntimeReadAuthorizationRedisHandler{
		sharedRedis: sharedRedis,
		personal:    personal,
		tenant:      tenant,
		slots:       make(chan struct{}, 32),
	}, nil
}

func (h *RuntimeReadAuthorizationRedisHandler) Start() error {
	ctx, cancel := context.WithCancel(pkgcontext.WithOperation(context.Background(), "iam.authorization.runtime.subscribe"))
	pubsub := h.sharedRedis.Subscribe(ctx, runtimeReadAuthorizationRequestChannel)
	if _, err := pubsub.Receive(ctx); err != nil {
		cancel()
		_ = pubsub.Close()
		return fmt.Errorf("subscribe runtime-read authorization request channel: %w", err)
	}
	h.cancel = cancel
	h.pubsub = pubsub
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
					}([]byte(message.Payload))
				default:
					// Every IAM replica receives the request. A replica with no
					// capacity leaves the distributed claim to another replica.
				}
			}
		}
	}()
	return nil
}

func (h *RuntimeReadAuthorizationRedisHandler) resolve(payload []byte) {
	if len(payload) <= 16 {
		return
	}
	requestID, err := uuid.FromBytes(payload[:16])
	if err != nil || requestID == uuid.Nil {
		return
	}
	var request iamproto.RuntimeReadAuthorizationRequestV1
	if proto.Unmarshal(payload[16:], &request) != nil {
		return
	}
	actorUserID, actorErr := uuid.FromBytes(request.ActorUserId)
	workspaceID, workspaceErr := uuid.FromBytes(request.WorkspaceId)
	if actorErr != nil || workspaceErr != nil || actorUserID == uuid.Nil || workspaceID == uuid.Nil {
		return
	}
	permissionParts := strings.Split(request.Permission, ":")
	if len(permissionParts) != 3 {
		return
	}
	for _, part := range permissionParts {
		if part == "" || len(part) > 64 || strings.IndexFunc(part, func(value rune) bool {
			return !(value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '_' || value == '-')
		}) >= 0 {
			return
		}
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(context.Background(), "iam.authorization.runtime.resolve"), 700*time.Millisecond)
	defer cancel()
	lockKey := "iam:authorization:runtime:dispatch:" + requestID.String()
	lockToken := uuid.NewString()
	acquired, lockErr := h.sharedRedis.SetNX(ctx, lockKey, lockToken, 2*time.Second).Result()
	if lockErr != nil || !acquired {
		return
	}
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 300*time.Millisecond)
		defer releaseCancel()
		_ = h.sharedRedis.Eval(releaseCtx, `
			if redis.call("GET", KEYS[1]) == ARGV[1] then
				return redis.call("DEL", KEYS[1])
			end
			return 0
		`, []string{lockKey}, lockToken).Err()
	}()

	allowed := false
	tenantID := uuid.Nil
	if len(request.TenantId) != 0 {
		tenantID, err = uuid.FromBytes(request.TenantId)
		if err != nil || tenantID == uuid.Nil {
			return
		}
		allowed, _ = h.tenant.Authorize(ctx, iamEntity.TenantRuntimeReadAuthorization{
			ActorUserID: actorUserID,
			TenantID:    tenantID,
			WorkspaceID: workspaceID,
			Permission:  request.Permission,
		})
	} else {
		username := strings.TrimSpace(request.ActorUsername)
		if username == "" || len(username) > 128 || strings.Contains(username, ":") {
			return
		}
		allowed, _ = h.personal.Authorize(ctx, iamEntity.PersonalRuntimeReadAuthorization{
			ActorUserID:   actorUserID,
			ActorUsername: username,
			WorkspaceID:   workspaceID,
			Permission:    request.Permission,
		})
	}

	wire, marshalErr := proto.Marshal(&iamproto.RuntimeReadAuthorizationResponseV1{Allowed: allowed})
	if marshalErr != nil {
		return
	}
	replyCtx, replyCancel := context.WithTimeout(context.WithoutCancel(ctx), 300*time.Millisecond)
	defer replyCancel()
	if publishErr := h.sharedRedis.Publish(replyCtx, runtimeReadAuthorizationReplyPrefix+requestID.String(), wire).Err(); publishErr != nil {
		logger.SysErrorCtx(ctx, "redis.RuntimeReadAuthorization", "Failed to publish runtime-read authorization response")
	}
}

func (h *RuntimeReadAuthorizationRedisHandler) Stop() {
	if h.cancel != nil {
		h.cancel()
	}
	if h.pubsub != nil {
		_ = h.pubsub.Close()
	}
	h.loopWG.Wait()
	h.workWG.Wait()
}
