package mailRepoImpl

import (
	"bytes"
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

type personalTemplateRepoPostgres struct {
	db              *pgxpool.Pool
	mailSchema      string
	hierarchySchema string
}

func NewPersonalTemplateRepository(db *pgxpool.Pool, cfg *config.Config) mailRepoInterface.PersonalTemplateRepository {
	return &personalTemplateRepoPostgres{db: db, mailSchema: cfg.SchemaSQL.Mail, hierarchySchema: cfg.SchemaSQL.Hierarchy}
}

func (r *personalTemplateRepoPostgres) Create(ctx context.Context, template *mailEntity.PersonalTemplate, outbox *mailEntity.MailOutboxRecord) error {
	var authorized bool
	var insertedID, existingID string
	var existingRequestHash []byte
	var versionInserted bool
	var outboxID sql.NullInt64
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		WITH authorized AS (
			SELECT 1 FROM %s.personal_workspaces WHERE id=$2 AND zone_id=$11 AND owner_id=$12
		), identity_inserted AS (
			INSERT INTO %s.mail_templates (
				id,workspace_id,scope,name,current_version,template_revision,status,
				create_idempotency_key,create_request_sha256,created_by,created_at,updated_at
			)
			SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$13,$14
			WHERE EXISTS(SELECT 1 FROM authorized)
			ON CONFLICT (workspace_id,create_idempotency_key) WHERE scope='workspace' DO NOTHING
			RETURNING id
		), existing AS (
			SELECT id,create_request_sha256 FROM %s.mail_templates
			WHERE workspace_id=$2 AND scope='workspace' AND create_idempotency_key=$8
			  AND EXISTS(SELECT 1 FROM authorized)
		), version_inserted AS (
			INSERT INTO %s.mail_template_versions (
				template_id,version,subject_template,text_template,html_template,
				variable_schema_json,content_sha256,created_by,created_at
			)
			SELECT $15,$16,$17,$18,$19,$20,$21,$22,$23 FROM identity_inserted RETURNING template_id
		), outbox_inserted AS (
			INSERT INTO %s.mail_outbox_records (
				event_id,routing_scope,job_topic,payload,actor_user_id,status,
				job_version,resource_id,payload_schema_version,trace_id,idle
			)
			SELECT $24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34 FROM version_inserted RETURNING id
		)
		SELECT EXISTS(SELECT 1 FROM authorized),COALESCE((SELECT id FROM identity_inserted),''),
		       COALESCE((SELECT id FROM existing),''),
		       COALESCE((SELECT create_request_sha256 FROM existing),''::bytea),
		       EXISTS(SELECT 1 FROM version_inserted),(SELECT id FROM outbox_inserted)
	`, r.hierarchySchema, r.mailSchema, r.mailSchema, r.mailSchema, r.mailSchema),
		template.ID, template.WorkspaceID, template.Scope, template.Name, template.CurrentVersion,
		template.TemplateRevision, template.Status, template.IdempotencyKey,
		template.CreateRequestSHA256, template.CreatedBy, template.ZoneID, template.ActorUserID,
		template.CreatedAt, template.UpdatedAt,
		template.TemplateID, template.Version, template.SubjectTemplate, template.TextTemplate,
		template.HTMLTemplate, template.VariableSchemaJSON, template.ContentSHA256,
		template.VersionCreatedBy, template.VersionCreatedAt,
		outbox.EventID, outbox.RoutingScope, outbox.JobTopic, outbox.Payload, outbox.ActorUserID,
		outbox.Status, outbox.JobVersion, outbox.ResourceID, outbox.PayloadSchemaVersion,
		outbox.TraceID, outbox.Idle,
	).Scan(&authorized, &insertedID, &existingID, &existingRequestHash, &versionInserted, &outboxID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return mailTaxonomy.ErrAlreadyExists
		}
		return fmt.Errorf("mail personal template repo: atomic create: %w", err)
	}
	if !authorized {
		return mailTaxonomy.ErrWorkspaceNotFound
	}
	if insertedID == "" {
		if existingID == "" {
			err = r.db.QueryRow(ctx, fmt.Sprintf(`
				SELECT t.id,t.create_request_sha256
				FROM %s.mail_templates AS t
				JOIN %s.personal_workspaces AS w ON w.id=t.workspace_id
				WHERE t.workspace_id=$1 AND t.scope='workspace' AND t.create_idempotency_key=$2
				  AND w.zone_id=$3 AND w.owner_id=$4
			`, r.mailSchema, r.hierarchySchema), template.WorkspaceID, template.IdempotencyKey,
				template.ZoneID, template.ActorUserID).Scan(&existingID, &existingRequestHash)
			if err != nil {
				return fmt.Errorf("mail personal template repo: resolve concurrent idempotency row: %w", err)
			}
		}
		if existingID == "" || !bytes.Equal(existingRequestHash, template.CreateRequestSHA256) {
			return mailTaxonomy.ErrIdempotencyConflict
		}
		template.ID = existingID
		return nil
	}
	if !versionInserted || !outboxID.Valid {
		return fmt.Errorf("mail personal template repo: create CTE incomplete: %w", mailTaxonomy.ErrInternal)
	}
	outbox.ID = outboxID.Int64
	return nil
}

func (r *personalTemplateRepoPostgres) GetByID(ctx context.Context, query *mailEntity.PersonalTemplate) (*mailEntity.PersonalTemplate, error) {
	template := &mailEntity.PersonalTemplate{}
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT t.id,t.workspace_id,t.scope,t.name,t.current_version,t.template_revision,t.status,
		       COALESCE(t.create_idempotency_key,''),COALESCE(t.create_request_sha256,''::bytea),t.archived_at,t.created_by,t.created_at,t.updated_at,
		       v.template_id,v.version,v.subject_template,v.text_template,v.html_template,
		       v.variable_schema_json,v.content_sha256,v.created_by,v.created_at
		FROM %s.mail_templates AS t
		JOIN %s.mail_template_versions AS v ON v.template_id=t.id AND v.version=t.current_version
		WHERE t.id=$1 AND (t.scope='platform' OR t.workspace_id=$2)
		  AND EXISTS(SELECT 1 FROM %s.personal_workspaces WHERE id=$2 AND zone_id=$3 AND owner_id=$4)
	`, r.mailSchema, r.mailSchema, r.hierarchySchema), query.ID, query.WorkspaceID, query.ZoneID, query.ActorUserID).Scan(
		&template.ID, &template.WorkspaceID, &template.Scope, &template.Name,
		&template.CurrentVersion, &template.TemplateRevision, &template.Status,
		&template.IdempotencyKey, &template.CreateRequestSHA256,
		&template.ArchivedAt, &template.CreatedBy, &template.CreatedAt, &template.UpdatedAt,
		&template.TemplateID, &template.Version, &template.SubjectTemplate, &template.TextTemplate,
		&template.HTMLTemplate, &template.VariableSchemaJSON, &template.ContentSHA256,
		&template.VersionCreatedBy, &template.VersionCreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, mailTaxonomy.ErrTemplateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("mail personal template repo: get: %w", err)
	}
	return template, nil
}

