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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// hypervisorResourcePlanRepository chịu trách nhiệm quản lý tầng dữ liệu bền vững (Persistence Layer)
// cho các gói tài nguyên Hypervisor (Resource Plans), lịch sử phiên bản (Revisions) và Outbox Events.
type hypervisorResourcePlanRepository struct {
	db *pgxpool.Pool
}

// NewHypervisorResourcePlanRepository khởi tạo instance của HypervisorResourcePlanRepository.
func NewHypervisorResourcePlanRepository(db *pgxpool.Pool) billingRepoInterface.HypervisorResourcePlanRepository {
	return &hypervisorResourcePlanRepository{db: db}
}

// GetHypervisorResourcePlanIdentity truy vấn định danh cơ bản của gói tài nguyên đang ở trạng thái ACTIVE.
func (r *hypervisorResourcePlanRepository) GetHypervisorResourcePlanIdentity(
	ctx context.Context,
	planID uuid.UUID,
) (*entity.HypervisorResourcePlanRevision, error) {
	var plan entity.HypervisorResourcePlanRevision

	// [COMMENT]: Chỉ đọc gói tài nguyên đang ở trạng thái ACTIVE; không trả về các gói đã bị archived hoặc disabled.
	query := `
		SELECT id, code, display_name, description
		FROM billing.hypervisor_resource_plans
		WHERE id = $1 AND status = 'ACTIVE'
	`
	err := r.db.QueryRow(ctx, query, planID).Scan(
		&plan.PlanID,
		&plan.Code,
		&plan.DisplayName,
		&plan.Description,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrHypervisorResourcePlanNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("Hypervisor resource plan repo: read plan identity: %w", err)
	}

	return &plan, nil
}

// CreateHypervisorResourcePlan thực hiện tạo mới gói tài nguyên, khởi tạo phiên bản đầu tiên (Revision 1)
// và ghi bản ghi Outbox trong một câu lệnh CTE nguyên tử duy nhất (CTE-First).
func (r *hypervisorResourcePlanRepository) CreateHypervisorResourcePlan(
	ctx context.Context,
	command entity.CreateHypervisorResourcePlanCommand,
) (*entity.HypervisorResourcePlanRevision, error) {
	var plan entity.HypervisorResourcePlanRevision

	// [COMMENT]: Sử dụng CTE lồng nhau để đảm bảo tính nguyên tử tuyệt đối mà không cần quản lý giao dịch tường minh.
	// 1. inserted_plan: Chèn thông tin danh mục gói tài nguyên.
	// 2. inserted_revision: Chèn phiên bản revision 1; tự động gán trạng thái ACTIVE nếu effective_from <= NOW(), ngược lại là SCHEDULED.
	// 3. inserted_outbox: Ghi sự kiện vào Outbox để resource-plan relay phát sang Controlplane.
	query := `
		WITH inserted_plan AS (
			INSERT INTO billing.hypervisor_resource_plans (
				id, code, display_name, description
			)
			VALUES ($1, $2, $3, $4)
			RETURNING id, code, display_name, description, created_at
		),
		inserted_revision AS (
			INSERT INTO billing.hypervisor_resource_plan_revisions (
				id, plan_id, revision_number, status, billing_model,
				cpu_cores, memory_mib, boot_disk_gib, content_sha256,
				effective_from, change_reason, created_by
			)
			SELECT
				$5, id, 1,
				CASE WHEN $10 <= NOW() THEN 'ACTIVE' ELSE 'SCHEDULED' END,
				'LIMIT_HOURLY', $6, $7, $8, $9, $10, $11, $12
			FROM inserted_plan
			RETURNING id, plan_id, revision_number, status, billing_model,
			          cpu_cores, memory_mib, boot_disk_gib, content_sha256,
			          effective_from, effective_to, created_at
		),
		inserted_outbox AS (
			INSERT INTO billing.hypervisor_resource_plan_outbox (
				id, event_id, plan_id, revision_id, payload
			)
			SELECT $13, $14, plan_id, id, $15
			FROM inserted_revision
			RETURNING id
		)
		SELECT
			revision.plan_id, revision.id, revision.revision_number,
			plan.code, plan.display_name, plan.description,
			revision.billing_model, revision.cpu_cores, revision.memory_mib, revision.boot_disk_gib,
			revision.content_sha256, revision.effective_from, revision.effective_to, revision.status, revision.created_at
		FROM inserted_revision revision
		JOIN inserted_plan plan ON plan.id = revision.plan_id
		JOIN inserted_outbox ON TRUE
	`

	err := r.db.QueryRow(
		ctx,
		query,
		command.PlanID,
		command.Code,
		command.DisplayName,
		command.Description,
		command.RevisionID,
		command.CPUCores,
		command.MemoryMIB,
		command.BootDiskGIB,
		command.ContentSHA256,
		command.EffectiveFrom,
		command.ChangeReason,
		command.CreatedBy,
		uuid.New(),
		command.EventID,
		command.OutboxPayload,
	).Scan(
		&plan.PlanID,
		&plan.RevisionID,
		&plan.RevisionNumber,
		&plan.Code,
		&plan.DisplayName,
		&plan.Description,
		&plan.BillingModel,
		&plan.CPUCores,
		&plan.MemoryMIB,
		&plan.BootDiskGIB,
		&plan.ContentSHA256,
		&plan.EffectiveFrom,
		&plan.EffectiveTo,
		&plan.State,
		&plan.CreatedAt,
	)

	if err != nil {
		var pgErr *pgconn.PgError
		// [COMMENT]: Mã 23505 (Unique Violation) xảy ra khi trùng mã plan code hoặc ID đã tồn tại.
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, billingTaxonomy.ErrHypervisorResourcePlanConflict
		}
		return nil, fmt.Errorf("Hypervisor resource plan repo: create plan: %w", err)
	}

	return &plan, nil
}

