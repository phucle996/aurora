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

// tenantPaymentRepository chịu trách nhiệm thực thi các thao tác cơ sở dữ liệu PostgreSQL cho nghiệp vụ thanh toán tổ chức (Tenant):
// - Tra cứu số dư ví tổ chức (`GetTenantWalletSummary`).
// - Khởi tạo phiên nạp tiền cho tổ chức kèm định danh Actor (`CreateTenantIntent`).
// - Tra cứu chi tiết Payment Intent của tổ chức (`GetTenantIntent`).
// - Quyết toán thanh toán webhook nguyên tử vào ví tổ chức với sổ cái kiểm toán bất biến (`ApplyTenantSettlement`).
type tenantPaymentRepository struct {
	db *pgxpool.Pool
}

// NewTenantPaymentRepository khởi tạo một instance mới của tenantPaymentRepository, trả về interface TenantPaymentRepository.
func NewTenantPaymentRepository(db *pgxpool.Pool) billingRepoInterface.TenantPaymentRepository {
	return &tenantPaymentRepository{db: db}
}

// GetTenantWalletSummary đọc thông tin tóm tắt số dư ví tiền tổ chức (tiền mặt, khuyến mãi, hạn mức thấu chi, phiên bản).
func (r *tenantPaymentRepository) GetTenantWalletSummary(
	ctx context.Context,
	tenantID uuid.UUID,
) (*entity.WalletSummary, error) {
	var summary entity.WalletSummary
	err := r.db.QueryRow(ctx, `
		SELECT id, currency, cash_balance, promotional_balance, overdraft_limit,
		       status, version, updated_at
		FROM billing.wallets
		WHERE owner_id=$1 AND owner_type='TENANT'::billing.owner_type AND currency='USD'
	`, tenantID).Scan(
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
		return nil, fmt.Errorf("tenant payment repo: read wallet: %w", err)
	}
	return &summary, nil
}

