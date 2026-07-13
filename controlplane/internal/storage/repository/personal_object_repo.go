package storageRepoImpl

import (
	"context"
	"fmt"

	"controlplane/internal/config"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageModel "controlplane/internal/storage/model"
	storageTaxonomy "controlplane/internal/storage/taxonomy"

	"github.com/jackc/pgx/v5/pgxpool"
)

// [COMMENT]: PersonalObjectRepoImpl thực thi interface PersonalObjectRepo cho PostgreSQL.
type PersonalObjectRepoImpl struct {
	db        *pgxpool.Pool
	storage   string // schema storage
	hierarchy string // schema hierarchy
}

// [COMMENT]: NewPersonalObjectRepo khởi tạo repository quản lý đối tượng cá nhân.
func NewPersonalObjectRepo(
	db *pgxpool.Pool,
	cfg *config.Config,
) storageRepoInterface.PersonalObjectRepo {
	return &PersonalObjectRepoImpl{
		db:        db,
		storage:   cfg.SchemaSQL.Storage,
		hierarchy: cfg.SchemaSQL.Hierarchy,
	}
}

// [COMMENT]: CreateObjectPresign kiểm tra quyền sở hữu bucket và insert Outbox Record cho các tác vụ của Object.
func (r *PersonalObjectRepoImpl) CreateObjectPresign(ctx context.Context, param *storageEntity.RequestObjectPresignParam, outbox *storageEntity.StorageOutboxRecord) error {
	// [COMMENT]: Convert Entity sang Model của CSDL để đồng bộ database tags
	mo := storageModel.OutboxEntityToModel(outbox)

	// [COMMENT]: Truy vấn CTE check IDOR: Chỉ chèn Outbox Record nếu bucket thuộc về workspace mà user là owner.
	query := fmt.Sprintf(`
		WITH check_bucket AS (
			SELECT b.id FROM %s.personal_buckets b
			JOIN %s.personal_workspaces w ON b.workspace_id = w.id
			WHERE b.id = $1 AND b.workspace_id = $2 AND w.owner_id = $3
		)
		INSERT INTO %s.storage_outbox_records (
			event_id, routing_scope, job_topic, payload, user_id, status, completed_at,
			job_version, resource_id, payload_schema_version, trace_id, idle, error_code, error_message
		)
		SELECT $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17
		FROM check_bucket
	`, r.storage, r.hierarchy, r.storage)

	res, err := r.db.Exec(ctx, query,
		param.BucketID,
		param.WorkspaceID,
		param.UserID.String(),
		mo.EventID,
		mo.RoutingScope,
		mo.JobTopic,
		mo.Payload,
		mo.UserID,
		mo.Status,
		mo.CompletedAt,
		mo.JobVersion,
		mo.ResourceID,
		mo.PayloadSchemaVersion,
		mo.TraceID,
		mo.Idle,
		mo.ErrorCode,
		mo.ErrorMessage,
	)
	if err != nil {
		return fmt.Errorf("storage repo: create object job failed: %w", err)
	}

	// [COMMENT]: Nếu không hàng nào bị tác động nghĩa là bucket IDOR check thất bại
	if res.RowsAffected() == 0 {
		return storageTaxonomy.ErrNotFound
	}
	return nil
}
