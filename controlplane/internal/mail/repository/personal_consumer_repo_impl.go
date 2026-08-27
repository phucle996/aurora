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
	jobpayload "controlplane/internal/security"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type personalConsumerRepoPostgres struct {
	db              *pgxpool.Pool
	mailSchema      string
	hierarchySchema string
	protector       jobpayload.Protector
}

// NewPersonalConsumerRepository khoi tao repository quan ly Personal Mail Consumer
func NewPersonalConsumerRepository(db *pgxpool.Pool, cfg *config.Config, protector jobpayload.Protector) mailRepoInterface.PersonalConsumerRepository {
	return &personalConsumerRepoPostgres{
		db:              db,
		mailSchema:      cfg.SchemaSQL.Mail,
		hierarchySchema: cfg.SchemaSQL.Hierarchy,
		protector:       protector,
	}
}

func (r *personalConsumerRepoPostgres) Create(ctx context.Context, consumer *mailEntity.CreatePersonalConsumer, outbox *mailEntity.MailOutboxRecord) error {
	protected, protectionErr := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: outbox.ZoneID, SourceDomain: "MAIL", JobTopic: outbox.JobTopic, ResourceID: outbox.ResourceID, JobVersion: outbox.JobVersion, PayloadSchemaVersion: outbox.PayloadSchemaVersion}, outbox.Payload)
	if protectionErr != nil {
		return protectionErr
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID
	// [COMMENT]: Outbox route phải chính là Zone đã được aggregate authorization guard kiểm tra; mismatch fail closed.
	if consumer == nil || outbox == nil || outbox.ZoneID != consumer.ZoneID {
		return mailTaxonomy.ErrInvalidArgument
	}
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
	// [COMMENT]: Delete template giữ business row đến Zone ACK; live outbox là fence ngăn consumer mới bind vào resource đang xóa.
	var templateOperation bool
	if err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s.mail_outbox_records WHERE resource_id=$1 AND status IN ('PENDING','PROCESSING'))`, r.mailSchema), consumer.TemplateID).Scan(&templateOperation); err != nil {
		return fmt.Errorf("mail personal consumer repo: check template operation: %w", err)
	}
	if templateOperation {
		return mailTaxonomy.ErrOperationInProgress
	}

	// [COMMENT]: Guarded INSERT vừa kiểm tra trusted workspace ownership vừa kiểm tra template version tồn tại.
	tag, err := tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.personal_mail_consumers (
			id, workspace_id, code, name, source_type, broker_resource_id, source_config_envelope, topic,
			consumer_group, template_id, template_version, sender_profile_id, sender_version,
			desired_state, parallelism, config_version, config_sha256, created_at, updated_at
		)
		SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19
		WHERE EXISTS (SELECT 1 FROM %s.personal_workspaces WHERE id=$2 AND zone_id=$20 AND owner_id=$21)
		  AND EXISTS (SELECT 1 FROM %s.personal_mail_templates t JOIN %s.personal_mail_template_versions v ON v.template_id=t.id AND v.version=$11 WHERE t.id=$10 AND t.workspace_id=$2)
	`, r.mailSchema, r.hierarchySchema, r.mailSchema, r.mailSchema),
		consumer.ID, consumer.WorkspaceID, consumer.Code, consumer.Name, consumer.SourceType, consumer.BrokerResourceID,
		consumer.SourceConfigEnvelope, consumer.Topic, consumer.ConsumerGroup, consumer.TemplateID,
		consumer.TemplateVersion, consumer.SenderProfileID, consumer.SenderVersion, consumer.DesiredState, consumer.Parallelism,
		consumer.ConfigVersion, consumer.ConfigSHA256, consumer.CreatedAt, consumer.UpdatedAt, consumer.ZoneID, consumer.ActorUserID)
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
	err = tx.QueryRow(ctx, fmt.Sprintf(`INSERT INTO %s.mail_outbox_records (event_id,zone_id,job_topic,payload,actor_user_id,status,job_version,resource_id,payload_schema_version,trace_id,idle,payload_key_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`, r.mailSchema), outbox.EventID, outbox.ZoneID, outbox.JobTopic, outbox.Payload, outbox.ActorUserID, outbox.Status, outbox.JobVersion, outbox.ResourceID, outbox.PayloadSchemaVersion, outbox.TraceID, outbox.Idle, outbox.PayloadKeyID).Scan(&outbox.ID)
	if err != nil {
		return fmt.Errorf("mail personal consumer repo: insert outbox: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("mail personal consumer repo: commit create: %w", err)
	}
	return nil
}

