package mailRepoImpl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"controlplane/internal/config"
	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"
	mailTaxonomy "controlplane/internal/mail/taxonomy"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type personalConsumerRepoPostgres struct {
	db              *pgxpool.Pool
	mailSchema      string
	hierarchySchema string
}

// NewPersonalConsumerRepository khoi tao repository quan ly Personal Mail Consumer
func NewPersonalConsumerRepository(db *pgxpool.Pool, cfg *config.Config) mailRepoInterface.PersonalConsumerRepository {
	return &personalConsumerRepoPostgres{
		db:              db,
		mailSchema:      cfg.SchemaSQL.Mail,
		hierarchySchema: cfg.SchemaSQL.Hierarchy,
	}
}

func (r *personalConsumerRepoPostgres) Create(ctx context.Context, consumer *mailEntity.PersonalConsumer, outbox *mailEntity.MailOutboxRecord) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("mail personal consumer repo: begin create: %w", err)
	}
	defer tx.Rollback(ctx)

	// [COMMENT]: KEY SHARE serialize create với hard-delete template; delete không thể vượt qua rồi để lại consumer trỏ vào ghost template.
	var lockedTemplateID string
	err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT t.id FROM %s.personal_mail_templates t JOIN %s.personal_mail_template_versions v ON v.template_id=t.id AND v.version=$2 JOIN %s.personal_workspaces w ON w.id=t.workspace_id WHERE t.id=$1 AND t.workspace_id=$3 AND w.zone_id=$4 AND w.owner_id=$5 FOR KEY SHARE OF t`, r.mailSchema, r.mailSchema, r.hierarchySchema), consumer.TemplateID, consumer.TemplateVersion, consumer.WorkspaceID, consumer.ZoneID, consumer.ActorUserID).Scan(&lockedTemplateID)
	if errors.Is(err, pgx.ErrNoRows) {
		return mailTaxonomy.ErrTemplateNotFound
	}
	if err != nil {
		return fmt.Errorf("mail personal consumer repo: lock template: %w", err)
	}

	// [COMMENT]: Guarded INSERT vừa kiểm tra trusted workspace ownership vừa kiểm tra template version tồn tại.
	tag, err := tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.mail_consumers (
			id, workspace_id, code, name, source_type, broker_resource_id, source_config_envelope, topic,
			consumer_group, template_id, template_version, sender_profile_id, sender_version,
			desired_state, parallelism, config_version, config_sha256, created_by, updated_by, created_at, updated_at
		)
		SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21
		WHERE EXISTS (SELECT 1 FROM %s.personal_workspaces WHERE id=$2 AND zone_id=$22 AND owner_id=$18)
		  AND EXISTS (SELECT 1 FROM %s.personal_mail_templates t JOIN %s.personal_mail_template_versions v ON v.template_id=t.id AND v.version=$11 WHERE t.id=$10 AND t.workspace_id=$2)
	`, r.mailSchema, r.hierarchySchema, r.mailSchema, r.mailSchema),
		consumer.ID, consumer.WorkspaceID, consumer.Code, consumer.Name, consumer.SourceType, consumer.BrokerResourceID,
		consumer.SourceConfigEnvelope, consumer.Topic, consumer.ConsumerGroup, consumer.TemplateID,
		consumer.TemplateVersion, consumer.SenderProfileID, consumer.SenderVersion, consumer.DesiredState, consumer.Parallelism,
		consumer.ConfigVersion, consumer.ConfigSHA256, consumer.CreatedBy, consumer.UpdatedBy, consumer.CreatedAt, consumer.UpdatedAt, consumer.ZoneID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return mailTaxonomy.ErrAlreadyExists
		}
		return fmt.Errorf("mail personal consumer repo: insert consumer: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var authorized bool
		if err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s.personal_workspaces WHERE id=$1 AND zone_id=$2 AND owner_id=$3)`, r.hierarchySchema), consumer.WorkspaceID, consumer.ZoneID, consumer.ActorUserID).Scan(&authorized); err != nil {
			return fmt.Errorf("mail personal consumer repo: classify create: %w", err)
		}
		if !authorized {
			return mailTaxonomy.ErrWorkspaceNotFound
		}
		return mailTaxonomy.ErrTemplateNotFound
	}

	// [COMMENT]: Outbox được insert trên cùng connection/transaction; commit là ranh giới bền vững duy nhất.
	err = tx.QueryRow(ctx, fmt.Sprintf(`INSERT INTO %s.mail_outbox_records (event_id,routing_scope,job_topic,payload,actor_user_id,status,job_version,resource_id,payload_schema_version,trace_id,idle) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`, r.mailSchema), outbox.EventID, outbox.RoutingScope, outbox.JobTopic, outbox.Payload, outbox.ActorUserID, outbox.Status, outbox.JobVersion, outbox.ResourceID, outbox.PayloadSchemaVersion, outbox.TraceID, outbox.Idle).Scan(&outbox.ID)
	if err != nil {
		return fmt.Errorf("mail personal consumer repo: insert outbox: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("mail personal consumer repo: commit create: %w", err)
	}
	return nil
}

func (r *personalConsumerRepoPostgres) GetByID(ctx context.Context, query *mailEntity.PersonalConsumer) (*mailEntity.PersonalConsumer, error) {
	consumer := &mailEntity.PersonalConsumer{}

	// [COMMENT]: Inline scan kết quả QueryRow trực tiếp vào các trường dữ liệu của struct Consumer
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT c.id, c.workspace_id, c.code, c.name, c.source_type, c.broker_resource_id, c.source_config_envelope,
		       c.topic, c.consumer_group, c.template_id, c.template_version,
		       c.sender_profile_id, c.sender_version, c.desired_state, c.parallelism, c.config_version,
		       c.config_sha256, c.deleted_at,
		       c.created_by, c.updated_by, c.created_at, c.updated_at
		FROM %s.mail_consumers AS c
		JOIN %s.personal_workspaces AS w ON w.id = c.workspace_id
		WHERE c.id = $1 AND c.workspace_id = $2 AND c.deleted_at IS NULL
		  AND w.zone_id = $3 AND w.owner_id = $4
	`, r.mailSchema, r.hierarchySchema),
		query.ID, query.WorkspaceID, query.ZoneID, query.ActorUserID,
	).Scan(
		&consumer.ID,
		&consumer.WorkspaceID,
		&consumer.Code,
		&consumer.Name,
		&consumer.SourceType,
		&consumer.BrokerResourceID,
		&consumer.SourceConfigEnvelope,
		&consumer.Topic,
		&consumer.ConsumerGroup,
		&consumer.TemplateID,
		&consumer.TemplateVersion,
		&consumer.SenderProfileID,
		&consumer.SenderVersion,
		&consumer.DesiredState,
		&consumer.Parallelism,
		&consumer.ConfigVersion,
		&consumer.ConfigSHA256,
		&consumer.DeletedAt,
		&consumer.CreatedBy,
		&consumer.UpdatedBy,
		&consumer.CreatedAt,
		&consumer.UpdatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, mailTaxonomy.ErrConsumerNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("mail personal consumer repo: get: %w", err)
	}

	// [COMMENT]: Detail chỉ aggregate heartbeat còn lease-like TTL và đúng desired config hiện tại;
	// report version cũ vẫn có thể tồn tại để diagnostics nhưng không được làm UI báo RUNNING sai.
	runtime := &mailEntity.ConsumerRuntimeSummary{}
	var activeInstances int64
	var consumerLag int64
	err = r.db.QueryRow(ctx, fmt.Sprintf(`
		WITH live AS (
			SELECT runtime_state, consumer_lag, error_code, error_message, reported_at, expires_at
			FROM %s.mail_consumer_runtime_reports
			WHERE consumer_id = $1 AND config_version = $2 AND expires_at > now()
		), ranked AS (
			SELECT *, row_number() OVER (
				ORDER BY CASE runtime_state
					WHEN 'error' THEN 7 WHEN 'degraded' THEN 6 WHEN 'draining' THEN 5
					WHEN 'starting' THEN 4 WHEN 'running' THEN 3 WHEN 'paused' THEN 2 ELSE 1
				END DESC, reported_at DESC
			) AS priority
			FROM live
		), aggregate AS (
			SELECT count(*)::bigint AS active_instances,
			       LEAST(COALESCE(sum(consumer_lag), 0), 9223372036854775807)::bigint AS consumer_lag
			FROM live
		)
		SELECT r.runtime_state, $2::bigint, a.active_instances, a.consumer_lag,
		       COALESCE(r.error_code, ''), COALESCE(r.error_message, ''),
		       r.reported_at, r.expires_at
		FROM ranked AS r CROSS JOIN aggregate AS a
		WHERE r.priority = 1
	`, r.mailSchema), consumer.ID, consumer.ConfigVersion).Scan(
		&runtime.State,
		&runtime.ConfigVersion,
		&activeInstances,
		&consumerLag,
		&runtime.ErrorCode,
		&runtime.ErrorMessage,
		&runtime.ReportedAt,
		&runtime.NextExpiryAt,
	)
	if err == nil {
		runtime.ActiveInstances = uint32(activeInstances)
		runtime.ConsumerLag = uint64(consumerLag)
		if consumer.DesiredState == mailEntity.ConsumerEnabled && runtime.ActiveInstances < consumer.Parallelism && runtime.State == mailEntity.ConsumerRuntimeRunning {
			// [COMMENT]: Một slot RUNNING không được che việc desired parallelism chưa đủ coverage.
			runtime.State = mailEntity.ConsumerRuntimeDegraded
			runtime.ErrorCode = "MAIL_RUNTIME_SLOT_COVERAGE_PARTIAL"
		}
		consumer.Runtime = runtime
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("mail personal consumer repo: get runtime summary: %w", err)
	}

	return consumer, nil
}

