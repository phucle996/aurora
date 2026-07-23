package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
	"google.golang.org/protobuf/encoding/protowire"
)

const (
	billingAuthorizationSubject = "iam.authorization.billing.get"
	userRoleInvalidatedSubject  = "iam.user_role.invalidated"
	maxAuthorizationL1Entries   = 32768
)

type authorizationCacheEntry struct {
	permissions map[string]struct{}
	expiresAt   time.Time
}

// AuthorizationResolver giữ quyền ngoài Trinity: L1 cục bộ, L2 dùng chung, IAM là SoT qua NATS.
type AuthorizationResolver struct {
	redis        *redis.Client
	nats         *nats.Conn
	l1Mu         sync.RWMutex
	l1           map[uuid.UUID]authorizationCacheEntry
	loads        singleflight.Group
	invalidation *nats.Subscription
}

func NewAuthorizationResolver(redisClient *redis.Client, natsConn *nats.Conn) (*AuthorizationResolver, error) {
	if redisClient == nil || natsConn == nil {
		return nil, errors.New("authorization resolver requires Redis cache and NATS")
	}
	resolver := &AuthorizationResolver{
		redis: redisClient,
		nats:  natsConn,
		l1:    make(map[uuid.UUID]authorizationCacheEntry),
	}
	subscription, err := natsConn.Subscribe(userRoleInvalidatedSubject, func(message *nats.Msg) {
		userID, parseErr := uuid.Parse(strings.TrimSpace(string(message.Data)))
		if parseErr != nil {
			return
		}
		// [COMMENT]: Mỗi pod tự xóa L1; không dùng queue group vì invalidation phải fan-out đến toàn bộ replica.
		resolver.l1Mu.Lock()
		delete(resolver.l1, userID)
		resolver.l1Mu.Unlock()
		dataKey, generationKey, dataGenerationKey, _ := authorizationKeys(userID)
		invalidationContext, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer cancel()
		// [COMMENT]: Cost lặp lại invalidation L2 để NATS còn là đường dự phòng nếu CP vừa commit DB nhưng Redis call lỗi.
		_ = resolver.redis.Eval(invalidationContext, `
			redis.call("INCR", KEYS[1])
			redis.call("EXPIRE", KEYS[1], ARGV[1])
			redis.call("DEL", KEYS[2], KEYS[3])
			return 1
		`, []string{generationKey, dataKey, dataGenerationKey}, int64(86400)).Err()
	})
	if err != nil {
		return nil, fmt.Errorf("subscribe IAM authorization invalidation: %w", err)
	}
	if err := natsConn.FlushTimeout(2 * time.Second); err != nil {
		_ = subscription.Unsubscribe()
		return nil, fmt.Errorf("activate IAM authorization invalidation subscription: %w", err)
	}
	resolver.invalidation = subscription
	return resolver, nil
}

func (r *AuthorizationResolver) Close() {
	if r != nil && r.invalidation != nil {
		_ = r.invalidation.Unsubscribe()
	}
}

func authorizationKeys(userID uuid.UUID) (data, generation, dataGeneration, lock string) {
	tag := fmt.Sprintf("{iam:authz:billing:%s}", userID)
	return tag + ":data", tag + ":generation", tag + ":data_generation", tag + ":lock"
}

func decodeBillingPermissions(binary []byte) (map[string]struct{}, error) {
	permissions := make(map[string]struct{})
	for len(binary) > 0 {
		number, wireType, consumed := protowire.ConsumeTag(binary)
		if consumed < 0 {
			return nil, errors.New("invalid IAM RoleEntry tag")
		}
		binary = binary[consumed:]
		if number != 1 || wireType != protowire.BytesType {
			skipped := protowire.ConsumeFieldValue(number, wireType, binary)
			if skipped < 0 {
				return nil, errors.New("invalid IAM RoleEntry field")
			}
			binary = binary[skipped:]
			continue
		}
		value, size := protowire.ConsumeBytes(binary)
		if size < 0 {
			return nil, errors.New("invalid IAM RoleEntry permission")
		}
		permission := string(value)
		parts := strings.Split(permission, ":")
		if len(parts) != 3 || parts[0] != "billing" || parts[1] == "" || parts[2] == "" {
			return nil, fmt.Errorf("IAM returned invalid Billing permission %q", permission)
		}
		permissions[permission] = struct{}{}
		binary = binary[size:]
	}
	if len(permissions) == 0 {
		return nil, errors.New("IAM returned no Billing permission")
	}
	return permissions, nil
}

func (r *AuthorizationResolver) readL2(ctx context.Context, userID uuid.UUID) (map[string]struct{}, bool, error) {
	dataKey, generationKey, dataGenerationKey, _ := authorizationKeys(userID)
	values, err := r.redis.MGet(ctx, dataKey, generationKey, dataGenerationKey).Result()
	if err != nil {
		return nil, false, fmt.Errorf("read authorization L2: %w", err)
	}
	if len(values) != 3 || values[0] == nil || values[2] == nil {
		return nil, false, nil
	}
	generation := "0"
	if values[1] != nil {
		generation = fmt.Sprint(values[1])
	}
	if generation != fmt.Sprint(values[2]) {
		return nil, false, nil
	}
	var binary []byte
	switch value := values[0].(type) {
	case string:
		binary = []byte(value)
	case []byte:
		binary = value
	default:
		return nil, false, errors.New("authorization L2 contains an invalid payload")
	}
	permissions, err := decodeBillingPermissions(binary)
	if err != nil {
		return nil, false, err
	}
	return permissions, true, nil
}

