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

// personalPaymentRepository chịu trách nhiệm thực thi các thao tác cơ sở dữ liệu PostgreSQL cho nghiệp vụ thanh toán cá nhân:
// - Tra cứu số dư ví cá nhân (`GetPersonalWalletSummary`).
// - Khởi tạo phiên nạp tiền cá nhân kèm liên kết mã Referral giữ chỗ (`CreatePersonalIntent`).
// - Tra cứu chi tiết Payment Intent theo ID (`GetPersonalIntent`).
// - Quyết toán thanh toán webhook nguyên tử với sổ cái kiểm toán bất biến (`ApplyPersonalSettlement`).
type personalPaymentRepository struct {
	db *pgxpool.Pool
}

// NewPersonalPaymentRepository khởi tạo một instance mới của personalPaymentRepository, trả về interface PersonalPaymentRepository.
func NewPersonalPaymentRepository(db *pgxpool.Pool) billingRepoInterface.PersonalPaymentRepository {
	return &personalPaymentRepository{db: db}
}

// GetPersonalWalletSummary đọc thông tin tóm tắt số dư ví tiền cá nhân (tiền mặt, khuyến mãi, hạn mức thấu chi, phiên bản).
func (r *personalPaymentRepository) GetPersonalWalletSummary(
	ctx context.Context,
	ownerID uuid.UUID,
) (*entity.WalletSummary, error) {
	var summary entity.WalletSummary
	err := r.db.QueryRow(ctx, `
		SELECT id, currency, cash_balance, promotional_balance, overdraft_limit,
		       status, version, updated_at
		FROM billing.wallets
		WHERE owner_id=$1 AND owner_type='PERSONAL'::billing.owner_type AND currency='USD'
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
		return nil, fmt.Errorf("personal payment repo: read wallet: %w", err)
	}
	return &summary, nil
}

// CreatePersonalIntent thực thi toàn bộ quy trình tạo Payment Intent cá nhân trong 1 câu lệnh CTE duy nhất:
// 1. `wallet_target`: Xác định ví cá nhân và kiểm tra trạng thái (`PENDING_ACTIVATION` hoặc `ACTIVE`).
// 2. `existing_intent`: Kiểm tra Idempotent Replay theo `idempotency_key`.
// 3. `expire_stale`: Tự động hủy các Intent cũ quá hạn chưa thanh toán (`status = 'EXPIRED'`).
// 4. `referral_target`: Đọc mã giới thiệu Referral đang giữ chỗ để kiểm tra hạn mức nạp tối thiểu.
// 5. `new_intent`: Chèn Payment Intent mới vào `billing.payment_intents` và tự động liên kết mã Referral (nếu đủ điều kiện).
//
// Kết quả trả về cho phép Go kiểm tra tức thì: Replay Idempotency, Precondition Failed (không đủ min top-up referral), hoặc tạo mới thành công.
func (r *personalPaymentRepository) CreatePersonalIntent(
	ctx context.Context,
	command entity.CreatePersonalPaymentIntentCommand,
) (*entity.PaymentIntent, error) {
	const query = `
		WITH wallet_target AS (
			SELECT id, status
			FROM billing.wallets
			WHERE owner_id = $1
			  AND owner_type = 'PERSONAL'::billing.owner_type
			  AND currency = $2
		),
		existing_intent AS (
			SELECT id, wallet_id, actor_user_id, amount_micro_units, currency, provider,
			       COALESCE(provider_payment_id, '') AS provider_payment_id,
			       CASE WHEN status = 'PENDING' AND expires_at <= NOW() THEN 'EXPIRED' ELSE status END AS status,
			       activates_wallet, personal_referral_reservation_id, expires_at, settled_at, created_at
			FROM billing.payment_intents
			WHERE owner_id = $1
			  AND owner_type = 'PERSONAL'::billing.owner_type
			  AND idempotency_key = $3
		),
		expire_stale AS (
			UPDATE billing.payment_intents
			SET status = 'EXPIRED', updated_at = NOW()
			WHERE owner_id = $1
			  AND owner_type = 'PERSONAL'::billing.owner_type
			  AND status = 'PENDING'
			  AND expires_at <= NOW()
			  AND NOT EXISTS (SELECT 1 FROM existing_intent)
			RETURNING id
		),
		referral_target AS (
			SELECT id, minimum_top_up_micro_units, currency
			FROM billing.personal_referral_reservations
			WHERE user_id = $1
			  AND redemption_kind = 'ONBOARDING'
			  AND status = 'RESERVED'
			  AND expires_at > NOW()
			ORDER BY created_at DESC
			LIMIT 1
		),
		new_intent AS (
			INSERT INTO billing.payment_intents (
				id, wallet_id, owner_id, owner_type, actor_user_id,
				amount_micro_units, currency, provider, status,
				activates_wallet, personal_referral_reservation_id,
				idempotency_key, expires_at
			)
			SELECT
				$4,
				w.id,
				$1,
				'PERSONAL'::billing.owner_type,
				$1,
				$5,
				$2,
				$6,
				'PENDING',
				w.status = 'PENDING_ACTIVATION',
				CASE
					WHEN w.status = 'PENDING_ACTIVATION'
					     AND ref.id IS NOT NULL
					     AND $5 >= ref.minimum_top_up_micro_units
					     AND $2 = ref.currency
					THEN ref.id
					ELSE NULL
				END,
				$3,
				$7
			FROM wallet_target w
			LEFT JOIN referral_target ref ON TRUE
			WHERE (w.status = 'PENDING_ACTIVATION' OR w.status = 'ACTIVE')
			  AND NOT EXISTS (SELECT 1 FROM existing_intent)
			  AND (
			      w.status <> 'PENDING_ACTIVATION'
			      OR ref.id IS NULL
			      OR ($5 >= ref.minimum_top_up_micro_units AND $2 = ref.currency)
			  )
			RETURNING id, wallet_id, owner_id, owner_type, actor_user_id,
			          amount_micro_units, currency, provider, status,
			          activates_wallet, personal_referral_reservation_id,
			          expires_at, created_at, TRUE AS is_created
		)
		SELECT
			COALESCE(w.status, 'NOT_FOUND') AS wallet_status,
			ex.id AS ex_id,
			ex.wallet_id AS ex_wallet_id,
			ex.actor_user_id AS ex_actor_id,
			ex.amount_micro_units AS ex_amount,
			ex.currency AS ex_currency,
			ex.provider AS ex_provider,
			ex.provider_payment_id AS ex_provider_payment_id,
			ex.status AS ex_status,
			ex.activates_wallet AS ex_activates_wallet,
			ex.personal_referral_reservation_id AS ex_referral_id,
			ex.expires_at AS ex_expires_at,
			ex.settled_at AS ex_settled_at,
			ex.created_at AS ex_created_at,
			(w.status = 'PENDING_ACTIVATION' AND ref.id IS NOT NULL AND ($5 < ref.minimum_top_up_micro_units OR $2 <> ref.currency)) AS referral_condition_failed,
			ni.id AS ni_id,
			ni.wallet_id AS ni_wallet_id,
			ni.owner_id AS ni_owner_id,
			ni.owner_type::text AS ni_owner_type,
			ni.actor_user_id AS ni_actor_id,
			ni.amount_micro_units AS ni_amount,
			ni.currency AS ni_currency,
			ni.provider AS ni_provider,
			ni.status AS ni_status,
			ni.activates_wallet AS ni_activates_wallet,
			ni.personal_referral_reservation_id AS ni_referral_id,
			ni.expires_at AS ni_expires_at,
			ni.created_at AS ni_created_at,
			COALESCE(ni.is_created, FALSE) AS is_created
		FROM (SELECT 1) _
		LEFT JOIN wallet_target w ON TRUE
		LEFT JOIN existing_intent ex ON TRUE
		LEFT JOIN referral_target ref ON TRUE
		LEFT JOIN new_intent ni ON TRUE;
	`

	var walletStatus string
	// Existing Intent fields
	var exID, exWalletID, exActorID, exReferralID *uuid.UUID
	var exAmount *int64
	var exCurrency, exProvider, exProviderPaymentID, exStatus *string
	var exActivatesWallet *bool
	var exExpiresAt, exSettledAt, exCreatedAt *time.Time
	// Flags & New Intent fields
	var referralConditionFailed bool
	var niID, niWalletID, niOwnerID, niActorID, niReferralID *uuid.UUID
	var niOwnerType, niCurrency, niProvider, niStatus *string
	var niAmount *int64
	var niActivatesWallet *bool
	var niExpiresAt, niCreatedAt *time.Time
	var isCreated bool

	newIntentID := uuid.New()
	err := r.db.QueryRow(
		ctx,
		query,
		command.OwnerID,        // $1
		command.Currency,       // $2
		command.IdempotencyKey, // $3
		newIntentID,            // $4
		command.Amount,         // $5
		command.Provider,       // $6
		command.ExpiresAt,      // $7
	).Scan(
		&walletStatus,
		&exID,
		&exWalletID,
		&exActorID,
		&exAmount,
		&exCurrency,
		&exProvider,
		&exProviderPaymentID,
		&exStatus,
		&exActivatesWallet,
		&exReferralID,
		&exExpiresAt,
		&exSettledAt,
		&exCreatedAt,
		&referralConditionFailed,
		&niID,
		&niWalletID,
		&niOwnerID,
		&niOwnerType,
		&niActorID,
		&niAmount,
		&niCurrency,
		&niProvider,
		&niStatus,
		&niActivatesWallet,
		&niReferralID,
		&niExpiresAt,
		&niCreatedAt,
		&isCreated,
	)
	if err != nil {
		return nil, fmt.Errorf("personal payment repo: create intent CTE: %w", err)
	}

	// 1. Kiểm tra trạng thái ví
	if walletStatus == "NOT_FOUND" {
		return nil, billingTaxonomy.ErrWalletNotFound
	}
	if walletStatus != entity.WalletStatusPendingActivation && walletStatus != entity.WalletStatusActive {
		return nil, billingTaxonomy.ErrInvalidWallet
	}

	// 2. Xử lý Idempotency Replay
	if exID != nil {
		if *exActorID != command.OwnerID ||
			*exAmount != command.Amount ||
			*exCurrency != command.Currency ||
			*exProvider != command.Provider {
			return nil, billingTaxonomy.ErrIdempotencyConflict
		}
		return &entity.PaymentIntent{
			ID:                    *exID,
			WalletID:              *exWalletID,
			OwnerID:               command.OwnerID,
			OwnerType:             entity.OwnerTypePersonal,
			ActorID:               *exActorID,
			AmountMicroUnits:      *exAmount,
			Currency:              *exCurrency,
			Provider:              *exProvider,
			ProviderPaymentID:     *exProviderPaymentID,
			Status:                *exStatus,
			ActivatesWallet:       *exActivatesWallet,
			ReferralReservationID: exReferralID,
			ExpiresAt:             *exExpiresAt,
			SettledAt:             exSettledAt,
			CreatedAt:             *exCreatedAt,
			Created:               false,
		}, nil
	}

	// 3. Kiểm tra điều kiện nạp tối thiểu của mã giới thiệu
	if referralConditionFailed {
		return nil, billingTaxonomy.ErrPreconditionFailed
	}

	// 4. Trả về Payment Intent mới được tạo
	if isCreated && niID != nil {
		return &entity.PaymentIntent{
			ID:                    *niID,
			WalletID:              *niWalletID,
			OwnerID:               command.OwnerID,
			OwnerType:             entity.OwnerTypePersonal,
			ActorID:               *niActorID,
			AmountMicroUnits:      *niAmount,
			Currency:              *niCurrency,
			Provider:              *niProvider,
			Status:                *niStatus,
			ActivatesWallet:       *niActivatesWallet,
			ReferralReservationID: niReferralID,
			ExpiresAt:             *niExpiresAt,
			CreatedAt:             *niCreatedAt,
			Created:               true,
		}, nil
	}

	return nil, fmt.Errorf("personal payment repo: failed to create intent")
}

// GetPersonalIntent tra cứu chi tiết Payment Intent theo ID và OwnerID.
func (r *personalPaymentRepository) GetPersonalIntent(
	ctx context.Context,
	ownerID uuid.UUID,
	intentID uuid.UUID,
) (*entity.PaymentIntent, error) {
	var intent entity.PaymentIntent
	var referralID *uuid.UUID
	var settledAt *time.Time
	err := r.db.QueryRow(ctx, `
		SELECT id, wallet_id, actor_user_id, amount_micro_units, currency, provider,
		       COALESCE(provider_payment_id, ''),
		       CASE WHEN status='PENDING' AND expires_at <= NOW() THEN 'EXPIRED' ELSE status END,
		       activates_wallet, personal_referral_reservation_id, expires_at, settled_at, created_at
		FROM billing.payment_intents
		WHERE id=$1 AND owner_id=$2 AND owner_type='PERSONAL'::billing.owner_type
	`, intentID, ownerID).Scan(
		&intent.ID,
		&intent.WalletID,
		&intent.ActorID,
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
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrPaymentIntentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("personal payment repo: get intent: %w", err)
	}
	intent.OwnerID = ownerID
	intent.OwnerType = entity.OwnerTypePersonal
	intent.ReferralReservationID = referralID
	intent.SettledAt = settledAt
	return &intent, nil
}

// Các UUID Namespace cố định dùng để sinh Deterministic UUIDv5 cho sổ cái và phần thưởng:
// - Đảm bảo idempotency tuyệt đối: Cùng một Intent ID / Reservation ID sẽ luôn sinh ra cùng 1 khóa Primary Key,
//   giúp chặn đứng việc ghi trùng sổ cái hoặc nhân bản tiền thưởng khi xử lý webhook lặp.
var (
	personalTopUpLedgerNamespace = uuid.MustParse("c74d3417-514d-5b39-b454-08ad1ea35ee7")
	referralGrantNamespace       = uuid.MustParse("f79c94dd-ff83-59ab-adf7-47fd1d33cbd4")
	referralLedgerNamespace      = uuid.MustParse("80de0063-8de1-58f0-a248-11450647759f")
	referralRedeemNamespace      = uuid.MustParse("03f6540b-4eb5-51f2-8a89-f40a32ab955e")
)

// ApplyPersonalSettlement thực thi toàn bộ quy trình quyết toán tiền nạp cá nhân trong 1 Transaction cấp độ Serializable:
// 1. Ghi nhận Transactional Webhook Inbox (`billing.payment_webhook_inbox`): Chống xử lý trùng webhook từ Payment Provider.
// 2. Khóa dòng Payment Intent (`billing.payment_intents FOR UPDATE`): Kiểm tra tính khớp nối số tiền, đơn vị tiền tệ, provider.
// 3. Khóa và kiểm tra tính duy nhất của mã thanh toán từ cổng (`provider_payment_id` không được tái sử dụng cho intent khác).
// 4. Lấy Advisory Lock theo OwnerID để chống Lock Inversion với các luồng giữ chỗ Referral đồng thời.
// 5. Khóa dòng ví (`billing.wallets FOR UPDATE`), cộng tiền nạp vào `cash_balance`, kích hoạt ví nếu đang chờ.
// 6. Ghi nhận hàng đợi đồng bộ kích hoạt tài nguyên lưu trữ (`billing.storage_pending_activation_reconcile`).
// 7. Ghi nhận bút toán sổ cái bất biến (`billing.wallet_ledger_entries` với entry_type = 'TOP_UP').
// 8. Ghi nhận tín hiệu Outbox (`billing.wallet_admission_outbox` với admission_mode = 'ALLOW') để mở quyền hạ tầng đám mây.
// 9. Xử lý thưởng giới thiệu Onboarding Referral (nếu có):
//    - Cấp hạn mức tín dụng `billing.credit_grants`.
//    - Cộng tiền khuyến mãi vào `promotional_balance`.
//    - Ghi nhận bút toán thưởng `PROMO_CREDIT` vào sổ cái.
//    - Ghi nhận quy đổi `billing.personal_referral_redemptions` và cập nhật reservation `REDEEMED`.
// 10. Cập nhật Intent sang `SETTLED` và đánh dấu Webhook Inbox sang `APPLIED`.
func (r *personalPaymentRepository) ApplyPersonalSettlement(
	ctx context.Context,
	settlement entity.PaymentSettlement,
) (*entity.SettlementResult, error) {
	if settlement.OwnerType != entity.OwnerTypePersonal {
		return nil, billingTaxonomy.ErrSettlementMismatch
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("personal payment repo: begin settlement: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	// ============================================================================
	// BƯỚC 1: TRANSACTIONAL WEBHOOK INBOX DEDUPLICATION
	// ============================================================================
	var inserted bool
	err = tx.QueryRow(ctx, `
		INSERT INTO billing.payment_webhook_inbox
			(provider, provider_event_id, owner_type, payload_hash, payment_intent_id)
		VALUES ($1, $2, 'PERSONAL', $3, $4)
		ON CONFLICT (provider, provider_event_id) DO NOTHING
		RETURNING TRUE
	`, settlement.Provider, settlement.ProviderEventID, settlement.PayloadHash,
		settlement.PaymentIntentID).Scan(&inserted)
	replayedEvent := false
	if errors.Is(err, pgx.ErrNoRows) {
		var storedHash, storedOwnerType, status string
		var storedIntentID *uuid.UUID
		if err = tx.QueryRow(ctx, `
			SELECT payload_hash, owner_type::text, status, payment_intent_id
			FROM billing.payment_webhook_inbox
			WHERE provider=$1 AND provider_event_id=$2
			FOR UPDATE
		`, settlement.Provider, settlement.ProviderEventID).Scan(
			&storedHash,
			&storedOwnerType,
			&status,
			&storedIntentID,
		); err != nil {
			return nil, fmt.Errorf("personal payment repo: read webhook replay: %w", err)
		}
		if storedHash != settlement.PayloadHash ||
			storedOwnerType != string(entity.OwnerTypePersonal) ||
			storedIntentID == nil ||
			*storedIntentID != settlement.PaymentIntentID {
			return nil, billingTaxonomy.ErrWebhookReplayConflict
		}
		if status == "REJECTED" {
			return nil, billingTaxonomy.ErrSettlementMismatch
		}
		replayedEvent = true
	} else if err != nil {
		return nil, fmt.Errorf("personal payment repo: insert webhook inbox: %w", err)
	}

	// ============================================================================
	// BƯỚC 2: KHÓA VÀ KIỂM TRA PAYMENT INTENT
	// ============================================================================
	var intent entity.PaymentIntent
	var referralID *uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id, wallet_id, owner_id, actor_user_id, amount_micro_units,
		       currency, provider, COALESCE(provider_payment_id, ''), status,
		       activates_wallet, personal_referral_reservation_id, expires_at, settled_at, created_at
		FROM billing.payment_intents
		WHERE id=$1 AND owner_type='PERSONAL'::billing.owner_type
		FOR UPDATE
	`, settlement.PaymentIntentID).Scan(
		&intent.ID,
		&intent.WalletID,
		&intent.OwnerID,
		&intent.ActorID,
		&intent.AmountMicroUnits,
		&intent.Currency,
		&intent.Provider,
		&intent.ProviderPaymentID,
		&intent.Status,
		&intent.ActivatesWallet,
		&referralID,
		&intent.ExpiresAt,
		&intent.SettledAt,
		&intent.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, rejectErr := tx.Exec(ctx, `
			UPDATE billing.payment_webhook_inbox
			SET status='REJECTED', error_code='PERSONAL_INTENT_NOT_FOUND', processed_at=NOW()
			WHERE provider=$1 AND provider_event_id=$2
		`, settlement.Provider, settlement.ProviderEventID); rejectErr != nil {
			return nil, fmt.Errorf("personal payment repo: reject unknown intent: %w", rejectErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, fmt.Errorf("personal payment repo: commit unknown intent rejection: %w", commitErr)
		}
		return nil, billingTaxonomy.ErrPaymentIntentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("personal payment repo: lock intent: %w", err)
	}
	intent.OwnerType = entity.OwnerTypePersonal
	if intent.Provider != settlement.Provider ||
		intent.AmountMicroUnits != settlement.Amount ||
		intent.Currency != settlement.Currency {
		if _, rejectErr := tx.Exec(ctx, `
			UPDATE billing.payment_webhook_inbox
			SET status='REJECTED', error_code='PERSONAL_SETTLEMENT_MISMATCH', processed_at=NOW()
			WHERE provider=$1 AND provider_event_id=$2
		`, settlement.Provider, settlement.ProviderEventID); rejectErr != nil {
			return nil, fmt.Errorf("personal payment repo: reject settlement mismatch: %w", rejectErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, fmt.Errorf("personal payment repo: commit mismatch rejection: %w", commitErr)
		}
		return nil, billingTaxonomy.ErrSettlementMismatch
	}

	// ============================================================================
	// BƯỚC 3: KIỂM TRA TÍNH DUY NHẤT CỦA PROVIDER PAYMENT ID
	// ============================================================================
	var conflictingIntent uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM billing.payment_intents
		WHERE provider=$1 AND provider_payment_id=$2 AND id<>$3
		LIMIT 1
		FOR UPDATE
	`, settlement.Provider, settlement.ProviderPaymentID, intent.ID).Scan(&conflictingIntent)
	if err == nil {
		if _, rejectErr := tx.Exec(ctx, `
			UPDATE billing.payment_webhook_inbox
			SET status='REJECTED', error_code='PROVIDER_PAYMENT_REUSED', processed_at=NOW()
			WHERE provider=$1 AND provider_event_id=$2
		`, settlement.Provider, settlement.ProviderEventID); rejectErr != nil {
			return nil, fmt.Errorf("personal payment repo: reject reused provider payment: %w", rejectErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, fmt.Errorf("personal payment repo: commit provider reuse rejection: %w", commitErr)
		}
		return nil, billingTaxonomy.ErrSettlementMismatch
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("personal payment repo: check provider payment uniqueness: %w", err)
	}

	// 4. Advisory Lock chống Lock Inversion
	if intent.ActivatesWallet && referralID != nil {
		if _, err = tx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			intent.OwnerID.String()+":PERSONAL:ONBOARDING",
		); err != nil {
			return nil, fmt.Errorf("personal payment repo: lock referral owner: %w", err)
		}
	}

	// ============================================================================
	// BƯỚC 5: KHÓA DÒNG VÍ VÀ CẬP NHẬT TIỀN MẶT
	// ============================================================================
	var walletStatus string
	var restrictionReason *string
	var cashBalance, promotionalBalance int64
	err = tx.QueryRow(ctx, `
		SELECT status, restriction_reason, cash_balance, promotional_balance
		FROM billing.wallets
		WHERE id=$1 AND owner_id=$2 AND owner_type='PERSONAL'::billing.owner_type
		FOR UPDATE
	`, intent.WalletID, intent.OwnerID).Scan(
		&walletStatus,
		&restrictionReason,
		&cashBalance,
		&promotionalBalance,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrWalletNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("personal payment repo: lock wallet: %w", err)
	}

	// Xử lý Intent đã Settled trước đó (Replay an toàn)
	if intent.Status == "SETTLED" {
		if intent.ProviderPaymentID != settlement.ProviderPaymentID {
			return nil, billingTaxonomy.ErrSettlementMismatch
		}
		if _, err = tx.Exec(ctx, `
			UPDATE billing.payment_webhook_inbox
			SET status='APPLIED', processed_at=COALESCE(processed_at, NOW())
			WHERE provider=$1 AND provider_event_id=$2
		`, settlement.Provider, settlement.ProviderEventID); err != nil {
			return nil, fmt.Errorf("personal payment repo: mark replay applied: %w", err)
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("personal payment repo: commit replay: %w", err)
		}
		return &entity.SettlementResult{
			PaymentIntentID:    intent.ID,
			WalletID:           intent.WalletID,
			OwnerID:            intent.OwnerID,
			OwnerType:          entity.OwnerTypePersonal,
			ActorID:            intent.ActorID,
			WalletStatus:       walletStatus,
			CashBalance:        cashBalance,
			PromotionalBalance: promotionalBalance,
			Replayed:           true,
		}, nil
	}

	// Kiểm tra điều kiện có thể nạp tiền và chống tràn số nguyên (Integer Overflow Guard)
	const maxInt64Value = int64(^uint64(0) >> 1)
	if walletStatus == entity.WalletStatusClosed ||
		(walletStatus != entity.WalletStatusPendingActivation &&
			walletStatus != entity.WalletStatusActive &&
			walletStatus != entity.WalletStatusSuspended) ||
		(cashBalance > 0 && settlement.Amount > maxInt64Value-cashBalance) {
		if _, rejectErr := tx.Exec(ctx, `
			UPDATE billing.payment_webhook_inbox
			SET status='REJECTED', error_code='PERSONAL_WALLET_NOT_CREDITABLE', processed_at=NOW()
			WHERE provider=$1 AND provider_event_id=$2
		`, settlement.Provider, settlement.ProviderEventID); rejectErr != nil {
			return nil, fmt.Errorf("personal payment repo: reject invalid wallet: %w", rejectErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, fmt.Errorf("personal payment repo: commit invalid wallet rejection: %w", commitErr)
		}
		return nil, billingTaxonomy.ErrInvalidWallet
	}

	cashBalance += settlement.Amount
	nextWalletStatus := walletStatus
	walletActivated := walletStatus == entity.WalletStatusSuspended && restrictionReason != nil && *restrictionReason == "CREDIT_EXHAUSTED"
	if walletActivated {
		nextWalletStatus = entity.WalletStatusActive
	}

	var walletVersion int64
	if err = tx.QueryRow(ctx, `
		UPDATE billing.wallets
		SET cash_balance=$1, status=$2::billing.wallet_lifecycle_status,
		    restriction_reason=CASE WHEN $2='ACTIVE' THEN NULL ELSE restriction_reason END,
		    status_changed_at=CASE WHEN status::text IS DISTINCT FROM $2 THEN NOW() ELSE status_changed_at END,
		    version=version+1, updated_at=NOW()
		WHERE id=$3
		RETURNING version
	`, cashBalance, nextWalletStatus, intent.WalletID).Scan(&walletVersion); err != nil {
		return nil, fmt.Errorf("personal payment repo: credit cash: %w", err)
	}

	// ============================================================================
	// BƯỚC 6: HÀNG ĐỢI ĐỒNG BỘ KÍCH HOẠT DỊCH VỤ LƯU TRỮ
	// ============================================================================
	if walletStatus == entity.WalletStatusPendingActivation {
		if _, err = tx.Exec(ctx, `
			INSERT INTO billing.storage_pending_activation_reconcile
				(wallet_id, owner_id, owner_type, target_wallet_version, status, updated_at)
			VALUES ($1,$2,'PERSONAL',$3,'PENDING',NOW())
			ON CONFLICT (wallet_id) DO UPDATE
			SET owner_id=EXCLUDED.owner_id, owner_type=EXCLUDED.owner_type,
				target_wallet_version=EXCLUDED.target_wallet_version,
				status='PENDING', last_error=NULL, updated_at=NOW()
		`, intent.WalletID, intent.OwnerID, walletVersion); err != nil {
			return nil, fmt.Errorf("personal payment repo: queue storage activation reconciliation: %w", err)
		}
	}

	// ============================================================================
	// BƯỚC 7: SỔ CÁI BẤT BIẾN VÀ TÍN HIỆU ADMISSION OUTBOX
	// ============================================================================
	admissionMode := "SUSPEND_BILLABLE"
	var admissionReason any = "ADMINISTRATIVE"
	if nextWalletStatus == entity.WalletStatusActive {
		admissionMode = "ALLOW"
		admissionReason = nil
	} else if walletStatus == entity.WalletStatusPendingActivation {
		admissionReason = "NOT_ACTIVATED"
	} else if restrictionReason != nil && *restrictionReason == "CREDIT_EXHAUSTED" {
		admissionReason = "CREDIT_EXHAUSTED"
	}

	topUpLedgerID := uuid.NewSHA1(personalTopUpLedgerNamespace, intent.ID[:])
	if _, err = tx.Exec(ctx, `
		INSERT INTO billing.wallet_ledger_entries
			(id, wallet_id, owner_id, owner_type, actor_user_id, amount_micro_units,
			 cash_balance_after, promotional_balance_after, currency,
			 entry_type, reference_id, description, occurred_at)
		VALUES ($1, $2, $3, 'PERSONAL', $3, $4, $5, $6, $7,
		        'TOP_UP', $8, 'Verified personal payment settlement', $9)
	`, topUpLedgerID, intent.WalletID, intent.OwnerID, settlement.Amount,
		cashBalance, promotionalBalance, settlement.Currency,
		settlement.ProviderPaymentID, settlement.SettledAt); err != nil {
		return nil, fmt.Errorf("personal payment repo: insert top-up ledger: %w", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO billing.wallet_admission_outbox
			(event_id, wallet_id, owner_id, owner_type, wallet_version, admission_mode, restriction_reason, effective_at)
		VALUES ($1,$2,$3,'PERSONAL',$4,$5,$6,NOW())
	`, uuid.New(), intent.WalletID, intent.OwnerID, walletVersion, admissionMode, admissionReason); err != nil {
		return nil, fmt.Errorf("personal payment repo: write wallet admission outbox: %w", err)
	}

	result := &entity.SettlementResult{
		PaymentIntentID:    intent.ID,
		WalletID:           intent.WalletID,
		OwnerID:            intent.OwnerID,
		OwnerType:          entity.OwnerTypePersonal,
		ActorID:            intent.ActorID,
		WalletStatus:       nextWalletStatus,
		CashBalance:        cashBalance,
		PromotionalBalance: promotionalBalance,
		WalletActivated:    walletActivated,
		Replayed:           replayedEvent,
	}

	// ============================================================================
	// BƯỚC 8: QUYẾT TOÁN MÃ GIỚI THIỆU ONBOARDING REFERRAL (NẾU CÓ)
	// ============================================================================
	if walletActivated && referralID != nil {
		var reservation entity.ReferralReservation
		var reservationUserID uuid.UUID
		var alreadyRedeemed bool
		referralErr := tx.QueryRow(ctx, `
			SELECT id, campaign_id, user_id, code_snapshot,
			       status, grant_amount_micro_units, minimum_top_up_micro_units,
			       currency, expires_at, grant_expires_at
			FROM billing.personal_referral_reservations
			WHERE id=$1
			FOR UPDATE
		`, *referralID).Scan(
			&reservation.ID,
			&reservation.CampaignID,
			&reservationUserID,
			&reservation.Code,
			&reservation.Status,
			&reservation.GrantAmountMicroUnits,
			&reservation.MinimumTopUpMicroUnits,
			&reservation.Currency,
			&reservation.ExpiresAt,
			&reservation.GrantExpiresAt,
		)
		if referralErr != nil && !errors.Is(referralErr, pgx.ErrNoRows) {
			return nil, fmt.Errorf("personal payment repo: lock referral reservation: %w", referralErr)
		}
		if referralErr == nil {
			if redemptionErr := tx.QueryRow(ctx, `
				SELECT EXISTS(
					SELECT 1
					FROM billing.personal_referral_redemptions
					WHERE user_id=$1 AND redemption_kind='ONBOARDING'
				)
			`, intent.OwnerID).Scan(&alreadyRedeemed); redemptionErr != nil {
				return nil, fmt.Errorf("personal payment repo: check referral redemption: %w", redemptionErr)
			}
		}

		switch {
		case errors.Is(referralErr, pgx.ErrNoRows):
			result.ReferralRejectReason = "RESERVATION_NOT_FOUND"
		case reservationUserID != intent.OwnerID:
			result.ReferralRejectReason = "OWNER_MISMATCH"
		case reservation.Status != "RESERVED":
			result.ReferralRejectReason = "RESERVATION_NOT_ACTIVE"
		case settlement.SettledAt.After(reservation.ExpiresAt):
			result.ReferralRejectReason = "RESERVATION_EXPIRED"
		case settlement.Amount < reservation.MinimumTopUpMicroUnits ||
			settlement.Currency != reservation.Currency:
			result.ReferralRejectReason = "TOP_UP_REQUIREMENT_NOT_MET"
		case alreadyRedeemed:
			result.ReferralRejectReason = "ONBOARDING_ALREADY_REDEEMED"
		case promotionalBalance > 0 &&
			reservation.GrantAmountMicroUnits > maxInt64Value-promotionalBalance:
			result.ReferralRejectReason = "PROMOTIONAL_BALANCE_OVERFLOW"
		default:
			grantID := uuid.NewSHA1(referralGrantNamespace, reservation.ID[:])
			var grantInserted bool
			grantErr := tx.QueryRow(ctx, `
				INSERT INTO billing.credit_grants
					(id, campaign_id, wallet_id, owner_id, owner_type,
					 amount_micro_units, currency, expires_at, idempotency_key)
				VALUES ($1, $2, $3, $4, 'PERSONAL', $5, $6, $7, $8)
				ON CONFLICT DO NOTHING
				RETURNING TRUE
			`, grantID, reservation.CampaignID, intent.WalletID, intent.OwnerID,
				reservation.GrantAmountMicroUnits, reservation.Currency,
				reservation.GrantExpiresAt, "referral:"+reservation.ID.String()).Scan(&grantInserted)
			if errors.Is(grantErr, pgx.ErrNoRows) {
				result.ReferralRejectReason = "CREDIT_GRANT_CONFLICT"
			} else if grantErr != nil {
				return nil, fmt.Errorf("personal payment repo: insert referral grant: %w", grantErr)
			}

			if grantInserted {
				promotionalBalance += reservation.GrantAmountMicroUnits
				var referralWalletVersion int64
				if err = tx.QueryRow(ctx, `
					UPDATE billing.wallets
					SET promotional_balance=$1, version=version+1, updated_at=NOW()
					WHERE id=$2
					RETURNING version
				`, promotionalBalance, intent.WalletID).Scan(&referralWalletVersion); err != nil {
					return nil, fmt.Errorf("personal payment repo: credit referral promotion: %w", err)
				}
				referralAdmissionMode := "SUSPEND_BILLABLE"
				var referralAdmissionReason any = "ADMINISTRATIVE"
				if nextWalletStatus == entity.WalletStatusActive {
					referralAdmissionMode = "ALLOW"
					referralAdmissionReason = nil
				} else if walletStatus == entity.WalletStatusPendingActivation {
					referralAdmissionReason = "NOT_ACTIVATED"
				} else if restrictionReason != nil && *restrictionReason == "CREDIT_EXHAUSTED" {
					referralAdmissionReason = "CREDIT_EXHAUSTED"
				}
				if _, err = tx.Exec(ctx, `
					INSERT INTO billing.wallet_admission_outbox
						(event_id, wallet_id, owner_id, owner_type, wallet_version, admission_mode, restriction_reason, effective_at)
					VALUES ($1,$2,$3,'PERSONAL',$4,$5,$6,NOW())
				`, uuid.New(), intent.WalletID, intent.OwnerID, referralWalletVersion, referralAdmissionMode, referralAdmissionReason); err != nil {
					return nil, fmt.Errorf("personal payment repo: write referral wallet admission outbox: %w", err)
				}

				referralLedgerID := uuid.NewSHA1(referralLedgerNamespace, reservation.ID[:])
				if _, err = tx.Exec(ctx, `
					INSERT INTO billing.wallet_ledger_entries
						(id, wallet_id, owner_id, owner_type, actor_user_id, amount_micro_units,
						 cash_balance_after, promotional_balance_after, currency,
						 entry_type, reference_id, description, occurred_at)
					VALUES ($1, $2, $3, 'PERSONAL', $3, $4, $5, $6, $7,
					        'PROMO_CREDIT', $8, 'Onboarding referral credit', $9)
				`, referralLedgerID, intent.WalletID, intent.OwnerID,
					reservation.GrantAmountMicroUnits, cashBalance, promotionalBalance,
					reservation.Currency, grantID.String(), settlement.SettledAt); err != nil {
					return nil, fmt.Errorf("personal payment repo: insert referral ledger: %w", err)
				}

				redemptionID := uuid.NewSHA1(referralRedeemNamespace, reservation.ID[:])
				if _, err = tx.Exec(ctx, `
					INSERT INTO billing.personal_referral_redemptions
						(id, reservation_id, campaign_id, wallet_id, user_id,
						 redemption_kind, payment_intent_id, credit_grant_id,
						 amount_micro_units, currency, redeemed_at)
					VALUES ($1, $2, $3, $4, $5, 'ONBOARDING',
					        $6, $7, $8, $9, $10)
				`, redemptionID, reservation.ID, reservation.CampaignID, intent.WalletID,
					intent.OwnerID, intent.ID, grantID, reservation.GrantAmountMicroUnits,
					reservation.Currency, settlement.SettledAt); err != nil {
					var pgErr *pgconn.PgError
					if errors.As(err, &pgErr) && pgErr.Code == "23505" {
						return nil, billingTaxonomy.ErrReferralAlreadyRedeemed
					}
					return nil, fmt.Errorf("personal payment repo: insert referral redemption: %w", err)
				}
				if _, err = tx.Exec(ctx, `
					UPDATE billing.personal_referral_reservations
					SET status='REDEEMED', redeemed_at=$1, updated_at=NOW()
					WHERE id=$2
				`, settlement.SettledAt, reservation.ID); err != nil {
					return nil, fmt.Errorf("personal payment repo: mark referral redeemed: %w", err)
				}
				result.ReferralApplied = true
				result.PromotionalBalance = promotionalBalance
			}
		}

		if result.ReferralRejectReason != "" && reservation.ID != uuid.Nil {
			if _, err = tx.Exec(ctx, `
				UPDATE billing.personal_referral_reservations
				SET status='REJECTED', rejection_reason=$1, updated_at=NOW()
				WHERE id=$2 AND status='RESERVED'
			`, result.ReferralRejectReason, reservation.ID); err != nil {
				return nil, fmt.Errorf("personal payment repo: reject referral reservation: %w", err)
			}
		}
	}

	// ============================================================================
	// BƯỚC 9: ĐÁNH DẤU INTENT SETTLED VÀ COMMIT TRANSACTION
	// ============================================================================
	if _, err = tx.Exec(ctx, `
		UPDATE billing.payment_intents
		SET status='SETTLED', provider_payment_id=$1, settled_at=$2, updated_at=NOW()
		WHERE id=$3
	`, settlement.ProviderPaymentID, settlement.SettledAt, intent.ID); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, billingTaxonomy.ErrSettlementMismatch
		}
		return nil, fmt.Errorf("personal payment repo: mark intent settled: %w", err)
	}
	if _, err = tx.Exec(ctx, `
		UPDATE billing.payment_webhook_inbox
		SET status='APPLIED', processed_at=NOW()
		WHERE provider=$1 AND provider_event_id=$2
	`, settlement.Provider, settlement.ProviderEventID); err != nil {
		return nil, fmt.Errorf("personal payment repo: mark webhook applied: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01") {
			return nil, billingTaxonomy.ErrConflict
		}
		return nil, fmt.Errorf("personal payment repo: commit settlement: %w", err)
	}
	return result, nil
}
