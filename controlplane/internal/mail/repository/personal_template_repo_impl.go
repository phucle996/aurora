package mailRepoImpl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"time"

	"controlplane/internal/config"
	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"
	mailTaxonomy "controlplane/internal/mail/taxonomy"
	mailproto "controlplane/internal/mail/transport/proto"
	jobpayload "controlplane/internal/security"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/klauspost/compress/zstd"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

const personalTemplateMaxDecompressBytes = 3 << 20 // 3 MB

var (
	zstdEncoder, _ = zstd.NewWriter(nil)
	zstdDecoder, _ = zstd.NewReader(nil)
)

var personalMailTemplateEventNamespace = uuid.MustParse("9314352a-19ba-5808-b8e2-14e06df7b791")

// [COMMENT]: personalTemplateRepoPostgres chỉ thực hiện: authorization (SELECT JOIN workspace), advisory lock (FOR UPDATE),
// và atomic aggregate + outbox INSERT trong CTE duy nhất.
// Service đã chuẩn bị outbox entity và staging values (compressedHTML, contentHash, templateID) trước khi gọi repo.
type personalTemplateRepoPostgres struct {
	db              *pgxpool.Pool
	mailSchema      string
	hierarchySchema string
	protector       jobpayload.Protector
}

func NewPersonalTemplateRepository(db *pgxpool.Pool, cfg *config.Config, protector jobpayload.Protector) mailRepoInterface.PersonalTemplateRepository {
	return &personalTemplateRepoPostgres{
		db:              db,
		mailSchema:      cfg.SchemaSQL.Mail,
		hierarchySchema: cfg.SchemaSQL.Hierarchy,
		protector:       protector,
	}
}