func (r *personalTemplateRepoPostgres) List(ctx context.Context, query *mailEntity.PersonalTemplate) ([]*mailEntity.PersonalTemplate, error) {
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT t.id,t.workspace_id,t.scope,t.name,t.current_version,t.template_revision,t.status,
		       COALESCE(t.create_idempotency_key,''),COALESCE(t.create_request_sha256,''::bytea),t.archived_at,t.created_by,t.created_at,t.updated_at
		FROM %s.mail_templates AS t
		WHERE (t.scope='platform' OR t.workspace_id=$1) AND ($4='' OR t.id>$4)
		  AND EXISTS(SELECT 1 FROM %s.personal_workspaces WHERE id=$1 AND zone_id=$2 AND owner_id=$3)
		ORDER BY t.id ASC LIMIT $5
	`, r.mailSchema, r.hierarchySchema), query.WorkspaceID, query.ZoneID, query.ActorUserID, query.AfterID, query.Limit)
	if err != nil {
		return nil, fmt.Errorf("mail personal template repo: list: %w", err)
	}
	defer rows.Close()
	templates := make([]*mailEntity.PersonalTemplate, 0, query.Limit)
	for rows.Next() {
		template := &mailEntity.PersonalTemplate{}
		// [COMMENT]: Inline scan từng dòng kết quả danh sách template cho Personal scope
		if err = rows.Scan(
			&template.ID, &template.WorkspaceID, &template.Scope, &template.Name,
			&template.CurrentVersion, &template.TemplateRevision, &template.Status,
			&template.IdempotencyKey, &template.CreateRequestSHA256,
			&template.ArchivedAt, &template.CreatedBy, &template.CreatedAt, &template.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("mail personal template repo: scan list: %w", err)
		}
		templates = append(templates, template)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("mail personal template repo: iterate list: %w", err)
	}
	return templates, nil
}

func (r *personalTemplateRepoPostgres) ListVersions(ctx context.Context, query *mailEntity.PersonalTemplate) ([]*mailEntity.PersonalTemplate, error) {
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT v.template_id,v.version,v.subject_template,v.text_template,v.html_template,
		       v.variable_schema_json,v.content_sha256,v.created_by,v.created_at
		FROM %s.mail_template_versions AS v
		JOIN %s.mail_templates AS t ON t.id=v.template_id
		WHERE v.template_id=$1 AND (t.scope='platform' OR t.workspace_id=$2)
		  AND ($5::bigint=0 OR v.version<$5)
		  AND EXISTS(SELECT 1 FROM %s.personal_workspaces WHERE id=$2 AND zone_id=$3 AND owner_id=$4)
		ORDER BY v.version DESC LIMIT $6
	`, r.mailSchema, r.mailSchema, r.hierarchySchema), query.ID, query.WorkspaceID, query.ZoneID, query.ActorUserID, query.BeforeVersion, query.Limit)
	if err != nil {
		return nil, fmt.Errorf("mail personal template repo: list versions: %w", err)
	}
	defer rows.Close()
	versions := make([]*mailEntity.PersonalTemplate, 0, query.Limit)
	for rows.Next() {
		version := &mailEntity.PersonalTemplate{}
		// [COMMENT]: Inline scan từng phiên bản template version cho Personal scope
		if err = rows.Scan(
			&version.TemplateID, &version.Version, &version.SubjectTemplate,
			&version.TextTemplate, &version.HTMLTemplate, &version.VariableSchemaJSON,
			&version.ContentSHA256, &version.VersionCreatedBy, &version.VersionCreatedAt,
		); err != nil {
			return nil, fmt.Errorf("mail personal template repo: scan version: %w", err)
		}
		versions = append(versions, version)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("mail personal template repo: iterate versions: %w", err)
	}
	if len(versions) == 0 {
		// [COMMENT]: Trả 404 cho template không thuộc scope; template hợp lệ nhưng chưa có version là trạng thái DB bất khả thi.
		if _, getErr := r.GetByID(ctx, query); getErr != nil {
			return nil, getErr
		}
	}
	return versions, nil
}

