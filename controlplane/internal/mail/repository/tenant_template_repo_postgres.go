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

type tenantTemplateRepoPostgres struct {
	db              *pgxpool.Pool
	mailSchema      string
	hierarchySchema string
}

// NewTenantTemplateRepository khoi tao repository quan ly Tenant Mail Template
func NewTenantTemplateRepository(db *pgxpool.Pool, cfg *config.Config) mailRepoInterface.TenantTemplateRepository {
	return &tenantTemplateRepoPostgres{
		db:              db,
		mailSchema:      cfg.SchemaSQL.Mail,
		hierarchySchema: cfg.SchemaSQL.Hierarchy,
	}
}

func (r *tenantTemplateRepoPostgres) Create(ctx context.Context, template *mailEntity.TenantTemplate, outbox *mailEntity.MailOutboxRecord) error {
	// [COMMENT]: Template projection phải dùng đúng Zone đã đi qua tenant/workspace guard.
	if template == nil || outbox == nil || outbox.ZoneID != template.ZoneID {
		return mailTaxonomy.ErrInvalidArgument
	}
	var authorized, versionInserted bool
	var insertedID, existingID string
	var outboxID sql.NullInt64

	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		WITH authorized AS (
			SELECT 1
			FROM %s.tenant_workspaces AS w
			JOIN %s.tenant_memberships AS m
			  ON m.tenant_id = w.tenant_id AND m.user_id = $8 AND m.status = 'active'
			WHERE w.id = $2 AND w.zone_id = $7 AND w.tenant_id = $28
		), identity_inserted AS (
			INSERT INTO %s.tenant_mail_templates (
				id, workspace_id, code, name, current_version, template_revision,
				next_version, next_template_revision, created_by, updated_by, created_at, updated_at
			)
			SELECT $1, $2, $3, $4, $5, $6, $5 + 1, $6 + 1, $8, $8, $9, $10
			WHERE EXISTS (SELECT 1 FROM authorized)
			ON CONFLICT (workspace_id, code) DO NOTHING
			RETURNING id
		), existing AS (
			SELECT id
			FROM %s.tenant_mail_templates
			WHERE workspace_id = $2 AND code = $3
			  AND EXISTS (SELECT 1 FROM authorized)
		), version_inserted AS (
			INSERT INTO %s.tenant_mail_template_versions (
				template_id, version, template_revision, event_id,
				subject_template, html_template, content_sha256, created_by, created_at
			)
			SELECT $11, $12, $6, $17, $13, $14, $15, $8, $16
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
	`, r.hierarchySchema, r.hierarchySchema, r.mailSchema, r.mailSchema, r.mailSchema, r.mailSchema),
		template.ID, template.WorkspaceID, template.Code, template.Name, template.CurrentVersion, template.TemplateRevision,
		template.ZoneID, template.ActorUserID, template.CreatedAt, template.UpdatedAt,
		template.TemplateID, template.Version, template.SubjectTemplate, template.HTMLTemplate, template.ContentSHA256, template.VersionCreatedAt,
		outbox.EventID, outbox.ZoneID, outbox.JobTopic, outbox.Payload, outbox.ActorUserID, outbox.Status, outbox.JobVersion, outbox.ResourceID, outbox.PayloadSchemaVersion, outbox.TraceID, outbox.Idle, template.TenantID,
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
		return fmt.Errorf("mail tenant template repo: atomic create: %w", err)
	}

	if !authorized {
		return mailTaxonomy.ErrWorkspaceNotFound
	}

	if insertedID == "" {
		return mailTaxonomy.ErrAlreadyExists
	}

	if !versionInserted || !outboxID.Valid {
		return fmt.Errorf("mail tenant template repo: create CTE incomplete: %w", mailTaxonomy.ErrInternal)
	}

	outbox.ID = outboxID.Int64
	return nil
}

func (r *tenantTemplateRepoPostgres) GetByID(ctx context.Context, query *mailEntity.TenantTemplate) (*mailEntity.TenantTemplate, error) {
	template := &mailEntity.TenantTemplate{}

	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT t.id, t.workspace_id, t.code, t.name, t.current_version, t.template_revision,
		       t.next_version, t.next_template_revision,
		       t.created_by, t.updated_by, t.created_at, t.updated_at,
		       v.template_id, v.version, v.subject_template, v.html_template, v.content_sha256, v.created_by, v.created_at
		FROM %s.tenant_mail_templates AS t
		JOIN %s.tenant_mail_template_versions AS v
		  ON v.template_id = t.id AND v.version = t.current_version
		WHERE t.id = $1 AND t.workspace_id = $2
		  AND EXISTS (
		      SELECT 1
		      FROM %s.tenant_workspaces AS w
		      JOIN %s.tenant_memberships AS m
		        ON m.tenant_id = w.tenant_id AND m.user_id = $4 AND m.status = 'active'
		      WHERE w.id = $2 AND w.zone_id = $3 AND w.tenant_id = $5
		  )
	`, r.mailSchema, r.mailSchema, r.hierarchySchema, r.hierarchySchema),
		query.ID, query.WorkspaceID, query.ZoneID, query.ActorUserID, query.TenantID,
	).Scan(
		&template.ID, &template.WorkspaceID, &template.Code, &template.Name, &template.CurrentVersion, &template.TemplateRevision,
		&template.NextVersion, &template.NextRevision,
		&template.CreatedBy, &template.UpdatedBy, &template.CreatedAt, &template.UpdatedAt,
		&template.TemplateID, &template.Version, &template.SubjectTemplate, &template.HTMLTemplate, &template.ContentSHA256, &template.VersionCreatedBy, &template.VersionCreatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, mailTaxonomy.ErrTemplateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("mail tenant template repo: get: %w", err)
	}

	return template, nil
}

