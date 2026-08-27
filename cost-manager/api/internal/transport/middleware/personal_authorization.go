package middleware

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"cost-manager/api/pkg/apires"
	"cost-manager/api/pkg/pkgcontext"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
	"google.golang.org/protobuf/encoding/protowire"
)

const (
	// personalAuthorizationRequestChannel là kênh Pub/Sub trên Shared Redis gửi yêu cầu làm mới phân quyền người dùng sang IAM
	personalAuthorizationRequestChannel = "iam.authorization.billing.get"

	// personalAuthorizationReplyPrefix là tiền tố kênh nhận phản hồi kết quả từ IAM (mỗi request lắng nghe tại kênh reply.{request_id})
	personalAuthorizationReplyPrefix = "iam.authorization.billing.reply."

	// personalAuthorizationInvalidation là kênh nhận thông điệp thu hồi quyền hạn ngay lập tức khi IAM có thay đổi Role/Permission của User
	personalAuthorizationInvalidation = "authz.invalidate.billing"

	// maxPersonalAuthorizationL1Entries giới hạn số lượng bản ghi quyền hạn tối đa trong RAM L1 để chống tràn bộ nhớ (OOM)
	maxPersonalAuthorizationL1Entries = 32768
)

// personalAuthorizationCacheEntry đại diện cho một phần tử quyền hạn lưu tạm trong bộ nhớ RAM L1 của tiến trình.
type personalAuthorizationCacheEntry struct {
	permissions map[string]struct{} // Tập hợp các quyền hạn dạng map O(1) để tra cứu siêu tốc
	expiresAt   time.Time           // Thời điểm hết hạn của cache entry trong RAM
}

// PersonalAuthorizationMiddleware quản lý toàn bộ cơ chế xác thực và phân quyền RBAC (Role-Based Access Control)
// cho các thao tác ở cấp độ Nền tảng (Platform Scope) của người dùng cá nhân (Platform Admin / Billing Operators):
// - Kiểm soát các API quản trị hệ thống như: Ban hành bảng giá (Publish Base Price), Cập nhật Catalog Metadata, v.v.
// - Khác với Self-User API (như xem ví của chính mình `/me` - chỉ cần xác thực danh tính), Platform API yêu cầu quyền cụ thể (RBAC).
// - Cơ chế Cache 3 Tầng: L1 RAM (5s) -> L2 Auth-State Redis (Protobuf + Generation Fencing) -> L3 IAM Authority (SingleFlight).
// - Cơ chế Distributed Lock với Jitter: Đảm bảo khi Cache Miss, chỉ 1 Pod gọi sang IAM, các Pod khác đợi và đọc lại L2.
// - Thu hồi quyền thời gian thực (Real-time Invalidation) qua Redis Pub/Sub.
type PersonalAuthorizationMiddleware struct {
	authRedis    *redis.Client                                // Kết nối tới cụm Auth-State Redis (L2 Cache và Generation Fences)
	sharedRedis  *redis.Client                                // Kết nối tới cụm Shared Redis (đường truyền Request/Reply và Invalidation)
	l1Mu         sync.RWMutex                                 // Khóa đồng bộ bảo vệ bộ nhớ RAM L1
	l1           map[uuid.UUID]personalAuthorizationCacheEntry // Bộ nhớ RAM L1: Lưu quyền theo User ID (UUID)
	loads        singleflight.Group                           // Singleflight gom các yêu cầu truy vấn IAM trùng lặp trong cùng tiến trình
	invalidation *redis.PubSub                                // Subscriber lắng nghe sự kiện thu hồi quyền từ IAM
	cancel       context.CancelFunc                           // Hàm hủy context dừng background worker khi tắt app
	wg           sync.WaitGroup                               // WaitGroup đảm bảo goroutine dọn dẹp an toàn trước khi shutdown
}