func (r *personalConsumerRepoPostgres) GetByID(ctx context.Context, query *mailEntity.GetPersonalConsumer) (*mailEntity.GetPersonalConsumer, error) {
	consumer := &mailEntity.GetPersonalConsumer{}

	// [COMMENT]: Inline scan kết quả QueryRow trực tiếp vào các trường dữ liệu của struct Consumer
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT c.id, c.workspace_id, c.code, c.name, c.source_type, c.broker_resource_id, c.source_config_envelope,
		       c.topic, c.consumer_group, c.template_id, c.template_version,
		       c.sender_profile_id, c.sender_version, c.desired_state, c.parallelism, c.config_version,
		       c.next_config_version, c.config_sha256, c.created_at, c.updated_at
		FROM %s.personal_mail_consumers AS c
		JOIN %s.personal_workspaces AS w ON w.id = c.workspace_id
		WHERE c.id = $1 AND c.workspace_id = $2
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
		&consumer.NextConfigVersion,
		&consumer.ConfigSHA256,
		&consumer.CreatedAt,
		&consumer.UpdatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, mailTaxonomy.ErrConsumerNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("mail personal consumer repo: get: %w", err)
	}

	return consumer, nil
}

func (r *personalConsumerRepoPostgres) List(ctx context.Context, query *mailEntity.ListPersonalConsumer) ([]*mailEntity.ListPersonalConsumer, error) {
	var sourceFilter, stateFilter any
	if query.SourceType != "" {
		sourceFilter = string(query.SourceType)
	}
	if query.DesiredState != "" {
		stateFilter = string(query.DesiredState)
	}

	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT c.id, c.workspace_id, c.code, c.name, c.source_type, c.broker_resource_id,
		       octet_length(c.source_config_envelope) > 0,
		       c.topic, c.consumer_group, c.template_id, c.template_version,
		       c.sender_profile_id, c.sender_version, c.desired_state, c.parallelism, c.config_version,
		       c.config_sha256, c.created_at, c.updated_at
		FROM %s.personal_mail_consumers AS c
		JOIN %s.personal_workspaces AS w ON w.id = c.workspace_id
		WHERE c.workspace_id = $1
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

	consumers := make([]*mailEntity.ListPersonalConsumer, 0, query.Limit)
	for rows.Next() {
		consumer := &mailEntity.ListPersonalConsumer{}
		// [COMMENT]: Inline scan từng cột dữ liệu trong kết quả rows.Next() vào struct Consumer
		if err = rows.Scan(
			&consumer.ID,
			&consumer.WorkspaceID,
			&consumer.Code,
			&consumer.Name,
			&consumer.SourceType,
			&consumer.BrokerResourceID,
			&consumer.SourceConfigured,
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

func (r *personalConsumerRepoPostgres) Update(ctx context.Context, consumer *mailEntity.UpdatePersonalConsumer, outbox *mailEntity.MailOutboxRecord) error {
	protected, protectionErr := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: outbox.ZoneID, SourceDomain: "MAIL", JobTopic: outbox.JobTopic, ResourceID: outbox.ResourceID, JobVersion: outbox.JobVersion, PayloadSchemaVersion: outbox.PayloadSchemaVersion}, outbox.Payload)
	if protectionErr != nil {
		return protectionErr
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID
	// [COMMENT]: Không cho service bug chuyển projection sang Zone khác aggregate đã authorize.
	if consumer == nil || outbox == nil || outbox.ZoneID != consumer.ZoneID {
		return mailTaxonomy.ErrInvalidArgument
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("mail personal consumer repo: begin update: %w", err)
	}
	defer tx.Rollback(ctx)

	// [COMMENT]: Hai statement cố ý dùng READ COMMITTED snapshot mới sau khi KEY SHARE hết chờ;
	// nếu template delete/publish commit trước thì live outbox phải nhìn thấy trước khi bind candidate.
	var lockedTemplateID string
	err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT t.id FROM %s.personal_mail_templates t JOIN %s.personal_mail_template_versions v ON v.template_id=t.id AND v.version=$2 JOIN %s.personal_workspaces w ON w.id=t.workspace_id WHERE t.id=$1 AND t.workspace_id=$3 AND w.zone_id=$4 AND w.owner_id=$5 FOR KEY SHARE OF t`, r.mailSchema, r.mailSchema, r.hierarchySchema), consumer.TemplateID, consumer.TemplateVersion, consumer.WorkspaceID, consumer.ZoneID, consumer.ActorUserID).Scan(&lockedTemplateID)
	if errors.Is(err, pgx.ErrNoRows) {
		return mailTaxonomy.ErrTemplateNotFound
	}
	if err != nil {
		return fmt.Errorf("mail personal consumer repo: lock update template: %w", err)
	}
	var lockedTemplateOperation bool
	if err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s.mail_outbox_records WHERE resource_id=$1 AND status IN ('PENDING','PROCESSING'))`, r.mailSchema), consumer.TemplateID).Scan(&lockedTemplateOperation); err != nil {
		return fmt.Errorf("mail personal consumer repo: check update template operation: %w", err)
	}
	if lockedTemplateOperation {
		return mailTaxonomy.ErrOperationInProgress
	}
	var lockedConsumerVersion uint64
	err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT c.config_version FROM %s.personal_mail_consumers c JOIN %s.personal_workspaces w ON w.id=c.workspace_id WHERE c.id=$1 AND c.workspace_id=$2 AND w.zone_id=$3 AND w.owner_id=$4 FOR UPDATE OF c`, r.mailSchema, r.hierarchySchema), consumer.ID, consumer.WorkspaceID, consumer.ZoneID, consumer.ActorUserID).Scan(&lockedConsumerVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return mailTaxonomy.ErrConsumerNotFound
	}
	if err != nil {
		return fmt.Errorf("mail personal consumer repo: lock update consumer: %w", err)
	}
	if lockedConsumerVersion != consumer.ExpectedConfigVersion {
		return mailTaxonomy.ErrVersionConflict
	}
	var lockedConsumerOperation bool
	if err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s.mail_outbox_records WHERE resource_id=$1 AND status IN ('PENDING','PROCESSING'))`, r.mailSchema), consumer.ID.String()).Scan(&lockedConsumerOperation); err != nil {
		return fmt.Errorf("mail personal consumer repo: check update consumer operation: %w", err)
	}
	if lockedConsumerOperation {
		return mailTaxonomy.ErrOperationInProgress
	}
	var authorized, admissionAllowed, templateAvailable, templateOperation, liveOperation, versionInserted bool
	var currentVersion, nextVersion uint64
	var outboxID sql.NullInt64

	err = tx.QueryRow(ctx, fmt.Sprintf(`
		WITH authorized AS (
			SELECT 1
			FROM %s.personal_workspaces
			WHERE id = $18 AND zone_id = $19 AND owner_id = $15
		), commercial_admission AS (
			SELECT 1
			FROM %s.commercial_admission_projection admission
			WHERE admission.owner_id = $15
			  AND admission.owner_type = 'PERSONAL'
			  AND admission.decision = 'ALLOW'
			  AND admission.effective_at <= NOW()
			  AND (admission.valid_until IS NULL OR admission.valid_until > NOW())
		), template_available AS (
			SELECT 1
			FROM %s.personal_mail_templates AS t
			JOIN %s.personal_mail_template_versions AS v
			  ON v.template_id = t.id AND v.version = $8
			WHERE t.id = $7
			  AND t.workspace_id = $18
			FOR KEY SHARE OF t
		), template_operation AS (
			SELECT 1 FROM %s.mail_outbox_records
			WHERE resource_id=$7 AND status IN ('PENDING','PROCESSING') LIMIT 1
		), target AS MATERIALIZED (
			SELECT config_version, next_config_version
			FROM %s.personal_mail_consumers
			WHERE id = $17 AND workspace_id = $18
			  AND EXISTS (SELECT 1 FROM authorized)
			FOR UPDATE
		), live_operation AS (
			SELECT 1 FROM %s.mail_outbox_records
			WHERE resource_id = $17::text AND status IN ('PENDING','PROCESSING')
			LIMIT 1
		), version_inserted AS (
			INSERT INTO %s.personal_mail_consumer_update_versions (
				consumer_id,config_version,event_id,name,source_type,broker_resource_id,
				source_config_envelope,topic,consumer_group,template_id,template_version,
				sender_profile_id,sender_version,desired_state,parallelism,config_sha256,created_at
			)
			SELECT $17,$13,$21,$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$14,$16
			FROM target
			WHERE config_version=$20 AND next_config_version=$13
			  AND NOT EXISTS (SELECT 1 FROM live_operation)
			  AND NOT EXISTS (SELECT 1 FROM template_operation)
			  AND EXISTS (SELECT 1 FROM template_available)
			  AND ($11::text != 'ENABLED' OR EXISTS (SELECT 1 FROM commercial_admission))
			ON CONFLICT DO NOTHING
			RETURNING consumer_id
		), counter_updated AS (
			UPDATE %s.personal_mail_consumers
			SET next_config_version=$13+1
			WHERE id=$17 AND workspace_id=$18 AND config_version=$20
			  AND next_config_version=$13 AND EXISTS (SELECT 1 FROM version_inserted)
			RETURNING id
		), outbox_inserted AS (
			INSERT INTO %s.mail_outbox_records (
				event_id, zone_id, job_topic, payload, actor_user_id, status,
				job_version, resource_id, payload_schema_version, trace_id, idle, payload_key_id
			)
			SELECT $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32
			FROM counter_updated
			RETURNING id
		)
		SELECT
			EXISTS (SELECT 1 FROM authorized),
			EXISTS (SELECT 1 FROM commercial_admission),
			EXISTS (SELECT 1 FROM template_available),
			EXISTS (SELECT 1 FROM template_operation),
			COALESCE((SELECT config_version FROM target), 0),
			COALESCE((SELECT next_config_version FROM target), 0),
			EXISTS (SELECT 1 FROM live_operation),
			EXISTS (SELECT 1 FROM version_inserted),
			(SELECT id FROM outbox_inserted)
	`, r.hierarchySchema, r.mailSchema, r.mailSchema, r.mailSchema, r.mailSchema, r.mailSchema, r.mailSchema, r.mailSchema, r.mailSchema, r.mailSchema),
		consumer.Name, consumer.SourceType, consumer.BrokerResourceID, consumer.SourceConfigEnvelope,
		consumer.Topic, consumer.ConsumerGroup, consumer.TemplateID,
		consumer.TemplateVersion, consumer.SenderProfileID, consumer.SenderVersion,
		consumer.DesiredState, consumer.Parallelism, consumer.ConfigVersion, consumer.ConfigSHA256,
		consumer.ActorUserID, consumer.UpdatedAt, consumer.ID, consumer.WorkspaceID, consumer.ZoneID, consumer.ExpectedConfigVersion,
		outbox.EventID, outbox.ZoneID, outbox.JobTopic, outbox.Payload, outbox.ActorUserID,
		outbox.Status, outbox.JobVersion, outbox.ResourceID, outbox.PayloadSchemaVersion, outbox.TraceID, outbox.Idle,
		outbox.PayloadKeyID,
	).Scan(
		&authorized,
		&admissionAllowed,
		&templateAvailable,
		&templateOperation,
		&currentVersion,
		&nextVersion,
		&liveOperation,
		&versionInserted,
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
	if templateOperation {
		return mailTaxonomy.ErrOperationInProgress
	}
	if liveOperation {
		return mailTaxonomy.ErrOperationInProgress
	}
	if currentVersion != consumer.ExpectedConfigVersion || nextVersion != consumer.ConfigVersion {
		return mailTaxonomy.ErrVersionConflict
	}
	if consumer.DesiredState == mailEntity.ConsumerEnabled && !admissionAllowed {
		return mailTaxonomy.ErrCommercialAdmissionUnavailable
	}
	if !versionInserted {
		return mailTaxonomy.ErrVersionConflict
	}
	if !outboxID.Valid {
		return fmt.Errorf("mail personal consumer repo: update outbox CTE returned no row: %w", mailTaxonomy.ErrInternal)
	}

	outbox.ID = outboxID.Int64
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("mail personal consumer repo: commit update: %w", err)
	}
	return nil
}

