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

type personalTemplateRepoPostgres struct {
	db              *pgxpool.Pool
	mailSchema      string
	hierarchySchema string
}

// NewPersonalTemplateRepository khoi tao repository quan ly Personal Mail Template
func NewPersonalTemplateRepository(db *pgxpool.Pool, cfg *config.Config) mailRepoInterface.PersonalTemplateRepository {
	return &personalTemplateRepoPostgres{
		db:              db,
		mailSchema:      cfg.SchemaSQL.Mail,
		hierarchySchema: cfg.SchemaSQL.Hierarchy,
	}
}

func (r *personalTemplateRepoPostgres) Create(ctx context.Context, template *mailEntity.PersonalTemplate, outbox *mailEntity.MailOutboxRecord) error {
	// [COMMENT]: Template projection phải dùng đúng Zone đã đi qua workspace ownership guard.
	if template == nil || outbox == nil || outbox.ZoneID != template.ZoneID {
		return mailTaxonomy.ErrInvalidArgument
	}
	var authorized, versionInserted bool
	var insertedID, existingID string
	var outboxID sql.NullInt64

	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		WITH authorized AS (
			SELECT 1
			FROM %s.personal_workspaces
			WHERE id = $2 AND zone_id = $7 AND owner_id = $8
		), identity_inserted AS (
			INSERT INTO %s.personal_mail_templates (
				id, workspace_id, code, name, current_version, template_revision, created_at, updated_at
			)
			SELECT $1, $2, $3, $4, $5, $6, $9, $10
			WHERE EXISTS (SELECT 1 FROM authorized)
			ON CONFLICT (workspace_id, code) DO NOTHING
			RETURNING id
		), existing AS (
			SELECT id
			FROM %s.personal_mail_templates
			WHERE workspace_id = $2 AND code = $3
			  AND EXISTS (SELECT 1 FROM authorized)
		), version_inserted AS (
			INSERT INTO %s.personal_mail_template_versions (
				template_id, version, subject_template, html_template, content_sha256, created_at
			)
			SELECT $11, $12, $13, $14, $15, $16
			FROM identity_inserted
			RETURNING template_id
		), outbox_inserted AS (
			INSERT INTO %s.mail_outbox_records (
				event_id, zone_id, job_topic, payload, actor_user_id, status,
				job_version, resource_id, payload_schema_version, trace_id, idle
			)
			SELECT $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27
			FROM version_inserted
			RETURNING id
		)
		SELECT
			EXISTS (SELECT 1 FROM authorized),
			COALESCE((SELECT id FROM identity_inserted), ''),
			COALESCE((SELECT id FROM existing), ''),
			EXISTS (SELECT 1 FROM version_inserted),
			(SELECT id FROM outbox_inserted)
	`, r.hierarchySchema, r.mailSchema, r.mailSchema, r.mailSchema, r.mailSchema),
		template.ID, template.WorkspaceID, template.Code, template.Name, template.CurrentVersion, template.TemplateRevision,
		template.ZoneID, template.ActorUserID, template.CreatedAt, template.UpdatedAt,
		template.TemplateID, template.Version, template.SubjectTemplate, template.HTMLTemplate, template.ContentSHA256, template.VersionCreatedAt,
		outbox.EventID, outbox.ZoneID, outbox.JobTopic, outbox.Payload, outbox.ActorUserID, outbox.Status, outbox.JobVersion, outbox.ResourceID, outbox.PayloadSchemaVersion, outbox.TraceID, outbox.Idle,
	).Scan(
		&authorized,
		&insertedID,
		&existingID,
		&versionInserted,
		&outboxID,
	)

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
		return mailTaxonomy.ErrAlreadyExists
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
		SELECT t.id, t.workspace_id, t.code, t.name, t.current_version, t.template_revision,
		       t.created_at, t.updated_at,
		       v.template_id, v.version, v.subject_template, v.html_template, v.content_sha256, v.created_at
		FROM %s.personal_mail_templates AS t
		JOIN %s.personal_mail_template_versions AS v
		  ON v.template_id = t.id AND v.version = t.current_version
		WHERE t.id = $1 AND t.workspace_id = $2
		  AND EXISTS (SELECT 1 FROM %s.personal_workspaces WHERE id = $2 AND zone_id = $3 AND owner_id = $4)
	`, r.mailSchema, r.mailSchema, r.hierarchySchema),
		query.ID, query.WorkspaceID, query.ZoneID, query.ActorUserID,
	).Scan(
		&template.ID, &template.WorkspaceID, &template.Code, &template.Name, &template.CurrentVersion, &template.TemplateRevision,
		&template.CreatedAt, &template.UpdatedAt,
		&template.TemplateID, &template.Version, &template.SubjectTemplate, &template.HTMLTemplate, &template.ContentSHA256, &template.VersionCreatedAt,
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
		SELECT t.id, t.workspace_id, t.code, t.name, t.current_version, t.template_revision,
		       t.created_at, t.updated_at
		FROM %s.personal_mail_templates AS t
		WHERE t.workspace_id = $1 AND ($4 = '' OR t.id > $4)
		  AND EXISTS (SELECT 1 FROM %s.personal_workspaces WHERE id = $1 AND zone_id = $2 AND owner_id = $3)
		ORDER BY t.id ASC LIMIT $5
	`, r.mailSchema, r.hierarchySchema),
		query.WorkspaceID, query.ZoneID, query.ActorUserID, query.AfterID, query.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("mail personal template repo: list: %w", err)
	}
	defer rows.Close()

	items := make([]*mailEntity.PersonalTemplate, 0, query.Limit)
	for rows.Next() {
		t := &mailEntity.PersonalTemplate{}
		if err = rows.Scan(
			&t.ID, &t.WorkspaceID, &t.Code, &t.Name, &t.CurrentVersion, &t.TemplateRevision,
			&t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("mail personal template repo: scan list: %w", err)
		}
		items = append(items, t)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("mail personal template repo: iterate list: %w", err)
	}
	return items, nil
}

func (r *personalTemplateRepoPostgres) ListVersions(ctx context.Context, query *mailEntity.PersonalTemplate) ([]*mailEntity.PersonalTemplate, error) {
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT v.template_id, v.version, v.subject_template, v.html_template, v.content_sha256, v.created_at
		FROM %s.personal_mail_template_versions AS v
		JOIN %s.personal_mail_templates AS t ON t.id = v.template_id
		WHERE v.template_id = $1 AND t.workspace_id = $2
		  AND ($5::bigint = 0 OR v.version < $5)
		  AND EXISTS (SELECT 1 FROM %s.personal_workspaces WHERE id = $2 AND zone_id = $3 AND owner_id = $4)
		ORDER BY v.version DESC LIMIT $6
	`, r.mailSchema, r.mailSchema, r.hierarchySchema),
		query.ID, query.WorkspaceID, query.ZoneID, query.ActorUserID, query.BeforeVersion, query.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("mail personal template repo: list versions: %w", err)
	}
	defer rows.Close()

	items := make([]*mailEntity.PersonalTemplate, 0, query.Limit)
	for rows.Next() {
		v := &mailEntity.PersonalTemplate{}
		if err = rows.Scan(
			&v.TemplateID, &v.Version, &v.SubjectTemplate, &v.HTMLTemplate, &v.ContentSHA256, &v.VersionCreatedAt,
		); err != nil {
			return nil, fmt.Errorf("mail personal template repo: scan version: %w", err)
		}
		items = append(items, v)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("mail personal template repo: iterate versions: %w", err)
	}
	if len(items) == 0 {
		if _, err = r.GetByID(ctx, query); err != nil {
			return nil, err
		}
	}

	return items, nil
}

