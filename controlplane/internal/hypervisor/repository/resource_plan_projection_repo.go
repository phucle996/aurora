package hypervisorRepoImpl

import (
	"context"
	"fmt"

	"controlplane/internal/config"
	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorRepoInterface "controlplane/internal/hypervisor/domain/repo"

	hypervisorTaxonomy "controlplane/internal/hypervisor/taxonomy"

	"github.com/jackc/pgx/v5"
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

// Insert serializes only revisions of the same plan. The advisory lock is a
// separate statement: the following CTE must see a fresh READ COMMITTED snapshot.
func (r *hypervisorResourcePlanProjectionRepository) Insert(
	ctx context.Context,
	projection *hypervisorEntity.HypervisorResourcePlanProjection,
) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("resource plan projection: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))",
		r.schema+":hypervisor-resource-plan:"+projection.PlanID.String()); err != nil {
		return fmt.Errorf("resource plan projection: lock plan: %w", err)
	}

	query := fmt.Sprintf(`
		WITH admissible AS MATERIALIZED (
			SELECT 1
			WHERE NOT EXISTS (
				SELECT 1 FROM %[1]s.hypervisor_resource_plan_revisions existing
				WHERE existing.revision_id = $1
				  AND ROW(existing.plan_id, existing.revision_number, existing.code,
				          existing.display_name, existing.description, existing.billing_model,
				          existing.cpu_cores, existing.memory_mib, existing.boot_disk_gib,
				          existing.content_sha256, existing.effective_from, existing.state, existing.allow_create)
				      IS DISTINCT FROM
				      ROW($2::uuid, $3::bigint, $4::varchar, $5::varchar, $6::text, $7::varchar,
				          $8::integer, $9::bigint, $10::bigint, $11::bytea, $12::timestamptz,
				          $14::varchar, $15::boolean)
			)
			AND NOT EXISTS (
				SELECT 1 FROM %[1]s.hypervisor_resource_plan_revisions other
				WHERE other.plan_id = $2 AND (
					(other.revision_number = $3 AND other.revision_id <> $1)
					OR (other.revision_number < $3 AND other.effective_from >= $12)
					OR (other.revision_number > $3 AND other.effective_from <= $12)
				)
			)
		),
		inserted AS (
			INSERT INTO %[1]s.hypervisor_resource_plan_revisions (
				revision_id, plan_id, revision_number, code, display_name, description,
				billing_model, cpu_cores, memory_mib, boot_disk_gib, content_sha256,
				effective_from, effective_to, state, allow_create, source_event_id
			)
			SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
				LEAST($13::timestamptz, (
					SELECT MIN(successor.effective_from)
					FROM %[1]s.hypervisor_resource_plan_revisions successor
					WHERE successor.plan_id = $2 AND successor.revision_number > $3
				)), $14, $15, $16
			FROM admissible
			ON CONFLICT (revision_id) DO NOTHING
			RETURNING revision_id
		),
		closed_prior AS (
			UPDATE %[1]s.hypervisor_resource_plan_revisions prior
			SET effective_to = $12
			WHERE prior.plan_id = $2 AND prior.revision_number < $3
			  AND (prior.effective_to IS NULL OR prior.effective_to > $12)
			  AND EXISTS (SELECT 1 FROM admissible)
			RETURNING revision_id
		)
		SELECT EXISTS (SELECT 1 FROM admissible) AND (
			EXISTS (SELECT 1 FROM inserted) OR EXISTS (
				SELECT 1 FROM %[1]s.hypervisor_resource_plan_revisions WHERE revision_id = $1
			)
		)
	`, r.schema)

	var accepted bool
	err = tx.QueryRow(ctx, query,
		projection.RevisionID, projection.PlanID, projection.RevisionNumber,
		projection.Code, projection.DisplayName, projection.Description, projection.BillingModel,
		projection.CPUCores, projection.MemoryMIB, projection.BootDiskGIB, projection.ContentSHA256,
		projection.EffectiveFrom, projection.EffectiveTo, projection.State, projection.AllowCreate,
		projection.SourceEventID,
	).Scan(&accepted)
	if err != nil {
		return fmt.Errorf("resource plan projection: apply revision: %w", err)
	}
	if !accepted {
		return hypervisorTaxonomy.ErrInvalidResourcePlanProjection
	}
	return tx.Commit(ctx)
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