// PublishHypervisorResourcePlanRevision xuất bản một phiên bản cấu hình mới (Revision N+1) cho gói tài nguyên:
// - Sử dụng Advisory Lock phân tán để ngăn chặn race condition xuất bản đồng thời.
// - Khóa độc quyền bản ghi kế hoạch và phiên bản mới nhất.
// - Kiểm tra Optimistic Concurrency Control (OCC) và tính tăng dần của mốc thời gian hiệu lực.
// - Đóng phiên bản cũ (effective_to = new.effective_from, status = SUPERSEDED).
// - Chèn phiên bản mới và ghi bản ghi Outbox trong cùng một transaction.
func (r *hypervisorResourcePlanRepository) PublishHypervisorResourcePlanRevision(
	ctx context.Context,
	command entity.PublishHypervisorResourcePlanRevisionCommand,
) (*entity.HypervisorResourcePlanRevision, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("Hypervisor resource plan repo: begin publish revision: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	// [COMMENT]: Khóa Advisory Lock theo PlanID trong transaction để serialize toàn bộ luồng publish của gói tài nguyên này.
	lockKey := "hypervisor-resource-plan:" + command.PlanID.String()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return nil, fmt.Errorf("Hypervisor resource plan repo: lock plan: %w", err)
	}

	// [COMMENT]: Khóa hàng dữ liệu gói tài nguyên (FOR UPDATE) để đảm bảo gói còn ACTIVE và không bị xóa đồng thời.
	var planID uuid.UUID
	var code, displayName, description string
	lockPlanQuery := `
		SELECT id, code, display_name, description
		FROM billing.hypervisor_resource_plans
		WHERE id = $1 AND status = 'ACTIVE'
		FOR UPDATE
	`
	err = tx.QueryRow(ctx, lockPlanQuery, command.PlanID).Scan(&planID, &code, &displayName, &description)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrHypervisorResourcePlanNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("Hypervisor resource plan repo: lock plan row: %w", err)
	}

	// [COMMENT]: Khóa và đọc phiên bản hợp lệ mới nhất để kiểm tra số revision và mốc thời gian hiệu lực.
	var latest int64
	var latestEffective time.Time
	lockLatestQuery := `
		SELECT revision_number, effective_from
		FROM billing.hypervisor_resource_plan_revisions
		WHERE plan_id = $1 AND status <> 'CANCELLED'
		ORDER BY revision_number DESC
		LIMIT 1
		FOR UPDATE
	`
	err = tx.QueryRow(ctx, lockLatestQuery, planID).Scan(&latest, &latestEffective)
	if err != nil {
		return nil, fmt.Errorf("Hypervisor resource plan repo: lock latest revision: %w", err)
	}

	// [COMMENT]: Rào cản OCC (Optimistic Concurrency Control):
	// 1. Số revision hiện tại phải khớp chính xác với ExpectedLatestRevision.
	// 2. Thời điểm hiệu lực mới (EffectiveFrom) bắt buộc phải sau thời điểm hiệu lực của bản ghi trước đó.
	if latest != command.ExpectedLatestRevision || !command.EffectiveFrom.After(latestEffective) {
		return nil, billingTaxonomy.ErrHypervisorResourcePlanConflict
	}

	// [COMMENT]: Đóng khoảng thời gian hiệu lực của phiên bản trước đó và chuyển sang SUPERSEDED nếu đang ACTIVE.
	closePriorQuery := `
		UPDATE billing.hypervisor_resource_plan_revisions
		SET effective_to = $1,
		    status = CASE WHEN status = 'ACTIVE' THEN 'SUPERSEDED' ELSE status END
		WHERE plan_id = $2 AND revision_number = $3 AND effective_to IS NULL
	`
	if _, err = tx.Exec(ctx, closePriorQuery, command.EffectiveFrom, planID, latest); err != nil {
		return nil, fmt.Errorf("Hypervisor resource plan repo: close prior revision: %w", err)
	}

	// [COMMENT]: Xác định trạng thái ban đầu của phiên bản mới: ACTIVE nếu đã đến thời điểm hiệu lực, ngược lại là SCHEDULED.
	status := "SCHEDULED"
	if !command.EffectiveFrom.After(time.Now().UTC()) {
		status = "ACTIVE"
	}

	// [COMMENT]: Chèn phiên bản mới với revision_number = latest + 1.
	insertRevisionQuery := `
		INSERT INTO billing.hypervisor_resource_plan_revisions (
			id, plan_id, revision_number, status, billing_model,
			cpu_cores, memory_mib, boot_disk_gib, content_sha256,
			effective_from, change_reason, created_by
		)
		VALUES ($1, $2, $3, $4, 'LIMIT_HOURLY', $5, $6, $7, $8, $9, $10, $11)
	`
	if _, err = tx.Exec(
		ctx,
		insertRevisionQuery,
		command.RevisionID,
		planID,
		latest+1,
		status,
		command.CPUCores,
		command.MemoryMIB,
		command.BootDiskGIB,
		command.ContentSHA256,
		command.EffectiveFrom,
		command.ChangeReason,
		command.CreatedBy,
	); err != nil {
		return nil, fmt.Errorf("Hypervisor resource plan repo: insert revision: %w", err)
	}

	// [COMMENT]: Ghi bản ghi Outbox để đồng bộ hóa phiên bản kế hoạch sang Controlplane / Zone Projections.
	insertOutboxQuery := `
		INSERT INTO billing.hypervisor_resource_plan_outbox (
			id, event_id, plan_id, revision_id, payload
		)
		VALUES ($1, $2, $3, $4, $5)
	`
	if _, err = tx.Exec(ctx, insertOutboxQuery, uuid.New(), command.EventID, planID, command.RevisionID, command.OutboxPayload); err != nil {
		return nil, fmt.Errorf("Hypervisor resource plan repo: insert outbox: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("Hypervisor resource plan repo: commit revision: %w", err)
	}

	return &entity.HypervisorResourcePlanRevision{
		PlanID:         planID,
		RevisionID:     command.RevisionID,
		RevisionNumber: latest + 1,
		Code:           code,
		DisplayName:    displayName,
		Description:    description,
		BillingModel:   "LIMIT_HOURLY",
		CPUCores:       command.CPUCores,
		MemoryMIB:      command.MemoryMIB,
		BootDiskGIB:    command.BootDiskGIB,
		ContentSHA256:  command.ContentSHA256,
		EffectiveFrom:  command.EffectiveFrom,
		State:          status,
	}, nil
}

