package repository

import (
	"context"
	"fmt"

	billingRepoInterface "cost-manager/api/internal/domain/repo"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// tenantAccountRepository chịu trách nhiệm thực thi các thao tác cơ sở dữ liệu PostgreSQL cho tài khoản tổ chức (Tenant):
// - Khởi tạo ví tiền tổ chức nguyên tử kèm Transactional Inbox và Outbox Admission (`ApplyTenantWalletProvision`).
type tenantAccountRepository struct {
	db *pgxpool.Pool
}

// NewTenantAccountRepository khởi tạo một instance mới của tenantAccountRepository, trả về interface TenantAccountRepository.
func NewTenantAccountRepository(db *pgxpool.Pool) billingRepoInterface.TenantAccountRepository {
	return &tenantAccountRepository{db: db}
}

// ApplyTenantWalletProvision thực thi toàn bộ quy trình tạo ví tổ chức nguyên tử trong 1 câu lệnh CTE duy nhất:
// 1. `inbox_upsert`: Chèn bản ghi inbox với status='APPLIED'. Nếu đã tồn tại `event_id` thì bỏ qua (ON CONFLICT DO NOTHING).
// 2. `inbox_replay`: Nếu `inbox_upsert` không chèn mới (do trùng event_id), đọc lại bản ghi inbox đã lưu để kiểm tra payload hash và actor_user_id.
// 3. `effective_inbox`: Hợp nhất kết quả từ 2 nhánh trên.
// 4. `wallet_upsert`: Nếu inbox là mới (`is_new = true`), chèn ví tổ chức mới (`billing.wallets`) trạng thái `PENDING_ACTIVATION`, `$0.00 USD`.
// 5. `existing_wallet`: Nếu ví đã tồn tại trước đó, lấy `id` của ví hiện tại.
// 6. `admission_outbox_insert`: Ghi nhận tín hiệu `SUSPEND_BILLABLE` vào `billing.wallet_admission_outbox` nếu ví vừa được tạo mới.
func (r *tenantAccountRepository) ApplyTenantWalletProvision(
	ctx context.Context,
	eventID uuid.UUID,
	tenantID uuid.UUID,
	actorID uuid.UUID,
	payloadHash string,
) error {
	const query = `
		WITH inbox_upsert AS (
			INSERT INTO billing.tenant_wallet_provision_inbox (
				event_id, schema_version, tenant_id, actor_user_id, payload_hash, status, processed_at
			)
			VALUES ($1, 1, $2, $3, $4, 'APPLIED', NOW())
			ON CONFLICT (event_id) DO NOTHING
			RETURNING event_id, tenant_id, actor_user_id, payload_hash, TRUE AS is_new
		),
		inbox_replay AS (
			SELECT event_id, tenant_id, actor_user_id, payload_hash, FALSE AS is_new
			FROM billing.tenant_wallet_provision_inbox
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
				$5, $2, 'TENANT'::billing.owner_type, 'USD', 0, 0,
				'PENDING_ACTIVATION', 'NOT_ACTIVATED', NOW()
			FROM inbox_upsert
			ON CONFLICT (owner_id, owner_type, currency) DO NOTHING
			RETURNING id, TRUE AS wallet_created
		),
		existing_wallet AS (
			SELECT id, FALSE AS wallet_created
			FROM billing.wallets
			WHERE owner_id = $2
			  AND owner_type = 'TENANT'::billing.owner_type
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
				$6, ew.id, $2, 'TENANT', 1,
				'SUSPEND_BILLABLE', 'NOT_ACTIVATED', NOW()
			FROM effective_wallet ew
			WHERE ew.wallet_created = TRUE
			ON CONFLICT (event_id) DO NOTHING
			RETURNING event_id
		)
		SELECT
			ei.is_new,
			ei.tenant_id,
			ei.actor_user_id,
			ei.payload_hash
		FROM effective_inbox ei;
	`

	var isNew bool
	var storedTenantID, storedActorID uuid.UUID
	var storedHash string

	err := r.db.QueryRow(
		ctx,
		query,
		eventID,
		tenantID,
		actorID,
		payloadHash,
		uuid.New(), // $5: walletID
		uuid.New(), // $6: outboxEventID
	).Scan(&isNew, &storedTenantID, &storedActorID, &storedHash)
	if err != nil {
		return fmt.Errorf("tenant account repo: apply wallet provision CTE: %w", err)
	}

	if !isNew {
		if storedTenantID != tenantID || storedActorID != actorID || storedHash != payloadHash {
			return fmt.Errorf("tenant account repo: event_id %s reused with different payload", eventID)
		}
	}
	return nil
}