func (r *tenantTemplateRepoPostgres) List(ctx context.Context, query *mailEntity.TenantTemplate) ([]*mailEntity.TenantTemplate, error) {
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT t.id, t.workspace_id, t.code, t.name, t.current_version, t.template_revision,
		       t.next_version, t.next_template_revision,
		       t.created_by, t.updated_by, t.created_at, t.updated_at
		FROM %s.tenant_mail_templates AS t
		WHERE t.workspace_id = $1 AND ($4 = '' OR t.id > $4)
		  AND EXISTS (
		      SELECT 1
		      FROM %s.tenant_workspaces AS w
		      JOIN %s.tenant_memberships AS m
		        ON m.tenant_id = w.tenant_id AND m.user_id = $3 AND m.status = 'active'
		      WHERE w.id = $1 AND w.zone_id = $2 AND w.tenant_id = $6
		  )
		ORDER BY t.id ASC LIMIT $5
	`, r.mailSchema, r.hierarchySchema, r.hierarchySchema),
		query.WorkspaceID, query.ZoneID, query.ActorUserID, query.AfterID, query.Limit, query.TenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("mail tenant template repo: list: %w", err)
	}
	defer rows.Close()

	items := make([]*mailEntity.TenantTemplate, 0, query.Limit)
	for rows.Next() {
		t := &mailEntity.TenantTemplate{}
		if err = rows.Scan(
			&t.ID, &t.WorkspaceID, &t.Code, &t.Name, &t.CurrentVersion, &t.TemplateRevision,
			&t.NextVersion, &t.NextRevision,
			&t.CreatedBy, &t.UpdatedBy, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("mail tenant template repo: scan list: %w", err)
		}
		items = append(items, t)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("mail tenant template repo: iterate list: %w", err)
	}
	return items, nil
}

func (r *tenantTemplateRepoPostgres) ListVersions(ctx context.Context, query *mailEntity.TenantTemplate) ([]*mailEntity.TenantTemplate, error) {
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT v.template_id, v.version, v.subject_template, v.html_template, v.content_sha256, v.created_by, v.created_at
		FROM %s.tenant_mail_template_versions AS v
		JOIN %s.tenant_mail_templates AS t ON t.id = v.template_id
		WHERE v.template_id = $1 AND t.workspace_id = $2
		  AND v.version <= t.current_version
		  AND ($5::bigint = 0 OR v.version < $5)
		  AND EXISTS (
		      SELECT 1
		      FROM %s.tenant_workspaces AS w
		      JOIN %s.tenant_memberships AS m
		        ON m.tenant_id = w.tenant_id AND m.user_id = $4 AND m.status = 'active'
		      WHERE w.id = $2 AND w.zone_id = $3 AND w.tenant_id = $7
		  )
		ORDER BY v.version DESC LIMIT $6
	`, r.mailSchema, r.mailSchema, r.hierarchySchema, r.hierarchySchema),
		query.ID, query.WorkspaceID, query.ZoneID, query.ActorUserID, query.BeforeVersion, query.Limit, query.TenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("mail tenant template repo: list versions: %w", err)
	}
	defer rows.Close()

	items := make([]*mailEntity.TenantTemplate, 0, query.Limit)
	for rows.Next() {
		v := &mailEntity.TenantTemplate{}
		if err = rows.Scan(
			&v.TemplateID, &v.Version, &v.SubjectTemplate, &v.HTMLTemplate, &v.ContentSHA256, &v.VersionCreatedBy, &v.VersionCreatedAt,
		); err != nil {
			return nil, fmt.Errorf("mail tenant template repo: scan version: %w", err)
		}
		items = append(items, v)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("mail tenant template repo: iterate versions: %w", err)
	}
	if len(items) == 0 {
		if _, err = r.GetByID(ctx, query); err != nil {
			return nil, err
		}
	}

	return items, nil
}

