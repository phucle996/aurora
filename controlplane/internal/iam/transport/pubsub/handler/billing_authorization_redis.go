package pubsubHandler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const (
	billingAuthorizationRequestChannel = "iam.authorization.billing.get"
	billingAuthorizationReplyPrefix    = "iam.authorization.billing.reply."
)

// BillingAuthorizationRedisHandler resolves Cost authorization cache misses through the
// central Shared Redis bus. Auth Redis remains the security-state projection store.
type BillingAuthorizationRedisHandler struct {
	sharedRedis  *goredis.Client
	authRedis    *goredis.Client
	platformRepo iamRepoInterface.RbacPlatformRepository
	tenantRepo   iamRepoInterface.RbacTenantRepository

	cancel context.CancelFunc
	pubsub *goredis.PubSub
	loopWG sync.WaitGroup
	workWG sync.WaitGroup
	slots  chan struct{}
}

func NewBillingAuthorizationRedisHandler(
	sharedRedis *goredis.Client,
	authRedis *goredis.Client,
	platformRepo iamRepoInterface.RbacPlatformRepository,
	tenantRepo iamRepoInterface.RbacTenantRepository,
) (*BillingAuthorizationRedisHandler, error) {
	if sharedRedis == nil || authRedis == nil || platformRepo == nil || tenantRepo == nil {
		return nil, errors.New("billing authorization Redis handler requires Shared Redis, Auth Redis and both RBAC repositories")
	}
	return &BillingAuthorizationRedisHandler{
		sharedRedis:  sharedRedis,
		authRedis:    authRedis,
		platformRepo: platformRepo,
		tenantRepo:   tenantRepo,
		// [COMMENT]: Bound DB concurrency per replica; overload becomes a short Cost timeout instead of exhausting PostgreSQL.
		slots: make(chan struct{}, 32),
	}, nil
}

func (h *BillingAuthorizationRedisHandler) Start() error {
	if h == nil {
		return errors.New("billing authorization Redis handler is nil")
	}
	ctx, cancel := context.WithCancel(context.Background())
	pubsub := h.sharedRedis.Subscribe(ctx, billingAuthorizationRequestChannel)
	// [COMMENT]: Receive is the readiness fence: Cost must never see this replica as a subscriber before Redis accepted SUBSCRIBE.
	if _, err := pubsub.Receive(ctx); err != nil {
		cancel()
		_ = pubsub.Close()
		return fmt.Errorf("subscribe Billing authorization request channel: %w", err)
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
					go func(payload string) {
						defer h.workWG.Done()
						defer func() { <-h.slots }()
						h.resolve([]byte(payload))
					}(message.Payload)
				default:
					// [COMMENT]: Every CP replica receives Pub/Sub; a saturated replica declines without taking the distributed lock.
				}
			}
		}
	}()
	return nil
}