// ListEffectiveHypervisorResourcePlans truy vấn danh sách các gói tài nguyên và phiên bản có hiệu lực
// tại một thời điểm cụ thể (Point-in-Time Query: effective_from <= at < effective_to).
func (r *hypervisorResourcePlanRepository) ListEffectiveHypervisorResourcePlans(
	ctx context.Context,
	query entity.HypervisorResourcePlanListQuery,
) ([]entity.HypervisorResourcePlanRevision, bool, error) {
	// [COMMENT]: Sử dụng CTE để lọc chính xác phiên bản có hiệu lực tại thời điểm query.At,
	// áp dụng giới hạn Limit + 1 để xác định cờ hasMore phân trang mà không cần câu lệnh COUNT riêng biệt.
	listQuery := `
		WITH effective AS (
			SELECT
				plan.id AS plan_id,
				revision.id AS revision_id,
				revision.revision_number,
				plan.code,
				plan.display_name,
				plan.description,
				revision.billing_model,
				revision.cpu_cores,
				revision.memory_mib,
				revision.boot_disk_gib,
				revision.content_sha256,
				revision.effective_from,
				revision.effective_to,
				revision.status,
				revision.created_at
			FROM billing.hypervisor_resource_plans plan
			JOIN billing.hypervisor_resource_plan_revisions revision
			  ON revision.plan_id = plan.id
			WHERE plan.status = 'ACTIVE'
			  AND revision.status <> 'CANCELLED'
			  AND revision.effective_from <= $1
			  AND (revision.effective_to IS NULL OR $1 < revision.effective_to)
		),
		bounded AS (
			SELECT *
			FROM effective
			ORDER BY code
			LIMIT $2
		)
		SELECT
			plan_id, revision_id, revision_number, code, display_name, description,
			billing_model, cpu_cores, memory_mib, boot_disk_gib, content_sha256,
			effective_from, effective_to, status, created_at
		FROM bounded
		ORDER BY code
	`

	rows, err := r.db.Query(ctx, listQuery, query.At, query.Limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("Hypervisor resource plan repo: list effective plans: %w", err)
	}
	defer rows.Close()

	plans := make([]entity.HypervisorResourcePlanRevision, 0, query.Limit+1)
	for rows.Next() {
		var plan entity.HypervisorResourcePlanRevision
		if err := rows.Scan(
			&plan.PlanID,
			&plan.RevisionID,
			&plan.RevisionNumber,
			&plan.Code,
			&plan.DisplayName,
			&plan.Description,
			&plan.BillingModel,
			&plan.CPUCores,
			&plan.MemoryMIB,
			&plan.BootDiskGIB,
			&plan.ContentSHA256,
			&plan.EffectiveFrom,
			&plan.EffectiveTo,
			&plan.State,
			&plan.CreatedAt,
		); err != nil {
			return nil, false, fmt.Errorf("Hypervisor resource plan repo: scan effective plan: %w", err)
		}
		plans = append(plans, plan)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("Hypervisor resource plan repo: iterate effective plans: %w", err)
	}

	hasMore := len(plans) > query.Limit
	if hasMore {
		plans = plans[:query.Limit]
	}

	return plans, hasMore, nil
}