// CreateTenantIntent thực thi toàn bộ quy trình tạo Payment Intent tổ chức trong 1 câu lệnh CTE duy nhất:
// 1. `wallet_target`: Xác định ví tổ chức và kiểm tra trạng thái (`PENDING_ACTIVATION` hoặc `ACTIVE`).
// 2. `existing_intent`: Kiểm tra Idempotent Replay theo bộ đôi `(actor_user_id, idempotency_key)`.
// 3. `expire_stale`: Tự động hủy các Intent cũ quá hạn chưa thanh toán (`status = 'EXPIRED'`).
// 4. `new_intent`: Chèn Payment Intent mới cho Tenant vào `billing.payment_intents`.
func (r *tenantPaymentRepository) CreateTenantIntent(
	ctx context.Context,
	command entity.CreateTenantPaymentIntentCommand,
) (*entity.PaymentIntent, error) {
	const query = `
		WITH wallet_target AS (
			SELECT id, status
			FROM billing.wallets
			WHERE owner_id = $1
			  AND owner_type = 'TENANT'::billing.owner_type
			  AND currency = $2
		),
		existing_intent AS (
			SELECT id, wallet_id, actor_user_id, amount_micro_units, currency, provider,
			       COALESCE(provider_payment_id, '') AS provider_payment_id,
			       CASE WHEN status = 'PENDING' AND expires_at <= NOW() THEN 'EXPIRED' ELSE status END AS status,
			       activates_wallet, expires_at, settled_at, created_at
			FROM billing.payment_intents
			WHERE owner_id = $1
			  AND owner_type = 'TENANT'::billing.owner_type
			  AND actor_user_id = $3
			  AND idempotency_key = $4
		),
		expire_stale AS (
			UPDATE billing.payment_intents
			SET status = 'EXPIRED', updated_at = NOW()
			WHERE owner_id = $1
			  AND owner_type = 'TENANT'::billing.owner_type
			  AND status = 'PENDING'
			  AND expires_at <= NOW()
			  AND NOT EXISTS (SELECT 1 FROM existing_intent)
			RETURNING id
		),
		new_intent AS (
			INSERT INTO billing.payment_intents (
				id, wallet_id, owner_id, owner_type, actor_user_id,
				amount_micro_units, currency, provider, status,
				activates_wallet, personal_referral_reservation_id,
				idempotency_key, expires_at
			)
			SELECT
				$5,
				w.id,
				$1,
				'TENANT'::billing.owner_type,
				$3,
				$6,
				$2,
				$7,
				'PENDING',
				w.status = 'PENDING_ACTIVATION',
				NULL,
				$4,
				$8
			FROM wallet_target w
			WHERE (w.status = 'PENDING_ACTIVATION' OR w.status = 'ACTIVE')
			  AND NOT EXISTS (SELECT 1 FROM existing_intent)
			RETURNING id, wallet_id, owner_id, owner_type, actor_user_id,
			          amount_micro_units, currency, provider, status,
			          activates_wallet, expires_at, created_at, TRUE AS is_created
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
			ex.expires_at AS ex_expires_at,
			ex.settled_at AS ex_settled_at,
			ex.created_at AS ex_created_at,
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
			ni.expires_at AS ni_expires_at,
			ni.created_at AS ni_created_at,
			COALESCE(ni.is_created, FALSE) AS is_created
		FROM (SELECT 1) _
		LEFT JOIN wallet_target w ON TRUE
		LEFT JOIN existing_intent ex ON TRUE
		LEFT JOIN new_intent ni ON TRUE;
	`

	var walletStatus string
	// Existing Intent fields
	var exID, exWalletID, exActorID *uuid.UUID
	var exAmount *int64
	var exCurrency, exProvider, exProviderPaymentID, exStatus *string
	var exActivatesWallet *bool
	var exExpiresAt, exSettledAt, exCreatedAt *time.Time
	// New Intent fields
	var niID, niWalletID, niOwnerID, niActorID *uuid.UUID
	var niOwnerType, niCurrency, niProvider, niStatus *string
	var niAmount *int64
	var niActivatesWallet *bool
	var niExpiresAt, niCreatedAt *time.Time
	var isCreated bool

	newIntentID := uuid.New()
	err := r.db.QueryRow(
		ctx,
		query,
		command.TenantID,       // $1
		command.Currency,       // $2
		command.ActorID,        // $3
		command.IdempotencyKey, // $4
		newIntentID,            // $5
		command.Amount,         // $6
		command.Provider,       // $7
		command.ExpiresAt,      // $8
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
		&exExpiresAt,
		&exSettledAt,
		&exCreatedAt,
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
		&niExpiresAt,
		&niCreatedAt,
		&isCreated,
	)
	if err != nil {
		return nil, fmt.Errorf("tenant payment repo: create intent CTE: %w", err)
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
		if *exAmount != command.Amount ||
			*exCurrency != command.Currency ||
			*exProvider != command.Provider {
			return nil, billingTaxonomy.ErrIdempotencyConflict
		}
		return &entity.PaymentIntent{
			ID:                *exID,
			WalletID:          *exWalletID,
			OwnerID:           command.TenantID,
			OwnerType:         entity.OwnerTypeTenant,
			ActorID:           *exActorID,
			AmountMicroUnits:  *exAmount,
			Currency:          *exCurrency,
			Provider:          *exProvider,
			ProviderPaymentID: *exProviderPaymentID,
			Status:            *exStatus,
			ActivatesWallet:   *exActivatesWallet,
			ExpiresAt:         *exExpiresAt,
			SettledAt:         exSettledAt,
			CreatedAt:         *exCreatedAt,
			Created:           false,
		}, nil
	}

	// 3. Trả về Payment Intent mới được tạo
	if isCreated && niID != nil {
		return &entity.PaymentIntent{
			ID:               *niID,
			WalletID:         *niWalletID,
			OwnerID:          command.TenantID,
			OwnerType:        entity.OwnerTypeTenant,
			ActorID:          *niActorID,
			AmountMicroUnits: *niAmount,
			Currency:         *niCurrency,
			Provider:         *niProvider,
			Status:           *niStatus,
			ActivatesWallet:  *niActivatesWallet,
			ExpiresAt:        *niExpiresAt,
			CreatedAt:        *niCreatedAt,
			Created:          true,
		}, nil
	}

	return nil, fmt.Errorf("tenant payment repo: failed to create intent")
}

