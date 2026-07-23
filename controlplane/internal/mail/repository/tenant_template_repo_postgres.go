package mailRepoImpl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/config"
	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"
	mailTaxonomy "controlplane/internal/mail/taxonomy"
	mailproto "controlplane/internal/mail/transport/rpc/proto"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

var tenantMailTemplateEventNamespace = uuid.MustParse("0a2b5357-19d2-5a98-8422-441639d67b2d")

// [COMMENT]: tenantTemplateRepoPostgres chỉ thực hiện: authorization (SELECT JOIN workspace + membership),
// advisory lock (FOR UPDATE), và atomic aggregate + outbox INSERT trong CTE duy nhất.
// Service đã chuẩn bị outbox entity và staging values (compressedHTML, contentHash, templateID) trước khi gọi repo.
type tenantTemplateRepoPostgres struct {
	db              *pgxpool.Pool
	mailSchema      string
	hierarchySchema string
}

func NewTenantTemplateRepository(db *pgxpool.Pool, cfg *config.Config) mailRepoInterface.TenantTemplateRepository {
	return &tenantTemplateRepoPostgres{
		db:              db,
		mailSchema:      cfg.SchemaSQL.Mail,
		hierarchySchema: cfg.SchemaSQL.Hierarchy,
	}
}

// [COMMENT]: Create tạo mới Tenant Template và outbox record nguyên tử.
// templateID, compressedHTML, contentHash do service tính và truyền riêng — không embed vào req entity.
// outbox.ActorUserID là uuid.UUID cụ thể (không pointer) — đảm bảo tại compile time.
func (r *tenantTemplateRepoPostgres) Create(ctx context.Context, req *mailEntity.CreateTenantTemplateRequest, outbox *mailEntity.MailOutboxRecord, templateID string, compressedHTML []byte, contentHash []byte) (*mailEntity.CreateTenantTemplateResponse, error) {
	now := time.Now().UTC()
	actor := req.ActorUserID

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
		templateID, req.WorkspaceID, req.Code, req.Name, 1, 1,
		req.ZoneID, req.ActorUserID, now, now,
		templateID, 1, req.SubjectTemplate, compressedHTML, contentHash, now,
		outbox.EventID, outbox.ZoneID, outbox.JobTopic, outbox.Payload, outbox.ActorUserID, outbox.Status, outbox.JobVersion, outbox.ResourceID, outbox.PayloadSchemaVersion, outbox.TraceID, outbox.Idle,
		req.TenantID,
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
			return nil, mailTaxonomy.ErrAlreadyExists
		}
		return nil, fmt.Errorf("mail tenant template repo: atomic create: %w", err)
	}

	if !authorized {
		return nil, mailTaxonomy.ErrWorkspaceNotFound
	}
	if insertedID == "" {
		return nil, mailTaxonomy.ErrAlreadyExists
	}
	if !versionInserted || !outboxID.Valid {
		return nil, fmt.Errorf("mail tenant template repo: create CTE incomplete: %w", mailTaxonomy.ErrInternal)
	}

	return &mailEntity.CreateTenantTemplateResponse{
		ID: templateID, WorkspaceID: &req.WorkspaceID, Code: req.Code, Name: req.Name,
		CurrentVersion: 1, TemplateRevision: 1, SubjectTemplate: req.SubjectTemplate,
		RawHTML: req.RawHTML, ContentSHA256: contentHash, CreatedBy: &actor, UpdatedBy: &actor,
		OperationID: outbox.EventID, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// [COMMENT]: GetByID truy vấn thông tin mẫu Tenant Template.
// Zstd fail-close: nếu decompression lỗi → trả lỗi, không fallback raw bytes.
func (r *tenantTemplateRepoPostgres) GetByID(ctx context.Context, req *mailEntity.GetTenantTemplateRequest) (*mailEntity.GetTenantTemplateResponse, error) {
	res := &mailEntity.GetTenantTemplateResponse{}
	var compressedHTML []byte

	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT t.id, t.workspace_id, t.code, t.name, t.current_version, t.template_revision,
		       t.created_by, t.updated_by, t.created_at, t.updated_at,
		       v.subject_template, v.html_template, v.content_sha256
		FROM %s.tenant_mail_templates AS t
		JOIN %s.tenant_mail_template_versions AS v
		  ON v.template_id = t.id AND v.version = t.current_version
		JOIN %s.tenant_workspaces AS w ON w.id = t.workspace_id
		JOIN %s.tenant_memberships AS m ON m.tenant_id = w.tenant_id AND m.user_id = $4 AND m.status = 'active'
		WHERE t.id = $1 AND t.workspace_id = $2 AND w.zone_id = $3 AND w.tenant_id = $5
	`, r.mailSchema, r.mailSchema, r.hierarchySchema, r.hierarchySchema),
		req.TemplateID, req.WorkspaceID, req.ZoneID, req.ActorUserID, req.TenantID,
	).Scan(
		&res.ID, &res.WorkspaceID, &res.Code, &res.Name, &res.CurrentVersion, &res.TemplateRevision,
		&res.CreatedBy, &res.UpdatedBy, &res.CreatedAt, &res.UpdatedAt,
		&res.SubjectTemplate, &compressedHTML, &res.ContentSHA256,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, mailTaxonomy.ErrTemplateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("mail tenant template repo: get: %w", err)
	}

	// [COMMENT]: Fail-close: nếu zstd decode thất bại → trả integrity error, không fallback raw.
	if len(compressedHTML) > 0 {
		html, dErr := decompressStreamingFailClose(compressedHTML)
		if dErr != nil {
			return nil, fmt.Errorf("mail tenant template repo: get decompress html: %w", dErr)
		}
		res.RawHTML = html
	}

	return res, nil
}

// [COMMENT]: List truy vấn danh sách Tenant Template phẳng theo phân trang cursor.
func (r *tenantTemplateRepoPostgres) List(ctx context.Context, req *mailEntity.ListTenantTemplatesRequest) ([]*mailEntity.TenantTemplateItem, error) {
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT t.id, t.workspace_id, t.code, t.name, t.current_version, t.template_revision,
		       t.created_by, t.updated_by, t.created_at, t.updated_at
		FROM %s.tenant_mail_templates AS t
		JOIN %s.tenant_workspaces AS w ON w.id = t.workspace_id
		JOIN %s.tenant_memberships AS m ON m.tenant_id = w.tenant_id AND m.user_id = $3 AND m.status = 'active'
		WHERE t.workspace_id = $1 AND w.zone_id = $2 AND w.tenant_id = $6 AND ($4 = '' OR t.id > $4)
		ORDER BY t.id ASC LIMIT $5
	`, r.mailSchema, r.hierarchySchema, r.hierarchySchema),
		req.WorkspaceID, req.ZoneID, req.ActorUserID, req.AfterID, req.Limit, req.TenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("mail tenant template repo: list: %w", err)
	}
	defer rows.Close()

	items := make([]*mailEntity.TenantTemplateItem, 0, req.Limit)
	for rows.Next() {
		t := &mailEntity.TenantTemplateItem{}
		if err = rows.Scan(
			&t.ID, &t.WorkspaceID, &t.Code, &t.Name, &t.CurrentVersion, &t.TemplateRevision,
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

// [COMMENT]: ListVersions truy vấn lịch sử phiên bản Tenant Template.
// Zstd fail-close per row; không fallback raw khi lỗi.
func (r *tenantTemplateRepoPostgres) ListVersions(ctx context.Context, req *mailEntity.ListTenantTemplateVersionsRequest) ([]*mailEntity.TenantTemplateVersionItem, error) {
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT v.template_id, v.version, v.subject_template, v.html_template, v.content_sha256, v.created_by, v.created_at
		FROM %s.tenant_mail_template_versions AS v
		JOIN %s.tenant_mail_templates AS t ON t.id = v.template_id
		JOIN %s.tenant_workspaces AS w ON w.id = t.workspace_id
		JOIN %s.tenant_memberships AS m ON m.tenant_id = w.tenant_id AND m.user_id = $4 AND m.status = 'active'
		WHERE v.template_id = $1 AND t.workspace_id = $2 AND w.zone_id = $3 AND w.tenant_id = $7
		  AND v.version <= t.current_version
		  AND ($5::bigint = 0 OR v.version < $5)
		ORDER BY v.version DESC LIMIT $6
	`, r.mailSchema, r.mailSchema, r.hierarchySchema, r.hierarchySchema),
		req.TemplateID, req.WorkspaceID, req.ZoneID, req.ActorUserID, req.BeforeVersion, req.Limit, req.TenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("mail tenant template repo: list versions: %w", err)
	}
	defer rows.Close()

	items := make([]*mailEntity.TenantTemplateVersionItem, 0, req.Limit)
	for rows.Next() {
		v := &mailEntity.TenantTemplateVersionItem{}
		var compressedHTML []byte
		if err = rows.Scan(
			&v.TemplateID, &v.Version, &v.SubjectTemplate, &compressedHTML, &v.ContentSHA256, &v.CreatedBy, &v.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("mail tenant template repo: scan version: %w", err)
		}
		// [COMMENT]: Fail-close per row; lỗi decompress = integrity error, không fallback raw.
		if len(compressedHTML) > 0 {
			html, dErr := decompressStreamingFailClose(compressedHTML)
			if dErr != nil {
				return nil, fmt.Errorf("mail tenant template repo: version %d decompress html: %w", v.Version, dErr)
			}
			v.RawHTML = html
		}
		items = append(items, v)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("mail tenant template repo: iterate versions: %w", err)
	}
	if len(items) == 0 {
		if _, err = r.GetByID(ctx, &mailEntity.GetTenantTemplateRequest{ActorUserID: req.ActorUserID, TenantID: req.TenantID, ZoneID: req.ZoneID, WorkspaceID: req.WorkspaceID, TemplateID: req.TemplateID}); err != nil {
			return nil, err
		}
	}

	return items, nil
}

// [COMMENT]: PublishVersion nạp phiên bản mới cho Tenant Template.
// Repo thực hiện: authorization, lock FOR UPDATE, optimistic revision check, chặn live operation,
// CTE INSERT version + UPDATE counter + INSERT outbox.
// outbox.EventID và payload proto được finalize tại repo vì chỉ repo biết nextRevision sau lock.
// compressedHTML và contentHash đã được service tính và truyền riêng.
func (r *tenantTemplateRepoPostgres) PublishVersion(ctx context.Context, req *mailEntity.PublishTenantTemplateVersionRequest, outbox *mailEntity.MailOutboxRecord, compressedHTML []byte, contentHash []byte) (*mailEntity.PublishTenantTemplateVersionResponse, error) {
	now := time.Now().UTC()

	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("mail tenant template repo: begin publish: %w", err)
	}
	defer tx.Rollback(ctx)

	var code, name string
	var currentVersion, currentRevision, nextVersion, nextRevision uint64

	err = tx.QueryRow(ctx, fmt.Sprintf(`
		SELECT t.code, t.name, t.current_version, t.template_revision, t.next_version, t.next_template_revision
		FROM %s.tenant_mail_templates t
		JOIN %s.tenant_workspaces w ON w.id=t.workspace_id
		JOIN %s.tenant_memberships m ON m.tenant_id=w.tenant_id AND m.user_id=$4 AND m.status='active'
		WHERE t.id=$1 AND t.workspace_id=$2 AND w.zone_id=$3 AND w.tenant_id=$5
		FOR UPDATE OF t
	`, r.mailSchema, r.hierarchySchema, r.hierarchySchema), req.TemplateID, req.WorkspaceID, req.ZoneID, req.ActorUserID, req.TenantID).Scan(
		&code, &name, &currentVersion, &currentRevision, &nextVersion, &nextRevision,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, mailTaxonomy.ErrTemplateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("mail tenant template repo: lock publish target: %w", err)
	}

	if currentRevision != req.ExpectedRevision {
		return nil, mailTaxonomy.ErrVersionConflict
	}

	var lockedOperation bool
	if err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s.mail_outbox_records WHERE resource_id=$1 AND status IN ('PENDING','PROCESSING'))`, r.mailSchema), req.TemplateID).Scan(&lockedOperation); err != nil {
		return nil, fmt.Errorf("mail tenant template repo: check publish operation: %w", err)
	}
	if lockedOperation {
		return nil, mailTaxonomy.ErrOperationInProgress
	}

	// [COMMENT]: Finalize outbox eventID và build payload proto với nextRevision đúng — chỉ repo mới biết nextRevision.
	eventID := uuid.NewSHA1(tenantMailTemplateEventNamespace, fmt.Appendf(nil, "template:%s:%d:publish:%s", req.TemplateID, nextRevision, req.ZoneID))
	event := &mailproto.MailTemplateVersionPublishedV1{
		Metadata:        &mailproto.MailEventMetadataV1{EventId: eventID[:], SchemaVersion: 1, OccurredAtUnixMs: now.UnixMilli(), Producer: "controlplane-mail"},
		TemplateId:      req.TemplateID, TemplateRevision: nextRevision, TemplateVersion: nextVersion,
		SubjectTemplate: req.SubjectTemplate, HtmlTemplate: compressedHTML, ContentSha256: contentHash,
	}
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		event.Metadata.Traceparent = "00-" + spanContext.TraceID().String() + "-" + spanContext.SpanID().String() + "-" + fmt.Sprintf("%02x", byte(spanContext.TraceFlags()))
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("mail tenant template repo: marshal publish event: %w", err)
	}

	// [COMMENT]: Finalize outbox fields sau lock; TraceID đã được service extract từ ctx và truyền vào outbox.
	outbox.EventID = eventID
	outbox.Payload = payload

	var authorized, versionInserted bool
	var outboxID sql.NullInt64

	err = tx.QueryRow(ctx, fmt.Sprintf(`
		WITH authorized AS (
			SELECT 1
			FROM %s.tenant_workspaces AS w
			JOIN %s.tenant_memberships AS m ON m.tenant_id = w.tenant_id AND m.user_id = $3 AND m.status = 'active'
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
			SET next_version=$6+1, next_template_revision=$12+1, updated_by=$3, updated_at=$10
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
			EXISTS (SELECT 1 FROM version_inserted),
			(SELECT id FROM outbox_inserted)
	`, r.hierarchySchema, r.hierarchySchema, r.mailSchema, r.mailSchema, r.mailSchema, r.mailSchema, r.mailSchema),
		req.WorkspaceID, req.ZoneID, req.ActorUserID, req.TemplateID, req.TemplateID, nextVersion, req.SubjectTemplate, compressedHTML, contentHash, now, req.ExpectedRevision, nextRevision, outbox.EventID, outbox.ZoneID, outbox.JobTopic, outbox.Payload, outbox.ActorUserID, outbox.Status, outbox.JobVersion, outbox.ResourceID, outbox.PayloadSchemaVersion, outbox.TraceID, outbox.Idle, req.TenantID,
	).Scan(
		&authorized,
		&versionInserted,
		&outboxID,
	)

	if err != nil {
		return nil, fmt.Errorf("mail tenant template repo: atomic publish: %w", err)
	}

	if !authorized {
		return nil, mailTaxonomy.ErrTemplateNotFound
	}
	if !versionInserted || !outboxID.Valid {
		return nil, mailTaxonomy.ErrVersionConflict
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("mail tenant template repo: commit publish: %w", err)
	}

	actor := req.ActorUserID
	// [COMMENT]: CurrentRevision = active head revision không đổi; PublishedRevision = nextRevision vừa được ghi.
	return &mailEntity.PublishTenantTemplateVersionResponse{
		ID: req.TemplateID, WorkspaceID: &req.WorkspaceID, Code: code, Name: name,
		CurrentVersion: currentVersion, CurrentRevision: currentRevision,
		PublishedVersion: nextVersion, PublishedRevision: nextRevision,
		SubjectTemplate: req.SubjectTemplate, RawHTML: req.RawHTML, ContentSHA256: contentHash,
		UpdatedBy: &actor, OperationID: eventID,
		HeadCreatedAt: now, CandidateCreatedAt: now,
	}, nil
}

