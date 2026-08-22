package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingTaxonomy "cost-manager/api/internal/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// personalAccountRepository chịu trách nhiệm thực thi các thao tác cơ sở dữ liệu PostgreSQL cho tài khoản cá nhân:
// - Khởi tạo ví tiền cá nhân nguyên tử kèm Transactional Inbox và Outbox Admission (`ApplyPersonalWalletProvision`).
// - Tổng hợp trạng thái onboarding của User cá nhân (`GetOnboarding`).
// - Quản lý quy trình giữ chỗ mã giới thiệu (`ReserveReferral`) với Transaction cấp độ Serializable và Advisory Lock.
// - Quản trị danh mục chiến dịch khuyến mãi (`ListReferralCampaigns`, `CreateReferralCampaign`, `UpdateReferralCampaignStatus`).
type personalAccountRepository struct {
	db *pgxpool.Pool
}

// NewPersonalAccountRepository khởi tạo một instance mới của personalAccountRepository, trả về interface PersonalAccountRepository.
func NewPersonalAccountRepository(db *pgxpool.Pool) billingRepoInterface.PersonalAccountRepository {
	return &personalAccountRepository{db: db}
}

// ApplyPersonalWalletProvision thực thi toàn bộ quy trình tạo ví cá nhân nguyên tử trong 1 câu lệnh CTE duy nhất:
// 1. `inbox_upsert`: Chèn bản ghi inbox với status='APPLIED'. Nếu đã tồn tại `event_id` thì bỏ qua (ON CONFLICT DO NOTHING).
// 2. `inbox_replay`: Nếu `inbox_upsert` không chèn mới (do trùng event_id), đọc lại bản ghi inbox đã lưu để kiểm tra payload hash.
// 3. `effective_inbox`: Hợp nhất kết quả từ 2 nhánh trên.
// 4. `wallet_upsert`: Nếu inbox là mới (`is_new = true`), chèn ví cá nhân mới (`billing.wallets`) trạng thái `PENDING_ACTIVATION`, `$0.00 USD`.
// 5. `existing_wallet`: Nếu ví đã tồn tại trước đó, lấy `id` của ví hiện tại.
// 6. `admission_outbox_insert`: Ghi nhận tín hiệu `SUSPEND_BILLABLE` vào `billing.wallet_admission_outbox` nếu ví vừa được tạo mới.
//
// Kết quả trả về cho phép Go kiểm tra tức thì: nếu là Replay thì xác minh tính toàn vẹn của payload hash; nếu là New thì hoàn tất thành công.
func (r *personalAccountRepository) ApplyPersonalWalletProvision(
	ctx context.Context,
	eventID uuid.UUID,
	ownerID uuid.UUID,
	payloadHash string,
) error {
	const query = `
		WITH inbox_upsert AS (
			INSERT INTO billing.personal_wallet_provision_inbox (
				event_id, schema_version, user_id, payload_hash, status, processed_at
			)
			VALUES ($1, 1, $2, $3, 'APPLIED', NOW())
			ON CONFLICT (event_id) DO NOTHING
			RETURNING event_id, user_id, payload_hash, TRUE AS is_new
		),
		inbox_replay AS (
			SELECT event_id, user_id, payload_hash, FALSE AS is_new
			FROM billing.personal_wallet_provision_inbox
			WHERE event_id = $1
			  AND NOT EXISTS (SELECT 1 FROM inbox_upsert)
		),
		effective_inbox AS (
			SELECT * FROM inbox_upsert
			UNION ALL
			SELECT * FROM inbox_replay
		),
		wallet_upsert AS (
			INSERT INTO billing.wallets (
				id, owner_id, owner_type, currency, cash_balance, promotional_balance,
				status, restriction_reason, status_changed_at
			)
			SELECT
				$4, $2, 'PERSONAL'::billing.owner_type, 'USD', 0, 0,
				'PENDING_ACTIVATION', 'NOT_ACTIVATED', NOW()
			FROM inbox_upsert
			ON CONFLICT (owner_id, owner_type, currency) DO NOTHING
			RETURNING id, TRUE AS wallet_created
		),
		existing_wallet AS (
			SELECT id, FALSE AS wallet_created
			FROM billing.wallets
			WHERE owner_id = $2
			  AND owner_type = 'PERSONAL'::billing.owner_type
			  AND currency = 'USD'
			  AND EXISTS (SELECT 1 FROM inbox_upsert)
			  AND NOT EXISTS (SELECT 1 FROM wallet_upsert)
		),
		effective_wallet AS (
			SELECT * FROM wallet_upsert
			UNION ALL
			SELECT * FROM existing_wallet
		),
		admission_outbox_insert AS (
			INSERT INTO billing.wallet_admission_outbox (
				event_id, wallet_id, owner_id, owner_type, wallet_version,
				admission_mode, restriction_reason, effective_at
			)
			SELECT
				$5, ew.id, $2, 'PERSONAL', 1,
				'SUSPEND_BILLABLE', 'NOT_ACTIVATED', NOW()
			FROM effective_wallet ew
			WHERE ew.wallet_created = TRUE
			ON CONFLICT (event_id) DO NOTHING
			RETURNING event_id
		)
		SELECT
			ei.is_new,
			ei.user_id,
			ei.payload_hash
		FROM effective_inbox ei;
	`

	var isNew bool
	var storedUserID uuid.UUID
	var storedHash string

	err := r.db.QueryRow(
		ctx,
		query,
		eventID,
		ownerID,
		payloadHash,
		uuid.New(), // $4: walletID
		uuid.New(), // $5: outboxEventID
	).Scan(&isNew, &storedUserID, &storedHash)
	if err != nil {
		return fmt.Errorf("personal account repo: apply wallet provision CTE: %w", err)
	}

	if !isNew {
		if storedUserID != ownerID || storedHash != payloadHash {
			return fmt.Errorf("personal account repo: event_id %s reused with different payload", eventID)
		}
	}
	return nil
}

