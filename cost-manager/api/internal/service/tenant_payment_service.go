package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strconv"
	"strings"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingSvcInterface "cost-manager/api/internal/domain/service"
	"cost-manager/api/internal/provisioner"
	"cost-manager/api/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

// tenantPaymentService là Domain Service điều phối các nghiệp vụ thanh toán cho tài khoản tổ chức (Tenant):
// 1. Tra cứu số dư ví tổ chức (`GetWallet`).
// 2. Tạo phiên thanh toán nạp tiền và sinh URL Checkout có chữ ký HMAC (`CreateTopUp`).
// 3. Tra cứu chi tiết trạng thái phiên thanh toán của tổ chức (`GetTopUp`).
// 4. Quyết toán tiền vào ví tổ chức và ghi nhận dòng sự kiện hoạt động người dùng (`ApplyVerifiedSettlement`).
type tenantPaymentService struct {
	repo        billingRepoInterface.TenantPaymentRepository // Repository quản lý các transaction và CTE thanh toán tổ chức trong PostgreSQL
	sharedRedis *goredis.Client                              // Redis Client để ghi log hoạt động người dùng (User Activity Timeline)
	policy      entity.PaymentPolicy                         // Chính sách thanh toán hệ thống (Provider, TTL, Khóa ký URL Checkout)
	checkoutURL url.URL                                      // URL cơ sở của trang thanh toán Checkout
	returnURL   url.URL                                      // URL chuyển hướng người dùng quay lại ứng dụng sau khi thanh toán
}

// NewTenantPaymentService khởi tạo một instance mới của tenantPaymentService, trả về interface TenantPaymentService.
func NewTenantPaymentService(
	repo billingRepoInterface.TenantPaymentRepository,
	sharedRedis *goredis.Client,
	policy entity.PaymentPolicy,
	checkoutURL url.URL,
	returnURL url.URL,
) billingSvcInterface.TenantPaymentService {
	return &tenantPaymentService{
		repo:        repo,
		sharedRedis: sharedRedis,
		policy:      policy,
		checkoutURL: checkoutURL,
		returnURL:   returnURL,
	}
}

// GetWallet tra cứu thông tin tóm tắt ví tiền tổ chức từ Repository theo TenantID.
func (s *tenantPaymentService) GetWallet(
	ctx context.Context,
	tenantID uuid.UUID,
) (*entity.WalletSummary, error) {
	return s.repo.GetTenantWalletSummary(ctx, tenantID)
}

// CreateTopUp khởi tạo phiên nạp tiền cho tổ chức và sinh Checkout URL có chữ ký HMAC-SHA256:
// 1. Gắn các thông số mặc định: USD, Provider thanh toán, và thời hạn hiệu lực (`IntentTTL`).
// 2. Gọi Repository tạo bản ghi `billing.payment_intents` cho Tenant.
// 3. Xây dựng Checkout URL có chữ ký HMAC để bảo đảm an toàn dữ liệu số tiền và thông tin tổ chức.
func (s *tenantPaymentService) CreateTopUp(
	ctx context.Context,
	command entity.CreateTenantPaymentIntentCommand,
) (*entity.PaymentIntent, error) {
	command.Currency = "USD"
	command.Provider = s.policy.Provider
	command.ExpiresAt = time.Now().UTC().Add(s.policy.IntentTTL)

	intent, err := s.repo.CreateTenantIntent(ctx, command)
	if err != nil {
		return nil, err
	}

	// Xây dựng checkout URL có chữ ký HMAC nếu intent đang chờ thanh toán
	if intent != nil && intent.Status == "PENDING" {
		checkoutURL := s.checkoutURL
		returnURL := s.returnURL
		returnQuery := returnURL.Query()
		returnQuery.Set("payment_intent_id", intent.ID.String())
		returnURL.RawQuery = returnQuery.Encode()

		signingPayload := strings.Join([]string{
			"aurora.checkout.v1",
			intent.ID.String(),
			string(intent.OwnerType),
			strconv.FormatInt(intent.AmountMicroUnits, 10),
			intent.Currency,
			strconv.FormatInt(intent.ExpiresAt.Unix(), 10),
			returnURL.String(),
		}, "\n")
		mac := hmac.New(sha256.New, []byte(s.policy.CheckoutSigningKey))
		_, _ = mac.Write([]byte(signingPayload))

		query := checkoutURL.Query()
		query.Set("payment_intent_id", intent.ID.String())
		query.Set("owner_type", string(intent.OwnerType))
		query.Set("amount_micro_units", strconv.FormatInt(intent.AmountMicroUnits, 10))
		query.Set("currency", intent.Currency)
		query.Set("expires_at", strconv.FormatInt(intent.ExpiresAt.Unix(), 10))
		query.Set("return_url", returnURL.String())
		query.Set("signature", base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
		checkoutURL.RawQuery = query.Encode()
		intent.CheckoutURL = checkoutURL.String()
	}

	return intent, nil
}

// GetTopUp tra cứu chi tiết phiên thanh toán của tổ chức theo TenantID và IntentID.
func (s *tenantPaymentService) GetTopUp(
	ctx context.Context,
	tenantID uuid.UUID,
	intentID uuid.UUID,
) (*entity.PaymentIntent, error) {
	return s.repo.GetTenantIntent(ctx, tenantID, intentID)
}

// ApplyVerifiedSettlement thực thi quyết toán tiền vào ví tổ chức từ webhook thanh toán đã xác thực:
// 1. Gọi Repository thực thi Transaction ACID: cộng tiền nạp vào ví tổ chức, kích hoạt ví và phát Outbox.
// 2. Ghi nhận sự kiện hoạt động người dùng (User Activity Timeline) vào Redis với context độc lập (150ms timeout).
func (s *tenantPaymentService) ApplyVerifiedSettlement(
	ctx context.Context,
	settlement entity.PaymentSettlement,
) (*entity.SettlementResult, error) {
	result, err := s.repo.ApplyTenantSettlement(ctx, settlement)
	if err != nil || result.Replayed {
		return result, err
	}

	activityAction := "billing.wallet.top_up"
	activityTitle := "Tenant wallet top-up settled"
	if result.WalletActivated {
		activityAction = "billing.wallet.activate"
		activityTitle = "Tenant wallet activated"
	}

	// Ghi nhận sự kiện vào Timeline với context độc lập
	activityCtx, cancelActivity := context.WithTimeout(context.WithoutCancel(ctx), 150*time.Millisecond)
	defer cancelActivity()
	if activityErr := provisioner.AppendUserActivity(activityCtx, s.sharedRedis, provisioner.UserActivityEvent{
		EventID:      uuid.New().String(),
		UserID:       result.ActorID.String(),
		Category:     "billing",
		Action:       activityAction,
		ActorType:    "system",
		Outcome:      "succeeded",
		Source:       "cost-manager",
		ResourceType: "tenant_wallet",
		ResourceID:   result.WalletID.String(),
		OperationID:  settlement.ProviderEventID,
		Title:        activityTitle,
		Summary:      "A verified payment settlement credited the tenant wallet",
		OccurredAt:   settlement.SettledAt,
		Metadata: map[string]any{
			"payment_intent_id":  settlement.PaymentIntentID.String(),
			"amount_micro_units": strconv.FormatInt(settlement.Amount, 10),
			"currency":           settlement.Currency,
			"tenant_id":          result.OwnerID.String(),
			"owner_type":         string(result.OwnerType),
			"wallet_activated":   result.WalletActivated,
		},
	}); activityErr != nil {
		logger.SysError("billing.user_activity.tenant_wallet_settlement", activityErr.Error())
	}
	return result, nil
}