func (r *personalConsumerRepoPostgres) List(ctx context.Context, query *mailEntity.PersonalConsumer) ([]*mailEntity.PersonalConsumer, error) {
	var sourceFilter, stateFilter any
	if query.SourceType != "" {
		sourceFilter = string(query.SourceType)
	}
	if query.DesiredState != "" {
		stateFilter = string(query.DesiredState)
	}

	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT c.id, c.workspace_id, c.code, c.name, c.source_type, c.broker_resource_id, c.source_config_envelope,
		       c.topic, c.consumer_group, c.template_id, c.template_version,
		       c.sender_profile_id, c.sender_version, c.desired_state, c.parallelism, c.config_version,
		       c.config_sha256, c.deleted_at,
		       c.created_by, c.updated_by, c.created_at, c.updated_at
		FROM %s.mail_consumers AS c
		JOIN %s.personal_workspaces AS w ON w.id = c.workspace_id
		WHERE c.workspace_id = $1 AND c.deleted_at IS NULL
		  AND w.zone_id = $2 AND w.owner_id = $3
		  AND ($4::text IS NULL OR c.source_type::text = $4::text)
		  AND ($5::text IS NULL OR c.desired_state::text = $5::text)
		  AND ($6::uuid IS NULL OR c.id > $6::uuid)
		ORDER BY c.id ASC LIMIT $7
	`, r.mailSchema, r.hierarchySchema),
		query.WorkspaceID, query.ZoneID, query.ActorUserID, sourceFilter, stateFilter, query.AfterID, query.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("mail personal consumer repo: list: %w", err)
	}
	defer rows.Close()

	consumers := make([]*mailEntity.PersonalConsumer, 0, query.Limit)
	for rows.Next() {
		consumer := &mailEntity.PersonalConsumer{}
		// [COMMENT]: Inline scan từng cột dữ liệu trong kết quả rows.Next() vào struct Consumer
		if err = rows.Scan(
			&consumer.ID,
			&consumer.WorkspaceID,
			&consumer.Code,
			&consumer.Name,
			&consumer.SourceType,
			&consumer.BrokerResourceID,
			&consumer.SourceConfigEnvelope,
			&consumer.Topic,
			&consumer.ConsumerGroup,
			&consumer.TemplateID,
			&consumer.TemplateVersion,
			&consumer.SenderProfileID,
			&consumer.SenderVersion,
			&consumer.DesiredState,
			&consumer.Parallelism,
			&consumer.ConfigVersion,
			&consumer.ConfigSHA256,
			&consumer.DeletedAt,
			&consumer.CreatedBy,
			&consumer.UpdatedBy,
			&consumer.CreatedAt,
			&consumer.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("mail personal consumer repo: scan list: %w", err)
		}
		consumers = append(consumers, consumer)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("mail personal consumer repo: iterate list: %w", err)
	}
	return consumers, nil
}

func (r *personalConsumerRepoPostgres) Update(ctx context.Context, consumer *mailEntity.PersonalConsumer, outbox *mailEntity.MailOutboxRecord) error {
	var authorized, templateAvailable, updated bool
	var currentVersion uint64
	var outboxID sql.NullInt64

	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		WITH authorized AS (
			SELECT 1
			FROM %s.personal_workspaces
			WHERE id = $18 AND zone_id = $19 AND owner_id = $15
		), template_available AS (
			SELECT 1
			FROM %s.personal_mail_templates AS t
			JOIN %s.personal_mail_template_versions AS v
			  ON v.template_id = t.id AND v.version = $8
			WHERE t.id = $7
			  AND t.workspace_id = $18
			FOR KEY SHARE OF t
		), target AS (
			SELECT config_version
			FROM %s.mail_consumers
			WHERE id = $17 AND workspace_id = $18 AND deleted_at IS NULL
			  AND EXISTS (SELECT 1 FROM authorized)
		), updated AS (
			UPDATE %s.mail_consumers
			SET name = $1,
			    source_type = $2,
			    broker_resource_id = $3,
			    source_config_envelope = $4,
			    topic = $5,
			    consumer_group = $6,
			    template_id = $7,
			    template_version = $8,
			    sender_profile_id = $9,
			    sender_version = $10,
			    desired_state = $11,
			    parallelism = $12,
			    config_version = $13,
			    config_sha256 = $14,
			    updated_by = $15,
			    updated_at = $16
			WHERE id = $17 AND workspace_id = $18 AND config_version = $20 AND deleted_at IS NULL
			  AND EXISTS (SELECT 1 FROM authorized)
			  AND EXISTS (SELECT 1 FROM template_available)
			RETURNING id
		), outbox_inserted AS (
			INSERT INTO %s.mail_outbox_records (
				event_id, routing_scope, job_topic, payload, actor_user_id, status,
				job_version, resource_id, payload_schema_version, trace_id, idle
			)
			SELECT $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31
			FROM updated
			RETURNING id
		)
		SELECT
			EXISTS (SELECT 1 FROM authorized),
			EXISTS (SELECT 1 FROM template_available),
			COALESCE((SELECT config_version FROM target), 0),
			EXISTS (SELECT 1 FROM updated),
			(SELECT id FROM outbox_inserted)
	`, r.hierarchySchema, r.mailSchema, r.mailSchema, r.mailSchema, r.mailSchema, r.mailSchema),
		consumer.Name, consumer.SourceType, consumer.BrokerResourceID, consumer.SourceConfigEnvelope,
		consumer.Topic, consumer.ConsumerGroup, consumer.TemplateID,
		consumer.TemplateVersion, consumer.SenderProfileID, consumer.SenderVersion,
		consumer.DesiredState, consumer.Parallelism, consumer.ConfigVersion, consumer.ConfigSHA256,
		consumer.ActorUserID, consumer.UpdatedAt, consumer.ID, consumer.WorkspaceID, consumer.ZoneID, consumer.ExpectedConfigVersion,
		outbox.EventID, outbox.RoutingScope, outbox.JobTopic, outbox.Payload, outbox.ActorUserID,
		outbox.Status, outbox.JobVersion, outbox.ResourceID, outbox.PayloadSchemaVersion, outbox.TraceID, outbox.Idle,
	).Scan(
		&authorized,
		&templateAvailable,
		&currentVersion,
		&updated,
		&outboxID,
	)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return mailTaxonomy.ErrAlreadyExists
		}
		return fmt.Errorf("mail personal consumer repo: atomic update: %w", err)
	}

	if !authorized || currentVersion == 0 {
		return mailTaxonomy.ErrConsumerNotFound
	}
	if !templateAvailable {
		return mailTaxonomy.ErrTemplateNotFound
	}
	if currentVersion != consumer.ExpectedConfigVersion || !updated {
		return mailTaxonomy.ErrVersionConflict
	}
	if !outboxID.Valid {
		return fmt.Errorf("mail personal consumer repo: update outbox CTE returned no row: %w", mailTaxonomy.ErrInternal)
	}

	outbox.ID = outboxID.Int64
	return nil
}

