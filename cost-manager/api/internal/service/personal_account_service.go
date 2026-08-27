package service

import (
	"context"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingSvcInterface "cost-manager/api/internal/domain/service"

	"github.com/google/uuid"
)

// personalAccountService là Domain Service điều phối các nghiệp vụ tài khoản cá nhân và chiến dịch giới thiệu:
// 1. Tiếp nhận và xử lý sự kiện khởi tạo ví cá nhân (`ProvisionPersonalWallet`) từ Redis Stream Consumer.
// 2. Tra cứu trạng thái onboarding tài khoản thanh toán (`GetOnboarding`) kèm chính sách nạp tiền tối thiểu.
// 3. Quản lý việc giữ chỗ mã khuyến mãi/giới thiệu (`ReserveReferral`) kèm áp dụng thời hạn hiệu lực (Referral TTL).
// 4. Quản lý vòng đời các chiến dịch giới thiệu (Referral Campaigns) ở cấp độ nền tảng (Platform Scope).
type personalAccountService struct {
	repo   billingRepoInterface.PersonalAccountRepository // Repository thực thi các transaction và CTEs nguyên tử trong PostgreSQL
	policy entity.PaymentPolicy                           // Chính sách thanh toán hệ thống (Hạn mức nạp tối thiểu, thời gian hết hạn mã giới thiệu)
}

// NewPersonalAccountService khởi tạo một instance mới của personalAccountService với Repository và PaymentPolicy, trả về interface PersonalAccountService.
func NewPersonalAccountService(
	repo billingRepoInterface.PersonalAccountRepository,
	policy entity.PaymentPolicy,
) billingSvcInterface.PersonalAccountService {
	return &personalAccountService{
		repo:   repo,
		policy: policy,
	}
}

// ProvisionPersonalWallet thực thi khởi tạo ví cá nhân từ sự kiện vòng đời IAM (`PersonalAccountActivatedV1`):
// - Ghi nhận Transactional Inbox để chống trùng lặp sự kiện (Deduplication).
// - Khởi tạo ví cá nhân `billing.wallets` với số dư $0.00 USD, trạng thái `PENDING_ACTIVATION`.
// - Ghi nhận Outbox `billing.wallet_admission_outbox` với chế độ `SUSPEND_BILLABLE` để chặn dùng dịch vụ tính phí trước khi nạp tiền.
func (s *personalAccountService) ProvisionPersonalWallet(
	ctx context.Context,
	eventID uuid.UUID,
	ownerID uuid.UUID,
	payloadHash string,
) error {
	return s.repo.ApplyPersonalWalletProvision(ctx, eventID, ownerID, payloadHash)
}

// GetOnboarding tổng hợp toàn bộ bức tranh trạng thái thanh toán ban đầu cho tài khoản cá nhân:
// - Thông tin ví hiện tại (`billing.wallets`).
// - Chính sách nạp tiền tối thiểu (`MinimumTopUp`) được tiêm từ PaymentPolicy.
// - Mã giới thiệu đang giữ chỗ gần nhất (`billing.personal_referral_reservations`).
// - Phiên thanh toán nạp tiền gần nhất (`billing.payment_intents`).
func (s *personalAccountService) GetOnboarding(
	ctx context.Context,
	ownerID uuid.UUID,
) (*entity.OnboardingSnapshot, error) {
	return s.repo.GetOnboarding(ctx, ownerID, s.policy.MinimumTopUp)
}

// ReserveReferral thực hiện giữ chỗ mã giới thiệu/khuyến mãi cho tài khoản cá nhân mới:
// - Tự động tính toán mốc thời gian hết hạn (`ExpiresAt = NOW() + ReferralTTL`) dựa trên cấu hình hệ thống.
// - Chuyển tiếp tới Repository để thực thi trong một Serializable Transaction với Advisory Lock chống Race Condition.
func (s *personalAccountService) ReserveReferral(
	ctx context.Context,
	command entity.ReserveReferralCommand,
) (*entity.ReferralReservation, error) {
	command.ExpiresAt = time.Now().UTC().Add(s.policy.ReferralTTL)
	return s.repo.ReserveReferral(ctx, command)
}

// ListReferralCampaigns liệt kê danh sách toàn bộ các chiến dịch khuyến mãi dành cho Quản trị viên nền tảng.
func (s *personalAccountService) ListReferralCampaigns(
	ctx context.Context,
) ([]entity.ReferralCampaign, error) {
	return s.repo.ListReferralCampaigns(ctx)
}

// CreateReferralCampaign khởi tạo một chiến dịch khuyến mãi mới (mặc định ở trạng thái PAUSED) trong cơ sở dữ liệu.
func (s *personalAccountService) CreateReferralCampaign(
	ctx context.Context,
	command entity.CreateReferralCampaignCommand,
) (*entity.ReferralCampaign, error) {
	return s.repo.CreateReferralCampaign(ctx, command)
}

// UpdateReferralCampaignStatus cập nhật trạng thái chiến dịch (ACTIVE, PAUSED, ENDED) kèm kiểm tra phiên bản (Optimistic Locking).
func (s *personalAccountService) UpdateReferralCampaignStatus(
	ctx context.Context,
	command entity.UpdateReferralCampaignStatusCommand,
) (*entity.ReferralCampaign, error) {
	return s.repo.UpdateReferralCampaignStatus(ctx, command)
}
