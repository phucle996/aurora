package pubsubHandler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"controlplane/internal/cacheengine"
	iamproto "controlplane/internal/iam/transport/proto"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const (
	// billingAuthorizationRequestChannel là kênh Pub/Sub trên Shared Redis nhận yêu cầu nạp lại Cache quyền hạn từ Cost Manager.
	billingAuthorizationRequestChannel = "iam.authorization.billing.get"

	// billingAuthorizationReplyPrefix là tiền tố kênh Pub/Sub phản hồi kết quả nạp quyền về cho Cost Manager theo từng request ID.
	billingAuthorizationReplyPrefix = "iam.authorization.billing.reply."

	// maxConcurrentSlots là số lượng luồng tối đa xử lý phân giải quyền đồng thời trên mỗi Pod IAM để bảo vệ PostgreSQL.
	maxConcurrentSlots = 32

	// dispatchLockTTL là thời hạn sống của khóa phân tán ngăn chặn nhiều Pod IAM cùng tranh chấp giải mã 1 request.
	dispatchLockTTL = 2 * time.Second

	// authDataTTL là thời gian sống của dữ liệu quyền hạn Billing lưu trong Auth Redis (120 giây).
	authDataTTL = 120 * time.Second

	// generationTTL là thời gian sống của khóa phiên bản Generation (24 giờ).
	generationTTL = 86400 * time.Second
)

// BillingAuthorizationRedisHandler lắng nghe các yêu cầu giải quyết Cache Miss quyền hạn từ Cost Manager
// qua Shared Redis Pub/Sub, truy vấn RBAC Role từ PostgreSQL/L1 Cache và nạp quyền vào Auth Redis.
type BillingAuthorizationRedisHandler struct {
	sharedRedis *goredis.Client
	authRedis   *goredis.Client
	cacheEngine *cacheengine.CacheRegistry

	cancel context.CancelFunc
	pubsub *goredis.PubSub
	loopWG sync.WaitGroup
	workWG sync.WaitGroup
	slots  chan struct{}
}

// NewBillingAuthorizationRedisHandler khởi tạo handler với các kết nối Shared Redis, Auth Redis và IAM Cache Registry.
func NewBillingAuthorizationRedisHandler(
	sharedRedis *goredis.Client,
	authRedis *goredis.Client,
	cacheEngine *cacheengine.CacheRegistry,
) (*BillingAuthorizationRedisHandler, error) {
	if sharedRedis == nil || authRedis == nil || cacheEngine == nil {
		return nil, errors.New("billing authorization Redis handler requires Shared Redis, Auth Redis and the IAM cache registry")
	}

	return &BillingAuthorizationRedisHandler{
		sharedRedis: sharedRedis,
		authRedis:   authRedis,
		cacheEngine: cacheEngine,
		// Giới hạn số lượng worker đồng thời trên mỗi replica để tránh làm kiệt quệ Connection Pool của PostgreSQL
		slots: make(chan struct{}, maxConcurrentSlots),
	}, nil
}

// Start đăng ký Subscriber trên kênh Pub/Sub của Shared Redis và khởi chạy Dispatcher Goroutine.
func (h *BillingAuthorizationRedisHandler) Start() error {
	if h == nil {
		return errors.New("billing authorization Redis handler is nil")
	}

	ctx, cancel := context.WithCancel(pkgcontext.WithOperation(context.Background(), "iam.authorization.billing.subscribe"))
	pubsub := h.sharedRedis.Subscribe(ctx, billingAuthorizationRequestChannel)

	// Readiness Fence: Đảm bảo Redis Server đã chấp nhận SUBSCRIBE trước khi thông báo sẵn sàng
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

				// Điều phối vào worker pool nếu còn slot trống
				select {
				case h.slots <- struct{}{}:
					h.workWG.Add(1)
					go func(payload string) {
						defer h.workWG.Done()
						defer func() { <-h.slots }()
						h.resolve([]byte(payload))
					}(message.Payload)
				default:
					// Tất cả Pod IAM đều nhận được Pub/Sub message. Nếu Pod này đang bận (hết slot),
					// nó sẽ chủ động từ chối để Pod IAM khác rảnh rỗi hơn tranh chấp khóa xử lý.
				}
			}
		}
	}()

	return nil
}