// [COMMENT]: Delete xóa Tenant Template bằng tombstone outbox.
// Repo finalize payload proto với revision đúng sau khi lock và đọc nextRevision.
func (r *tenantTemplateRepoPostgres) Delete(ctx context.Context, req *mailEntity.DeleteTenantTemplateRequest, outbox *mailEntity.MailOutboxRecord) (uuid.UUID, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return uuid.Nil, fmt.Errorf("mail tenant template repo: begin delete: %w", err)
	}
	defer tx.Rollback(ctx)

	var currentVersion, revision, nextRevision uint64
	err = tx.QueryRow(ctx, fmt.Sprintf(`
		SELECT t.current_version, t.template_revision, t.next_template_revision
		FROM %s.tenant_mail_templates t
		JOIN %s.tenant_workspaces w ON w.id=t.workspace_id
		JOIN %s.tenant_memberships m ON m.tenant_id=w.tenant_id AND m.user_id=$4 AND m.status='active'
		WHERE t.id=$1 AND t.workspace_id=$2 AND w.zone_id=$3 AND w.tenant_id=$5
		FOR UPDATE OF t
	`, r.mailSchema, r.hierarchySchema, r.hierarchySchema), req.TemplateID, req.WorkspaceID, req.ZoneID, req.ActorUserID, req.TenantID).Scan(&currentVersion, &revision, &nextRevision)

	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, mailTaxonomy.ErrTemplateNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("mail tenant template repo: lock delete target: %w", err)
	}
	if revision != req.ExpectedRevision {
		return uuid.Nil, mailTaxonomy.ErrVersionConflict
	}

	var liveOperation bool
	if err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s.mail_outbox_records WHERE resource_id=$1 AND status IN ('PENDING','PROCESSING'))`, r.mailSchema), req.TemplateID).Scan(&liveOperation); err != nil {
		return uuid.Nil, fmt.Errorf("mail tenant template repo: check live operation: %w", err)
	}
	if liveOperation {
		return uuid.Nil, mailTaxonomy.ErrOperationInProgress
	}

	var inUse bool
	if err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT EXISTS (
		SELECT 1 FROM %s.mail_consumers c
		WHERE c.workspace_id=$1 AND c.template_id=$2
		UNION ALL
		SELECT 1 FROM %s.mail_consumer_update_versions candidate
		JOIN %s.mail_consumers active ON active.id=candidate.consumer_id
		WHERE active.workspace_id=$1 AND candidate.template_id=$2
		  AND candidate.config_version > active.config_version
	)`, r.mailSchema, r.mailSchema, r.mailSchema), req.WorkspaceID, req.TemplateID).Scan(&inUse); err != nil {
		return uuid.Nil, fmt.Errorf("mail tenant template repo: check template usage: %w", err)
	}
	if inUse {
		return uuid.Nil, mailTaxonomy.ErrTemplateInUse
	}

	now := time.Now().UTC()
	// [COMMENT]: Finalize proto event delete dengan revision đúng — chỉ repo biết nextRevision sau lock.
	event := &mailproto.MailTemplateDeletedV1{
		Metadata:             &mailproto.MailEventMetadataV1{EventId: outbox.EventID[:], SchemaVersion: 1, OccurredAtUnixMs: now.UnixMilli(), Producer: "controlplane-mail"},
		TemplateId:           req.TemplateID, TemplateRevision: nextRevision, LastPublishedVersion: currentVersion,
	}
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		event.Metadata.Traceparent = "00-" + spanContext.TraceID().String() + "-" + spanContext.SpanID().String() + "-" + fmt.Sprintf("%02x", byte(spanContext.TraceFlags()))
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(event)
	if err != nil {
		return uuid.Nil, fmt.Errorf("mail tenant template repo: marshal delete event: %w", err)
	}
	outbox.Payload = payload

	var deletedID sql.NullInt64
	if err = tx.QueryRow(ctx, fmt.Sprintf(`INSERT INTO %s.mail_outbox_records (event_id,zone_id,job_topic,payload,actor_user_id,status,job_version,resource_id,payload_schema_version,trace_id,idle) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`, r.mailSchema), outbox.EventID, outbox.ZoneID, outbox.JobTopic, outbox.Payload, outbox.ActorUserID, outbox.Status, outbox.JobVersion, outbox.ResourceID, outbox.PayloadSchemaVersion, outbox.TraceID, outbox.Idle).Scan(&deletedID); err != nil {
		return uuid.Nil, fmt.Errorf("mail tenant template repo: insert delete outbox: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("mail tenant template repo: commit delete: %w", err)
	}

	return outbox.EventID, nil
}
