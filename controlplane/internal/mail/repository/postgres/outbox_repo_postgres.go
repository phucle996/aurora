package mailRepoImpl

import (
	"context"
	"fmt"

	"controlplane/internal/config"
	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"

	"github.com/google/uuid"
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
				event_id, zone_id, job_topic, payload_json, status, created_at,
				job_version, resource_id, payload_schema_version, trace_id, idle,
				error_code, error_message
			)
			VALUES ($1, $2, $3, $4, $5, NOW(), $6, $7, $8, $9, $10, $11, $12)
			RETURNING id
		`, schema),
		fetchPendingQuery: fmt.Sprintf(`
			SELECT id, event_id, zone_id, job_topic, payload_json, status, attempts, last_attempt, created_at,
			       job_version, resource_id, payload_schema_version, trace_id, idle, error_code, error_message
			FROM %s.mail_outbox_records
			WHERE status = 'PENDING'
			ORDER BY created_at ASC
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		`, schema),
		markPublishedQuery: fmt.Sprintf(`
			UPDATE %s.mail_outbox_records
			SET status = 'PUBLISHED', last_attempt = NOW()
			WHERE id = $1
		`, schema),
		markFailedQuery: fmt.Sprintf(`
			UPDATE %s.mail_outbox_records
			SET status = 'FAILED', attempts = attempts + 1, last_attempt = NOW()
			WHERE id = $1
		`, schema),
	}
}

func (r *MailOutboxRepoImpl) Save(ctx context.Context, record *mailEntity.MailOutboxRecord) error {
	err := r.db.QueryRow(ctx, r.saveQuery,
		record.EventID,
		record.ZoneID,
		record.JobTopic,
		record.PayloadJSON,
		string(record.Status),
		record.JobVersion,
		record.ResourceID,
		record.PayloadSchemaVersion,
		record.TraceID,
		record.Idle,
		record.ErrorCode,
		record.ErrorMessage,
	).Scan(&record.ID)
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
		rec := &mailEntity.MailOutboxRecord{}
		var statusStr string
		var zoneIDStr string
		err := rows.Scan(
			&rec.ID,
			&rec.EventID,
			&zoneIDStr,
			&rec.JobTopic,
			&rec.PayloadJSON,
			&statusStr,
			&rec.Attempts,
			&rec.LastAttempt,
			&rec.CreatedAt,
			&rec.JobVersion,
			&rec.ResourceID,
			&rec.PayloadSchemaVersion,
			&rec.TraceID,
			&rec.Idle,
			&rec.ErrorCode,
			&rec.ErrorMessage,
		)
		if err != nil {
			return nil, err
		}
		rec.Status = mailEntity.OutboxStatus(statusStr)
		if u, err := uuid.Parse(zoneIDStr); err == nil {
			rec.ZoneID = u
		} else {
			return nil, fmt.Errorf("failed to parse zone_id: %w", err)
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}

func (r *MailOutboxRepoImpl) MarkPublished(ctx context.Context, id int64) error {
	_, err := r.db.Exec(ctx, r.markPublishedQuery, id)
	return err
}

func (r *MailOutboxRepoImpl) MarkFailed(ctx context.Context, id int64, reason string) error {
	_, err := r.db.Exec(ctx, r.markFailedQuery, id)
	return err
}