// decompressStreamingFailClose giải nén zstd bằng streaming Decoder với size cap.
// Fail-close — không fallback raw bytes khi lỗi.
func decompressStreamingFailClose(compressed []byte) (string, error) {
	// [COMMENT]: Dùng LimitReader để tránh zip bomb OOM — nếu decompressed > cap → fail-close ngay.
	decoder, dErr := zstd.NewReader(
		io.LimitReader(newByteReader(compressed), personalTemplateMaxDecompressBytes+1),
	)
	if dErr != nil {
		return "", mailTaxonomy.ErrHTMLDecompressFailed
	}
	defer decoder.Close()

	var out []byte
	tmp := make([]byte, 32*1024)
	for {
		n, readErr := decoder.Read(tmp)
		if n > 0 {
			out = append(out, tmp[:n]...)
			if len(out) > personalTemplateMaxDecompressBytes {
				return "", mailTaxonomy.ErrHTMLDecompressSizeExceeded
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", mailTaxonomy.ErrHTMLDecompressFailed
		}
	}
	// [COMMENT]: Strict UTF-8 validation — fail-close nếu có byte không hợp lệ.
	if !isValidUTF8(out) {
		return "", mailTaxonomy.ErrHTMLUTF8Invalid
	}
	return string(out), nil
}

func isValidUTF8(b []byte) bool {
	for _, r := range string(b) {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}

type byteReader struct {
	data []byte
	pos  int
}

func newByteReader(b []byte) *byteReader { return &byteReader{data: b} }
func (r *byteReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// [COMMENT]: Create thực hiện thao tác khởi tạo Personal Template và Outbox record theo CTE nguyên tử.
// templateID, compressedHTML, contentHash do service tính và truyền riêng — không embed vào req entity.
// outbox.ActorUserID là uuid.UUID cụ thể (không pointer) — đảm bảo tại compile time.
func (r *personalTemplateRepoPostgres) Create(ctx context.Context, req *mailEntity.CreatePersonalTemplateRequest, outbox *mailEntity.MailOutboxRecord, templateID string, compressedHTML []byte, contentHash []byte) (*mailEntity.CreatePersonalTemplateResponse, error) {
	protected, protectionErr := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: outbox.ZoneID, SourceDomain: "MAIL", JobTopic: outbox.JobTopic, ResourceID: outbox.ResourceID, JobVersion: outbox.JobVersion, PayloadSchemaVersion: outbox.PayloadSchemaVersion}, outbox.Payload)
	if protectionErr != nil {
		return nil, protectionErr
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID
	now := time.Now().UTC()

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
				id, workspace_id, code, name, current_version, template_revision,
				next_version, next_template_revision, created_at, updated_at
			)
			SELECT $1, $2, $3, $4, $5, $6, $5 + 1, $6 + 1, $9, $10
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
				template_id, version, template_revision, event_id,
				subject_template, html_template, content_sha256, created_at
			)
			SELECT $11, $12, $6, $17, $13, $14, $15, $16
			FROM identity_inserted
			RETURNING template_id
		), outbox_inserted AS (
			INSERT INTO %s.mail_outbox_records (
				event_id, zone_id, job_topic, payload, actor_user_id, status,
				job_version, resource_id, payload_schema_version, trace_id, idle, payload_key_id
			)
			SELECT $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28
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
		templateID, req.WorkspaceID, req.Code, req.Name, 1, 1,
		req.ZoneID, req.ActorUserID, now, now,
		templateID, 1, req.SubjectTemplate, compressedHTML, contentHash, now,
		outbox.EventID, outbox.ZoneID, outbox.JobTopic, outbox.Payload, outbox.ActorUserID, outbox.Status, outbox.JobVersion, outbox.ResourceID, outbox.PayloadSchemaVersion, outbox.TraceID, outbox.Idle, outbox.PayloadKeyID,
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
		return nil, fmt.Errorf("mail personal template repo: atomic create: %w", err)
	}

	if !authorized {
		return nil, mailTaxonomy.ErrWorkspaceNotFound
	}
	if insertedID == "" {
		return nil, mailTaxonomy.ErrAlreadyExists
	}
	if !versionInserted || !outboxID.Valid {
		return nil, fmt.Errorf("mail personal template repo: create CTE incomplete: %w", mailTaxonomy.ErrInternal)
	}

	return &mailEntity.CreatePersonalTemplateResponse{
		ID: templateID, WorkspaceID: &req.WorkspaceID, Code: req.Code, Name: req.Name,
		CurrentVersion: 1, TemplateRevision: 1, SubjectTemplate: req.SubjectTemplate,
		RawHTML: req.RawHTML, ContentSHA256: contentHash, OperationID: outbox.EventID,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

// [COMMENT]: GetByID truy vấn thông tin mẫu template và phiên bản hiện tại.
// Zstd fail-close: nếu decompression lỗi → trả lỗi, không fallback raw bytes.
func (r *personalTemplateRepoPostgres) GetByID(ctx context.Context, req *mailEntity.GetPersonalTemplateRequest) (*mailEntity.GetPersonalTemplateResponse, error) {
	res := &mailEntity.GetPersonalTemplateResponse{}
	var compressedHTML []byte

	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT t.id, t.workspace_id, t.code, t.name, t.current_version, t.template_revision,
		       t.created_at, t.updated_at,
		       v.subject_template, v.html_template, v.content_sha256
		FROM %s.personal_mail_templates AS t
		JOIN %s.personal_mail_template_versions AS v
		  ON v.template_id = t.id AND v.version = t.current_version
		WHERE t.id = $1 AND t.workspace_id = $2
		  AND EXISTS (SELECT 1 FROM %s.personal_workspaces WHERE id = $2 AND zone_id = $3 AND owner_id = $4)
	`, r.mailSchema, r.mailSchema, r.hierarchySchema),
		req.TemplateID, req.WorkspaceID, req.ZoneID, req.ActorUserID,
	).Scan(
		&res.ID, &res.WorkspaceID, &res.Code, &res.Name, &res.CurrentVersion, &res.TemplateRevision,
		&res.CreatedAt, &res.UpdatedAt,
		&res.SubjectTemplate, &compressedHTML, &res.ContentSHA256,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, mailTaxonomy.ErrTemplateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("mail personal template repo: get: %w", err)
	}

	// [COMMENT]: Fail-close: nếu zstd decode thất bại → trả integrity error, không fallback raw.
	if len(compressedHTML) > 0 {
		html, dErr := decompressStreamingFailClose(compressedHTML)
		if dErr != nil {
			return nil, fmt.Errorf("mail personal template repo: get decompress html: %w", dErr)
		}
		res.RawHTML = html
	}

	return res, nil
}

// [COMMENT]: List truy vấn danh sách mẫu template phẳng theo phân trang cursor.
func (r *personalTemplateRepoPostgres) List(ctx context.Context, req *mailEntity.ListPersonalTemplatesRequest) ([]*mailEntity.PersonalTemplateItem, error) {
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT t.id, t.workspace_id, t.code, t.name, t.current_version, t.template_revision,
		       t.created_at, t.updated_at
		FROM %s.personal_mail_templates AS t
		WHERE t.workspace_id = $1 AND ($4 = '' OR t.id > $4)
		  AND EXISTS (SELECT 1 FROM %s.personal_workspaces WHERE id = $1 AND zone_id = $2 AND owner_id = $3)
		ORDER BY t.id ASC LIMIT $5
	`, r.mailSchema, r.hierarchySchema),
		req.WorkspaceID, req.ZoneID, req.ActorUserID, req.AfterID, req.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("mail personal template repo: list: %w", err)
	}
	defer rows.Close()

	items := make([]*mailEntity.PersonalTemplateItem, 0, req.Limit)
	for rows.Next() {
		t := &mailEntity.PersonalTemplateItem{}
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

// [COMMENT]: ListVersions truy vấn lịch sử phiên bản của một template.
// Zstd fail-close per row; không fallback raw khi lỗi.
func (r *personalTemplateRepoPostgres) ListVersions(ctx context.Context, req *mailEntity.ListPersonalTemplateVersionsRequest) ([]*mailEntity.PersonalTemplateVersionItem, error) {
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT v.template_id, v.version, v.subject_template, v.html_template, v.content_sha256, v.created_at
		FROM %s.personal_mail_template_versions AS v
		JOIN %s.personal_mail_templates AS t ON t.id = v.template_id
		WHERE v.template_id = $1 AND t.workspace_id = $2
		  AND v.version <= t.current_version
		  AND ($5::bigint = 0 OR v.version < $5)
		  AND EXISTS (SELECT 1 FROM %s.personal_workspaces WHERE id = $2 AND zone_id = $3 AND owner_id = $4)
		ORDER BY v.version DESC LIMIT $6
	`, r.mailSchema, r.mailSchema, r.hierarchySchema),
		req.TemplateID, req.WorkspaceID, req.ZoneID, req.ActorUserID, req.BeforeVersion, req.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("mail personal template repo: list versions: %w", err)
	}
	defer rows.Close()

	items := make([]*mailEntity.PersonalTemplateVersionItem, 0, req.Limit)
	for rows.Next() {
		v := &mailEntity.PersonalTemplateVersionItem{}
		var compressedHTML []byte
		if err = rows.Scan(
			&v.TemplateID, &v.Version, &v.SubjectTemplate, &compressedHTML, &v.ContentSHA256, &v.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("mail personal template repo: scan version: %w", err)
		}
		// [COMMENT]: Fail-close per row; lỗi decompress = integrity error, không fallback raw.
		if len(compressedHTML) > 0 {
			html, dErr := decompressStreamingFailClose(compressedHTML)
			if dErr != nil {
				return nil, fmt.Errorf("mail personal template repo: version %d decompress html: %w", v.Version, dErr)
			}
			v.RawHTML = html
		}
		items = append(items, v)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("mail personal template repo: iterate versions: %w", err)
	}
	if len(items) == 0 {
		if _, err = r.GetByID(ctx, &mailEntity.GetPersonalTemplateRequest{ActorUserID: req.ActorUserID, ZoneID: req.ZoneID, WorkspaceID: req.WorkspaceID, TemplateID: req.TemplateID}); err != nil {
			return nil, err
		}
	}

	return items, nil
}

// [COMMENT]: PublishVersion phát hành phiên bản mới cho template hiện có.
// Repo thực hiện: authorization, lock FOR UPDATE, optimistic revision check, chặn live operation,
// CTE INSERT version + UPDATE counter + INSERT outbox.
// outbox.EventID và payload proto được finalize tại repo vì chỉ repo biết nextRevision sau lock.
// compressedHTML và contentHash đã được service tính và truyền riêng.
func (r *personalTemplateRepoPostgres) PublishVersion(ctx context.Context, req *mailEntity.PublishPersonalTemplateVersionRequest, outbox *mailEntity.MailOutboxRecord, compressedHTML []byte, contentHash []byte) (*mailEntity.PublishPersonalTemplateVersionResponse, error) {
	now := time.Now().UTC()

	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("mail personal template repo: begin publish: %w", err)
	}
	defer tx.Rollback(ctx)

	var code, name string
	var currentVersion, currentRevision, nextVersion, nextRevision uint64

	err = tx.QueryRow(ctx, fmt.Sprintf(`
		SELECT t.code, t.name, t.current_version, t.template_revision, t.next_version, t.next_template_revision
		FROM %s.personal_mail_templates t
		JOIN %s.personal_workspaces w ON w.id=t.workspace_id
		WHERE t.id=$1 AND t.workspace_id=$2 AND w.zone_id=$3 AND w.owner_id=$4
		FOR UPDATE OF t
	`, r.mailSchema, r.hierarchySchema), req.TemplateID, req.WorkspaceID, req.ZoneID, req.ActorUserID).Scan(
		&code, &name, &currentVersion, &currentRevision, &nextVersion, &nextRevision,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, mailTaxonomy.ErrTemplateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("mail personal template repo: lock publish target: %w", err)
	}

	if currentRevision != req.ExpectedRevision {
		return nil, mailTaxonomy.ErrVersionConflict
	}

	var lockedOperation bool
	if err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s.mail_outbox_records WHERE resource_id=$1 AND status IN ('PENDING','PROCESSING'))`, r.mailSchema), req.TemplateID).Scan(&lockedOperation); err != nil {
		return nil, fmt.Errorf("mail personal template repo: check publish operation: %w", err)
	}
	if lockedOperation {
		return nil, mailTaxonomy.ErrOperationInProgress
	}

	// [COMMENT]: Finalize outbox eventID và build payload proto với nextRevision đúng — chỉ repo mới biết nextRevision.
	eventID := uuid.NewSHA1(personalMailTemplateEventNamespace, fmt.Appendf(nil, "template:%s:%d:publish:%s", req.TemplateID, nextRevision, req.ZoneID))
	event := &mailproto.MailTemplateVersionPublishedV1{
		Metadata:   &mailproto.MailEventMetadataV1{EventId: eventID[:], SchemaVersion: 1, OccurredAtUnixMs: now.UnixMilli(), Producer: "controlplane-mail"},
		TemplateId: req.TemplateID, TemplateRevision: nextRevision, TemplateVersion: nextVersion,
		SubjectTemplate: req.SubjectTemplate, HtmlTemplate: compressedHTML, ContentSha256: contentHash,
	}
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		event.Metadata.Traceparent = "00-" + spanContext.TraceID().String() + "-" + spanContext.SpanID().String() + "-" + fmt.Sprintf("%02x", byte(spanContext.TraceFlags()))
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("mail personal template repo: marshal publish event: %w", err)
	}

	// [COMMENT]: Finalize outbox fields sau lock; TraceID đã được service extract từ ctx và truyền vào outbox.
	outbox.EventID = eventID
	outbox.Payload = payload
	protected, protectionErr := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: outbox.ZoneID, SourceDomain: "MAIL", JobTopic: outbox.JobTopic, ResourceID: outbox.ResourceID, JobVersion: outbox.JobVersion, PayloadSchemaVersion: outbox.PayloadSchemaVersion}, outbox.Payload)
	if protectionErr != nil {
		return nil, protectionErr
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID

	var authorized, versionInserted bool
	var outboxID sql.NullInt64

	err = tx.QueryRow(ctx, fmt.Sprintf(`
		WITH authorized AS (
			SELECT 1
			FROM %s.personal_workspaces
			WHERE id = $1 AND zone_id = $2 AND owner_id = $3
		), target AS MATERIALIZED (
			SELECT current_version, template_revision, next_version, next_template_revision
			FROM %s.personal_mail_templates
			WHERE id = $4 AND workspace_id = $1
			  AND EXISTS (SELECT 1 FROM authorized)
			FOR UPDATE
		), live_operation AS (
			SELECT 1 FROM %s.mail_outbox_records
			WHERE resource_id=$4 AND status IN ('PENDING','PROCESSING')
			LIMIT 1
		), version_inserted AS (
			INSERT INTO %s.personal_mail_template_versions (
				template_id, version, template_revision, event_id,
				subject_template, html_template, content_sha256, created_at
			)
			SELECT $5, $6, $12, $13, $7, $8, $9, $10
			FROM target
			WHERE template_revision=$11 AND next_version=$6 AND next_template_revision=$12
			  AND NOT EXISTS (SELECT 1 FROM live_operation)
			ON CONFLICT DO NOTHING
			RETURNING template_id
		), counter_updated AS (
			UPDATE %s.personal_mail_templates
			SET next_version=$6+1, next_template_revision=$12+1
			WHERE id = $4 AND workspace_id = $1 AND template_revision = $11
			  AND next_version=$6 AND next_template_revision=$12
			  AND EXISTS (SELECT 1 FROM version_inserted)
			RETURNING id
		), outbox_inserted AS (
			INSERT INTO %s.mail_outbox_records (
				event_id, zone_id, job_topic, payload, actor_user_id, status,
				job_version, resource_id, payload_schema_version, trace_id, idle, payload_key_id
			)
			SELECT $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24
			FROM counter_updated
			RETURNING id
		)
		SELECT
			EXISTS (SELECT 1 FROM authorized),
			EXISTS (SELECT 1 FROM version_inserted),
			(SELECT id FROM outbox_inserted)
	`, r.hierarchySchema, r.mailSchema, r.mailSchema, r.mailSchema, r.mailSchema, r.mailSchema),
		req.WorkspaceID, req.ZoneID, req.ActorUserID, req.TemplateID, req.TemplateID, nextVersion, req.SubjectTemplate, compressedHTML, contentHash, now, req.ExpectedRevision, nextRevision, outbox.EventID, outbox.ZoneID, outbox.JobTopic, outbox.Payload, outbox.ActorUserID, outbox.Status, outbox.JobVersion, outbox.ResourceID, outbox.PayloadSchemaVersion, outbox.TraceID, outbox.Idle, outbox.PayloadKeyID,
	).Scan(
		&authorized,
		&versionInserted,
		&outboxID,
	)

	if err != nil {
		return nil, fmt.Errorf("mail personal template repo: atomic publish: %w", err)
	}

	if !authorized {
		return nil, mailTaxonomy.ErrTemplateNotFound
	}
	if !versionInserted || !outboxID.Valid {
		return nil, mailTaxonomy.ErrVersionConflict
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("mail personal template repo: commit publish: %w", err)
	}

	// [COMMENT]: CurrentRevision = active head revision không đổi; PublishedRevision = nextRevision vừa được ghi.
	return &mailEntity.PublishPersonalTemplateVersionResponse{
		ID: req.TemplateID, WorkspaceID: &req.WorkspaceID, Code: code, Name: name,
		CurrentVersion: currentVersion, CurrentRevision: currentRevision,
		PublishedVersion: nextVersion, PublishedRevision: nextRevision,
		SubjectTemplate: req.SubjectTemplate, RawHTML: req.RawHTML, ContentSHA256: contentHash,
		OperationID:   eventID,
		HeadCreatedAt: now, CandidateCreatedAt: now,
	}, nil
}

