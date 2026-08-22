package middleware

import (
	"context"
	"errors"
	"fmt"
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
	// tenantAuthorizationRequestChannel là kênh Pub/Sub trên Shared Redis để gửi yêu cầu phân quyền sang IAM
	tenantAuthorizationRequestChannel = "iam.authorization.billing.get"

	// tenantAuthorizationReplyPrefix là tiền tố kênh nhận phản hồi kết quả từ IAM (mỗi request có một channel riêng dạng reply.{request_id})
	tenantAuthorizationReplyPrefix = "iam.authorization.billing.reply."

	// tenantAuthorizationInvalidation là kênh nhận tín hiệu thu hồi quyền ngay lập tức khi quyền của user bị thay đổi trong IAM
	tenantAuthorizationInvalidation = "authz.invalidate.billing"

	// maxTenantAuthorizationL1Entries giới hạn số lượng bản ghi tối đa trong bộ nhớ RAM L1 để chống tràn bộ nhớ (OOM)
	maxTenantAuthorizationL1Entries = 32768
)

// tenantAuthorizationCacheEntry đại diện cho một phần tử quyền hạn lưu trong bộ nhớ RAM L1 của tiến trình.
type tenantAuthorizationCacheEntry struct {
	permissions map[string]struct{} // Tập hợp các quyền hạn đã được chuẩn hóa (dùng map O(1) để tra cứu)
	expiresAt   time.Time           // Thời điểm hết hạn của cache entry trong RAM
}

// TenantAuthorizationMiddleware quản lý toàn bộ cơ chế xác thực và phân quyền RBAC (Role-Based Access Control)
// cho các thao tác bên trong một Tenant / Doanh nghiệp / Tổ chức cụ thể:
// - Đảm bảo tính cô lập tuyệt đối (Tenant Isolation): Quyền của Tenant A không bao giờ thao tác được trên Tenant B.
// - Mô hình Cache 3 Tầng: L1 RAM (2s) -> L2 Auth-State Redis Protobuf -> L3 IAM Authority (SoT).
// - Cơ chế SingleFlight: Chống nghẽn tải (Cache Stampede) khi có nhiều request cùng kiểm tra quyền một lúc.
// - Thu hồi quyền theo thời gian thực (Real-time Invalidation) qua Redis Pub/Sub.
type TenantAuthorizationMiddleware struct {
	authRedis    *redis.Client                            // Kết nối tới cụm Auth-State Redis (nơi lưu trữ L2 Cache và Security Fences)
	sharedRedis  *redis.Client                            // Kết nối tới cụm Shared Redis (nơi làm đường truyền Request/Reply và Invalidation)
	l1Mu         sync.RWMutex                             // Khóa đồng bộ bảo vệ bộ nhớ RAM L1
	l1           map[string]tenantAuthorizationCacheEntry // Bộ nhớ RAM L1: Lưu quyền theo key "{tenant_id}:{user_id}"
	loads        singleflight.Group                       // Singleflight gom các yêu cầu truy vấn IAM trùng lặp
	invalidation *redis.PubSub                            // Subscriber lắng nghe sự kiện thu hồi quyền từ IAM
	cancel       context.CancelFunc                       // Hàm hủy context dừng background worker khi shutdown
	wg           sync.WaitGroup                           // WaitGroup đảm bảo goroutine dọn dẹp an toàn trước khi tắt tiến trình
}

// NewTenantAuthorizationMiddleware khởi tạo middleware phân quyền Tenant kèm kết nối 2 cụm Redis độc lập.
// Luồng khởi tạo:
// 1. Kiểm tra tính hợp lệ của cả 2 Redis client (Auth Redis và Shared Redis).
// 2. Đăng ký lắng nghe kênh Invalidation trên Shared Redis và chờ tín hiệu xác nhận (Subscribe ACK) từ Redis.
// 3. Khởi chạy goroutine chạy ngầm liên tục nhận thông điệp thu hồi quyền để xóa L1 Cache tức thì.
func NewTenantAuthorizationMiddleware(authRedisClient, sharedRedisClient *redis.Client) (*TenantAuthorizationMiddleware, error) {
	if authRedisClient == nil || sharedRedisClient == nil {
		return nil, errors.New("authorization middleware requires Auth Redis and Shared Redis")
	}

	ctx, cancel := context.WithCancel(context.Background())
	authorization := &TenantAuthorizationMiddleware{
		authRedis:   authRedisClient,
		sharedRedis: sharedRedisClient,
		l1:          make(map[string]tenantAuthorizationCacheEntry),
		cancel:      cancel,
	}

	// 1. Đăng ký nhận tín hiệu thu hồi quyền từ IAM
	pubsub := sharedRedisClient.Subscribe(ctx, tenantAuthorizationInvalidation)

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

				// Xóa ngay lập tức tất cả các bản ghi trong L1 RAM của User này trên mọi Tenant
				authorization.l1Mu.Lock()
				suffix := ":" + userID.String()
				for key := range authorization.l1 {
					if strings.HasSuffix(key, suffix) {
						delete(authorization.l1, key)
					}
				}
				authorization.l1Mu.Unlock()
			}
		}
	}()

	return authorization, nil
}