func (r *tenantTemplateRepoPostgres) PublishVersion(ctx context.Context, template *mailEntity.TenantTemplate, outbox *mailEntity.MailOutboxRecord) error {
	// [COMMENT]: Version event không được route lệch khỏi Zone của aggregate đã authorize.
	if template == nil || outbox == nil || outbox.ZoneID != template.ZoneID {
		return mailTaxonomy.ErrInvalidArgument
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("mail tenant template repo: begin publish: %w", err)
	}
	defer tx.Rollback(ctx)

	// [COMMENT]: Lock và live-operation read tách statement để snapshot sau lock nhìn thấy delete/publish vừa commit.
	var lockedTemplateID string
	err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT t.id FROM %s.tenant_mail_templates t JOIN %s.tenant_workspaces w ON w.id=t.workspace_id JOIN %s.tenant_memberships m ON m.tenant_id=w.tenant_id AND m.user_id=$4 AND m.status='active' WHERE t.id=$1 AND t.workspace_id=$2 AND w.zone_id=$3 AND w.tenant_id=$5 FOR UPDATE OF t`, r.mailSchema, r.hierarchySchema, r.hierarchySchema), template.ID, template.WorkspaceID, template.ZoneID, template.ActorUserID, template.TenantID).Scan(&lockedTemplateID)
	if errors.Is(err, pgx.ErrNoRows) {
		return mailTaxonomy.ErrTemplateNotFound
	}
	if err != nil {
		return fmt.Errorf("mail tenant template repo: lock publish target: %w", err)
	}
	var lockedOperation bool
	if err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s.mail_outbox_records WHERE resource_id=$1 AND status IN ('PENDING','PROCESSING'))`, r.mailSchema), template.ID).Scan(&lockedOperation); err != nil {
		return fmt.Errorf("mail tenant template repo: check publish operation: %w", err)
	}
	if lockedOperation {
		return mailTaxonomy.ErrOperationInProgress
	}
	var authorized, liveOperation, versionInserted bool
	var currentVersion, currentRevision, nextVersion, nextRevision uint64
	var outboxID sql.NullInt64

	err = tx.QueryRow(ctx, fmt.Sprintf(`
		WITH authorized AS (
			SELECT 1
			FROM %s.tenant_workspaces AS w
			JOIN %s.tenant_memberships AS m
			  ON m.tenant_id = w.tenant_id AND m.user_id = $3 AND m.status = 'active'
			WHERE w.id = $1 AND w.zone_id = $2 AND w.tenant_id = $24
		), target AS MATERIALIZED (
			SELECT current_version, template_revision, next_version, next_template_revision
			FROM %s.tenant_mail_templates
			WHERE id = $4 AND workspace_id = $1
			  AND EXISTS (SELECT 1 FROM authorized)
			FOR UPDATE
		), live_operation AS (
			SELECT 1 FROM %s.mail_outbox_records
			WHERE resource_id=$4 AND status IN ('PENDING','PROCESSING')
			LIMIT 1
		), version_inserted AS (
			INSERT INTO %s.tenant_mail_template_versions (
				template_id, version, template_revision, event_id,
				subject_template, html_template, content_sha256, created_by, created_at
			)
			SELECT $5, $6, $12, $13, $7, $8, $9, $3, $10
			FROM target
			WHERE template_revision=$11 AND next_version=$6 AND next_template_revision=$12
			  AND NOT EXISTS (SELECT 1 FROM live_operation)
			ON CONFLICT DO NOTHING
			RETURNING template_id
		), counter_updated AS (
			UPDATE %s.tenant_mail_templates
			SET next_version=$6+1, next_template_revision=$12+1
			WHERE id = $4 AND workspace_id = $1 AND template_revision = $11
			  AND next_version=$6 AND next_template_revision=$12
			  AND EXISTS (SELECT 1 FROM version_inserted)
			RETURNING id
		), outbox_inserted AS (
			INSERT INTO %s.mail_outbox_records (
				event_id, zone_id, job_topic, payload, actor_user_id, status,
				job_version, resource_id, payload_schema_version, trace_id, idle
			)
			SELECT $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23
			FROM counter_updated
			RETURNING id
		)
		SELECT
			EXISTS (SELECT 1 FROM authorized),
			COALESCE((SELECT current_version FROM target), 0),
			COALESCE((SELECT template_revision FROM target), 0),
			COALESCE((SELECT next_version FROM target), 0),
			COALESCE((SELECT next_template_revision FROM target), 0),
			EXISTS (SELECT 1 FROM live_operation),
			EXISTS (SELECT 1 FROM version_inserted),
			(SELECT id FROM outbox_inserted)
	`, r.hierarchySchema, r.hierarchySchema, r.mailSchema, r.mailSchema, r.mailSchema, r.mailSchema, r.mailSchema),
		template.WorkspaceID, template.ZoneID, template.ActorUserID, template.ID, template.TemplateID, template.Version, template.SubjectTemplate, template.HTMLTemplate, template.ContentSHA256, template.VersionCreatedAt, template.ExpectedRevision, template.TemplateRevision, outbox.EventID, outbox.ZoneID, outbox.JobTopic, outbox.Payload, outbox.ActorUserID, outbox.Status, outbox.JobVersion, outbox.ResourceID, outbox.PayloadSchemaVersion, outbox.TraceID, outbox.Idle, template.TenantID,
	).Scan(
		&authorized,
		&currentVersion,
		&currentRevision,
		&nextVersion,
		&nextRevision,
		&liveOperation,
		&versionInserted,
		&outboxID,
	)

	if err != nil {
		return fmt.Errorf("mail tenant template repo: atomic publish: %w", err)
	}

	if !authorized || currentRevision == 0 {
		return mailTaxonomy.ErrTemplateNotFound
	}
	if liveOperation {
		return mailTaxonomy.ErrOperationInProgress
	}
	if currentRevision != template.ExpectedRevision || template.Version != nextVersion || template.TemplateRevision != nextRevision || !versionInserted {
		return mailTaxonomy.ErrVersionConflict
	}
	if !outboxID.Valid {
		return fmt.Errorf("mail tenant template repo: publish outbox missing: %w", mailTaxonomy.ErrInternal)
	}

	outbox.ID = outboxID.Int64
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("mail tenant template repo: commit publish: %w", err)
	}
	return nil
}

