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

// personalPaymentService là Domain Service điều phối các nghiệp vụ thanh toán cho tài khoản cá nhân:
// 1. Tra cứu số dư ví cá nhân (`GetWallet`).
// 2. Tạo phiên thanh toán nạp tiền và sinh URL Checkout có chữ ký HMAC (`CreateTopUp`).
// 3. Tra cứu chi tiết trạng thái phiên thanh toán (`GetTopUp`).
// 4. Quyết toán tiền vào ví và ghi nhận dòng sự kiện hoạt động người dùng (`ApplyVerifiedSettlement`).
type personalPaymentService struct {
	repo        billingRepoInterface.PersonalPaymentRepository // Repository quản lý các transaction và CTE thanh toán cá nhân trong PostgreSQL
	sharedRedis *goredis.Client                                // Redis Client để ghi log hoạt động người dùng (User Activity Timeline)
	policy      entity.PaymentPolicy                           // Chính sách thanh toán hệ thống (Provider, TTL, Khóa ký URL Checkout)
	checkoutURL url.URL                                        // URL cơ sở của trang thanh toán Checkout
	returnURL   url.URL                                        // URL chuyển hướng người dùng quay lại ứng dụng sau khi thanh toán
}

// NewPersonalPaymentService khởi tạo một instance mới của personalPaymentService, trả về interface PersonalPaymentService.
func NewPersonalPaymentService(
	repo billingRepoInterface.PersonalPaymentRepository,
	sharedRedis *goredis.Client,
	policy entity.PaymentPolicy,
	checkoutURL url.URL,
	returnURL url.URL,
) billingSvcInterface.PersonalPaymentService {
	return &personalPaymentService{
		repo:        repo,
		sharedRedis: sharedRedis,
		policy:      policy,
		checkoutURL: checkoutURL,
		returnURL:   returnURL,
	}
}

// GetWallet tra cứu thông tin tóm tắt ví tiền cá nhân từ Repository theo OwnerID (`user_id`).
func (s *personalPaymentService) GetWallet(
	ctx context.Context,
	ownerID uuid.UUID,
) (*entity.WalletSummary, error) {
	return s.repo.GetPersonalWalletSummary(ctx, ownerID)
}

// CreateTopUp khởi tạo phiên nạp tiền (Payment Intent) và sinh liên kết Checkout có chữ ký bảo mật:
// 1. Gắn các thông số mặc định của hệ thống: Đơn vị tiền tệ USD, Provider thanh toán, và thời hạn hiệu lực (`IntentTTL`).
// 2. Gọi Repository tạo bản ghi `billing.payment_intents` (kèm tự động liên kết mã Referral đang giữ chỗ).
// 3. Nếu Intent ở trạng thái `PENDING`, xây dựng Checkout URL có chữ ký HMAC-SHA256 để chống giả mạo số tiền nạp:
//    - Payload ký gồm: `aurora.checkout.v1\n{intent_id}\n{owner_type}\n{amount}\n{currency}\n{expires_at}\n{return_url}`.
//    - Ký bằng `CheckoutSigningKey` và đính kèm `signature` vào query parameters.
func (s *personalPaymentService) CreateTopUp(
	ctx context.Context,
	command entity.CreatePersonalPaymentIntentCommand,
) (*entity.PaymentIntent, error) {
	command.Currency = "USD"
	command.Provider = s.policy.Provider
	command.ExpiresAt = time.Now().UTC().Add(s.policy.IntentTTL)

	intent, err := s.repo.CreatePersonalIntent(ctx, command)
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

		// Định dạng chuỗi signing payload bảo đảm toàn vẹn dữ liệu
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

		// Đóng gói query parameters và signature vào Checkout URL
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

// GetTopUp tra cứu chi tiết phiên thanh toán của tài khoản cá nhân theo OwnerID và IntentID.
func (s *personalPaymentService) GetTopUp(
	ctx context.Context,
	ownerID uuid.UUID,
	intentID uuid.UUID,
) (*entity.PaymentIntent, error) {
	return s.repo.GetPersonalIntent(ctx, ownerID, intentID)
}

// ApplyVerifiedSettlement thực thi quyết toán tiền vào ví cá nhân từ webhook đã được xác thực:
// 1. Gọi Repository thực thi Transaction ACID: cộng tiền nạp, kích hoạt ví PENDING -> ACTIVE, cộng thưởng Referral, phát Outbox.
// 2. Ghi nhận sự kiện hoạt động người dùng (User Activity Timeline) vào Redis:
//    - Tách biệt context bằng `context.WithoutCancel(ctx)` với timeout ngắn 150ms.
//    - Lỗi ghi log hoạt động tuyệt đối KHÔNG được làm gián đoạn hoặc rollback giao dịch tiền đã commit thành công trong PostgreSQL.
func (s *personalPaymentService) ApplyVerifiedSettlement(
	ctx context.Context,
	settlement entity.PaymentSettlement,
) (*entity.SettlementResult, error) {
	result, err := s.repo.ApplyPersonalSettlement(ctx, settlement)
	if err != nil || result.Replayed {
		return result, err
	}

	activityAction := "billing.wallet.top_up"
	activityTitle := "Personal wallet top-up settled"
	if result.WalletActivated {
		activityAction = "billing.wallet.activate"
		activityTitle = "Personal wallet activated"
	}

	// Ghi nhận sự kiện vào Timeline với context độc lập (Fire-and-forget UX log)
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
		ResourceType: "personal_wallet",
		ResourceID:   result.WalletID.String(),
		OperationID:  settlement.ProviderEventID,
		Title:        activityTitle,
		Summary:      "A verified payment settlement credited the personal wallet",
		OccurredAt:   settlement.SettledAt,
		Metadata: map[string]any{
			"payment_intent_id":  settlement.PaymentIntentID.String(),
			"amount_micro_units": strconv.FormatInt(settlement.Amount, 10),
			"currency":           settlement.Currency,
			"owner_id":           result.OwnerID.String(),
			"owner_type":         string(result.OwnerType),
			"referral_applied":   result.ReferralApplied,
			"wallet_activated":   result.WalletActivated,
		},
	}); activityErr != nil {
		logger.SysError("billing.user_activity.personal_wallet_settlement", activityErr.Error())
	}
	return result, nil
}