func (h *BillingAuthorizationRedisHandler) resolve(payload []byte) {
	// [COMMENT]: Platform wire is request+user UUID; tenant wire appends the
	// edge-verified tenant UUID. Fixed widths prevent parser ambiguity and
	// payload amplification on the Shared Redis security bridge.
	if len(payload) != 32 && len(payload) != 48 {
		return
	}
	requestID, requestErr := uuid.FromBytes(payload[:16])
	userID, userErr := uuid.FromBytes(payload[16:32])
	if requestErr != nil || userErr != nil || requestID == uuid.Nil || userID == uuid.Nil {
		return
	}
	tenantID := uuid.Nil
	if len(payload) == 48 {
		parsedTenantID, tenantErr := uuid.FromBytes(payload[32:48])
		if tenantErr != nil || parsedTenantID == uuid.Nil {
			return
		}
		tenantID = parsedTenantID
	}
	replyChannel := billingAuthorizationReplyPrefix + requestID.String()
	respond := func(ok bool, response []byte) {
		wire := make([]byte, 1, len(response)+1)
		if ok {
			wire[0] = 1
		}
		wire = append(wire, response...)
		if err := h.sharedRedis.Publish(context.Background(), replyChannel, wire).Err(); err != nil {
			logger.SysError("redis.BillingAuthorization", "Failed to publish authorization response")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	lockKey := "iam:authorization:billing:dispatch:" + requestID.String()
	lockToken := uuid.NewString()
	acquired, err := h.sharedRedis.SetNX(ctx, lockKey, lockToken, 2*time.Second).Result()
	if err != nil || !acquired {
		return
	}
	defer func() {
		// [COMMENT]: Compare-delete avoids deleting a replacement lock if Redis stalls past the original TTL.
		_ = h.sharedRedis.Eval(context.Background(), `
			if redis.call("GET", KEYS[1]) == ARGV[1] then
				return redis.call("DEL", KEYS[1])
			end
			return 0
		`, []string{lockKey}, lockToken).Err()
	}()

	if tenantID != uuid.Nil {
		binaryEntry, loadErr := h.tenantRepo.GetUserTenantBillingPermissions(ctx, userID, tenantID)
		if loadErr != nil {
			respond(false, []byte("tenant billing permission is required"))
			return
		}
		var roleEntry iamproto.RoleEntry
		if proto.Unmarshal(binaryEntry, &roleEntry) != nil {
			respond(false, []byte("authorization snapshot is invalid"))
			return
		}

		expectedPrefix := tenantID.String() + ":" + uuid.Nil.String() + ":billing:"
		permissions := make([]string, 0, len(roleEntry.Permissions))
		seen := make(map[string]struct{}, len(roleEntry.Permissions))
		for _, permission := range roleEntry.Permissions {
			parts := strings.Split(permission, ":")
			if len(parts) != 5 || !strings.HasPrefix(permission, expectedPrefix) ||
				parts[3] == "" || parts[4] == "" {
				// [COMMENT]: Never flatten a tenant permission into a platform
				// permission; an invalid snapshot fails the whole decision closed.
				respond(false, []byte("authorization snapshot is invalid"))
				return
			}
			if _, exists := seen[permission]; !exists {
				seen[permission] = struct{}{}
				permissions = append(permissions, permission)
			}
		}
		if len(permissions) == 0 {
			respond(false, []byte("tenant billing permission is required"))
			return
		}
		sort.Strings(permissions)
		responseBinary, marshalErr := proto.Marshal(&iamproto.RoleEntry{Permissions: permissions})
		if marshalErr != nil {
			respond(false, []byte("authorization snapshot is invalid"))
			return
		}
		// [COMMENT]: Critical tenant mutations bypass Cost caches, so this reply
		// is deliberately not projected into the platform authorization keys.
		respond(true, responseBinary)
		return
	}

	dataKey := fmt.Sprintf("authz:billing:{%s}:data", userID)
	generationKey := fmt.Sprintf("authz:billing:{%s}:generation", userID)
	dataGenerationKey := fmt.Sprintf("authz:billing:{%s}:data_generation", userID)
	for attempt := 0; attempt < 2; attempt++ {
		expectedGeneration, generationErr := h.authRedis.Get(ctx, generationKey).Result()
		if errors.Is(generationErr, goredis.Nil) {
			expectedGeneration = "0"
		} else if generationErr != nil {
			respond(false, []byte("authorization cache is unavailable"))
			return
		}

		binaryEntry, loadErr := h.platformRepo.GetUserRolePermissions(ctx, userID)
		if loadErr != nil {
			logger.SysError("redis.BillingAuthorization", "Failed to load user authorization")
			respond(false, []byte("authorization service unavailable"))
			return
		}
		var roleEntry iamproto.RoleEntry
		if proto.Unmarshal(binaryEntry, &roleEntry) != nil {
			respond(false, []byte("authorization snapshot is invalid"))
			return
		}

		// [COMMENT]: A workspace-scoped role must never be promoted into global Billing permission.
		permissions := make([]string, 0, len(roleEntry.Permissions))
		seen := make(map[string]struct{}, len(roleEntry.Permissions))
		for _, raw := range roleEntry.Permissions {
			parts := strings.Split(raw, ":")
			permission := ""
			switch {
			case len(parts) == 3 && parts[0] == "billing":
				permission = raw
			case len(parts) == 5 && parts[2] == "billing" &&
				(parts[1] == "*" || parts[1] == uuid.Nil.String()):
				permission = strings.Join(parts[2:], ":")
			default:
				continue
			}
			if _, exists := seen[permission]; !exists {
				seen[permission] = struct{}{}
				permissions = append(permissions, permission)
			}
		}
		sort.Strings(permissions)
		if len(permissions) == 0 {
			respond(false, []byte("billing permission is required"))
			return
		}
		responseBinary, marshalErr := proto.Marshal(&iamproto.RoleEntry{Permissions: permissions})
		if marshalErr != nil {
			respond(false, []byte("authorization snapshot is invalid"))
			return
		}

		// [COMMENT]: Generation fencing rejects the DB snapshot when RBAC mutates during the query.
		written, writeErr := h.authRedis.Eval(ctx, `
			local current = redis.call("GET", KEYS[2]) or "0"
			if current ~= ARGV[2] then return 0 end
			redis.call("SET", KEYS[1], ARGV[1], "EX", ARGV[3])
			redis.call("SET", KEYS[3], ARGV[2], "EX", ARGV[3])
			if redis.call("EXISTS", KEYS[2]) == 0 then
				redis.call("SET", KEYS[2], ARGV[2], "EX", ARGV[4])
			end
			return 1
		`, []string{dataKey, generationKey, dataGenerationKey},
			responseBinary, expectedGeneration, int64(120), int64(86400)).Int()
		if writeErr != nil {
			respond(false, []byte("authorization cache is unavailable"))
			return
		}
		if written == 1 {
			respond(true, responseBinary)
			return
		}
	}
	respond(false, []byte("authorization changed while it was being resolved"))
}

func (h *BillingAuthorizationRedisHandler) Stop() {
	if h == nil {
		return
	}
	if h.cancel != nil {
		h.cancel()
	}
	if h.pubsub != nil {
		_ = h.pubsub.Close()
	}
	// [COMMENT]: Stop dispatcher before waiting workers so no WaitGroup Add can race with Wait.
	h.loopWG.Wait()
	h.workWG.Wait()
}
