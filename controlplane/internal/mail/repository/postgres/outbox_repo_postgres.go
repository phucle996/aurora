package mailRepoImpl

import (
	"context"
	"fmt"

	"controlplane/internal/config"
	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"
	mailModel "controlplane/internal/mail/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MailOutboxRepoImpl struct {
	db        *pgxpool.Pool
	schema    string
	saveQuery string
}

func NewMailOutboxRepository(db *pgxpool.Pool, cfg *config.Config) mailRepoInterface.MailOutboxRepository {
	schema := cfg.SchemaSQL.Mail
	return &MailOutboxRepoImpl{
		db:     db,
		schema: schema,
		saveQuery: fmt.Sprintf(`
			INSERT INTO %s.mail_outbox_records (
				event_id, zone_id, job_topic, payload, user_id, status,
				job_version, resource_id, payload_schema_version, trace_id, idle
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			RETURNING id
		`, schema),
	}
}

// getExecutor trích xuất pgx.Tx từ context nếu có (cho chạy chung transaction), ngược lại fallback về db pool
func (r *MailOutboxRepoImpl) getExecutor(ctx context.Context) QueryExecutor {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return r.db
}

func (r *MailOutboxRepoImpl) Create(ctx context.Context, record *mailEntity.MailOutboxRecord) error {
	// Chuyển đổi từ Domain Entity sang DB Model để tách biệt logic nghiệp vụ khỏi tầng lưu trữ
	model := mailModel.OutboxEntityToModel(*record)

	// Lấy executor (giao dịch hoạt động hoặc db pool)
	executor := r.getExecutor(ctx)

	err := executor.QueryRow(ctx, r.saveQuery,
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
		// Trả lại ID tự sinh từ database về cho Domain Entity
		record.ID = model.ID
	}
	return err
}

