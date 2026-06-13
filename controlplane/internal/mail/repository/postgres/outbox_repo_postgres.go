package mailRepoImpl

import (
	"context"
	"fmt"

	"controlplane/internal/config"
	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"
	mailModel "controlplane/internal/mail/model"

	"github.com/jackc/pgx/v5/pgxpool"
)

type MailOutboxRepoImpl struct {
	db                 *pgxpool.Pool
	schema             string
	saveQuery          string
	fetchPendingQuery  string
	markPublishedQuery string
	markFailedQuery    string
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
		fetchPendingQuery: fmt.Sprintf(`
			SELECT id, event_id, zone_id, job_topic, payload, user_id, status,
			       job_version, resource_id, payload_schema_version, trace_id, idle
			FROM %s.mail_outbox_records
			WHERE status = 'PENDING'
			ORDER BY id ASC
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		`, schema),
		markPublishedQuery: fmt.Sprintf(`
			UPDATE %s.mail_outbox_records
			SET status = 'PUBLISHED', completed_at = NOW()
			WHERE id = $1
		`, schema),
		markFailedQuery: fmt.Sprintf(`
			UPDATE %s.mail_outbox_records
			SET status = 'FAILED', error_message = $2, completed_at = NOW()
			WHERE id = $1
		`, schema),
	}
}

func (r *MailOutboxRepoImpl) Create(ctx context.Context, record *mailEntity.MailOutboxRecord) error {
	// Chuyển đổi từ Domain Entity sang DB Model để tách biệt logic nghiệp vụ khỏi tầng lưu trữ
	model := mailModel.OutboxEntityToModel(*record)

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
		// Trả lại ID tự sinh từ database về cho Domain Entity
		record.ID = model.ID
	}
	return err
}

func (r *MailOutboxRepoImpl) FetchPendingForUpdate(ctx context.Context, limit int) ([]*mailEntity.MailOutboxRecord, error) {
	rows, err := r.db.Query(ctx, r.fetchPendingQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []*mailEntity.MailOutboxRecord
	for rows.Next() {
		var model mailModel.MailOutboxRecord
		// Quét dữ liệu trực tiếp vào struct DB Model bao gồm cả payload và user_id
		err := rows.Scan(
			&model.ID,
			&model.EventID,
			&model.ZoneID,
			&model.JobTopic,
			&model.Payload,
			&model.UserID,
			&model.Status,
			&model.JobVersion,
			&model.ResourceID,
			&model.PayloadSchemaVersion,
			&model.TraceID,
			&model.Idle,
		)
		if err != nil {
			return nil, err
		}

		// Chuyển đổi DB Model thành Domain Entity trước khi trả về tầng Service
		entity := mailModel.OutboxModelToEntity(model)
		records = append(records, &entity)
	}
	return records, rows.Err()
}

func (r *MailOutboxRepoImpl) MarkPublished(ctx context.Context, id int64) error {
	_, err := r.db.Exec(ctx, r.markPublishedQuery, id)
	return err
}

func (r *MailOutboxRepoImpl) MarkFailed(ctx context.Context, id int64, reason string) error {
	_, err := r.db.Exec(ctx, r.markFailedQuery, id, reason)
	return err
}
