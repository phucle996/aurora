package pubsubHandler

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

const (
	tenantAccessRequestChannel = "iam.tenant.access.resolve"
	tenantAccessReplyPrefix    = "iam.tenant.access.reply."
)

var tenantAccessDomainPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,253}[a-z0-9]$`)

type TenantAccessRedisHandler struct {
	sharedRedis *goredis.Client
	service     iamSvcInterface.TenantRbacService

	cancel context.CancelFunc
	pubsub *goredis.PubSub
	loopWG sync.WaitGroup
	workWG sync.WaitGroup
	slots  chan struct{}
}

func NewTenantAccessRedisHandler(sharedRedis *goredis.Client, service iamSvcInterface.TenantRbacService) (*TenantAccessRedisHandler, error) {
	if sharedRedis == nil || service == nil {
		return nil, errors.New("tenant access Redis handler requires Shared Redis and tenant RBAC service")
	}
	return &TenantAccessRedisHandler{
		sharedRedis: sharedRedis,
		service:     service,
		slots:       make(chan struct{}, 32),
	}, nil
}

func (h *TenantAccessRedisHandler) Start() error {
	ctx, cancel := context.WithCancel(pkgcontext.WithOperation(context.Background(), "iam.tenant_access.subscribe"))
	pubsub := h.sharedRedis.Subscribe(ctx, tenantAccessRequestChannel)
	if _, err := pubsub.Receive(ctx); err != nil {
		cancel()
		_ = pubsub.Close()
		return fmt.Errorf("subscribe tenant access request channel: %w", err)
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
					// [COMMENT]: Pub/Sub fans out to every replica. A saturated replica
					// declines; another healthy replica may acquire the dispatch fence.
				}
			}
		}
	}()
	return nil
}

func (h *TenantAccessRedisHandler) resolve(payload []byte) {
	// [COMMENT]: request_id + user_id + tenant_id are fixed-width UUIDs. The
	// remaining bytes are the canonical tenant domain and are bounded at this
	// transport boundary before any database work.
	if len(payload) < 49 || len(payload) > 303 {
		return
	}
	requestID, requestErr := uuid.FromBytes(payload[:16])
	userID, userErr := uuid.FromBytes(payload[16:32])
	tenantID, tenantErr := uuid.FromBytes(payload[32:48])
	domain := strings.ToLower(strings.TrimSpace(string(payload[48:])))
	if requestErr != nil || userErr != nil || tenantErr != nil ||
		requestID == uuid.Nil || userID == uuid.Nil || tenantID == uuid.Nil ||
		len(domain) < 3 || len(domain) > 255 || strings.Contains(domain, "..") ||
		strings.Contains(domain, ".-") || strings.Contains(domain, "-.") ||
		!tenantAccessDomainPattern.MatchString(domain) {
		return
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(context.Background(), "iam.tenant_access.resolve"), 700*time.Millisecond)
	defer cancel()
	lockKey := "iam:tenant_access:dispatch:" + requestID.String()
	lockToken := uuid.NewString()
	acquired, err := h.sharedRedis.SetNX(ctx, lockKey, lockToken, 2*time.Second).Result()
	if err != nil || !acquired {
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

	response := []byte{0}
	out, resolveErr := h.service.ResolveTenantAccess(ctx, &iamEntity.ResolveTenantAccess{
		UserID: userID, TenantID: tenantID, TenantDomain: domain,
	})
	if resolveErr == nil {
		response = make([]byte, 5)
		response[0] = 1
		binary.BigEndian.PutUint32(response[1:5], uint32(out.RoleLevel))
	}
	replyCtx, replyCancel := context.WithTimeout(context.WithoutCancel(ctx), 300*time.Millisecond)
	defer replyCancel()
	if publishErr := h.sharedRedis.Publish(replyCtx, tenantAccessReplyPrefix+requestID.String(), response).Err(); publishErr != nil {
		logger.SysErrorCtx(ctx, "redis.TenantAccess", "Failed to publish tenant access response")
	}
}

func (h *TenantAccessRedisHandler) Stop() {
	if h.cancel != nil {
		h.cancel()
	}
	if h.pubsub != nil {
		_ = h.pubsub.Close()
	}
	h.loopWG.Wait()
	h.workWG.Wait()
}