// GetTenantIntent tra cứu chi tiết Payment Intent theo IntentID và TenantID.
func (r *tenantPaymentRepository) GetTenantIntent(
	ctx context.Context,
	tenantID uuid.UUID,
	intentID uuid.UUID,
) (*entity.PaymentIntent, error) {
	var intent entity.PaymentIntent
	var settledAt *time.Time
	err := r.db.QueryRow(ctx, `
		SELECT id, wallet_id, actor_user_id, amount_micro_units, currency, provider,
		       COALESCE(provider_payment_id, ''),
		       CASE WHEN status='PENDING' AND expires_at <= NOW() THEN 'EXPIRED' ELSE status END,
		       activates_wallet, expires_at, settled_at, created_at
		FROM billing.payment_intents
		WHERE id=$1 AND owner_id=$2 AND owner_type='TENANT'::billing.owner_type
	`, intentID, tenantID).Scan(
		&intent.ID,
		&intent.WalletID,
		&intent.ActorID,
		&intent.AmountMicroUnits,
		&intent.Currency,
		&intent.Provider,
		&intent.ProviderPaymentID,
		&intent.Status,
		&intent.ActivatesWallet,
		&intent.ExpiresAt,
		&settledAt,
		&intent.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrPaymentIntentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("tenant payment repo: get intent: %w", err)
	}
	intent.OwnerID = tenantID
	intent.OwnerType = entity.OwnerTypeTenant
	intent.SettledAt = settledAt
	return &intent, nil
}

// UUID Namespace cố định cho bút toán sổ cái nạp tiền ví tổ chức
var tenantTopUpLedgerNamespace = uuid.MustParse("c74d3417-514d-5b39-b454-08ad1ea35ee7")