// NewPersonalAuthorizationMiddleware khởi tạo middleware phân quyền cấp độ Nền tảng (Platform Scope).
// Luồng khởi tạo:
// 1. Kiểm tra tính hợp lệ của cả 2 kết nối Redis (Auth Redis và Shared Redis).
// 2. Đăng ký nhận tín hiệu thu hồi quyền (Invalidation) trên Shared Redis và đợi xác nhận (Subscribe ACK).
// 3. Khởi chạy goroutine chạy ngầm liên tục nhận thông điệp thu hồi quyền để xóa L1 Cache tức thì.
func NewPersonalAuthorizationMiddleware(authRedisClient, sharedRedisClient *redis.Client) (*PersonalAuthorizationMiddleware, error) {
	if authRedisClient == nil || sharedRedisClient == nil {
		return nil, errors.New("authorization middleware requires Auth Redis and Shared Redis")
	}

	ctx, cancel := context.WithCancel(context.Background())
	authorization := &PersonalAuthorizationMiddleware{
		authRedis:   authRedisClient,
		sharedRedis: sharedRedisClient,
		l1:          make(map[uuid.UUID]personalAuthorizationCacheEntry),
		cancel:      cancel,
	}

	// 1. Đăng ký lắng nghe kênh Invalidation từ IAM
	pubsub := sharedRedisClient.Subscribe(ctx, personalAuthorizationInvalidation)

	// Đợi phản hồi Subscribe thành công từ Redis trước khi cho phép Pod nhận traffic
	if _, err := pubsub.Receive(ctx); err != nil {
		cancel()
		_ = pubsub.Close()
		return nil, fmt.Errorf("subscribe IAM authorization invalidation: %w", err)
	}
	authorization.invalidation = pubsub

	// 2. Chạy goroutine nền xử lý Invalidation message
	authorization.wg.Add(1)
	go func() {
		defer authorization.wg.Done()
		channel := pubsub.Channel(redis.WithChannelSize(256))
		for {
			select {
			case <-ctx.Done():
				return
			case message, ok := <-channel:
				if !ok {
					return
				}

				// Phân tích User ID bị thay đổi quyền từ payload
				userID, parseErr := uuid.Parse(strings.TrimSpace(message.Payload))
				if parseErr != nil {
					continue
				}

				// Xóa ngay lập tức bản ghi trong L1 RAM của User này
				authorization.l1Mu.Lock()
				delete(authorization.l1, userID)
				authorization.l1Mu.Unlock()
			}
		}
	}()

	return authorization, nil
}

// Close thực hiện đóng kết nối và dừng an toàn goroutine lắng nghe invalidation.
func (r *PersonalAuthorizationMiddleware) Close() {
	if r == nil {
		return
	}
	if r.cancel != nil {
		r.cancel()
	}
	if r.invalidation != nil {
		_ = r.invalidation.Close()
	}
	r.wg.Wait()
}

// ============================================================================
// 1. WORKFLOW: ĐỌC VÀ XÁC THỰC L2 CACHE TRÊN AUTH-STATE REDIS (READ L2)
// ============================================================================