// Close thực hiện đóng kết nối và dừng an toàn goroutine lắng nghe invalidation.
func (r *TenantAuthorizationMiddleware) Close() {
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
// 1. WORKFLOW: PHÂN GIẢI QUYỀN HẠN CỦA USER TRONG TENANT (RESOLVE TENANT)
// ============================================================================

// resolveTenant phân giải tập hợp quyền hạn của một User trong một Tenant cụ thể qua 3 tầng Cache:
//  1. Nếu không phải yêu cầu quan trọng (critical = false): Kiểm tra L1 RAM -> L2 Auth Redis.
//  2. Nếu L1 và L2 đều Miss (hoặc critical = true): Dùng SingleFlight gọi sang IAM qua Shared Redis,
//     sau đó đọc dữ liệu Protobuf binary từ L2 Auth Redis do IAM vừa cập nhật.
func (r *TenantAuthorizationMiddleware) resolveTenant(
	ctx context.Context,
	userID uuid.UUID,
	tenantID uuid.UUID,
	critical bool,
) (map[string]struct{}, error) {
	cacheKey := tenantID.String() + ":" + userID.String()

	// 1. Tầng 1: Đọc nhanh từ L1 RAM trong tiến trình (nếu không bắt buộc bypass cache)
	if !critical {
		r.l1Mu.RLock()
		entry, exists := r.l1[cacheKey]
		r.l1Mu.RUnlock()
		if exists && time.Now().Before(entry.expiresAt) {
			return entry.permissions, nil
		}

		// 2. Tầng 2: Đọc từ L2 Cache trên Auth-State Redis (dữ liệu dạng Protobuf binary)
		dataKey := fmt.Sprintf("authz:billing:tenant:{%s}:%s:data", tenantID, userID)
		if binary, err := r.authRedis.Get(ctx, dataKey).Bytes(); err == nil {
			permissions, parseErr := parseTenantBillingPermissions(binary, tenantID)
			if parseErr != nil {
				return nil, parseErr
			}
			r.cacheTenantPermissions(cacheKey, permissions)
			return permissions, nil
		} else if !errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("read tenant authorization L2: %w", err)
		}
	}

	// 3. Tầng 3: Gọi trực tiếp sang IAM qua SingleFlight để nạp mới dữ liệu
	loadKey := "tenant:" + cacheKey
	if critical {
		loadKey += ":critical"
	}
	value, err, _ := r.loads.Do(loadKey, func() (any, error) {
		// Bắn yêu cầu nạp quyền sang IAM và chờ IAM xác nhận thành công
		if requestErr := r.requestTenantRefresh(ctx, userID, tenantID, critical); requestErr != nil {
			return nil, requestErr
		}

		// Sau khi IAM xác nhận đã ghi vào Auth-State Redis, Cost đọc lại cục Protobuf binary
		dataKey := fmt.Sprintf("authz:billing:tenant:{%s}:%s:data", tenantID, userID)
		binary, readErr := r.authRedis.Get(ctx, dataKey).Bytes()
		if readErr != nil {
			if errors.Is(readErr, redis.Nil) {
				return nil, errors.New("IAM acknowledged tenant authorization refresh without an Auth Redis projection")
			}
			return nil, fmt.Errorf("read tenant authorization L2 after IAM acknowledgement: %w", readErr)
		}

		// Giải mã Protobuf nhị phân và kiểm tra tính hợp lệ của quyền Tenant
		permissions, parseErr := parseTenantBillingPermissions(binary, tenantID)
		if parseErr != nil {
			return nil, parseErr
		}
		return permissions, nil
	})
	if err != nil {
		return nil, err
	}

	permissions := value.(map[string]struct{})
	if !critical {
		r.cacheTenantPermissions(cacheKey, permissions)
	}
	return permissions, nil
}