// [COMMENT]: Delete xóa mẫu template bằng tombstone outbox record.
// Repo finalize payload proto với revision đúng sau khi lock và đọc nextRevision.
func (r *personalTemplateRepoPostgres) Delete(ctx context.Context, req *mailEntity.DeletePersonalTemplateRequest, outbox *mailEntity.MailOutboxRecord) (uuid.UUID, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return uuid.Nil, fmt.Errorf("mail personal template repo: begin delete: %w", err)
	}
	defer tx.Rollback(ctx)

	var currentVersion, revision, nextRevision uint64
	err = tx.QueryRow(ctx, fmt.Sprintf(`
		SELECT t.current_version, t.template_revision, t.next_template_revision
		FROM %s.personal_mail_templates t
		JOIN %s.personal_workspaces w ON w.id=t.workspace_id
		WHERE t.id=$1 AND t.workspace_id=$2 AND w.zone_id=$3 AND w.owner_id=$4
		FOR UPDATE OF t
	`, r.mailSchema, r.hierarchySchema), req.TemplateID, req.WorkspaceID, req.ZoneID, req.ActorUserID).Scan(&currentVersion, &revision, &nextRevision)

	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, mailTaxonomy.ErrTemplateNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("mail personal template repo: lock delete target: %w", err)
	}
	if revision != req.ExpectedRevision {
		return uuid.Nil, mailTaxonomy.ErrVersionConflict
	}

	var liveOperation bool
	if err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s.mail_outbox_records WHERE resource_id=$1 AND status IN ('PENDING','PROCESSING'))`, r.mailSchema), req.TemplateID).Scan(&liveOperation); err != nil {
		return uuid.Nil, fmt.Errorf("mail personal template repo: check live operation: %w", err)
	}
	if liveOperation {
		return uuid.Nil, mailTaxonomy.ErrOperationInProgress
	}

	var inUse bool
	if err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT EXISTS (
		SELECT 1 FROM %s.personal_mail_consumers c
		WHERE c.workspace_id=$1 AND c.template_id=$2
		UNION ALL
		SELECT 1 FROM %s.personal_mail_consumer_update_versions candidate
		JOIN %s.personal_mail_consumers active ON active.id=candidate.consumer_id
		WHERE active.workspace_id=$1 AND candidate.template_id=$2
		  AND candidate.config_version > active.config_version
	)`, r.mailSchema, r.mailSchema, r.mailSchema), req.WorkspaceID, req.TemplateID).Scan(&inUse); err != nil {
		return uuid.Nil, fmt.Errorf("mail personal template repo: check template usage: %w", err)
	}
	if inUse {
		return uuid.Nil, mailTaxonomy.ErrTemplateInUse
	}

	now := time.Now().UTC()
	// [COMMENT]: Finalize proto event delete với revision đúng — chỉ repo biết nextRevision sau lock.
	event := &mailproto.MailTemplateDeletedV1{
		Metadata:   &mailproto.MailEventMetadataV1{EventId: outbox.EventID[:], SchemaVersion: 1, OccurredAtUnixMs: now.UnixMilli(), Producer: "controlplane-mail"},
		TemplateId: req.TemplateID, TemplateRevision: nextRevision, LastPublishedVersion: currentVersion,
	}
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		event.Metadata.Traceparent = "00-" + spanContext.TraceID().String() + "-" + spanContext.SpanID().String() + "-" + fmt.Sprintf("%02x", byte(spanContext.TraceFlags()))
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(event)
	if err != nil {
		return uuid.Nil, fmt.Errorf("mail personal template repo: marshal delete event: %w", err)
	}
	outbox.Payload = payload
	protected, protectionErr := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: outbox.ZoneID, SourceDomain: "MAIL", JobTopic: outbox.JobTopic, ResourceID: outbox.ResourceID, JobVersion: outbox.JobVersion, PayloadSchemaVersion: outbox.PayloadSchemaVersion}, outbox.Payload)
	if protectionErr != nil {
		return uuid.Nil, protectionErr
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID

	var deletedID sql.NullInt64
	if err = tx.QueryRow(ctx, fmt.Sprintf(`INSERT INTO %s.mail_outbox_records (event_id,zone_id,job_topic,payload,actor_user_id,status,job_version,resource_id,payload_schema_version,trace_id,idle,payload_key_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`, r.mailSchema), outbox.EventID, outbox.ZoneID, outbox.JobTopic, outbox.Payload, outbox.ActorUserID, outbox.Status, outbox.JobVersion, outbox.ResourceID, outbox.PayloadSchemaVersion, outbox.TraceID, outbox.Idle, outbox.PayloadKeyID).Scan(&deletedID); err != nil {
		return uuid.Nil, fmt.Errorf("mail personal template repo: insert delete outbox: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("mail personal template repo: commit delete: %w", err)
	}

	return outbox.EventID, nil
}
