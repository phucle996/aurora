package repository

import (
	"context"
	"fmt"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// walletAdmissionOutboxRepository chịu trách nhiệm thực thi các câu lệnh truy vấn và cập nhật bảng Outbox `billing.wallet_admission_outbox` trong PostgreSQL:
// 1. Claim batch các bản ghi chưa publish bằng CTE (`FOR UPDATE SKIP LOCKED`).
// 2. Đánh dấu bản ghi đã publish thành công (`MarkWalletAdmissionPublished`).
// 3. Ghi nhận lỗi và tăng số lần retry nếu gửi sang Redis Stream thất bại (`RecordWalletAdmissionError`).
type walletAdmissionOutboxRepository struct {
	db *pgxpool.Pool
}

// NewWalletAdmissionOutboxRepository khởi tạo một instance mới của walletAdmissionOutboxRepository, trả về interface WalletAdmissionOutboxRepository.
func NewWalletAdmissionOutboxRepository(db *pgxpool.Pool) billingRepoInterface.WalletAdmissionOutboxRepository {
	return &walletAdmissionOutboxRepository{db: db}
}

// ClaimUnpublishedWalletAdmissionBatch sử dụng Single-Statement CTE kết hợp `FOR UPDATE SKIP LOCKED`:
// - Chọn tối đa `limit` bản ghi chưa publish (`published_at IS NULL`) và chưa bị claim bởi pod khác (hoặc claim đã hết hạn > 1 phút).
// - Gán `claim_token` và cập nhật `claimed_at = NOW()` nguyên tử trong 1 câu SQL duy nhất.
// - Ngăn chặn hoàn toàn tình trạng Deadlock hoặc xử lý trùng giữa nhiều Relay Pods đang chạy song song.
func (r *walletAdmissionOutboxRepository) ClaimUnpublishedWalletAdmissionBatch(
	ctx context.Context,
	limit int,
	claimToken uuid.UUID,
) ([]*entity.WalletAdmissionOutboxRow, error) {
	rows, err := r.db.Query(ctx, `
		WITH picked AS (
			SELECT event_id
			FROM billing.wallet_admission_outbox
			WHERE published_at IS NULL
			  AND (claimed_at IS NULL OR claimed_at < NOW() - INTERVAL '1 minute')
			ORDER BY occurred_at, event_id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE billing.wallet_admission_outbox outbox
		SET claim_token = $2, claimed_at = NOW()
		FROM picked
		WHERE outbox.event_id = picked.event_id
		RETURNING outbox.event_id, outbox.owner_id,
		          outbox.owner_type::text, outbox.wallet_version,
		          outbox.admission_mode, outbox.restriction_reason,
		          outbox.effective_at, outbox.valid_until, outbox.occurred_at,
		          outbox.claim_token`, limit, claimToken)
	if err != nil {
		return nil, fmt.Errorf("wallet admission outbox: claim batch: %w", err)
	}
	defer rows.Close()

	result := make([]*entity.WalletAdmissionOutboxRow, 0)
	for rows.Next() {
		var row entity.WalletAdmissionOutboxRow
		var ownerType string
		if err := rows.Scan(
			&row.EventID,
			&row.OwnerID,
			&ownerType,
			&row.WalletVersion,
			&row.AdmissionMode,
			&row.RestrictionReason,
			&row.EffectiveAt,
			&row.ValidUntil,
			&row.OccurredAt,
			&row.ClaimToken,
		); err != nil {
			return nil, fmt.Errorf("wallet admission outbox: scan: %w", err)
		}
		row.OwnerType = entity.OwnerType(ownerType)
		result = append(result, &row)
	}
	return result, rows.Err()
}

// MarkWalletAdmissionPublished đánh dấu bản ghi Outbox đã được phát tán thành công sang Redis Stream (`published_at = NOW()`).
func (r *walletAdmissionOutboxRepository) MarkWalletAdmissionPublished(
	ctx context.Context,
	eventID, claimToken uuid.UUID,
) error {
	if _, err := r.db.Exec(
		ctx,
		`UPDATE billing.wallet_admission_outbox
		 SET published_at = NOW(), claim_token = NULL, claimed_at = NULL, last_error = NULL
		 WHERE event_id = $1 AND claim_token = $2`,
		eventID, claimToken,
	); err != nil {
		return fmt.Errorf("wallet admission outbox: mark published: %w", err)
	}
	return nil
}

// RecordWalletAdmissionError ghi nhận lỗi phát tán và tăng biến đếm `retry_count` để phục vụ quan sát và backoff.
func (r *walletAdmissionOutboxRepository) RecordWalletAdmissionError(
	ctx context.Context,
	eventID, claimToken uuid.UUID,
	message string,
) error {
	if _, err := r.db.Exec(
		ctx,
		`UPDATE billing.wallet_admission_outbox
		 SET retry_count = retry_count + 1, claim_token = NULL, claimed_at = NULL, last_error = $1
		 WHERE event_id = $2 AND claim_token = $3`,
		message, eventID, claimToken,
	); err != nil {
		return fmt.Errorf("wallet admission outbox: record error: %w", err)
	}
	return nil
}