func (r *personalTemplateRepoPostgres) PublishVersion(ctx context.Context, template *mailEntity.PersonalTemplate, outbox *mailEntity.MailOutboxRecord) error {
	// [COMMENT]: Version event không được route lệch khỏi Zone của aggregate đã authorize.
	if template == nil || outbox == nil || outbox.ZoneID != template.ZoneID {
		return mailTaxonomy.ErrInvalidArgument
	}
	var authorized, versionInserted, updated bool
	var currentVersion, currentRevision uint64
	var outboxID sql.NullInt64

	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		WITH authorized AS (
			SELECT 1
			FROM %s.personal_workspaces
			WHERE id = $1 AND zone_id = $2 AND owner_id = $3
		), target AS (
			SELECT current_version, template_revision
			FROM %s.personal_mail_templates
			WHERE id = $4 AND workspace_id = $1
			  AND EXISTS (SELECT 1 FROM authorized)
		), version_inserted AS (
			INSERT INTO %s.personal_mail_template_versions (
				template_id, version, subject_template, html_template, content_sha256, created_at
			)
			SELECT $5, $6, $7, $8, $9, $10
			FROM target
			WHERE template_revision = $11 AND $6 = current_version + 1
			ON CONFLICT DO NOTHING
			RETURNING template_id
		), head_updated AS (
			UPDATE %s.personal_mail_templates
			SET current_version = $6, template_revision = $12, updated_at = $13
			WHERE id = $4 AND workspace_id = $1 AND template_revision = $11
			  AND EXISTS (SELECT 1 FROM version_inserted)
			RETURNING id
		), outbox_inserted AS (
			INSERT INTO %s.mail_outbox_records (
				event_id, zone_id, job_topic, payload, actor_user_id, status,
				job_version, resource_id, payload_schema_version, trace_id, idle
			)
			SELECT $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24
			FROM head_updated
			RETURNING id
		)
		SELECT
			EXISTS (SELECT 1 FROM authorized),
			COALESCE((SELECT current_version FROM target), 0),
			COALESCE((SELECT template_revision FROM target), 0),
			EXISTS (SELECT 1 FROM version_inserted),
			EXISTS (SELECT 1 FROM head_updated),
			(SELECT id FROM outbox_inserted)
	`, r.hierarchySchema, r.mailSchema, r.mailSchema, r.mailSchema, r.mailSchema),
		template.WorkspaceID, template.ZoneID, template.ActorUserID, template.ID, template.TemplateID, template.Version, template.SubjectTemplate, template.HTMLTemplate, template.ContentSHA256, template.VersionCreatedAt, template.ExpectedRevision, template.TemplateRevision, template.UpdatedAt, outbox.EventID, outbox.ZoneID, outbox.JobTopic, outbox.Payload, outbox.ActorUserID, outbox.Status, outbox.JobVersion, outbox.ResourceID, outbox.PayloadSchemaVersion, outbox.TraceID, outbox.Idle,
	).Scan(
		&authorized,
		&currentVersion,
		&currentRevision,
		&versionInserted,
		&updated,
		&outboxID,
	)

	if err != nil {
		return fmt.Errorf("mail personal template repo: atomic publish: %w", err)
	}

	if !authorized || currentRevision == 0 {
		return mailTaxonomy.ErrTemplateNotFound
	}
	if currentRevision != template.ExpectedRevision || template.Version != currentVersion+1 || !versionInserted || !updated {
		return mailTaxonomy.ErrVersionConflict
	}
	if !outboxID.Valid {
		return fmt.Errorf("mail personal template repo: publish outbox missing: %w", mailTaxonomy.ErrInternal)
	}

	outbox.ID = outboxID.Int64
	return nil
}

func (r *personalTemplateRepoPostgres) Delete(ctx context.Context, template *mailEntity.PersonalTemplate, outbox *mailEntity.MailOutboxRecord) error {
	// [COMMENT]: Hard-delete tombstone phải route đúng Zone của guarded workspace.
	if template == nil || outbox == nil || outbox.ZoneID != template.ZoneID {
		return mailTaxonomy.ErrInvalidArgument
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("mail personal template repo: begin delete: %w", err)
	}
	defer tx.Rollback(ctx)
	var revision uint64
	err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT t.template_revision FROM %s.personal_mail_templates t JOIN %s.personal_workspaces w ON w.id=t.workspace_id WHERE t.id=$1 AND t.workspace_id=$2 AND w.zone_id=$3 AND w.owner_id=$4 FOR UPDATE OF t`, r.mailSchema, r.hierarchySchema), template.ID, template.WorkspaceID, template.ZoneID, template.ActorUserID).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return mailTaxonomy.ErrTemplateNotFound
	}
	if err != nil {
		return fmt.Errorf("mail personal template repo: lock delete target: %w", err)
	}
	if revision != template.ExpectedRevision {
		return mailTaxonomy.ErrVersionConflict
	}
	var inUse bool
	if err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s.mail_consumers WHERE workspace_id=$1 AND template_id=$2)`, r.mailSchema), template.WorkspaceID, template.ID).Scan(&inUse); err != nil {
		return fmt.Errorf("mail personal template repo: check template usage: %w", err)
	}
	if inUse {
		return mailTaxonomy.ErrTemplateInUse
	}
	// [COMMENT]: Immutable versions chỉ được xóa trong transaction hard-delete đã khóa identity.
	if _, err = tx.Exec(ctx, `SELECT set_config('mail.allow_template_version_mutation','on',true)`); err != nil {
		return fmt.Errorf("mail personal template repo: enable aggregate delete: %w", err)
	}
	if _, err = tx.Exec(ctx, fmt.Sprintf(`DELETE FROM %s.personal_mail_templates WHERE id=$1 AND workspace_id=$2`, r.mailSchema), template.ID, template.WorkspaceID); err != nil {
		return fmt.Errorf("mail personal template repo: hard delete: %w", err)
	}
	// [COMMENT]: Tombstone là rebuild authority sau outbox retention; không chứa template body hay owner data.
	if _, err = tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s.personal_mail_template_projection_tombstones (template_id,workspace_id,template_revision,last_published_version,event_id,deleted_at) VALUES ($1,$2,$3,$4,$5,$6)`, r.mailSchema), template.ID, template.WorkspaceID, template.ExpectedRevision+1, template.CurrentVersion, outbox.EventID, template.UpdatedAt); err != nil {
		return fmt.Errorf("mail personal template repo: insert projection tombstone: %w", err)
	}
	if err = tx.QueryRow(ctx, fmt.Sprintf(`INSERT INTO %s.mail_outbox_records (event_id,zone_id,job_topic,payload,actor_user_id,status,job_version,resource_id,payload_schema_version,trace_id,idle) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`, r.mailSchema), outbox.EventID, outbox.ZoneID, outbox.JobTopic, outbox.Payload, outbox.ActorUserID, outbox.Status, outbox.JobVersion, outbox.ResourceID, outbox.PayloadSchemaVersion, outbox.TraceID, outbox.Idle).Scan(&outbox.ID); err != nil {
		return fmt.Errorf("mail personal template repo: insert delete outbox: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("mail personal template repo: commit delete: %w", err)
	}
	return nil
}