func (r *AuthorizationResolver) requestIAM(ctx context.Context, userID uuid.UUID) ([]byte, error) {
	requestContext, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()
	response, err := r.nats.RequestWithContext(requestContext, billingAuthorizationSubject, []byte(userID.String()))
	if err != nil {
		return nil, fmt.Errorf("request IAM Billing authorization: %w", err)
	}
	if response.Header.Get("Aurora-Error") != "" {
		return nil, errors.New(response.Header.Get("Aurora-Error"))
	}
	if _, err := decodeBillingPermissions(response.Data); err != nil {
		return nil, err
	}
	return response.Data, nil
}

func (r *AuthorizationResolver) load(ctx context.Context, userID uuid.UUID, forceIAM bool) (map[string]struct{}, error) {
	dataKey, generationKey, dataGenerationKey, lockKey := authorizationKeys(userID)
	maxAttempts := 3
	if forceIAM {
		maxAttempts = 12
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if !forceIAM {
			if permissions, hit, err := r.readL2(ctx, userID); err != nil {
				return nil, err
			} else if hit {
				return permissions, nil
			}
		}

		token := uuid.NewString()
		acquired, err := r.redis.SetNX(ctx, lockKey, token, 2*time.Second).Result()
		if err != nil {
			return nil, fmt.Errorf("acquire authorization refresh lock: %w", err)
		}
		if !acquired {
			// [COMMENT]: Jitter làm lệch nhịp các pod cùng miss; waiter chỉ đọc L2 và không dồn request vào IAM.
			timer := time.NewTimer(time.Duration(30+rand.IntN(70)) * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
				continue
			}
		}

		permissions, loadErr := func() (map[string]struct{}, error) {
			defer func() {
				// [COMMENT]: Compare-and-delete ngăn owner cũ xóa lock đã hết hạn rồi được pod khác chiếm.
				_ = r.redis.Eval(context.Background(), `
					if redis.call("GET", KEYS[1]) == ARGV[1] then
						return redis.call("DEL", KEYS[1])
					end
					return 0
				`, []string{lockKey}, token).Err()
			}()

			expectedGeneration, generationErr := r.redis.Get(ctx, generationKey).Result()
			if errors.Is(generationErr, redis.Nil) {
				expectedGeneration = "0"
			} else if generationErr != nil {
				return nil, fmt.Errorf("read authorization generation: %w", generationErr)
			}
			binary, requestErr := r.requestIAM(ctx, userID)
			if requestErr != nil {
				return nil, requestErr
			}
			// [COMMENT]: Lua generation fence không cho DB snapshot cũ ghi đè invalidation xảy ra trong lúc NATS/DB đang chạy.
			written, writeErr := r.redis.Eval(ctx, `
				local current = redis.call("GET", KEYS[2]) or "0"
				if current ~= ARGV[2] then return 0 end
				redis.call("SET", KEYS[1], ARGV[1], "EX", ARGV[3])
				redis.call("SET", KEYS[3], ARGV[2], "EX", ARGV[3])
				if redis.call("EXISTS", KEYS[2]) == 0 then
					redis.call("SET", KEYS[2], ARGV[2], "EX", ARGV[4])
				end
				return 1
			`, []string{dataKey, generationKey, dataGenerationKey},
				binary, expectedGeneration, int64(120), int64(86400)).Int()
			if writeErr != nil {
				return nil, fmt.Errorf("write authorization L2: %w", writeErr)
			}
			if written != 1 {
				return nil, nil
			}
			return decodeBillingPermissions(binary)
		}()
		if loadErr != nil {
			return nil, loadErr
		}
		if permissions != nil {
			return permissions, nil
		}
	}
	return nil, errors.New("authorization cache is changing; retry the request")
}

// Resolve kiểm tra L1 trước; critical=true bỏ cả L1/L2 và buộc lấy dữ liệu mới từ IAM.
func (r *AuthorizationResolver) Resolve(ctx context.Context, userID uuid.UUID, critical bool) (map[string]struct{}, error) {
	if !critical {
		r.l1Mu.RLock()
		entry, exists := r.l1[userID]
		r.l1Mu.RUnlock()
		if exists && time.Now().Before(entry.expiresAt) {
			return entry.permissions, nil
		}
	}

	loadKey := userID.String()
	if critical {
		loadKey += ":critical"
	}
	value, err, _ := r.loads.Do(loadKey, func() (any, error) {
		return r.load(ctx, userID, critical)
	})
	if err != nil {
		return nil, err
	}
	permissions := value.(map[string]struct{})
	// [COMMENT]: L1 TTL ngắn giới hạn stale window nếu NATS invalidation tạm thời gián đoạn.
	r.l1Mu.Lock()
	if len(r.l1) >= maxAuthorizationL1Entries {
		now := time.Now()
		for cachedUserID, entry := range r.l1 {
			if now.After(entry.expiresAt) {
				delete(r.l1, cachedUserID)
			}
		}
		// [COMMENT]: Hard cap chống cardinality attack; eviction ngẫu nhiên chỉ tạo cache miss, không đổi correctness.
		if len(r.l1) >= maxAuthorizationL1Entries {
			for cachedUserID := range r.l1 {
				delete(r.l1, cachedUserID)
				break
			}
		}
	}
	r.l1[userID] = authorizationCacheEntry{permissions: permissions, expiresAt: time.Now().Add(5 * time.Second)}
	r.l1Mu.Unlock()
	return permissions, nil
}