// readL2 đọc dữ liệu phân quyền của User từ Auth-State Redis kèm cơ chế bảo vệ Generation Fencing:
// - `authz:billing:{user_id}:data`: Gói tin Protobuf binary chứa danh sách permissions.
// - `authz:billing:{user_id}:generation`: Số nguyên thế hệ hiện tại của phân quyền.
// - `authz:billing:{user_id}:data_generation`: Số nguyên thế hệ tại thời điểm dữ liệu được ghi vào data.
//
// Quy tắc an toàn (Security Invariant):
// - Nếu `generation != data_generation`: Dữ liệu phân quyền bị cũ (Stale) do vừa có sự kiện thay đổi quyền -> Coi như Cache Miss!
func (r *PersonalAuthorizationMiddleware) readL2(ctx context.Context, userID uuid.UUID) (map[string]struct{}, bool, error) {
	tag := fmt.Sprintf("authz:billing:{%s}", userID)
	dataKey := tag + ":data"
	generationKey := tag + ":generation"
	dataGenerationKey := tag + ":data_generation"

	// 1. MGet 3 key cùng lúc trong 1 round-trip duy nhất
	values, err := r.authRedis.MGet(ctx, dataKey, generationKey, dataGenerationKey).Result()
	if err != nil {
		return nil, false, fmt.Errorf("read authorization L2: %w", err)
	}

	// 2. Kiểm tra sự tồn tại của data và data_generation
	if len(values) != 3 || values[0] == nil || values[2] == nil {
		return nil, false, nil
	}

	// 3. Kiểm tra Generation Fence chống dữ liệu cũ
	generation := "0"
	if values[1] != nil {
		generation = fmt.Sprint(values[1])
	}
	if generation != fmt.Sprint(values[2]) {
		return nil, false, nil
	}

	// 4. Lấy mảng byte Protobuf nhị phân
	var binary []byte
	switch value := values[0].(type) {
	case string:
		binary = []byte(value)
	case []byte:
		binary = value
	default:
		return nil, false, errors.New("authorization L2 contains an invalid payload")
	}

	// 5. Giải mã Protobuf nhị phân (Zero-reflection với protowire)
	permissions := make(map[string]struct{})
	for len(binary) > 0 {
		number, wireType, consumed := protowire.ConsumeTag(binary)
		if consumed < 0 {
			return nil, false, errors.New("invalid IAM RoleEntry tag")
		}
		binary = binary[consumed:]

		// Bỏ qua các trường khác nếu không phải field số 1 (permissions)
		if number != 1 || wireType != protowire.BytesType {
			skipped := protowire.ConsumeFieldValue(number, wireType, binary)
			if skipped < 0 {
				return nil, false, errors.New("invalid IAM RoleEntry field")
			}
			binary = binary[skipped:]
			continue
		}

		value, size := protowire.ConsumeBytes(binary)
		if size < 0 {
			return nil, false, errors.New("invalid IAM RoleEntry permission")
		}
		permission := string(value)

		// Kiểm tra định dạng quyền Platform: billing:{resource}:{action} (3 phần)
		parts := strings.Split(permission, ":")
		if len(parts) != 3 || parts[0] != "billing" || parts[1] == "" || parts[2] == "" {
			return nil, false, fmt.Errorf("IAM returned invalid Billing permission %q", permission)
		}

		permissions[permission] = struct{}{}
		binary = binary[size:]
	}

	if len(permissions) == 0 {
		return nil, false, errors.New("IAM returned no Billing permission")
	}

	return permissions, true, nil
}

// ============================================================================
// 2. WORKFLOW: GỬI REQUEST SANG IAM QUA SHARED REDIS (REQUEST REFRESH)
// ============================================================================

// requestPersonalRefresh gửi yêu cầu làm mới phân quyền sang IAM service qua kênh Redis Pub/Sub.
// Gói tin cố định độ dài 33 bytes: [mode(1 byte) + request_id(16 bytes) + user_id(16 bytes)].
// - mode = 1: Critical request (bắt buộc nạp mới hoàn toàn từ PostgreSQL của IAM).
// - mode = 0: Normal request.
// IAM sau khi nhận được yêu cầu sẽ cập nhật dữ liệu vào Auth-State Redis và gửi lại 1 byte ACK.
func (r *PersonalAuthorizationMiddleware) requestPersonalRefresh(ctx context.Context, userID uuid.UUID, critical bool) error {
	requestContext, cancel := context.WithTimeout(ctx, 900*time.Millisecond)
	defer cancel()

	requestID := uuid.New()
	replyChannel := personalAuthorizationReplyPrefix + requestID.String()

	// 1. Mở subscription kênh nhận kết quả trước khi publish request để tránh race condition
	pubsub := r.sharedRedis.Subscribe(requestContext, replyChannel)
	defer pubsub.Close()
	if _, err := pubsub.Receive(requestContext); err != nil {
		return fmt.Errorf("subscribe Billing authorization reply: %w", err)
	}
	replies := pubsub.Channel(redis.WithChannelSize(1))

	// 2. Đóng gói payload nhị phân 33 bytes
	request := make([]byte, 0, 33)
	if critical {
		request = append(request, 1)
	} else {
		request = append(request, 0)
	}
	request = append(request, requestID[:]...)
	request = append(request, userID[:]...)

	// 3. Publish request lên kênh chung của IAM
	subscribers, err := r.sharedRedis.Publish(requestContext, personalAuthorizationRequestChannel, request).Result()
	if err != nil {
		return fmt.Errorf("publish IAM Billing authorization request: %w", err)
	}
	if subscribers == 0 {
		return errors.New("IAM Billing authorization middleware is unavailable")
	}

	// 4. Chờ IAM xử lý và trả về phản hồi
	select {
	case <-requestContext.Done():
		return fmt.Errorf("request IAM Billing authorization: %w", requestContext.Err())
	case response, ok := <-replies:
		if !ok {
			return errors.New("IAM Billing authorization reply subscription closed")
		}
		payload := []byte(response.Payload)
		if len(payload) != 1 {
			return errors.New("IAM returned an invalid Billing authorization acknowledgement")
		}
		if payload[0] != 1 {
			return errors.New("IAM declined Billing authorization refresh")
		}
		return nil
	}
}

