package iamRepoImpl

import (
	"context"
	"fmt"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamModel "controlplane/internal/iam/model"

	"github.com/jackc/pgx/v5/pgxpool"
)

// IamOutboxRepoImpl triển khai IamOutboxRepository sử dụng pgxpool làm driver PostgreSQL
type IamOutboxRepoImpl struct {
	db        *pgxpool.Pool
	schema    string
	saveQuery string
}

// NewIamOutboxRepository khởi tạo IamOutboxRepoImpl với các câu truy vấn được tối ưu hóa sẵn
func NewIamOutboxRepository(db *pgxpool.Pool, cfg *config.Config) iamRepoInterface.IamOutboxRepository {
	schema := cfg.SchemaSQL.IAM
	return &IamOutboxRepoImpl{
		db:     db,
		schema: schema,
		saveQuery: fmt.Sprintf(`
			INSERT INTO %s.iam_outbox_records (
				event_id, zone_id, job_topic, payload, user_id, status,
				job_version, resource_id, payload_schema_version, trace_id, idle
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			RETURNING id
		`, schema),
	}
}

// Create chèn một bản ghi outbox mới vào DB và cập nhật lại ID tự sinh
func (r *IamOutboxRepoImpl) Create(ctx context.Context, record *iamEntity.IamOutboxRecord) error {
	// Chuyển đổi từ thực thể Domain sang Model DB để tách biệt các lớp kiến trúc
	model := iamModel.IamOutboxEntityToModel(*record)

	err := r.db.QueryRow(ctx, r.saveQuery,
		model.EventID,
		model.ZoneID,
		model.JobTopic,
		model.Payload,
		model.UserID,
		model.Status,
		model.JobVersion,
		model.ResourceID,
		model.PayloadSchemaVersion,
		model.TraceID,
		model.Idle,
	).Scan(&model.ID)

	if err == nil {
		record.ID = model.ID
	}
	return err
}