func (r *personalConsumerRepoPostgres) Delete(ctx context.Context, consumer *mailEntity.DeletePersonalConsumer, outbox *mailEntity.MailOutboxRecord) error {
	protected, protectionErr := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: outbox.ZoneID, SourceDomain: "MAIL", JobTopic: outbox.JobTopic, ResourceID: outbox.ResourceID, JobVersion: outbox.JobVersion, PayloadSchemaVersion: outbox.PayloadSchemaVersion}, outbox.Payload)
	if protectionErr != nil {
		return protectionErr
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID
	// [COMMENT]: Tombstone phải đi đúng Zone của guarded workspace mutation.
	if consumer == nil || outbox == nil || outbox.ZoneID != consumer.ZoneID {
		return mailTaxonomy.ErrInvalidArgument
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("mail personal consumer repo: begin delete: %w", err)
	}
	defer tx.Rollback(ctx)
	var lockedVersion uint64
	err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT c.config_version FROM %s.personal_mail_consumers c JOIN %s.personal_workspaces w ON w.id=c.workspace_id WHERE c.id=$1 AND c.workspace_id=$2 AND w.zone_id=$3 AND w.owner_id=$4 FOR UPDATE OF c`, r.mailSchema, r.hierarchySchema), consumer.ID, consumer.WorkspaceID, consumer.ZoneID, consumer.ActorUserID).Scan(&lockedVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return mailTaxonomy.ErrConsumerNotFound
	}
	if err != nil {
		return fmt.Errorf("mail personal consumer repo: lock delete consumer: %w", err)
	}
	if lockedVersion != consumer.ExpectedConfigVersion {
		return mailTaxonomy.ErrVersionConflict
	}
	var lockedOperation bool
	if err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s.mail_outbox_records WHERE resource_id=$1 AND status IN ('PENDING','PROCESSING'))`, r.mailSchema), consumer.ID.String()).Scan(&lockedOperation); err != nil {
		return fmt.Errorf("mail personal consumer repo: check delete consumer operation: %w", err)
	}
	if lockedOperation {
		return mailTaxonomy.ErrOperationInProgress
	}
	var authorized, liveOperation, outboxInserted bool
	var currentVersion uint64
	var outboxID sql.NullInt64

	err = tx.QueryRow(ctx, fmt.Sprintf(`
		WITH authorized AS (
			SELECT 1
			FROM %s.personal_workspaces
			WHERE id = $1 AND zone_id = $2 AND owner_id = $3
		), target AS MATERIALIZED (
			SELECT config_version, desired_state
			FROM %s.personal_mail_consumers
			WHERE id = $4 AND workspace_id = $1
			  AND EXISTS (SELECT 1 FROM authorized)
			FOR UPDATE
		), live_operation AS (
			SELECT 1 FROM %s.mail_outbox_records
			WHERE resource_id=$4::text AND status IN ('PENDING','PROCESSING')
			LIMIT 1
		), transitioned AS (
            UPDATE %s.personal_mail_consumers c SET desired_state='deleting',updated_at=NOW()
            FROM target t WHERE c.id=$4 AND t.config_version=$5 AND t.desired_state='drained'
                AND c.desired_state='drained' AND NOT EXISTS (SELECT 1 FROM live_operation)
            RETURNING c.id
        ), outbox_inserted AS (
			INSERT INTO %s.mail_outbox_records (
				event_id, zone_id, job_topic, payload, actor_user_id, status,
				job_version, resource_id, payload_schema_version, trace_id, idle, payload_key_id
			)
			SELECT $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17
			FROM transitioned
			RETURNING id
		)
		SELECT
			EXISTS (SELECT 1 FROM authorized),
			COALESCE((SELECT config_version FROM target), 0),
			EXISTS (SELECT 1 FROM live_operation),
			EXISTS (SELECT 1 FROM outbox_inserted),
			(SELECT id FROM outbox_inserted)
	`, r.hierarchySchema, r.mailSchema, r.mailSchema, r.mailSchema, r.mailSchema),
		consumer.WorkspaceID, consumer.ZoneID, consumer.ActorUserID, consumer.ID, consumer.ExpectedConfigVersion,
		outbox.EventID, outbox.ZoneID, outbox.JobTopic, outbox.Payload, outbox.ActorUserID,
		outbox.Status, outbox.JobVersion, outbox.ResourceID, outbox.PayloadSchemaVersion, outbox.TraceID, outbox.Idle,
		outbox.PayloadKeyID,
	).Scan(
		&authorized,
		&currentVersion,
		&liveOperation,
		&outboxInserted,
		&outboxID,
	)

	if err != nil {
		return fmt.Errorf("mail personal consumer repo: atomic delete: %w", err)
	}

	if !authorized || currentVersion == 0 {
		return mailTaxonomy.ErrConsumerNotFound
	}
	if liveOperation {
		return mailTaxonomy.ErrOperationInProgress
	}
	if currentVersion != consumer.ExpectedConfigVersion || !outboxInserted {
		return mailTaxonomy.ErrVersionConflict
	}
	if !outboxID.Valid {
		return fmt.Errorf("mail personal consumer repo: delete outbox CTE returned no row: %w", mailTaxonomy.ErrInternal)
	}

	outbox.ID = outboxID.Int64
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("mail personal consumer repo: commit delete: %w", err)
	}
	return nil
}