// ============================================================================
// 2. WORKFLOW: GỬI REQUEST SANG IAM QUA SHARED REDIS (REQUEST REFRESH)
// ============================================================================

// requestTenantRefresh gửi thông điệp yêu cầu làm mới phân quyền sang IAM service qua kênh Redis Pub/Sub.
// Gói tin cố định độ dài 49 bytes: [mode(1 byte) + request_id(16 bytes) + user_id(16 bytes) + tenant_id(16 bytes)].
// - mode = 1: Critical request (bắt buộc nạp mới hoàn toàn từ PostgreSQL của IAM).
// - mode = 0: Normal request.
func (r *TenantAuthorizationMiddleware) requestTenantRefresh(
	ctx context.Context,
	userID uuid.UUID,
	tenantID uuid.UUID,
	critical bool,
) error {
	requestContext, cancel := context.WithTimeout(ctx, 900*time.Millisecond)
	defer cancel()

	requestID := uuid.New()
	replyChannel := tenantAuthorizationReplyPrefix + requestID.String()

	// 1. Mở subscription kênh nhận kết quả trước khi bắn request để tránh race condition
	pubsub := r.sharedRedis.Subscribe(requestContext, replyChannel)
	defer pubsub.Close()
	if _, err := pubsub.Receive(requestContext); err != nil {
		return fmt.Errorf("subscribe tenant Billing authorization reply: %w", err)
	}
	replies := pubsub.Channel(redis.WithChannelSize(1))

	// 2. Đóng gói gói tin nhị phân 49 bytes
	request := make([]byte, 0, 49)
	if critical {
		request = append(request, 1)
	} else {
		request = append(request, 0)
	}
	request = append(request, requestID[:]...)
	request = append(request, userID[:]...)
	request = append(request, tenantID[:]...)

	// 3. Bắn request lên kênh chung của IAM
	subscribers, err := r.sharedRedis.Publish(
		requestContext,
		tenantAuthorizationRequestChannel,
		request,
	).Result()
	if err != nil {
		return fmt.Errorf("publish tenant IAM Billing authorization request: %w", err)
	}
	if subscribers == 0 {
		return errors.New("IAM tenant Billing authorization middleware is unavailable")
	}

	// 4. Chờ IAM xử lý và trả về phản hồi
	select {
	case <-requestContext.Done():
		return fmt.Errorf("request tenant IAM Billing authorization: %w", requestContext.Err())
	case response, ok := <-replies:
		if !ok {
			return errors.New("IAM tenant Billing authorization reply subscription closed")
		}
		payload := []byte(response.Payload)
		if len(payload) != 1 {
			return errors.New("IAM returned an invalid tenant Billing authorization acknowledgement")
		}
		if payload[0] != 1 {
			return errors.New("IAM declined tenant Billing authorization refresh")
		}
		return nil
	}
}

// ============================================================================
// 3. WORKFLOW: GIẢI MÃ PROTOBUF NHỊ PHÂN VÀ VALIDATE QUYỀN (PROTOBUF DECODING)
// ============================================================================

// parseTenantBillingPermissions giải mã gói tin Protobuf nhị phân (Zero-reflection với protowire)
// và xác thực từng quyền hạn phải tuân thủ nghiêm ngặt định dạng quyền Tenant của hệ thống:
// `{tenant_id}:{workspace_id}:{service}:{resource}:{action}`
// Ví dụ: `7b5e4a82-...:00000000-0000-0000-0000-000000000000:billing:wallet:top_up`
//
// Quy tắc an toàn:
// - Quyền phải bắt đầu bằng chính xác `tenant_id` của Tenant hiện tại (ngăn chặn quyền của Tenant khác lọt vào).
// - Không được chứa các trường rỗng.
func parseTenantBillingPermissions(
	binary []byte,
	tenantID uuid.UUID,
) (map[string]struct{}, error) {
	permissions := make(map[string]struct{})
	expectedPrefix := tenantID.String() + ":00000000-0000-0000-0000-000000000000:billing:"

	// Đọc từng trường (Field) của Protobuf message
	for len(binary) > 0 {
		number, wireType, consumed := protowire.ConsumeTag(binary)
		if consumed < 0 {
			return nil, errors.New("invalid IAM tenant RoleEntry tag")
		}
		binary = binary[consumed:]

		// Bỏ qua các field khác nếu không phải field số 1 (danh sách quyền)
		if number != 1 || wireType != protowire.BytesType {
			skipped := protowire.ConsumeFieldValue(number, wireType, binary)
			if skipped < 0 {
				return nil, errors.New("invalid IAM tenant RoleEntry field")
			}
			binary = binary[skipped:]
			continue
		}

		// Đọc giá trị chuỗi permission
		value, size := protowire.ConsumeBytes(binary)
		if size < 0 {
			return nil, errors.New("invalid IAM tenant RoleEntry permission")
		}
		permission := string(value)

		// Kiểm tra định dạng 5 phần: tenant_id : workspace_id : billing : resource : action
		parts := strings.Split(permission, ":")
		if len(parts) != 5 || !strings.HasPrefix(permission, expectedPrefix) ||
			parts[3] == "" || parts[4] == "" {
			return nil, fmt.Errorf("IAM returned invalid tenant Billing permission %q", permission)
		}

		permissions[permission] = struct{}{}
		binary = binary[size:]
	}

	if len(permissions) == 0 {
		return nil, errors.New("IAM returned no tenant Billing permission")
	}

	return permissions, nil
}