// ClaimHypervisorResourcePlanOutbox nhận và khóa một lô bản ghi Outbox chưa phát hành (SKIP LOCKED)
// để worker tiến hành publish sang Redis Stream:
func (r *hypervisorResourcePlanRepository) ClaimHypervisorResourcePlanOutbox(
	ctx context.Context,
	claimToken uuid.UUID,
	leaseUntil time.Time,
	limit int,
) ([]entity.HypervisorResourcePlanOutboxRow, error) {
	// [COMMENT]: Sử dụng SKIP LOCKED trong CTE candidates để nhiều outbox worker pods có thể tranh chấp
	// nhận batch đồng thời mà không bị deadlock hay chặn lẫn nhau.
	query := `
		WITH candidates AS (
			SELECT id
			FROM billing.hypervisor_resource_plan_outbox
			WHERE published_at IS NULL
			  AND available_at <= NOW()
			  AND (lease_until IS NULL OR lease_until < NOW())
			ORDER BY occurred_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT $3
		),
		claimed AS (
			UPDATE billing.hypervisor_resource_plan_outbox outbox
			SET claim_token = $1, lease_until = $2
			FROM candidates
			WHERE outbox.id = candidates.id
			RETURNING outbox.id, outbox.event_id, outbox.payload, outbox.claim_token, outbox.retry_count, outbox.occurred_at
		)
		SELECT id, event_id, payload, claim_token, retry_count
		FROM claimed
		ORDER BY occurred_at, id
	`

	rows, err := r.db.Query(ctx, query, claimToken, leaseUntil, limit)
	if err != nil {
		return nil, fmt.Errorf("Hypervisor resource plan repo: claim outbox: %w", err)
	}
	defer rows.Close()

	result := make([]entity.HypervisorResourcePlanOutboxRow, 0, limit)
	for rows.Next() {
		var row entity.HypervisorResourcePlanOutboxRow
		if err := rows.Scan(&row.ID, &row.EventID, &row.Payload, &row.ClaimToken, &row.RetryCount); err != nil {
			return nil, fmt.Errorf("Hypervisor resource plan repo: scan outbox: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Hypervisor resource plan repo: iterate outbox: %w", err)
	}

	return result, nil
}

// MarkHypervisorResourcePlanOutboxPublished đánh dấu bản ghi Outbox đã được phát hành thành công:
func (r *hypervisorResourcePlanRepository) MarkHypervisorResourcePlanOutboxPublished(
	ctx context.Context,
	id, claimToken uuid.UUID,
) error {
	// [COMMENT]: Xác thực cả id và claimToken để bảo đảm worker vẫn còn giữ hợp đồng thuê (Lease) khi hoàn tất.
	query := `
		UPDATE billing.hypervisor_resource_plan_outbox
		SET published_at = NOW(), claim_token = NULL, lease_until = NULL, last_error = NULL
		WHERE id = $1 AND claim_token = $2 AND lease_until > NOW() AND published_at IS NULL
	`
	result, err := r.db.Exec(ctx, query, id, claimToken)
	if err != nil {
		return fmt.Errorf("Hypervisor resource plan repo: mark outbox published: %w", err)
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("Hypervisor resource plan repo: outbox claim lost for %s", id)
	}

	return nil
}

// RetryHypervisorResourcePlanOutbox ghi nhận lỗi tạm thời và lên lịch thử lại bản ghi Outbox:
func (r *hypervisorResourcePlanRepository) RetryHypervisorResourcePlanOutbox(
	ctx context.Context,
	id, claimToken uuid.UUID,
	lastError string,
	availableAt time.Time,
) error {
	// [COMMENT]: Tăng retry_count, lưu last_error và dời mốc available_at theo thuật toán backoff; giải phóng claim_token để pod khác thử lại sau.
	query := `
		UPDATE billing.hypervisor_resource_plan_outbox
		SET retry_count = retry_count + 1,
		    last_error = $3,
		    available_at = $4,
		    claim_token = NULL,
		    lease_until = NULL
		WHERE id = $1 AND claim_token = $2 AND published_at IS NULL
	`
	_, err := r.db.Exec(ctx, query, id, claimToken, lastError, availableAt)
	if err != nil {
		return fmt.Errorf("Hypervisor resource plan repo: retry outbox: %w", err)
	}

	return nil
}

func (r *hypervisorResourcePlanRepository) ListPlans(ctx context.Context, query entity.HypervisorResourcePlanAdminQuery) ([]entity.HypervisorResourcePlanAdminItem, bool, error) {
	rows, err := r.db.Query(ctx, `
		WITH page AS (
			SELECT id, code, display_name, description, status
			FROM billing.hypervisor_resource_plans
			WHERE id > $1 ORDER BY id LIMIT $2
		)
		SELECT page.id, page.code, page.display_name, page.description, page.status,
			COALESCE(MAX(revision.revision_number) FILTER (WHERE revision.status <> 'CANCELLED'), 0),
			COALESCE(MAX(revision.revision_number) FILTER (
				WHERE revision.status <> 'CANCELLED' AND revision.effective_from <= $3
				AND (revision.effective_to IS NULL OR $3 < revision.effective_to)), 0)
		FROM page LEFT JOIN billing.hypervisor_resource_plan_revisions revision ON revision.plan_id = page.id
		GROUP BY page.id, page.code, page.display_name, page.description, page.status
		ORDER BY page.id
	`, query.After, query.Limit+1, query.At)
	if err != nil {
		return nil, false, fmt.Errorf("resource plan: list: %w", err)
	}
	defer rows.Close()
	items := make([]entity.HypervisorResourcePlanAdminItem, 0, query.Limit+1)
	for rows.Next() {
		var item entity.HypervisorResourcePlanAdminItem
		if err := rows.Scan(&item.PlanID, &item.Code, &item.DisplayName, &item.Description, &item.State, &item.LatestRevisionNumber, &item.EffectiveRevisionNumber); err != nil {
			return nil, false, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(items) > query.Limit
	if more {
		items = items[:query.Limit]
	}
	return items, more, nil
}
func (r *hypervisorResourcePlanRepository) ListRevisions(ctx context.Context, query entity.HypervisorResourcePlanHistoryQuery) ([]entity.HypervisorResourcePlanHistoryItem, bool, error) {
	rows, err := r.db.Query(ctx, `
		WITH latest AS (
			SELECT MAX(revision_number) AS number
			FROM billing.hypervisor_resource_plan_revisions WHERE plan_id=$1 AND status <> 'CANCELLED'
		), page AS (
			SELECT id, plan_id, revision_number, cpu_cores, memory_mib, boot_disk_gib,
				effective_from, effective_to, status, change_reason
			FROM billing.hypervisor_resource_plan_revisions
			WHERE plan_id=$1 AND ($2::bigint=0 OR revision_number < $2)
			ORDER BY revision_number DESC LIMIT $3
		)
		SELECT page.plan_id, page.id, page.revision_number, page.cpu_cores, page.memory_mib,
			page.boot_disk_gib, page.effective_from, page.effective_to, page.status, page.change_reason,
			COALESCE(page.revision_number=latest.number, FALSE),
			page.status <> 'CANCELLED' AND page.effective_from <= $4 AND (page.effective_to IS NULL OR $4 < page.effective_to)
		FROM page CROSS JOIN latest ORDER BY page.revision_number DESC
	`, query.PlanID, query.Before, query.Limit+1, query.At)
	if err != nil {
		return nil, false, fmt.Errorf("resource plan: history: %w", err)
	}
	defer rows.Close()
	items := make([]entity.HypervisorResourcePlanHistoryItem, 0, query.Limit+1)
	for rows.Next() {
		var item entity.HypervisorResourcePlanHistoryItem
		var effectiveTo pgtype.Timestamptz
		if err := rows.Scan(&item.PlanID, &item.RevisionID, &item.RevisionNumber, &item.CPUCores, &item.MemoryMIB, &item.BootDiskGIB, &item.EffectiveFrom, &effectiveTo, &item.State, &item.ChangeReason, &item.IsLatest, &item.IsEffective); err != nil {
			return nil, false, err
		}
		if effectiveTo.Valid {
			item.EffectiveTo = effectiveTo.Time
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(items) > query.Limit
	if more {
		items = items[:query.Limit]
	}
	return items, more, nil
}