// ApplyTenantSettlement thực thi toàn bộ quy trình quyết toán tiền nạp tổ chức trong 1 Transaction cấp độ Serializable:
// 1. Ghi nhận Transactional Webhook Inbox (`billing.payment_webhook_inbox`).
// 2. Khóa dòng Payment Intent (`billing.payment_intents FOR UPDATE`): Kiểm tra tính toàn vẹn số tiền, đơn vị tiền tệ, provider.
// 3. Khóa và kiểm tra tính duy nhất của `provider_payment_id`.
// 4. Khóa dòng ví tổ chức (`billing.wallets FOR UPDATE`), cộng tiền nạp vào `cash_balance`, kích hoạt ví nếu đang chờ.
// 5. Ghi nhận hàng đợi đồng bộ kích hoạt tài nguyên lưu trữ (`billing.storage_pending_activation_reconcile`).
// 6. Ghi nhận bút toán sổ cái bất biến (`billing.wallet_ledger_entries` với entry_type = 'TOP_UP').
// 7. Ghi nhận tín hiệu Outbox (`billing.wallet_admission_outbox` với admission_mode = 'ALLOW') để mở quyền sử dụng hạ tầng cho Tenant.
// 8. Cập nhật Intent sang `SETTLED` và đánh dấu Webhook Inbox sang `APPLIED`.
func (r *tenantPaymentRepository) ApplyTenantSettlement(
	ctx context.Context,
	settlement entity.PaymentSettlement,
) (*entity.SettlementResult, error) {
	if settlement.OwnerType != entity.OwnerTypeTenant {
		return nil, billingTaxonomy.ErrSettlementMismatch
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("tenant payment repo: begin settlement: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	// ============================================================================
	// BƯỚC 1: TRANSACTIONAL WEBHOOK INBOX DEDUPLICATION
	// ============================================================================
	var inserted bool
	err = tx.QueryRow(ctx, `
		INSERT INTO billing.payment_webhook_inbox
			(provider, provider_event_id, owner_type, payload_hash, payment_intent_id)
		VALUES ($1, $2, 'TENANT', $3, $4)
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
			return nil, fmt.Errorf("tenant payment repo: read webhook replay: %w", err)
		}
		if storedHash != settlement.PayloadHash ||
			storedOwnerType != string(entity.OwnerTypeTenant) ||
			storedIntentID == nil ||
			*storedIntentID != settlement.PaymentIntentID {
			return nil, billingTaxonomy.ErrWebhookReplayConflict
		}
		if status == "REJECTED" {
			return nil, billingTaxonomy.ErrSettlementMismatch
		}
		replayedEvent = true
	} else if err != nil {
		return nil, fmt.Errorf("tenant payment repo: insert webhook inbox: %w", err)
	}

	// ============================================================================
	// BƯỚC 2: KHÓA VÀ KIỂM TRA PAYMENT INTENT
	// ============================================================================
	var intent entity.PaymentIntent
	err = tx.QueryRow(ctx, `
		SELECT id, wallet_id, owner_id, actor_user_id, amount_micro_units,
		       currency, provider, COALESCE(provider_payment_id, ''), status,
		       activates_wallet, expires_at, settled_at, created_at
		FROM billing.payment_intents
		WHERE id=$1 AND owner_type='TENANT'::billing.owner_type
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
		&intent.ExpiresAt,
		&intent.SettledAt,
		&intent.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, rejectErr := tx.Exec(ctx, `
			UPDATE billing.payment_webhook_inbox
			SET status='REJECTED', error_code='TENANT_INTENT_NOT_FOUND', processed_at=NOW()
			WHERE provider=$1 AND provider_event_id=$2
		`, settlement.Provider, settlement.ProviderEventID); rejectErr != nil {
			return nil, fmt.Errorf("tenant payment repo: reject unknown intent: %w", rejectErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, fmt.Errorf("tenant payment repo: commit unknown intent rejection: %w", commitErr)
		}
		return nil, billingTaxonomy.ErrPaymentIntentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("tenant payment repo: lock intent: %w", err)
	}
	intent.OwnerType = entity.OwnerTypeTenant
	if intent.Provider != settlement.Provider ||
		intent.AmountMicroUnits != settlement.Amount ||
		intent.Currency != settlement.Currency {
		if _, rejectErr := tx.Exec(ctx, `
			UPDATE billing.payment_webhook_inbox
			SET status='REJECTED', error_code='TENANT_SETTLEMENT_MISMATCH', processed_at=NOW()
			WHERE provider=$1 AND provider_event_id=$2
		`, settlement.Provider, settlement.ProviderEventID); rejectErr != nil {
			return nil, fmt.Errorf("tenant payment repo: reject settlement mismatch: %w", rejectErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, fmt.Errorf("tenant payment repo: commit mismatch rejection: %w", commitErr)
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
			return nil, fmt.Errorf("tenant payment repo: reject reused provider payment: %w", rejectErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, fmt.Errorf("tenant payment repo: commit provider reuse rejection: %w", commitErr)
		}
		return nil, billingTaxonomy.ErrSettlementMismatch
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("tenant payment repo: check provider payment uniqueness: %w", err)
	}

	// ============================================================================
	// BƯỚC 4: KHÓA DÒNG VÍ VÀ CẬP NHẬT TIỀN MẶT
	// ============================================================================
	var walletStatus string
	var restrictionReason *string
	var cashBalance, promotionalBalance int64
	err = tx.QueryRow(ctx, `
		SELECT status, restriction_reason, cash_balance, promotional_balance
		FROM billing.wallets
		WHERE id=$1 AND owner_id=$2 AND owner_type='TENANT'::billing.owner_type
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
		return nil, fmt.Errorf("tenant payment repo: lock wallet: %w", err)
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
			return nil, fmt.Errorf("tenant payment repo: mark replay applied: %w", err)
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("tenant payment repo: commit replay: %w", err)
		}
		return &entity.SettlementResult{
			PaymentIntentID:    intent.ID,
			WalletID:           intent.WalletID,
			OwnerID:            intent.OwnerID,
			OwnerType:          entity.OwnerTypeTenant,
			ActorID:            intent.ActorID,
			WalletStatus:       walletStatus,
			CashBalance:        cashBalance,
			PromotionalBalance: promotionalBalance,
			Replayed:           true,
		}, nil
	}

	// Kiểm tra điều kiện nạp tiền và chống tràn số nguyên (Integer Overflow Guard)
	const maxInt64Value = int64(^uint64(0) >> 1)
	if walletStatus == entity.WalletStatusClosed ||
		(walletStatus != entity.WalletStatusPendingActivation &&
			walletStatus != entity.WalletStatusActive &&
			walletStatus != entity.WalletStatusSuspended) ||
		(cashBalance > 0 && settlement.Amount > maxInt64Value-cashBalance) {
		if _, rejectErr := tx.Exec(ctx, `
			UPDATE billing.payment_webhook_inbox
			SET status='REJECTED', error_code='TENANT_WALLET_NOT_CREDITABLE', processed_at=NOW()
			WHERE provider=$1 AND provider_event_id=$2
		`, settlement.Provider, settlement.ProviderEventID); rejectErr != nil {
			return nil, fmt.Errorf("tenant payment repo: reject invalid wallet: %w", rejectErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, fmt.Errorf("tenant payment repo: commit invalid wallet rejection: %w", commitErr)
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
		return nil, fmt.Errorf("tenant payment repo: credit cash: %w", err)
	}

	// ============================================================================
	// BƯỚC 5: HÀNG ĐỢI ĐỒNG BỘ KÍCH HOẠT DỊCH VỤ LƯU TRỮ
	// ============================================================================
	if walletStatus == entity.WalletStatusPendingActivation {
		if _, err = tx.Exec(ctx, `
			INSERT INTO billing.storage_pending_activation_reconcile
				(wallet_id, owner_id, owner_type, target_wallet_version, status, updated_at)
			VALUES ($1,$2,'TENANT',$3,'PENDING',NOW())
			ON CONFLICT (wallet_id) DO UPDATE
			SET owner_id=EXCLUDED.owner_id, owner_type=EXCLUDED.owner_type,
				target_wallet_version=EXCLUDED.target_wallet_version,
				status='PENDING', last_error=NULL, updated_at=NOW()
		`, intent.WalletID, intent.OwnerID, walletVersion); err != nil {
			return nil, fmt.Errorf("tenant payment repo: queue storage activation reconciliation: %w", err)
		}
	}

	// ============================================================================
	// BƯỚC 6: SỔ CÁI BẤT BIẾN VÀ TÍN HIỆU ADMISSION OUTBOX
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

	topUpLedgerID := uuid.NewSHA1(tenantTopUpLedgerNamespace, intent.ID[:])
	if _, err = tx.Exec(ctx, `
		INSERT INTO billing.wallet_ledger_entries
			(id, wallet_id, owner_id, owner_type, actor_user_id, amount_micro_units,
			 cash_balance_after, promotional_balance_after, currency,
			 entry_type, reference_id, description, occurred_at)
		VALUES ($1, $2, $3, 'TENANT', $4, $5, $6, $7, $8,
		        'TOP_UP', $9, 'Verified tenant payment settlement', $10)
	`, topUpLedgerID, intent.WalletID, intent.OwnerID, intent.ActorID,
		settlement.Amount, cashBalance, promotionalBalance, settlement.Currency,
		settlement.ProviderPaymentID, settlement.SettledAt); err != nil {
		return nil, fmt.Errorf("tenant payment repo: insert top-up ledger: %w", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO billing.wallet_admission_outbox
			(event_id, wallet_id, owner_id, owner_type, wallet_version, admission_mode, restriction_reason, effective_at)
		VALUES ($1,$2,$3,'TENANT',$4,$5,$6,NOW())
	`, uuid.New(), intent.WalletID, intent.OwnerID, walletVersion, admissionMode, admissionReason); err != nil {
		return nil, fmt.Errorf("tenant payment repo: write wallet admission outbox: %w", err)
	}

	// ============================================================================
	// BƯỚC 7: ĐÁNH DẤU INTENT SETTLED VÀ COMMIT TRANSACTION
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
		return nil, fmt.Errorf("tenant payment repo: mark intent settled: %w", err)
	}
	if _, err = tx.Exec(ctx, `
		UPDATE billing.payment_webhook_inbox
		SET status='APPLIED', processed_at=NOW()
		WHERE provider=$1 AND provider_event_id=$2
	`, settlement.Provider, settlement.ProviderEventID); err != nil {
		return nil, fmt.Errorf("tenant payment repo: mark webhook applied: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01") {
			return nil, billingTaxonomy.ErrConflict
		}
		return nil, fmt.Errorf("tenant payment repo: commit settlement: %w", err)
	}
	return &entity.SettlementResult{
		PaymentIntentID:    intent.ID,
		WalletID:           intent.WalletID,
		OwnerID:            intent.OwnerID,
		OwnerType:          entity.OwnerTypeTenant,
		ActorID:            intent.ActorID,
		WalletStatus:       nextWalletStatus,
		CashBalance:        cashBalance,
		PromotionalBalance: promotionalBalance,
		WalletActivated:    walletActivated,
		Replayed:           replayedEvent,
	}, nil
}