// ============================================================================
// 3. WORKFLOW: NẠP DỮ LIỆU TỪ L2/L3 VỚI DISTRIBUTED LOCK & JITTER (LOAD)
// ============================================================================

// load xử lý quy trình nạp quyền hạn khi L1 Cache Miss:
// 1. Đọc thử từ L2 Auth-State Redis (nếu không ép forceIAM).
// 2. Nếu L2 Miss: Cạnh tranh chiếm Distributed Lock (`authz:billing:{user_id}:lock`) trên Redis trong 2s.
// 3. Nếu chiếm được Lock: Gọi sang IAM (requestPersonalRefresh) và đọc lại L2 sau khi IAM ghi xong.
//    Giải phóng Lock bằng Lua Script an toàn (Compare-and-Delete).
// 4. Nếu không chiếm được Lock (Pod khác đang nạp): Chờ một khoảng ngẫu nhiên (Jitter 30ms - 100ms) rồi thử đọc lại L2.
func (r *PersonalAuthorizationMiddleware) load(ctx context.Context, userID uuid.UUID, forceIAM bool) (map[string]struct{}, error) {
	lockKey := fmt.Sprintf("authz:billing:{%s}:lock", userID)
	maxAttempts := 3
	if forceIAM {
		maxAttempts = 12
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		// 1. Đọc thử L2 nếu không phải bắt buộc gọi IAM
		if !forceIAM {
			if permissions, hit, err := r.readL2(ctx, userID); err != nil {
				return nil, err
			} else if hit {
				return permissions, nil
			}
		}

		// 2. Thử chiếm Distributed Lock bằng SetNX
		token := uuid.NewString()
		acquired, err := r.authRedis.SetNX(ctx, lockKey, token, 2*time.Second).Result()
		if err != nil {
			return nil, fmt.Errorf("acquire authorization refresh lock: %w", err)
		}

		// Nếu không chiếm được lock, ngủ ngẫu nhiên một khoảng ngắn (Jitter) rồi thử lại
		if !acquired {
			timer := time.NewTimer(time.Duration(30+rand.IntN(70)) * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
				continue
			}
		}

		// 3. Pod chiếm được Lock: Thực hiện gọi IAM và đọc lại L2
		permissions, loadErr := func() (map[string]struct{}, error) {
			defer func() {
				// Giải phóng Lock an toàn bằng Lua Script: chỉ xóa nếu token khớp (tránh xóa nhầm lock của pod khác nếu bị timeout)
				_ = r.authRedis.Eval(context.Background(), `
					if redis.call("GET", KEYS[1]) == ARGV[1] then
						return redis.call("DEL", KEYS[1])
					end
					return 0
				`, []string{lockKey}, token).Err()
			}()

			// Yêu cầu IAM làm mới dữ liệu
			if requestErr := r.requestPersonalRefresh(ctx, userID, forceIAM); requestErr != nil {
				return nil, requestErr
			}

			// Đọc lại từ L2 sau khi IAM xác nhận đã cập nhật
			permissions, hit, readErr := r.readL2(ctx, userID)
			if readErr != nil {
				return nil, readErr
			}
			if !hit {
				return nil, errors.New("IAM acknowledged authorization refresh without an Auth Redis projection")
			}
			return permissions, nil
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

// ============================================================================
// 4. WORKFLOW: PHÂN GIẢI QUYỀN HẠN PLATFORM (RESOLVE PERSONAL)
// ============================================================================

// resolvePersonal phân giải tập hợp quyền hạn của User ở cấp độ Platform:
// 1. Kiểm tra nhanh L1 RAM trong tiến trình (TTL 5s).
// 2. Nếu L1 Miss: Dùng SingleFlight gọi hàm `load` để nạp dữ liệu (chống nhân bản truy vấn).
// 3. Lưu kết quả vào L1 RAM kèm cơ chế Eviction dọn dẹp khi vượt ngưỡng 32,768 entries.
func (r *PersonalAuthorizationMiddleware) resolvePersonal(ctx context.Context, userID uuid.UUID, critical bool) (map[string]struct{}, error) {
	// 1. Kiểm tra L1 RAM nếu không bắt buộc bypass cache
	if !critical {
		r.l1Mu.RLock()
		entry, exists := r.l1[userID]
		r.l1Mu.RUnlock()
		if exists && time.Now().Before(entry.expiresAt) {
			return entry.permissions, nil
		}
	}

	// 2. Dùng SingleFlight gom các concurrent request trong cùng 1 pod
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

	// 3. Lưu vào L1 RAM với TTL ngắn 5s (giới hạn stale window nếu invalidation tạm thời bị gián đoạn)
	r.l1Mu.Lock()
	if len(r.l1) >= maxPersonalAuthorizationL1Entries {
		now := time.Now()
		for cachedUserID, entry := range r.l1 {
			if now.After(entry.expiresAt) {
				delete(r.l1, cachedUserID)
			}
		}
		// Hard cap chống tấn công Cardinality tràn bộ nhớ
		if len(r.l1) >= maxPersonalAuthorizationL1Entries {
			for cachedUserID := range r.l1 {
				delete(r.l1, cachedUserID)
				break
			}
		}
	}
	r.l1[userID] = personalAuthorizationCacheEntry{
		permissions: permissions,
		expiresAt:   time.Now().Add(5 * time.Second),
	}
	r.l1Mu.Unlock()

	return permissions, nil
}

// ============================================================================
// 5. GIN MIDDLEWARE: KIỂM TRA QUYỀN HẠN PLATFORM (AUTHORIZE)
// ============================================================================

// Authorize là Gin HandlerFunc bảo vệ các API quản trị cấp độ Platform của người dùng cá nhân.
//
// Quy trình kiểm tra:
// 1. Trích xuất User ID từ Context xác thực của ACR Gateway.
// 2. Đảm bảo Context không bị ô nhiễm bởi Tenant Context không phải "platform".
// 3. Phân giải danh sách quyền Platform của User (`resolvePersonal`).
// 4. Nếu thiếu quyền -> Trả về HTTP 403 Forbidden.
// 5. Nếu IAM gặp sự cố -> Trả về HTTP 503 Service Unavailable (Fail-closed an toàn tuyệt đối).
func (r *PersonalAuthorizationMiddleware) Authorize(permission string, critical bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. Lấy User ID từ Context
		userID, ok := pkgcontext.GetUserID(c, "middleware.billing_authorize")
		if !ok {
			c.Abort()
			return
		}

		// 2. Đảm bảo phạm vi Platform Scope
		if value, hasTenantContext := c.Get(pkgcontext.CtxTenantID); hasTenantContext {
			if tenant, isPlatform := value.(string); !isPlatform || tenant != "platform" {
				apires.RespondForbidden(c, "personal platform-range authorization requires platform scope")
				c.Abort()
				return
			}
		}

		// 3. Phân giải quyền hạn
		permissions, err := r.resolvePersonal(c.Request.Context(), userID, critical)
		if err != nil {
			apires.RespondServiceUnavailable(c, "IAM authorization is temporarily unavailable")
			c.Abort()
			return
		}

		// 4. Kiểm tra sự tồn tại của quyền yêu cầu
		if _, allowed := permissions[permission]; !allowed {
			apires.RespondForbidden(c, "billing permission is required")
			c.Abort()
			return
		}

		c.Next()
	}
}