func (r *personalConsumerRepoPostgres) Delete(ctx context.Context, consumer *mailEntity.PersonalConsumer, outbox *mailEntity.MailOutboxRecord) error {
	var authorized, updated bool
	var currentVersion uint64
	var outboxID sql.NullInt64

	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		WITH authorized AS (
			SELECT 1
			FROM %s.personal_workspaces
			WHERE id = $1 AND zone_id = $2 AND owner_id = $3
		), target AS (
			SELECT config_version
			FROM %s.mail_consumers
			WHERE id = $4 AND workspace_id = $1 AND deleted_at IS NULL
			  AND EXISTS (SELECT 1 FROM authorized)
		), updated AS (
			UPDATE %s.mail_consumers
			SET desired_state = 'deleting',
			    config_version = config_version + 1,
			    updated_by = $3,
			    updated_at = $6
			WHERE id = $4 AND workspace_id = $1 AND config_version = $5 AND deleted_at IS NULL
			  AND EXISTS (SELECT 1 FROM authorized)
			RETURNING id
		), outbox_inserted AS (
			INSERT INTO %s.mail_outbox_records (
				event_id, routing_scope, job_topic, payload, actor_user_id, status,
				job_version, resource_id, payload_schema_version, trace_id, idle
			)
			SELECT $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17
			FROM updated
			RETURNING id
		)
		SELECT
			EXISTS (SELECT 1 FROM authorized),
			COALESCE((SELECT config_version FROM target), 0),
			EXISTS (SELECT 1 FROM updated),
			(SELECT id FROM outbox_inserted)
	`, r.hierarchySchema, r.mailSchema, r.mailSchema, r.mailSchema),
		consumer.WorkspaceID, consumer.ZoneID, consumer.ActorUserID, consumer.ID, consumer.ExpectedConfigVersion, consumer.UpdatedAt,
		outbox.EventID, outbox.RoutingScope, outbox.JobTopic, outbox.Payload, outbox.ActorUserID,
		outbox.Status, outbox.JobVersion, outbox.ResourceID, outbox.PayloadSchemaVersion, outbox.TraceID, outbox.Idle,
	).Scan(
		&authorized,
		&currentVersion,
		&updated,
		&outboxID,
	)

	if err != nil {
		return fmt.Errorf("mail personal consumer repo: atomic delete: %w", err)
	}

	if !authorized || currentVersion == 0 {
		return mailTaxonomy.ErrConsumerNotFound
	}
	if currentVersion != consumer.ExpectedConfigVersion || !updated {
		return mailTaxonomy.ErrVersionConflict
	}
	if !outboxID.Valid {
		return fmt.Errorf("mail personal consumer repo: delete outbox CTE returned no row: %w", mailTaxonomy.ErrInternal)
	}

	outbox.ID = outboxID.Int64
	return nil
}