// resolve giải mã yêu cầu, chiếm khóa phân tán, phân giải quyền hạn RBAC và nạp vào Auth Redis.
func (h *BillingAuthorizationRedisHandler) resolve(payload []byte) {
	// 1. Kiểm tra cấu trúc nhị phân của payload yêu cầu:
	// - Độ dài 33 bytes: Personal Scope [mode (1B) + requestID (16B) + userID (16B)]
	// - Độ dài 49 bytes: Tenant Scope   [mode (1B) + requestID (16B) + userID (16B) + tenantID (16B)]
	if (len(payload) != 33 && len(payload) != 49) || payload[0] > 1 {
		return
	}

	critical := payload[0] == 1
	requestID, requestErr := uuid.FromBytes(payload[1:17])
	userID, userErr := uuid.FromBytes(payload[17:33])
	if requestErr != nil || userErr != nil || requestID == uuid.Nil || userID == uuid.Nil {
		return
	}

	tenantID := uuid.Nil
	if len(payload) == 49 {
		parsedTenantID, tenantErr := uuid.FromBytes(payload[33:49])
		if tenantErr != nil || parsedTenantID == uuid.Nil {
			return
		}
		tenantID = parsedTenantID
	}

	replyChannel := billingAuthorizationReplyPrefix + requestID.String()
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(context.Background(), "iam.authorization.billing.resolve"), 700*time.Millisecond)
	defer cancel()

	// respond gửi phản hồi nhị phân {1: Thành công, 0: Thất bại} về cho Cost Manager
	respond := func(ok bool) {
		wire := []byte{0}
		if ok {
			wire[0] = 1
		}
		responseCtx, responseCancel := context.WithTimeout(context.WithoutCancel(ctx), 300*time.Millisecond)
		defer responseCancel()
		if err := h.sharedRedis.Publish(responseCtx, replyChannel, wire).Err(); err != nil {
			logger.SysErrorCtx(ctx, "redis.BillingAuthorization", "Failed to publish authorization response")
		}
	}

	// 2. Chiếm khóa phân tán (Distributed Lock qua SetNX):
	// Đảm bảo trong cụm IAM chỉ có DUY NHẤT 1 Pod thực thi nạp dữ liệu cho requestID này.
	lockKey := "iam:authorization:billing:dispatch:" + requestID.String()
	lockToken := uuid.NewString()
	acquired, err := h.sharedRedis.SetNX(ctx, lockKey, lockToken, dispatchLockTTL).Result()
	if err != nil || !acquired {
		return
	}

	// Compare-and-Delete: Giải phóng khóa an toàn, tránh xóa nhầm khóa của tiến trình khác nếu bị trễ TTL
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

	// ========================================================================
	// NHÁNH A: PHÂN GIẢI QUYỀN HẠN TỔ CHỨC (TENANT SCOPED RESOLUTION)
	// ========================================================================
	if tenantID != uuid.Nil {
		if critical {
			// Yêu cầu critical -> Xóa L1 Cache cục bộ để ép đọc dữ liệu tươi mới từ PostgreSQL
			h.cacheEngine.L1.Delete("membership_role:" + userID.String() + ":" + tenantID.String())
		}

		cacheValue, loadErr := h.cacheEngine.GetOrLoad(ctx, "membership_role", userID.String()+":"+tenantID.String())
		if loadErr != nil {
			respond(false)
			return
		}

		roleEntry, ok := cacheValue.(*iamproto.RoleEntry)
		if !ok || roleEntry == nil {
			respond(false)
			return
		}

		// Ràng buộc bảo mật (Security Invariant):
		// Tuyệt đối không cho phép quyền hạn cấp Tenant bị mở rộng thành quyền cấp Platform.
		expectedPrefix := tenantID.String() + ":" + uuid.Nil.String() + ":billing:"
		permissions := make([]string, 0, len(roleEntry.Permissions))
		seen := make(map[string]struct{}, len(roleEntry.Permissions))

		for _, permission := range roleEntry.Permissions {
			parts := strings.Split(permission, ":")
			if len(parts) != 5 || !strings.HasPrefix(permission, expectedPrefix) || parts[3] == "" || parts[4] == "" {
				respond(false)
				return
			}
			if _, exists := seen[permission]; !exists {
				seen[permission] = struct{}{}
				permissions = append(permissions, permission)
			}
		}

		if len(permissions) == 0 {
			respond(false)
			return
		}

		sort.Strings(permissions)
		responseBinary, marshalErr := proto.Marshal(&iamproto.RoleEntry{Permissions: permissions})
		if marshalErr != nil {
			respond(false)
			return
		}

		// Ghi quyền hạn Tenant vào Auth Redis (TTL 5 giây cho luồng ngắn hạn)
		dataKey := fmt.Sprintf("authz:billing:tenant:{%s}:%s:data", tenantID, userID)
		if writeErr := h.authRedis.Set(ctx, dataKey, responseBinary, 5*time.Second).Err(); writeErr != nil {
			respond(false)
			return
		}

		respond(true)
		return
	}

	// ========================================================================
	// NHÁNH B: PHÂN GIẢI QUYỀN HẠN CÁ NHÂN / PLATFORM (PERSONAL SCOPED RESOLUTION)
	// ========================================================================
	dataKey := fmt.Sprintf("authz:billing:{%s}:data", userID)
	generationKey := fmt.Sprintf("authz:billing:{%s}:generation", userID)
	dataGenerationKey := fmt.Sprintf("authz:billing:{%s}:data_generation", userID)

	// Thử tối đa 2 lần để đối phó với hiện tượng Generation Fencing Conflict
	for attempt := 0; attempt < 2; attempt++ {
		if critical {
			h.cacheEngine.L1.Delete("user_role:" + userID.String())
		}

		// Lấy số phiên bản Generation hiện tại trong Auth Redis
		expectedGeneration, generationErr := h.authRedis.Get(ctx, generationKey).Result()
		if errors.Is(generationErr, goredis.Nil) {
			expectedGeneration = "0"
		} else if generationErr != nil {
			respond(false)
			return
		}

		// Truy vấn RoleEntry từ Cache Registry / DB
		cacheValue, loadErr := h.cacheEngine.GetOrLoad(ctx, "user_role", userID.String())
		if loadErr != nil {
			logger.SysErrorCtx(ctx, "redis.BillingAuthorization", "Failed to load user authorization")
			respond(false)
			return
		}

		roleEntry, ok := cacheValue.(*iamproto.RoleEntry)
		if !ok || roleEntry == nil {
			respond(false)
			return
		}

		// Chuẩn hóa và lọc danh sách quyền hạn Billing
		permissions := make([]string, 0, len(roleEntry.Permissions))
		seen := make(map[string]struct{}, len(roleEntry.Permissions))

		for _, raw := range roleEntry.Permissions {
			parts := strings.Split(raw, ":")
			permission := ""
			switch {
			case len(parts) == 3 && parts[0] == "billing":
				permission = raw
			case len(parts) == 5 && parts[2] == "billing" && (parts[1] == "*" || parts[1] == uuid.Nil.String()):
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
			respond(false)
			return
		}

		responseBinary, marshalErr := proto.Marshal(&iamproto.RoleEntry{Permissions: permissions})
		if marshalErr != nil {
			respond(false)
			return
		}

		// Generation Fencing Lua Script:
		// Kiểm tra xem số hiệu Generation có bị thay đổi trong lúc IAM đang query DB hay không.
		// Nếu Generation bị lệch (do quyền vừa bị chỉnh sửa/thu hồi) -> Script từ chối ghi (trả về 0) để thử lại.
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
			responseBinary, expectedGeneration, int64(authDataTTL.Seconds()), int64(generationTTL.Seconds())).Int()

		if writeErr != nil {
			respond(false)
			return
		}

		if written == 1 {
			// Nạp dữ liệu vào Auth Redis thành công -> Báo OK cho Cost Manager
			respond(true)
			return
		}
	}

	respond(false)
}

// Stop dừng tiến trình lắng nghe Pub/Sub và chờ toàn bộ worker hoàn tất.
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

	// Đảm bảo đóng dispatcher trước khi chờ workers để tránh Data Race giữa Add và Wait
	h.loopWG.Wait()
	h.workWG.Wait()
}