// ============================================================================
// 4. WORKFLOW: QUẢN LÝ BỘ NHỚ L1 VÀ EVICTION POLICY
// ============================================================================

// cacheTenantPermissions lưu danh sách quyền vào RAM L1 cục bộ với thời gian sống 2 giây (TTL 2s).
// Cơ chế dọn dẹp (Eviction):
//   - Nếu số lượng bản ghi trong RAM vượt ngưỡng maxTenantAuthorizationL1Entries (32,768),
//     tiến hành quét và xóa các bản ghi đã hết hạn (expiresAt < now).
//   - Nếu sau khi quét vẫn đầy, xóa ngẫu nhiên 1 bản ghi cũ để bảo vệ bộ nhớ.
func (r *TenantAuthorizationMiddleware) cacheTenantPermissions(
	key string,
	permissions map[string]struct{},
) {
	r.l1Mu.Lock()
	defer r.l1Mu.Unlock()

	// Kiểm tra và dọn dẹp RAM khi đạt giới hạn dung lượng
	if len(r.l1) >= maxTenantAuthorizationL1Entries {
		now := time.Now()
		for cachedKey, entry := range r.l1 {
			if now.After(entry.expiresAt) {
				delete(r.l1, cachedKey)
			}
		}
		if len(r.l1) >= maxTenantAuthorizationL1Entries {
			for cachedKey := range r.l1 {
				delete(r.l1, cachedKey)
				break
			}
		}
	}

	r.l1[key] = tenantAuthorizationCacheEntry{
		permissions: permissions,
		expiresAt:   time.Now().Add(2 * time.Second),
	}
}

// ============================================================================
// 5. GIN MIDDLEWARE: KIỂM TRA QUYỀN HẠN TENANT (AUTHORIZE)
// ============================================================================

// Authorize là Gin HandlerFunc bảo vệ các API thuộc phạm vi Tenant.
//
// Quy trình kiểm tra:
// 1. Trích xuất User ID và Tenant ID đã được xác thực từ ACR Gateway Context.
// 2. Phân giải danh sách quyền của User trong Tenant này (`resolveTenant`).
// 3. Ghép chuỗi quyền yêu cầu: `{tenant_id}:00000000-0000-0000-0000-000000000000:{permission}`.
// 4. Nếu thiếu quyền -> Trả về HTTP 403 Forbidden.
// 5. Nếu IAM gặp sự cố tạm thời -> Trả về HTTP 503 Service Unavailable (Fail-closed, tuyệt đối không bypass).
func (r *TenantAuthorizationMiddleware) Authorize(permission string, critical bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. Lấy danh tính User và Tenant từ Context
		userID, ok := pkgcontext.GetUserID(c, "middleware.billing_authorize")
		if !ok {
			c.Abort()
			return
		}
		tenantID, ok := pkgcontext.GetTenantID(c, "middleware.tenant_authorize")
		if !ok {
			c.Abort()
			return
		}

		// 2. Phân giải quyền hạn
		permissions, err := r.resolveTenant(c.Request.Context(), userID, tenantID, critical)
		if err != nil {
			apires.RespondServiceUnavailable(c, "IAM tenant authorization is temporarily unavailable")
			c.Abort()
			return
		}

		// 3. Kiểm tra sự tồn tại của quyền yêu cầu
		required := tenantID.String() + ":00000000-0000-0000-0000-000000000000:" + permission
		if _, allowed := permissions[required]; !allowed {
			apires.RespondForbidden(c, "tenant billing permission is required")
			c.Abort()
			return
		}

		c.Next()
	}
}