func (r *tenantTemplateRepoPostgres) Delete(ctx context.Context, template *mailEntity.TenantTemplate, outbox *mailEntity.MailOutboxRecord) error {
	// [COMMENT]: Hard-delete tombstone phải route đúng Zone của guarded tenant workspace.
	if template == nil || outbox == nil || outbox.ZoneID != template.ZoneID {
		return mailTaxonomy.ErrInvalidArgument
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("mail tenant template repo: begin delete: %w", err)
	}
	defer tx.Rollback(ctx)
	var revision uint64
	err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT t.template_revision FROM %s.tenant_mail_templates t JOIN %s.tenant_workspaces w ON w.id=t.workspace_id JOIN %s.tenant_memberships m ON m.tenant_id=w.tenant_id AND m.user_id=$4 AND m.status='active' WHERE t.id=$1 AND t.workspace_id=$2 AND w.zone_id=$3 AND w.tenant_id=$5 FOR UPDATE OF t`, r.mailSchema, r.hierarchySchema, r.hierarchySchema), template.ID, template.WorkspaceID, template.ZoneID, template.ActorUserID, template.TenantID).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return mailTaxonomy.ErrTemplateNotFound
	}
	if err != nil {
		return fmt.Errorf("mail tenant template repo: lock delete target: %w", err)
	}
	if revision != template.ExpectedRevision {
		return mailTaxonomy.ErrVersionConflict
	}
	var liveOperation bool
	if err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s.mail_outbox_records WHERE resource_id=$1 AND status IN ('PENDING','PROCESSING'))`, r.mailSchema), template.ID).Scan(&liveOperation); err != nil {
		return fmt.Errorf("mail tenant template repo: check live operation: %w", err)
	}
	if liveOperation {
		return mailTaxonomy.ErrOperationInProgress
	}
	var inUse bool
	// [COMMENT]: Guard cả candidate consumer chưa promote; history cũ không block vì version
	// của nó không còn lớn hơn active config_version.
	if err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT EXISTS (
		SELECT 1 FROM %s.mail_consumers c
		WHERE c.workspace_id=$1 AND c.template_id=$2
		UNION ALL
		SELECT 1 FROM %s.mail_consumer_update_versions candidate
		JOIN %s.mail_consumers active ON active.id=candidate.consumer_id
		WHERE active.workspace_id=$1 AND candidate.template_id=$2
		  AND candidate.config_version > active.config_version
	)`, r.mailSchema, r.mailSchema, r.mailSchema), template.WorkspaceID, template.ID).Scan(&inUse); err != nil {
		return fmt.Errorf("mail tenant template repo: check template usage: %w", err)
	}
	if inUse {
		return mailTaxonomy.ErrTemplateInUse
	}
	// [COMMENT]: CP giữ nguyên identity/version; JO chỉ hard-delete aggregate sau khi Zone xác nhận hạ tầng đã xóa.
	if err = tx.QueryRow(ctx, fmt.Sprintf(`INSERT INTO %s.mail_outbox_records (event_id,zone_id,job_topic,payload,actor_user_id,status,job_version,resource_id,payload_schema_version,trace_id,idle) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`, r.mailSchema), outbox.EventID, outbox.ZoneID, outbox.JobTopic, outbox.Payload, outbox.ActorUserID, outbox.Status, outbox.JobVersion, outbox.ResourceID, outbox.PayloadSchemaVersion, outbox.TraceID, outbox.Idle).Scan(&outbox.ID); err != nil {
		return fmt.Errorf("mail tenant template repo: insert delete outbox: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("mail tenant template repo: commit delete: %w", err)
	}
	return nil
}