func (r *personalTemplateRepoPostgres) PublishVersion(ctx context.Context, template *mailEntity.PersonalTemplate, outbox *mailEntity.MailOutboxRecord) error {
	var authorized, versionInserted, headUpdated bool
	var currentVersion, currentRevision uint64
	var outboxID sql.NullInt64
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		WITH authorized AS (
			SELECT 1 FROM %s.personal_workspaces WHERE id=$1 AND zone_id=$2 AND owner_id=$3
		), target AS (
			SELECT current_version,template_revision FROM %s.mail_templates
			WHERE id=$4 AND workspace_id=$1 AND scope='workspace' AND status='active'
			  AND EXISTS(SELECT 1 FROM authorized)
		), version_inserted AS (
			INSERT INTO %s.mail_template_versions (
				template_id,version,subject_template,text_template,html_template,
				variable_schema_json,content_sha256,created_by,created_at
			)
			SELECT $5,$6,$7,$8,$9,$10,$11,$12,$13 FROM target
			WHERE template_revision=$14 AND $6=current_version+1 AND EXISTS(SELECT 1 FROM authorized)
			ON CONFLICT DO NOTHING RETURNING template_id
		), head_updated AS (
			UPDATE %s.mail_templates
			SET current_version=$6,template_revision=$15,updated_at=$16
			WHERE id=$4 AND workspace_id=$1 AND template_revision=$14 AND status='active'
			  AND EXISTS(SELECT 1 FROM version_inserted) RETURNING id
		), outbox_inserted AS (
			INSERT INTO %s.mail_outbox_records (
				event_id,routing_scope,job_topic,payload,actor_user_id,status,
				job_version,resource_id,payload_schema_version,trace_id,idle
			)
			SELECT $17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27 FROM head_updated RETURNING id
		)
		SELECT EXISTS(SELECT 1 FROM authorized),COALESCE((SELECT current_version FROM target),0),
		       COALESCE((SELECT template_revision FROM target),0),EXISTS(SELECT 1 FROM version_inserted),
		       EXISTS(SELECT 1 FROM head_updated),(SELECT id FROM outbox_inserted)
	`, r.hierarchySchema, r.mailSchema, r.mailSchema, r.mailSchema, r.mailSchema),
		template.WorkspaceID, template.ZoneID, template.ActorUserID, template.ID,
		template.TemplateID, template.Version, template.SubjectTemplate, template.TextTemplate,
		template.HTMLTemplate, template.VariableSchemaJSON, template.ContentSHA256,
		template.VersionCreatedBy, template.VersionCreatedAt, template.ExpectedRevision, template.TemplateRevision,
		template.UpdatedAt, outbox.EventID, outbox.RoutingScope, outbox.JobTopic,
		outbox.Payload, outbox.ActorUserID, outbox.Status, outbox.JobVersion,
		outbox.ResourceID, outbox.PayloadSchemaVersion, outbox.TraceID, outbox.Idle,
	).Scan(&authorized, &currentVersion, &currentRevision, &versionInserted, &headUpdated, &outboxID)
	if err != nil {
		return fmt.Errorf("mail personal template repo: atomic publish: %w", err)
	}
	if !authorized || currentRevision == 0 {
		return mailTaxonomy.ErrTemplateNotFound
	}
	if currentRevision != template.ExpectedRevision || template.Version != currentVersion+1 || !versionInserted || !headUpdated {
		return mailTaxonomy.ErrVersionConflict
	}
	if !outboxID.Valid {
		return fmt.Errorf("mail personal template repo: publish outbox CTE returned no row: %w", mailTaxonomy.ErrInternal)
	}
	outbox.ID = outboxID.Int64
	return nil
}

func (r *personalTemplateRepoPostgres) Archive(ctx context.Context, template *mailEntity.PersonalTemplate, outbox *mailEntity.MailOutboxRecord) error {
	var authorized, archived bool
	var currentRevision uint64
	var outboxID sql.NullInt64
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		WITH authorized AS (
			SELECT 1 FROM %s.personal_workspaces WHERE id=$1 AND zone_id=$2 AND owner_id=$3
		), target AS (
			SELECT template_revision FROM %s.mail_templates
			WHERE id=$4 AND workspace_id=$1 AND scope='workspace' AND status='active'
			  AND EXISTS(SELECT 1 FROM authorized)
		), archived AS (
			UPDATE %s.mail_templates SET status='archived',archived_at=$6,
			       template_revision=template_revision+1,updated_at=$6
			WHERE id=$4 AND workspace_id=$1 AND template_revision=$5 AND status='active'
			  AND EXISTS(SELECT 1 FROM authorized) RETURNING id
		), outbox_inserted AS (
			INSERT INTO %s.mail_outbox_records (
				event_id,routing_scope,job_topic,payload,actor_user_id,status,
				job_version,resource_id,payload_schema_version,trace_id,idle
			)
			SELECT $7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17 FROM archived RETURNING id
		)
		SELECT EXISTS(SELECT 1 FROM authorized),COALESCE((SELECT template_revision FROM target),0),
		       EXISTS(SELECT 1 FROM archived),(SELECT id FROM outbox_inserted)
	`, r.hierarchySchema, r.mailSchema, r.mailSchema, r.mailSchema),
		template.WorkspaceID, template.ZoneID, template.ActorUserID, template.ID, template.ExpectedRevision, template.UpdatedAt,
		outbox.EventID, outbox.RoutingScope, outbox.JobTopic, outbox.Payload, outbox.ActorUserID,
		outbox.Status, outbox.JobVersion, outbox.ResourceID, outbox.PayloadSchemaVersion,
		outbox.TraceID, outbox.Idle,
	).Scan(&authorized, &currentRevision, &archived, &outboxID)
	if err != nil {
		return fmt.Errorf("mail personal template repo: atomic archive: %w", err)
	}
	if !authorized || currentRevision == 0 {
		return mailTaxonomy.ErrTemplateNotFound
	}
	if currentRevision != template.ExpectedRevision || !archived {
		return mailTaxonomy.ErrVersionConflict
	}
	if !outboxID.Valid {
		return fmt.Errorf("mail personal template repo: archive outbox CTE returned no row: %w", mailTaxonomy.ErrInternal)
	}
	outbox.ID = outboxID.Int64
	return nil
}
