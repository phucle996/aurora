package hypervisorRepoImpl

import (
	"context"
	"fmt"

	"controlplane/internal/config"
	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorRepoInterface "controlplane/internal/hypervisor/domain/repo"

	"github.com/jackc/pgx/v5/pgxpool"
)

// hypervisorResourcePlanProjectionRepository triển khai HypervisorResourcePlanProjectionRepository trên PostgreSQL.
// Đảm bảo việc lưu trữ bản chiếu cấu hình gói tài nguyên (Resource Plan Revision) và quản lý khoảng thời gian hiệu lực (effective window).
type hypervisorResourcePlanProjectionRepository struct {
	db     *pgxpool.Pool
	schema string
}

// NewHypervisorResourcePlanProjectionRepository khởi tạo repository quản lý bản chiếu gói tài nguyên.
func NewHypervisorResourcePlanProjectionRepository(
	db *pgxpool.Pool,
	cfg *config.Config,
) hypervisorRepoInterface.HypervisorResourcePlanProjectionRepository {
	return &hypervisorResourcePlanProjectionRepository{
		db:     db,
		schema: cfg.SchemaSQL.Hypervisor,
	}
}

// Insert ghi nhận bản ghi Resource Plan Revision mới vào cơ sở dữ liệu và tự động điều chỉnh khoảng thời gian hiệu lực (effective window) qua CTE.
func (r *hypervisorResourcePlanProjectionRepository) Insert(
	ctx context.Context,
	projection *hypervisorEntity.HypervisorResourcePlanProjection,
) error {
	// [COMMENT]: Giao dịch CTE nguyên tử 3 bước:
	// 1. inserted: Chèn revision mới (chống trùng lặp qua ON CONFLICT (revision_id) DO NOTHING).
	// 2. closed_prior: Tự động đóng khoảng hiệu lực (effective_to = effective_from của bản ghi mới) cho các revision cũ hơn chưa có ngày kết thúc.
	// 3. closed_from_successor: Đảm bảo tính nhất quán nếu nhận event lệch thứ tự (out-of-order) bằng cách gán effective_to bằng MIN(effective_from) của revision kế tiếp nếu đã tồn tại.
	query := fmt.Sprintf(`
		WITH inserted AS (
			INSERT INTO %s.hypervisor_resource_plan_revisions (
				revision_id,
				plan_id,
				revision_number,
				code,
				display_name,
				description,
				billing_model,
				cpu_cores,
				memory_mib,
				boot_disk_gib,
				content_sha256,
				effective_from,
				effective_to,
				state,
				allow_create,
				source_event_id
			) VALUES (
				$1,  -- revision_id
				$2,  -- plan_id
				$3,  -- revision_number
				$4,  -- code
				$5,  -- display_name
				$6,  -- description
				$7,  -- billing_model
				$8,  -- cpu_cores
				$9,  -- memory_mib
				$10, -- boot_disk_gib
				$11, -- content_sha256
				$12, -- effective_from
				$13, -- effective_to
				$14, -- state
				$15, -- allow_create
				$16  -- source_event_id
			)
			ON CONFLICT (revision_id) DO NOTHING
			RETURNING revision_id
		),
		closed_prior AS (
			UPDATE %s.hypervisor_resource_plan_revisions existing
			SET effective_to = $12
			FROM inserted
			WHERE existing.plan_id         = $2
			  AND existing.revision_number < $3
			  AND existing.effective_to IS NULL
		),
		closed_from_successor AS (
			UPDATE %s.hypervisor_resource_plan_revisions current
			SET effective_to = (
				SELECT MIN(successor.effective_from)
				FROM %s.hypervisor_resource_plan_revisions successor
				WHERE successor.plan_id         = current.plan_id
				  AND successor.revision_number > current.revision_number
			)
			WHERE current.revision_id = $1
			  AND current.effective_to IS NULL
		)
		SELECT 1
	`, r.schema, r.schema, r.schema, r.schema)

	_, err := r.db.Exec(
		ctx,
		query,
		projection.RevisionID,
		projection.PlanID,
		projection.RevisionNumber,
		projection.Code,
		projection.DisplayName,
		projection.Description,
		projection.BillingModel,
		projection.CPUCores,
		projection.MemoryMIB,
		projection.BootDiskGIB,
		projection.ContentSHA256,
		projection.EffectiveFrom,
		projection.EffectiveTo,
		projection.State,
		projection.AllowCreate,
		projection.SourceEventID,
	)
	if err != nil {
		return fmt.Errorf("Hypervisor resource plan projection repo: insert revision: %w", err)
	}

	return nil
}

// ListEffective truy vấn danh sách tất cả các Resource Plan Revision đang trong khoảng thời gian có hiệu lực.
func (r *hypervisorResourcePlanProjectionRepository) ListEffective(
	ctx context.Context,
) ([]hypervisorEntity.HypervisorResourcePlanProjection, error) {
	query := fmt.Sprintf(`
		SELECT
			plan_id,
			revision_id,
			revision_number,
			code,
			display_name,
			description,
			billing_model,
			cpu_cores,
			memory_mib,
			boot_disk_gib,
			content_sha256,
			effective_from,
			effective_to,
			state,
			allow_create,
			source_event_id
		FROM %s.hypervisor_resource_plan_revisions
		WHERE effective_from <= NOW()
		  AND (effective_to IS NULL OR NOW() < effective_to)
		ORDER BY plan_id, revision_number
	`, r.schema)

	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("Hypervisor resource plan projection repo: list effective revisions: %w", err)
	}
	defer rows.Close()

	projections := make([]hypervisorEntity.HypervisorResourcePlanProjection, 0)
	for rows.Next() {
		var projection hypervisorEntity.HypervisorResourcePlanProjection
		if err := rows.Scan(
			&projection.PlanID,
			&projection.RevisionID,
			&projection.RevisionNumber,
			&projection.Code,
			&projection.DisplayName,
			&projection.Description,
			&projection.BillingModel,
			&projection.CPUCores,
			&projection.MemoryMIB,
			&projection.BootDiskGIB,
			&projection.ContentSHA256,
			&projection.EffectiveFrom,
			&projection.EffectiveTo,
			&projection.State,
			&projection.AllowCreate,
			&projection.SourceEventID,
		); err != nil {
			return nil, fmt.Errorf("Hypervisor resource plan projection repo: scan effective revision: %w", err)
		}
		projections = append(projections, projection)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Hypervisor resource plan projection repo: iterate effective revisions: %w", err)
	}

	return projections, nil
}