// getPersonalWalletSummary đọc thông tin tóm tắt ví tiền cá nhân (số dư tiền mặt, khuyến mãi, hạn mức, phiên bản).
func (r *personalAccountRepository) getPersonalWalletSummary(
	ctx context.Context,
	ownerID uuid.UUID,
) (*entity.WalletSummary, error) {
	var summary entity.WalletSummary
	err := r.db.QueryRow(ctx, `
		SELECT id, currency, cash_balance, promotional_balance, overdraft_limit,
		       status, version, updated_at
		FROM billing.wallets
		WHERE owner_id = $1
		  AND owner_type = 'PERSONAL'::billing.owner_type
		  AND currency = 'USD'
	`, ownerID).Scan(
		&summary.WalletID,
		&summary.Currency,
		&summary.CashBalanceMicroUnits,
		&summary.PromotionalBalanceMicroUnits,
		&summary.OverdraftLimitMicroUnits,
		&summary.Status,
		&summary.Version,
		&summary.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrWalletNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("personal account repo: read wallet: %w", err)
	}
	return &summary, nil
}

// GetOnboarding tổng hợp toàn bộ bức tranh thanh toán cho tài khoản cá nhân:
// 1. Đọc thông tin ví tiền cá nhân từ `billing.wallets`.
// 2. Đọc mã giới thiệu đang giữ chỗ gần nhất từ `billing.personal_referral_reservations` (tự động chuyển CANCELLED nếu hết hạn).
// 3. Đọc phiên thanh toán gần nhất từ `billing.payment_intents` (tự động chuyển EXPIRED nếu quá thời hạn).
func (r *personalAccountRepository) GetOnboarding(
	ctx context.Context,
	ownerID uuid.UUID,
	minimumTopUp int64,
) (*entity.OnboardingSnapshot, error) {
	wallet, err := r.getPersonalWalletSummary(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	snapshot := &entity.OnboardingSnapshot{
		Wallet:       *wallet,
		MinimumTopUp: minimumTopUp,
	}

	// Đọc thông tin mã giới thiệu gần nhất
	var reservation entity.ReferralReservation
	var redeemedAt *time.Time
	err = r.db.QueryRow(ctx, `
		SELECT id, campaign_id, code_snapshot,
		       CASE WHEN status='RESERVED' AND expires_at <= NOW() THEN 'CANCELLED' ELSE status END,
		       grant_amount_micro_units,
		       minimum_top_up_micro_units, currency, expires_at, grant_expires_at, redeemed_at,
		       COALESCE(rejection_reason,
		                CASE WHEN status='RESERVED' AND expires_at <= NOW() THEN 'RESERVATION_EXPIRED' END,
		                '')
		FROM billing.personal_referral_reservations
		WHERE user_id=$1
		  AND redemption_kind='ONBOARDING'
		ORDER BY created_at DESC
		LIMIT 1
	`, ownerID).Scan(
		&reservation.ID,
		&reservation.CampaignID,
		&reservation.Code,
		&reservation.Status,
		&reservation.GrantAmountMicroUnits,
		&reservation.MinimumTopUpMicroUnits,
		&reservation.Currency,
		&reservation.ExpiresAt,
		&reservation.GrantExpiresAt,
		&redeemedAt,
		&reservation.RejectionReason,
	)
	if err == nil {
		reservation.RedeemedAt = redeemedAt
		snapshot.Referral = &reservation
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("personal account repo: read referral reservation: %w", err)
	}

	// Đọc phiên nạp tiền thanh toán gần nhất
	var intent entity.PaymentIntent
	var referralID *uuid.UUID
	var settledAt *time.Time
	err = r.db.QueryRow(ctx, `
		SELECT id, wallet_id, amount_micro_units, currency, provider,
		       COALESCE(provider_payment_id, ''),
		       CASE WHEN status='PENDING' AND expires_at <= NOW() THEN 'EXPIRED' ELSE status END,
		       activates_wallet,
		       personal_referral_reservation_id, expires_at, settled_at, created_at
		FROM billing.payment_intents
		WHERE owner_id=$1 AND owner_type='PERSONAL'::billing.owner_type
		ORDER BY created_at DESC
		LIMIT 1
	`, ownerID).Scan(
		&intent.ID,
		&intent.WalletID,
		&intent.AmountMicroUnits,
		&intent.Currency,
		&intent.Provider,
		&intent.ProviderPaymentID,
		&intent.Status,
		&intent.ActivatesWallet,
		&referralID,
		&intent.ExpiresAt,
		&settledAt,
		&intent.CreatedAt,
	)
	if err == nil {
		intent.OwnerID = ownerID
		intent.ReferralReservationID = referralID
		intent.SettledAt = settledAt
		snapshot.LatestPaymentIntent = &intent
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("personal account repo: read latest payment intent: %w", err)
	}
	return snapshot, nil
}

// ReserveReferral thực hiện giữ chỗ mã khuyến mãi/giới thiệu trong 1 câu lệnh CTE duy nhất:
// 1. `wallet_target`: Kiểm tra ví cá nhân phải ở trạng thái `PENDING_ACTIVATION`.
// 2. `prior_redemption`: Kiểm tra xem người dùng đã từng quy đổi mã giới thiệu trước đây chưa.
// 3. `expire_stale`: Tự động hủy các reservation cũ đã hết hạn (`status = 'CANCELLED'`).
// 4. `existing_reservation`: Kiểm tra reservation đang hoạt động để xử lý Idempotent Replay.
// 5. `campaign_target`: Xác thực chiến dịch khuyến mãi ACTIVE và thời hạn hiệu lực.
// 6. `capacity_check`: Kiểm tra giới hạn số lượt nạp (capacity) của chiến dịch.
// 7. `new_reservation`: Chèn bản ghi giữ chỗ mới vào `billing.personal_referral_reservations`.
func (r *personalAccountRepository) ReserveReferral(
	ctx context.Context,
	command entity.ReserveReferralCommand,
) (*entity.ReferralReservation, error) {
	const query = `
		WITH wallet_target AS (
			SELECT id, status
			FROM billing.wallets
			WHERE owner_id = $1
			  AND owner_type = 'PERSONAL'::billing.owner_type
			  AND currency = 'USD'
		),
		prior_redemption AS (
			SELECT EXISTS(
				SELECT 1
				FROM billing.personal_referral_redemptions
				WHERE user_id = $1 AND redemption_kind = 'ONBOARDING'
			) AS redeemed
		),
		expire_stale AS (
			UPDATE billing.personal_referral_reservations
			SET status = 'CANCELLED', rejection_reason = 'RESERVATION_EXPIRED', updated_at = NOW()
			WHERE user_id = $1
			  AND redemption_kind = 'ONBOARDING'
			  AND status = 'RESERVED'
			  AND expires_at <= NOW()
			RETURNING id
		),
		existing_reservation AS (
			SELECT id, campaign_id, code_snapshot, status, grant_amount_micro_units,
			       minimum_top_up_micro_units, currency, expires_at, grant_expires_at, redeemed_at,
			       COALESCE(rejection_reason, '') AS rejection_reason, idempotency_key
			FROM billing.personal_referral_reservations
			WHERE user_id = $1
			  AND redemption_kind = 'ONBOARDING'
			  AND status = 'RESERVED'
			  AND expires_at > NOW()
		),
		campaign_target AS (
			SELECT id, code, amount_micro_units, minimum_top_up_micro_units,
			       currency, max_redemptions, version, starts_at, ends_at
			FROM billing.promotion_campaigns
			WHERE code = $2
			  AND campaign_type = 'ONBOARDING_REFERRAL'
			  AND status = 'ACTIVE'
			  AND starts_at <= NOW()
			  AND (ends_at IS NULL OR NOW() < ends_at)
		),
		capacity_check AS (
			SELECT
				(SELECT COUNT(*) FROM billing.personal_referral_redemptions WHERE campaign_id = c.id)
				+
				(SELECT COUNT(*) FROM billing.personal_referral_reservations
				 WHERE campaign_id = c.id AND status = 'RESERVED' AND expires_at > NOW()) AS occupied
			FROM campaign_target c
		),
		new_reservation AS (
			INSERT INTO billing.personal_referral_reservations (
				id, campaign_id, wallet_id, user_id, redemption_kind, status,
				campaign_version, code_snapshot, grant_amount_micro_units,
				minimum_top_up_micro_units, currency, grant_expires_at,
				idempotency_key, expires_at
			)
			SELECT
				$3,
				c.id,
				w.id,
				$1,
				'ONBOARDING',
				'RESERVED',
				c.version,
				c.code,
				c.amount_micro_units,
				c.minimum_top_up_micro_units,
				c.currency,
				c.ends_at,
				$4,
				CASE
					WHEN c.ends_at IS NOT NULL AND c.ends_at < $5 THEN c.ends_at
					ELSE $5
				END
			FROM wallet_target w
			CROSS JOIN prior_redemption pr
			CROSS JOIN campaign_target c
			LEFT JOIN capacity_check cap ON TRUE
			WHERE w.status = 'PENDING_ACTIVATION'
			  AND NOT pr.redeemed
			  AND NOT EXISTS (SELECT 1 FROM existing_reservation)
			  AND (c.max_redemptions IS NULL OR cap.occupied < c.max_redemptions)
			RETURNING id, campaign_id, code_snapshot, status, grant_amount_micro_units,
			          minimum_top_up_micro_units, currency, expires_at, grant_expires_at,
			          TRUE AS is_created
		)
		SELECT
			COALESCE(w.status, 'NOT_FOUND') AS wallet_status,
			COALESCE(pr.redeemed, FALSE) AS already_redeemed,
			ex.id AS ex_id,
			ex.campaign_id AS ex_campaign_id,
			ex.code_snapshot AS ex_code,
			ex.status AS ex_status,
			ex.grant_amount_micro_units AS ex_grant_amount,
			ex.minimum_top_up_micro_units AS ex_min_top_up,
			ex.currency AS ex_currency,
			ex.expires_at AS ex_expires_at,
			ex.grant_expires_at AS ex_grant_expires_at,
			ex.redeemed_at AS ex_redeemed_at,
			ex.rejection_reason AS ex_rejection_reason,
			ex.idempotency_key AS ex_idempotency_key,
			c.id AS campaign_id,
			(c.max_redemptions IS NOT NULL AND cap.occupied >= c.max_redemptions) AS capacity_exceeded,
			nr.id AS nr_id,
			nr.campaign_id AS nr_campaign_id,
			nr.code_snapshot AS nr_code,
			nr.status AS nr_status,
			nr.grant_amount_micro_units AS nr_grant_amount,
			nr.minimum_top_up_micro_units AS nr_min_top_up,
			nr.currency AS nr_currency,
			nr.expires_at AS nr_expires_at,
			nr.grant_expires_at AS nr_grant_expires_at,
			COALESCE(nr.is_created, FALSE) AS is_created
		FROM (SELECT 1) _
		LEFT JOIN wallet_target w ON TRUE
		LEFT JOIN prior_redemption pr ON TRUE
		LEFT JOIN existing_reservation ex ON TRUE
		LEFT JOIN campaign_target c ON TRUE
		LEFT JOIN capacity_check cap ON TRUE
		LEFT JOIN new_reservation nr ON TRUE;
	`

	var walletStatus string
	var alreadyRedeemed bool
	// Existing Reservation
	var exID, exCampaignID *uuid.UUID
	var exCode, exStatus, exCurrency, exRejectionReason, exIdempotencyKey *string
	var exGrantAmount, exMinTopUp *int64
	var exExpiresAt, exGrantExpiresAt, exRedeemedAt *time.Time
	// Campaign validation
	var campaignID *uuid.UUID
	var capacityExceeded *bool
	// New Reservation
	var nrID, nrCampaignID *uuid.UUID
	var nrCode, nrStatus, nrCurrency *string
	var nrGrantAmount, nrMinTopUp *int64
	var nrExpiresAt, nrGrantExpiresAt *time.Time
	var isCreated bool

	newReservationID := uuid.New()
	err := r.db.QueryRow(
		ctx,
		query,
		command.OwnerID,        // $1
		command.Code,           // $2
		newReservationID,       // $3
		command.IdempotencyKey, // $4
		command.ExpiresAt,      // $5
	).Scan(
		&walletStatus,
		&alreadyRedeemed,
		&exID,
		&exCampaignID,
		&exCode,
		&exStatus,
		&exGrantAmount,
		&exMinTopUp,
		&exCurrency,
		&exExpiresAt,
		&exGrantExpiresAt,
		&exRedeemedAt,
		&exRejectionReason,
		&exIdempotencyKey,
		&campaignID,
		&capacityExceeded,
		&nrID,
		&nrCampaignID,
		&nrCode,
		&nrStatus,
		&nrGrantAmount,
		&nrMinTopUp,
		&nrCurrency,
		&nrExpiresAt,
		&nrGrantExpiresAt,
		&isCreated,
	)
	if err != nil {
		return nil, fmt.Errorf("personal account repo: reserve referral CTE: %w", err)
	}

	// 1. Kiểm tra trạng thái ví
	if walletStatus == "NOT_FOUND" {
		return nil, billingTaxonomy.ErrWalletNotFound
	}
	if walletStatus != entity.WalletStatusPendingActivation {
		return nil, billingTaxonomy.ErrWalletAlreadyActive
	}

	// 2. Kiểm tra lịch sử quy đổi
	if alreadyRedeemed {
		return nil, billingTaxonomy.ErrReferralAlreadyRedeemed
	}

	// 3. Xử lý Idempotent Replay
	if exID != nil {
		if exIdempotencyKey != nil && *exIdempotencyKey == command.IdempotencyKey &&
			exCode != nil && *exCode == command.Code {
			return &entity.ReferralReservation{
				ID:                     *exID,
				CampaignID:             *exCampaignID,
				Code:                   *exCode,
				Status:                 *exStatus,
				GrantAmountMicroUnits:  *exGrantAmount,
				MinimumTopUpMicroUnits: *exMinTopUp,
				Currency:               *exCurrency,
				ExpiresAt:              *exExpiresAt,
				GrantExpiresAt:         exGrantExpiresAt,
				RedeemedAt:             exRedeemedAt,
				RejectionReason:        *exRejectionReason,
			}, nil
		}
		return nil, billingTaxonomy.ErrReferralAlreadyReserved
	}

	// 4. Kiểm tra sự tồn tại của chiến dịch
	if campaignID == nil {
		return nil, billingTaxonomy.ErrReferralNotFound
	}

	// 5. Kiểm tra giới hạn số lượt
	if capacityExceeded != nil && *capacityExceeded {
		return nil, billingTaxonomy.ErrReferralUnavailable
	}

	// 6. Trả về Reservation mới
	if isCreated && nrID != nil {
		return &entity.ReferralReservation{
			ID:                     *nrID,
			CampaignID:             *nrCampaignID,
			Code:                   *nrCode,
			Status:                 *nrStatus,
			GrantAmountMicroUnits:  *nrGrantAmount,
			MinimumTopUpMicroUnits: *nrMinTopUp,
			Currency:               *nrCurrency,
			ExpiresAt:              *nrExpiresAt,
			GrantExpiresAt:         nrGrantExpiresAt,
		}, nil
	}

	return nil, fmt.Errorf("personal account repo: failed to reserve referral")
}

// ListReferralCampaigns truy vấn danh sách toàn bộ các chiến dịch khuyến mãi kèm số lượng quy đổi và giữ chỗ thời gian thực.
func (r *personalAccountRepository) ListReferralCampaigns(
	ctx context.Context,
) ([]entity.ReferralCampaign, error) {
	rows, err := r.db.Query(ctx, `
		SELECT c.id, c.code, c.name, c.amount_micro_units,
		       c.minimum_top_up_micro_units, c.currency, c.status,
		       c.max_redemptions, c.version, c.starts_at, c.ends_at,
		       c.created_at, c.updated_at,
		       (SELECT COUNT(*) FROM billing.personal_referral_redemptions rd WHERE rd.campaign_id=c.id),
		       (SELECT COUNT(*) FROM billing.personal_referral_reservations rr
		        WHERE rr.campaign_id=c.id AND rr.status='RESERVED' AND rr.expires_at > NOW())
		FROM billing.promotion_campaigns c
		WHERE c.campaign_type='ONBOARDING_REFERRAL'
		ORDER BY c.created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("personal account repo: list referral campaigns: %w", err)
	}
	defer rows.Close()

	campaigns := make([]entity.ReferralCampaign, 0)
	for rows.Next() {
		var campaign entity.ReferralCampaign
		if err := rows.Scan(
			&campaign.ID,
			&campaign.Code,
			&campaign.Name,
			&campaign.AmountMicroUnits,
			&campaign.MinimumTopUpMicroUnits,
			&campaign.Currency,
			&campaign.Status,
			&campaign.MaxRedemptions,
			&campaign.Version,
			&campaign.StartsAt,
			&campaign.EndsAt,
			&campaign.CreatedAt,
			&campaign.UpdatedAt,
			&campaign.Redemptions,
			&campaign.ActiveReservations,
		); err != nil {
			return nil, fmt.Errorf("personal account repo: scan referral campaign: %w", err)
		}
		campaigns = append(campaigns, campaign)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("personal account repo: iterate referral campaigns: %w", err)
	}
	return campaigns, nil
}

// CreateReferralCampaign khởi tạo một chiến dịch khuyến mãi mới (trạng thái ban đầu: PAUSED) với mã code duy nhất.
func (r *personalAccountRepository) CreateReferralCampaign(
	ctx context.Context,
	command entity.CreateReferralCampaignCommand,
) (*entity.ReferralCampaign, error) {
	campaign := &entity.ReferralCampaign{
		ID:                     uuid.New(),
		Code:                   command.Code,
		Name:                   command.Name,
		AmountMicroUnits:       command.AmountMicroUnits,
		MinimumTopUpMicroUnits: command.MinimumTopUpMicroUnits,
		Currency:               command.Currency,
		Status:                 "PAUSED",
		MaxRedemptions:         command.MaxRedemptions,
		Version:                1,
		StartsAt:               command.StartsAt,
		EndsAt:                 command.EndsAt,
	}
	err := r.db.QueryRow(ctx, `
		INSERT INTO billing.promotion_campaigns
			(id, code, name, amount_micro_units, currency, starts_at, ends_at,
			 status, campaign_type, minimum_top_up_micro_units, max_redemptions)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'PAUSED',
		        'ONBOARDING_REFERRAL', $8, $9)
		RETURNING created_at, updated_at
	`, campaign.ID, campaign.Code, campaign.Name, campaign.AmountMicroUnits,
		campaign.Currency, campaign.StartsAt, campaign.EndsAt,
		campaign.MinimumTopUpMicroUnits, campaign.MaxRedemptions,
	).Scan(&campaign.CreatedAt, &campaign.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, billingTaxonomy.ErrConflict
		}
		return nil, fmt.Errorf("personal account repo: create referral campaign: %w", err)
	}
	return campaign, nil
}

// UpdateReferralCampaignStatus cập nhật trạng thái chiến dịch kèm Optimistic Concurrency Control (Fencing qua version).
func (r *personalAccountRepository) UpdateReferralCampaignStatus(
	ctx context.Context,
	command entity.UpdateReferralCampaignStatusCommand,
) (*entity.ReferralCampaign, error) {
	var campaign entity.ReferralCampaign
	err := r.db.QueryRow(ctx, `
		UPDATE billing.promotion_campaigns
		SET status=$1, version=version+1, updated_at=NOW()
		WHERE id=$2 AND campaign_type='ONBOARDING_REFERRAL' AND version=$3
		RETURNING id, code, name, amount_micro_units, minimum_top_up_micro_units,
		          currency, status, max_redemptions, version, starts_at, ends_at,
		          created_at, updated_at
	`, command.Status, command.ID, command.ExpectedVersion).Scan(
		&campaign.ID,
		&campaign.Code,
		&campaign.Name,
		&campaign.AmountMicroUnits,
		&campaign.MinimumTopUpMicroUnits,
		&campaign.Currency,
		&campaign.Status,
		&campaign.MaxRedemptions,
		&campaign.Version,
		&campaign.StartsAt,
		&campaign.EndsAt,
		&campaign.CreatedAt,
		&campaign.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if checkErr := r.db.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM billing.promotion_campaigns WHERE id=$1 AND campaign_type='ONBOARDING_REFERRAL')`,
			command.ID,
		).Scan(&exists); checkErr != nil {
			return nil, fmt.Errorf("personal account repo: check referral campaign conflict: %w", checkErr)
		}
		if exists {
			return nil, billingTaxonomy.ErrConflict
		}
		return nil, billingTaxonomy.ErrReferralNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("personal account repo: update referral campaign: %w", err)
	}
	return &campaign, nil
}