func (r *personalConsumerRepoPostgres) LoadDrainTarget(ctx context.Context, cmd mailEntity.PersonalConsumerDrainCommand) (mailEntity.PersonalConsumerDrainTarget, error) {
	var target mailEntity.PersonalConsumerDrainTarget
	err := r.db.QueryRow(ctx, fmt.Sprintf(`SELECT c.config_version,c.parallelism,c.desired_state::text
 FROM %s.personal_mail_consumers c JOIN %s.personal_workspaces w ON w.id=c.workspace_id
 WHERE c.id=$1 AND c.workspace_id=$2 AND w.zone_id=$3 AND w.owner_id=$4`, r.mailSchema, r.hierarchySchema), cmd.ConsumerID, cmd.WorkspaceID, cmd.ZoneID, cmd.ActorUserID).Scan(&target.ConfigVersion, &target.Parallelism, &target.State)
	if errors.Is(err, pgx.ErrNoRows) {
		return target, mailTaxonomy.ErrConsumerNotFound
	}
	return target, err
}
func (r *personalConsumerRepoPostgres) RequestDrain(ctx context.Context, cmd mailEntity.PersonalConsumerDrainCommand, parallelism uint32, outbox mailEntity.MailOutboxRecord) error {
	sealed, err := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: outbox.ZoneID, SourceDomain: "MAIL", JobTopic: outbox.JobTopic, ResourceID: outbox.ResourceID, JobVersion: 1, PayloadSchemaVersion: 1}, outbox.Payload)
	if err != nil {
		return err
	}
	var accepted bool
	err = r.db.QueryRow(ctx, fmt.Sprintf(`WITH target AS MATERIALIZED (
 SELECT c.id FROM %s.personal_mail_consumers c JOIN %s.personal_workspaces w ON w.id=c.workspace_id
 WHERE c.id=$1 AND c.workspace_id=$2 AND w.zone_id=$3 AND w.owner_id=$4
 AND c.config_version=$5 AND c.parallelism=$6 AND c.desired_state IN ('enabled','paused')
 FOR UPDATE OF c
 ), transitioned AS (
 UPDATE %s.personal_mail_consumers c SET desired_state='draining',updated_at=NOW()
 FROM target t WHERE c.id=t.id AND c.desired_state IN ('enabled','paused')
 AND NOT EXISTS(SELECT 1 FROM %s.mail_outbox_records o WHERE o.resource_id=c.id::text AND o.status IN ('PENDING','PROCESSING'))
 RETURNING c.id
 ), appended AS (
 INSERT INTO %s.mail_outbox_records(event_id,zone_id,job_topic,payload,payload_key_id,actor_user_id,status,job_version,resource_id,payload_schema_version,idle)
 SELECT $7,$3,'mail.consumer.drain',$8,$9,$4,'PENDING',1,id::text,1,$10 FROM transitioned RETURNING id
 ) SELECT EXISTS(SELECT 1 FROM appended)`, r.mailSchema, r.hierarchySchema, r.mailSchema, r.mailSchema, r.mailSchema),
		cmd.ConsumerID, cmd.WorkspaceID, cmd.ZoneID, cmd.ActorUserID, cmd.ExpectedConfigVersion, parallelism, outbox.EventID, sealed.Payload, sealed.KeyID, outbox.Idle).Scan(&accepted)
	if err != nil {
		return err
	}
	if !accepted {
		return mailTaxonomy.ErrVersionConflict
	}
	return nil
}
