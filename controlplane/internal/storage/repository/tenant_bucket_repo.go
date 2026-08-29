package storageRepoImpl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/config"
	jobpayload "controlplane/internal/security"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageModel "controlplane/internal/storage/model"
	storageTaxonomy "controlplane/internal/storage/taxonomy"
	storageproto "controlplane/internal/storage/transport/proto"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
)

// TenantBucketRepoImpl thực thi interface TenantBucketRepo cho kết nối PostgreSQL.
type TenantBucketRepoImpl struct {
	db        *pgxpool.Pool
	storage   string // schema storage
	hierarchy string // schema hierarchy
	protector jobpayload.Protector
}

// NewTenantBucketRepo khởi tạo repository quản lý bucket doanh nghiệp.
func NewTenantBucketRepo(
	db *pgxpool.Pool,
	cfg *config.Config,
	protector jobpayload.Protector,
) storageRepoInterface.TenantBucketRepo {
	return &TenantBucketRepoImpl{
		db:        db,
		storage:   cfg.SchemaSQL.Storage,
		hierarchy: cfg.SchemaSQL.Hierarchy,
		protector: protector,
	}
}

func (r *TenantBucketRepoImpl) Create(
	ctx context.Context,
	bucket *storageEntity.TenantBucket,
	credential *storageEntity.TenantCredential,
	actorUserID uuid.UUID,
	outbox *storageEntity.StorageOutboxRecord,
) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("storage repo: begin tenant bucket create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	admission := storageEntity.CommercialAdmissionProjection{
		OwnerID:   bucket.TenantID,
		OwnerType: string(storageEntity.StorageOwnerTypeTenant),
	}
	if err := tx.QueryRow(ctx, fmt.Sprintf(`
		SELECT policy_version, decision, restriction_reason, effective_at,
		       valid_until, source_event_id
		FROM %s.commercial_admission_projection
		WHERE owner_id=$1 AND owner_type='TENANT'
		  AND decision='ALLOW'
		  AND effective_at <= NOW()
		  AND (valid_until IS NULL OR valid_until > NOW())
		FOR KEY SHARE`, r.storage), bucket.TenantID).Scan(
		&admission.PolicyVersion, &admission.Decision, &admission.RestrictionReason,
		&admission.EffectiveAt, &admission.ValidUntil, &admission.EventID,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storageTaxonomy.ErrCommercialAdmissionDenied
		}
		return fmt.Errorf("storage repo: lock tenant create admission: %w", err)
	}

	var syncEvent storageproto.BucketCreateSync
	if err := proto.Unmarshal(outbox.Payload, &syncEvent); err != nil {
		return fmt.Errorf("storage repo: decode tenant bucket create payload: %w", err)
	}
	if syncEvent.SchemaVersion != 2 || syncEvent.Name != bucket.Name ||
		!bytes.Equal(syncEvent.BucketId, bucket.ID[:]) ||
		!bytes.Equal(syncEvent.OwnerId, bucket.TenantID[:]) ||
		syncEvent.OwnerType != string(storageEntity.StorageOwnerTypeTenant) ||
		!bytes.Equal(syncEvent.WorkspaceId, bucket.WorkspaceID[:]) ||
		!bytes.Equal(syncEvent.ZoneId, bucket.ZoneID[:]) {
		return errors.New("storage repo: tenant bucket create payload scope mismatch")
	}
	syncEvent.AdmissionPolicyVersion = admission.PolicyVersion
	syncEvent.AdmissionDecision = admission.Decision
	syncEvent.AdmissionEffectiveAt = admission.EffectiveAt.UTC().Format(time.RFC3339Nano)
	syncEvent.AdmissionSourceEventId = admission.EventID.String()
	if admission.ValidUntil != nil {
		syncEvent.AdmissionValidUntil = admission.ValidUntil.UTC().Format(time.RFC3339Nano)
	}
	if admission.RestrictionReason != nil {
		syncEvent.AdmissionRestrictionReason = *admission.RestrictionReason
	}
	outbox.Payload, err = proto.Marshal(&syncEvent)
	if err != nil {
		return fmt.Errorf("storage repo: encode tenant bucket create payload: %w", err)
	}

	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{
		ZoneID:               outbox.ZoneID,
		SourceDomain:         "STORAGE",
		JobTopic:             outbox.JobTopic,
		ResourceID:           outbox.ResourceID,
		JobVersion:           outbox.JobVersion,
		PayloadSchemaVersion: outbox.PayloadSchemaVersion,
	}, outbox.Payload)
	if err != nil {
		return err
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID

	mo := storageModel.OutboxEntityToModel(outbox)

	// Atomic CTE: admission check + workspace auth + bucket insert + credential + outbox.
	// admitted CTE verifies owner has ALLOW decision in commercial_admission_projection.
	// Returns sentinel (workspace_found, admitted) so repo can map distinct errors without
	// a separate round-trip.
	query := fmt.Sprintf(`
		WITH admitted AS (
			SELECT EXISTS (
				SELECT 1 FROM %s.commercial_admission_projection
				WHERE owner_id = $5
				  AND owner_type = 'TENANT'
				  AND effective_at <= NOW()
				  AND (valid_until IS NULL OR valid_until > NOW())
				  AND decision = 'ALLOW'
			) AS ok
		),
		authorized_workspace AS (
			SELECT w.id, w.tenant_id, w.zone_id
			FROM %s.tenant_workspaces w
			JOIN %s.tenant_memberships m 
			  ON m.tenant_id = w.tenant_id 
			 AND m.user_id = $7 
			 AND m.status = 'active'
			WHERE w.id = $3 
			  AND w.tenant_id = $5 
			  AND w.zone_id = $4
			  AND (SELECT ok FROM admitted)
			FOR KEY SHARE OF w
		),
		ins_bucket AS (
			INSERT INTO %s.tenant_buckets (
				id, name, workspace_id, zone_id, tenant_id, status, capacity_quota_bytes, created_at, updated_at,
				encrypt_enabled, versioning_enabled, object_locking_enabled, replication_enabled,
				retention_days, legal_hold_enabled, tags
			)
			SELECT $1, $2, aw.id, aw.zone_id, aw.tenant_id, 'PROVISIONING', $6, NOW(), NOW(),
			       $25, $26, $27, $28, $29, $30, $31
			FROM authorized_workspace aw
			RETURNING id, name, workspace_id, zone_id, tenant_id, status, capacity_quota_bytes, created_at, updated_at
		),
		ins_credential AS (
			INSERT INTO %s.tenant_credentials (
				id, bucket_id, access_key, policy, created_at, updated_at
			)
			SELECT $8, ib.id, $9, $10, NOW(), NOW()
			FROM ins_bucket ib
		),
		inserted_outbox AS (
			INSERT INTO %s.storage_outbox_records (
				event_id, zone_id, job_topic, payload, owner_id, owner_type, status, completed_at,
				job_version, resource_id, payload_schema_version, trace_id, idle,
				error_code, error_message, actor_user_id, payload_key_id,
				resource_name, rollback_quota_bytes
			)
			SELECT $11, ib.zone_id, $12, $13, ib.tenant_id, $14, $15, $16,
			       $17, ib.id::text, $18, $19, $20,
			       $21, $22, $7, $23,
			       ib.name, $24
			FROM ins_bucket ib
		),
		inserted_resource_admission AS (
			INSERT INTO %s.resource_admission_projection (
				resource_id, resource_name, zone_id, owner_id, owner_type,
				policy_version, decision, restriction_reason, effective_at,
				valid_until, source_event_id, updated_at
			)
			SELECT ib.id, ib.name, ib.zone_id, ib.tenant_id, 'TENANT',
			       $32, $33, $34, $35, $36, $37, NOW()
			FROM ins_bucket ib
		)
		SELECT
			(SELECT ok FROM admitted)          AS admitted,
			(SELECT id FROM ins_bucket)        AS created_id;
	`, r.storage, r.hierarchy, r.hierarchy, r.storage, r.storage, r.storage, r.storage)

	tagsBytes, _ := json.Marshal(bucket.Tags)

	var admitted bool
	var createdID *uuid.UUID
	err = tx.QueryRow(ctx, query,
		// $1-$6: Bucket params
		bucket.ID,
		bucket.Name,
		bucket.WorkspaceID,
		bucket.ZoneID,
		bucket.TenantID,
		bucket.CapacityQuotaBytes,
		// $7: Actor user ID
		actorUserID,
		// $8-$10: Credential params
		credential.ID,
		credential.AccessKey,
		credential.Policy,
		// $11-$24: Outbox params
		mo.EventID,
		mo.JobTopic,
		mo.Payload,
		mo.OwnerType,
		mo.Status,
		mo.CompletedAt,
		mo.JobVersion,
		mo.PayloadSchemaVersion,
		mo.TraceID,
		mo.Idle,
		mo.ErrorCode,
		mo.ErrorMessage,
		mo.PayloadKeyID,
		mo.RollbackQuotaBytes,
		bucket.EncryptEnabled,
		bucket.VersioningEnabled,
		bucket.ObjectLockingEnabled,
		bucket.ReplicationEnabled,
		bucket.RetentionDays,
		bucket.LegalHoldEnabled,
		tagsBytes,
		admission.PolicyVersion,
		admission.Decision,
		admission.RestrictionReason,
		admission.EffectiveAt,
		admission.ValidUntil,
		admission.EventID,
	).Scan(&admitted, &createdID)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return storageTaxonomy.ErrAlreadyExists
		}
		return fmt.Errorf("storage repo: create tenant bucket failed: %w", err)
	}
	if !admitted {
		return storageTaxonomy.ErrCommercialAdmissionDenied
	}
	if createdID == nil {
		return storageTaxonomy.ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("storage repo: commit tenant bucket create: %w", err)
	}

	bucket.Status = storageEntity.BucketStatusProvisioning
	return nil
}

func (r *TenantBucketRepoImpl) GetByID(
	ctx context.Context,
	id uuid.UUID,
	workspaceID uuid.UUID,
	tenantID uuid.UUID,
	userID uuid.UUID,
	zoneID uuid.UUID,
) (*storageEntity.TenantBucket, error) {
	query := fmt.Sprintf(`
		SELECT b.id, b.name, b.workspace_id, b.zone_id, b.tenant_id, b.status,
		       b.capacity_quota_bytes, b.used_bytes,
		       b.encrypt_enabled, b.versioning_enabled, b.object_locking_enabled,
		       b.replication_enabled, b.retention_days, b.legal_hold_enabled,
		       b.tags, b.lifecycle_rules, b.created_at, b.updated_at
		FROM %s.tenant_buckets b
		JOIN %s.tenant_workspaces w ON b.workspace_id = w.id
		JOIN %s.tenant_memberships m 
		  ON m.tenant_id = w.tenant_id 
		 AND m.user_id = $4 
		 AND m.status = 'active'
		WHERE b.id = $1 
		  AND b.workspace_id = $2 
		  AND b.tenant_id = $3 
		  AND w.zone_id = $5
	`, r.storage, r.hierarchy, r.hierarchy)

	b := &storageEntity.TenantBucket{}
	var tagsBytes, lifecycleBytes []byte

	err := r.db.QueryRow(ctx, query, id, workspaceID, tenantID, userID, zoneID).Scan(
		&b.ID,
		&b.Name,
		&b.WorkspaceID,
		&b.ZoneID,
		&b.TenantID,
		&b.Status,
		&b.CapacityQuotaBytes,
		&b.UsedBytes,
		&b.EncryptEnabled,
		&b.VersioningEnabled,
		&b.ObjectLockingEnabled,
		&b.ReplicationEnabled,
		&b.RetentionDays,
		&b.LegalHoldEnabled,
		&tagsBytes,
		&lifecycleBytes,
		&b.CreatedAt,
		&b.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, storageTaxonomy.ErrNotFound
		}
		return nil, fmt.Errorf("storage repo: get tenant bucket by id failed: %w", err)
	}

	if len(tagsBytes) > 0 {
		_ = json.Unmarshal(tagsBytes, &b.Tags)
	}
	if len(lifecycleBytes) > 0 {
		_ = json.Unmarshal(lifecycleBytes, &b.LifecycleRules)
	}
	return b, nil
}

func (r *TenantBucketRepoImpl) GetByName(ctx context.Context, name string) (*storageEntity.TenantBucket, error) {
	query := fmt.Sprintf(`
		SELECT id, name, workspace_id, zone_id, tenant_id, status, capacity_quota_bytes, used_bytes,
		       encrypt_enabled, versioning_enabled, object_locking_enabled,
		       replication_enabled, retention_days, legal_hold_enabled,
		       tags, lifecycle_rules, created_at, updated_at
		FROM %s.tenant_buckets
		WHERE name = $1
	`, r.storage)

	b := &storageEntity.TenantBucket{}
	var tagsBytes, lifecycleBytes []byte

	err := r.db.QueryRow(ctx, query, name).Scan(
		&b.ID,
		&b.Name,
		&b.WorkspaceID,
		&b.ZoneID,
		&b.TenantID,
		&b.Status,
		&b.CapacityQuotaBytes,
		&b.UsedBytes,
		&b.EncryptEnabled,
		&b.VersioningEnabled,
		&b.ObjectLockingEnabled,
		&b.ReplicationEnabled,
		&b.RetentionDays,
		&b.LegalHoldEnabled,
		&tagsBytes,
		&lifecycleBytes,
		&b.CreatedAt,
		&b.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, storageTaxonomy.ErrNotFound
		}
		return nil, fmt.Errorf("storage repo: get tenant bucket by name failed: %w", err)
	}

	if len(tagsBytes) > 0 {
		_ = json.Unmarshal(tagsBytes, &b.Tags)
	}
	if len(lifecycleBytes) > 0 {
		_ = json.Unmarshal(lifecycleBytes, &b.LifecycleRules)
	}
	return b, nil
}

func (r *TenantBucketRepoImpl) ListByWorkspace(
	ctx context.Context,
	workspaceID uuid.UUID,
	tenantID uuid.UUID,
	userID uuid.UUID,
	zoneID uuid.UUID,
) ([]*storageEntity.TenantBucket, error) {
	query := fmt.Sprintf(`
		SELECT b.id, b.name, b.workspace_id, b.zone_id, b.tenant_id, b.status,
		       b.capacity_quota_bytes, b.used_bytes,
		       b.encrypt_enabled, b.versioning_enabled, b.object_locking_enabled,
		       b.replication_enabled, b.retention_days, b.legal_hold_enabled,
		       b.tags, b.lifecycle_rules, b.created_at, b.updated_at
		FROM %s.tenant_buckets b
		JOIN %s.tenant_workspaces w ON b.workspace_id = w.id
		JOIN %s.tenant_memberships m 
		  ON m.tenant_id = w.tenant_id 
		 AND m.user_id = $3 
		 AND m.status = 'active'
		WHERE b.workspace_id = $1 
		  AND b.tenant_id = $2 
		  AND w.zone_id = $4
		ORDER BY b.created_at DESC
	`, r.storage, r.hierarchy, r.hierarchy)

	rows, err := r.db.Query(ctx, query, workspaceID, tenantID, userID, zoneID)
	if err != nil {
		return nil, fmt.Errorf("storage repo: list tenant buckets failed: %w", err)
	}
	defer rows.Close()

	var buckets []*storageEntity.TenantBucket
	for rows.Next() {
		b := &storageEntity.TenantBucket{}
		var tagsBytes, lifecycleBytes []byte

		err := rows.Scan(
			&b.ID,
			&b.Name,
			&b.WorkspaceID,
			&b.ZoneID,
			&b.TenantID,
			&b.Status,
			&b.CapacityQuotaBytes,
			&b.UsedBytes,
			&b.EncryptEnabled,
			&b.VersioningEnabled,
			&b.ObjectLockingEnabled,
			&b.ReplicationEnabled,
			&b.RetentionDays,
			&b.LegalHoldEnabled,
			&tagsBytes,
			&lifecycleBytes,
			&b.CreatedAt,
			&b.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("storage repo: scan tenant bucket row failed: %w", err)
		}
		if len(tagsBytes) > 0 {
			_ = json.Unmarshal(tagsBytes, &b.Tags)
		}
		if len(lifecycleBytes) > 0 {
			_ = json.Unmarshal(lifecycleBytes, &b.LifecycleRules)
		}
		buckets = append(buckets, b)
	}

	return buckets, nil
}

func (r *TenantBucketRepoImpl) ListNamesByWorkspace(
	ctx context.Context,
	workspaceID uuid.UUID,
	tenantID uuid.UUID,
	userID uuid.UUID,
	zoneID uuid.UUID,
) ([]string, error) {
	query := fmt.Sprintf(`
		SELECT b.name
		FROM %s.tenant_buckets b
		JOIN %s.tenant_workspaces w ON b.workspace_id = w.id
		JOIN %s.tenant_memberships m 
		  ON m.tenant_id = w.tenant_id 
		 AND m.user_id = $3 
		 AND m.status = 'active'
		WHERE b.workspace_id = $1 
		  AND b.tenant_id = $2 
		  AND w.zone_id = $4
		ORDER BY b.created_at DESC
	`, r.storage, r.hierarchy, r.hierarchy)

	rows, err := r.db.Query(ctx, query, workspaceID, tenantID, userID, zoneID)
	if err != nil {
		return nil, fmt.Errorf("storage repo: list tenant bucket names failed: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("storage repo: scan tenant bucket name failed: %w", err)
		}
		names = append(names, name)
	}

	return names, nil
}

func (r *TenantBucketRepoImpl) UpdateQuota(
	ctx context.Context,
	param *storageEntity.UpdateTenantBucketQuota,
	outbox *storageEntity.StorageOutboxRecord,
) error {
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{
		ZoneID:               outbox.ZoneID,
		SourceDomain:         "STORAGE",
		JobTopic:             outbox.JobTopic,
		ResourceID:           outbox.ResourceID,
		JobVersion:           outbox.JobVersion,
		PayloadSchemaVersion: outbox.PayloadSchemaVersion,
	}, outbox.Payload)
	if err != nil {
		return err
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID
	mo := storageModel.OutboxEntityToModel(outbox)

	// admitted CTE is conditional: no check when reducing quota, required when increasing.
	// $6 = new_quota_bytes, compared against current capacity_quota_bytes (not used_bytes).
	query := fmt.Sprintf(`
		WITH admitted AS (
			SELECT (
				$6::bigint <= (
					SELECT b.capacity_quota_bytes
					FROM %s.tenant_buckets b
					WHERE b.id = $1 AND b.tenant_id = $3
				)
				OR EXISTS (
					SELECT 1 FROM %s.commercial_admission_projection
					WHERE owner_id = $3
					  AND owner_type = 'TENANT'
					  AND effective_at <= NOW()
					  AND (valid_until IS NULL OR valid_until > NOW())
					  AND decision = 'ALLOW'
				)
			) AS ok
		),
		authorized_target AS (
			SELECT b.id, b.name, b.zone_id, b.tenant_id, b.used_bytes, b.capacity_quota_bytes
			FROM %s.tenant_buckets b
			JOIN %s.tenant_workspaces w ON b.workspace_id = w.id
			JOIN %s.tenant_memberships m 
			  ON m.tenant_id = w.tenant_id 
			 AND m.user_id = $4 
			 AND m.status = 'active'
			WHERE b.id = $1 
			  AND b.workspace_id = $2 
			  AND b.tenant_id = $3 
			  AND w.zone_id = $5
			  AND b.status = 'READY'
			  AND (SELECT ok FROM admitted)
			FOR UPDATE OF b
		),
		updated_bucket AS (
			UPDATE %s.tenant_buckets
			SET status = 'UPDATING',
			    updated_at = NOW()
			WHERE id IN (SELECT id FROM authorized_target WHERE $6 >= used_bytes + 1073741824)
			RETURNING id, name, zone_id, tenant_id
		),
		inserted_outbox AS (
			INSERT INTO %s.storage_outbox_records (
				event_id, zone_id, job_topic, payload, owner_id, owner_type, status,
				job_version, resource_id, resource_name, payload_schema_version, trace_id, idle,
				actor_user_id, payload_key_id, rollback_quota_bytes
			)
			SELECT $7, ub.zone_id, $8, $9, ub.tenant_id, $10, $11,
			       $12, ub.id::text, ub.name, $13, $14, $15,
			       $4, $16, at.capacity_quota_bytes
			FROM updated_bucket ub
			JOIN authorized_target at ON ub.id = at.id
		)
		SELECT
			(SELECT ok FROM admitted)         AS admitted,
			(SELECT id FROM updated_bucket)   AS updated_id;
	`, r.storage, r.storage, r.storage, r.hierarchy, r.hierarchy, r.storage, r.storage)

	var admitted bool
	var updatedID *uuid.UUID
	err = r.db.QueryRow(ctx, query,
		param.BucketID,
		param.WorkspaceID,
		param.TenantID,
		param.UserID,
		param.ZoneID,
		param.QuotaBytes,
		mo.EventID,
		mo.JobTopic,
		mo.Payload,
		mo.OwnerType,
		mo.Status,
		mo.JobVersion,
		mo.PayloadSchemaVersion,
		mo.TraceID,
		mo.Idle,
		mo.PayloadKeyID,
	).Scan(&admitted, &updatedID)

	if err != nil {
		return fmt.Errorf("storage repo: update tenant bucket quota failed: %w", err)
	}
	if !admitted {
		return storageTaxonomy.ErrCommercialAdmissionDenied
	}
	if updatedID == nil {
		// authorized_target found nothing (not found) or quota check failed (resize too low)
		// Disambiguate: check if bucket exists
		var exists bool
		_ = r.db.QueryRow(ctx, fmt.Sprintf(
			`SELECT EXISTS(SELECT 1 FROM %s.tenant_buckets WHERE id=$1 AND tenant_id=$2 AND status NOT IN ('DELETING'))`,
			r.storage,
		), param.BucketID, param.TenantID).Scan(&exists)
		if !exists {
			return storageTaxonomy.ErrNotFound
		}
		return storageTaxonomy.ErrResizeLimitTooLow
	}

	return nil
}

func (r *TenantBucketRepoImpl) UpdateVersioning(
	ctx context.Context,
	param *storageEntity.UpdateTenantBucketVersioning,
	outbox *storageEntity.StorageOutboxRecord,
) (*storageEntity.TenantBucket, error) {
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{
		ZoneID:               outbox.ZoneID,
		SourceDomain:         "STORAGE",
		JobTopic:             outbox.JobTopic,
		ResourceID:           outbox.ResourceID,
		JobVersion:           outbox.JobVersion,
		PayloadSchemaVersion: outbox.PayloadSchemaVersion,
	}, outbox.Payload)
	if err != nil {
		return nil, err
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID
	mo := storageModel.OutboxEntityToModel(outbox)

	query := fmt.Sprintf(`
		WITH admitted AS (
			SELECT EXISTS (
				SELECT 1 FROM %s.commercial_admission_projection
				WHERE owner_id = $3
				  AND owner_type = 'TENANT'
				  AND effective_at <= NOW()
				  AND (valid_until IS NULL OR valid_until > NOW())
				  AND decision = 'ALLOW'
			) AS ok
		),
		authorized_target AS (
			SELECT b.id, b.name, b.zone_id, b.tenant_id
			FROM %s.tenant_buckets b
			JOIN %s.tenant_workspaces w ON b.workspace_id = w.id
			JOIN %s.tenant_memberships m 
			  ON m.tenant_id = w.tenant_id 
			 AND m.user_id = $4 
			 AND m.status = 'active'
			WHERE b.id = $1 
			  AND b.workspace_id = $2 
			  AND b.tenant_id = $3 
			  AND w.zone_id = $5
			  AND b.status = 'READY'
			  AND (SELECT ok FROM admitted)
			FOR UPDATE OF b
		),
		updated_bucket AS (
			UPDATE %s.tenant_buckets
			SET status = 'UPDATING',
			    updated_at = NOW()
			WHERE id IN (SELECT id FROM authorized_target)
			RETURNING id, name, workspace_id, zone_id, tenant_id, status, capacity_quota_bytes, used_bytes,
			          encrypt_enabled, versioning_enabled, object_locking_enabled,
			          replication_enabled, retention_days, legal_hold_enabled,
			          tags, lifecycle_rules, created_at, updated_at
		),
		inserted_outbox AS (
			INSERT INTO %s.storage_outbox_records (
				event_id, zone_id, job_topic, payload, owner_id, owner_type, status,
				job_version, resource_id, resource_name, payload_schema_version, trace_id, idle,
				actor_user_id, payload_key_id
			)
			SELECT $6, at.zone_id, $7, $8, at.tenant_id, $9, $10,
			       $11, at.id::text, at.name, $12, $13, $14,
			       $4, $15
			FROM authorized_target at
		)
		SELECT
			(SELECT ok FROM admitted)                       AS admitted,
			(SELECT id FROM updated_bucket)                 AS updated_id,
			(SELECT name FROM updated_bucket)               AS name,
			(SELECT workspace_id FROM updated_bucket)       AS workspace_id,
			(SELECT zone_id FROM updated_bucket)            AS zone_id,
			(SELECT tenant_id FROM updated_bucket)          AS tenant_id,
			(SELECT status FROM updated_bucket)             AS status,
			(SELECT capacity_quota_bytes FROM updated_bucket) AS capacity_quota_bytes,
			(SELECT used_bytes FROM updated_bucket)         AS used_bytes,
			(SELECT encrypt_enabled FROM updated_bucket)    AS encrypt_enabled,
			(SELECT versioning_enabled FROM updated_bucket) AS versioning_enabled,
			(SELECT object_locking_enabled FROM updated_bucket) AS object_locking_enabled,
			(SELECT replication_enabled FROM updated_bucket) AS replication_enabled,
			(SELECT retention_days FROM updated_bucket)     AS retention_days,
			(SELECT legal_hold_enabled FROM updated_bucket) AS legal_hold_enabled,
			(SELECT tags FROM updated_bucket)               AS tags,
			(SELECT lifecycle_rules FROM updated_bucket)    AS lifecycle_rules,
			(SELECT created_at FROM updated_bucket)         AS created_at,
			(SELECT updated_at FROM updated_bucket)         AS updated_at;
	`, r.storage, r.storage, r.hierarchy, r.hierarchy, r.storage, r.storage)

	var admitted bool
	b := &storageEntity.TenantBucket{}
	var bucketID *uuid.UUID
	var tagsBytes, lifecycleBytes []byte

	err = r.db.QueryRow(ctx, query,
		param.BucketID,
		param.WorkspaceID,
		param.TenantID,
		param.UserID,
		param.ZoneID,
		mo.EventID,
		mo.JobTopic,
		mo.Payload,
		mo.OwnerType,
		mo.Status,
		mo.JobVersion,
		mo.PayloadSchemaVersion,
		mo.TraceID,
		mo.Idle,
		mo.PayloadKeyID,
	).Scan(
		&admitted,
		&bucketID,
		&b.Name,
		&b.WorkspaceID,
		&b.ZoneID,
		&b.TenantID,
		&b.Status,
		&b.CapacityQuotaBytes,
		&b.UsedBytes,
		&b.EncryptEnabled,
		&b.VersioningEnabled,
		&b.ObjectLockingEnabled,
		&b.ReplicationEnabled,
		&b.RetentionDays,
		&b.LegalHoldEnabled,
		&tagsBytes,
		&lifecycleBytes,
		&b.CreatedAt,
		&b.UpdatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("storage repo: update tenant bucket versioning failed: %w", err)
	}
	if !admitted {
		return nil, storageTaxonomy.ErrCommercialAdmissionDenied
	}
	if bucketID == nil {
		return nil, storageTaxonomy.ErrNotFound
	}
	b.ID = *bucketID

	if len(tagsBytes) > 0 {
		_ = json.Unmarshal(tagsBytes, &b.Tags)
	}
	if len(lifecycleBytes) > 0 {
		_ = json.Unmarshal(lifecycleBytes, &b.LifecycleRules)
	}
	return b, nil
}

func (r *TenantBucketRepoImpl) UpdateLifecycle(
	ctx context.Context,
	param *storageEntity.UpdateTenantBucketLifecycle,
	outbox *storageEntity.StorageOutboxRecord,
) (*storageEntity.TenantBucket, error) {
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{
		ZoneID:               outbox.ZoneID,
		SourceDomain:         "STORAGE",
		JobTopic:             outbox.JobTopic,
		ResourceID:           outbox.ResourceID,
		JobVersion:           outbox.JobVersion,
		PayloadSchemaVersion: outbox.PayloadSchemaVersion,
	}, outbox.Payload)
	if err != nil {
		return nil, err
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID
	mo := storageModel.OutboxEntityToModel(outbox)

	query := fmt.Sprintf(`
		WITH admitted AS (
			SELECT EXISTS (
				SELECT 1 FROM %s.commercial_admission_projection
				WHERE owner_id = $3
				  AND owner_type = 'TENANT'
				  AND effective_at <= NOW()
				  AND (valid_until IS NULL OR valid_until > NOW())
				  AND decision = 'ALLOW'
			) AS ok
		),
		authorized_target AS (
			SELECT b.id, b.name, b.zone_id, b.tenant_id, b.versioning_enabled
			FROM %s.tenant_buckets b
			JOIN %s.tenant_workspaces w ON b.workspace_id = w.id
			JOIN %s.tenant_memberships m 
			  ON m.tenant_id = w.tenant_id 
			 AND m.user_id = $4 
			 AND m.status = 'active'
			WHERE b.id = $1 
			  AND b.workspace_id = $2 
			  AND b.tenant_id = $3 
			  AND w.zone_id = $5
			  AND b.status = 'READY'
			  AND (SELECT ok FROM admitted)
			FOR UPDATE OF b
		),
		updated_bucket AS (
			UPDATE %s.tenant_buckets
			SET status = 'UPDATING',
			    updated_at = NOW()
			WHERE id IN (SELECT id FROM authorized_target)
			RETURNING id, name, workspace_id, zone_id, tenant_id, status, capacity_quota_bytes, used_bytes,
			          encrypt_enabled, versioning_enabled, object_locking_enabled,
			          replication_enabled, retention_days, legal_hold_enabled,
			          tags, lifecycle_rules, created_at, updated_at
		),
		inserted_outbox AS (
			INSERT INTO %s.storage_outbox_records (
				event_id, zone_id, job_topic, payload, owner_id, owner_type, status,
				job_version, resource_id, resource_name, payload_schema_version, trace_id, idle,
				actor_user_id, payload_key_id
			)
			SELECT $6, at.zone_id, $7, $8, at.tenant_id, $9, $10,
			       $11, at.id::text, at.name, $12, $13, $14,
			       $4, $15
			FROM authorized_target at
		)
		SELECT
			(SELECT ok FROM admitted)                           AS admitted,
			(SELECT id FROM updated_bucket)                     AS updated_id,
			(SELECT name FROM updated_bucket)                   AS name,
			(SELECT workspace_id FROM updated_bucket)           AS workspace_id,
			(SELECT zone_id FROM updated_bucket)                AS zone_id,
			(SELECT tenant_id FROM updated_bucket)              AS tenant_id,
			(SELECT status FROM updated_bucket)                 AS status,
			(SELECT capacity_quota_bytes FROM updated_bucket)   AS capacity_quota_bytes,
			(SELECT used_bytes FROM updated_bucket)             AS used_bytes,
			(SELECT encrypt_enabled FROM updated_bucket)        AS encrypt_enabled,
			(SELECT versioning_enabled FROM updated_bucket)     AS versioning_enabled,
			(SELECT object_locking_enabled FROM updated_bucket) AS object_locking_enabled,
			(SELECT replication_enabled FROM updated_bucket)    AS replication_enabled,
			(SELECT retention_days FROM updated_bucket)         AS retention_days,
			(SELECT legal_hold_enabled FROM updated_bucket)     AS legal_hold_enabled,
			(SELECT tags FROM updated_bucket)                   AS tags,
			(SELECT lifecycle_rules FROM updated_bucket)        AS lifecycle_rules,
			(SELECT created_at FROM updated_bucket)             AS created_at,
			(SELECT updated_at FROM updated_bucket)             AS updated_at;
	`, r.storage, r.storage, r.hierarchy, r.hierarchy, r.storage, r.storage)

	var admitted bool
	b := &storageEntity.TenantBucket{}
	var bucketID *uuid.UUID
	var tagsBytes, lifecycleBytes []byte

	err = r.db.QueryRow(ctx, query,
		param.BucketID,
		param.WorkspaceID,
		param.TenantID,
		param.UserID,
		param.ZoneID,
		mo.EventID,
		mo.JobTopic,
		mo.Payload,
		mo.OwnerType,
		mo.Status,
		mo.JobVersion,
		mo.PayloadSchemaVersion,
		mo.TraceID,
		mo.Idle,
		mo.PayloadKeyID,
	).Scan(
		&admitted,
		&bucketID,
		&b.Name,
		&b.WorkspaceID,
		&b.ZoneID,
		&b.TenantID,
		&b.Status,
		&b.CapacityQuotaBytes,
		&b.UsedBytes,
		&b.EncryptEnabled,
		&b.VersioningEnabled,
		&b.ObjectLockingEnabled,
		&b.ReplicationEnabled,
		&b.RetentionDays,
		&b.LegalHoldEnabled,
		&tagsBytes,
		&lifecycleBytes,
		&b.CreatedAt,
		&b.UpdatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("storage repo: update tenant bucket lifecycle failed: %w", err)
	}
	if !admitted {
		return nil, storageTaxonomy.ErrCommercialAdmissionDenied
	}
	if bucketID == nil {
		return nil, storageTaxonomy.ErrNotFound
	}
	b.ID = *bucketID

	if len(tagsBytes) > 0 {
		_ = json.Unmarshal(tagsBytes, &b.Tags)
	}
	if len(lifecycleBytes) > 0 {
		_ = json.Unmarshal(lifecycleBytes, &b.LifecycleRules)
	}
	return b, nil
}

func (r *TenantBucketRepoImpl) Delete(
	ctx context.Context,
	id uuid.UUID,
	workspaceID uuid.UUID,
	tenantID uuid.UUID,
	userID uuid.UUID,
	zoneID uuid.UUID,
	outbox *storageEntity.StorageOutboxRecord,
) error {
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{
		ZoneID:               outbox.ZoneID,
		SourceDomain:         "STORAGE",
		JobTopic:             outbox.JobTopic,
		ResourceID:           outbox.ResourceID,
		JobVersion:           outbox.JobVersion,
		PayloadSchemaVersion: outbox.PayloadSchemaVersion,
	}, outbox.Payload)
	if err != nil {
		return err
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID
	mo := storageModel.OutboxEntityToModel(outbox)

	// In CP Delete, mark status = 'DELETING' and record pending outbox event.
	// Actual physical removal from DB occurs in JO settlement when DP returns SUCCEEDED.
	query := fmt.Sprintf(`
		WITH authorized_target AS (
			SELECT b.id, b.name, b.zone_id, b.tenant_id
			FROM %s.tenant_buckets b
			JOIN %s.tenant_workspaces w ON b.workspace_id = w.id
			JOIN %s.tenant_memberships m 
			  ON m.tenant_id = w.tenant_id 
			 AND m.user_id = $4 
			 AND m.status = 'active'
			WHERE b.id = $1 
			  AND b.workspace_id = $2 
			  AND b.tenant_id = $3 
			  AND w.zone_id = $5
			  AND b.status = 'READY'
			  AND NOT EXISTS (
				SELECT 1 FROM %s.tenant_credentials credential
				WHERE credential.bucket_id = b.id AND credential.state <> 'READY'
			  )
			FOR UPDATE OF b
		),
		updated_bucket AS (
			UPDATE %s.tenant_buckets
			SET status = 'DELETING',
			    updated_at = NOW()
			WHERE id IN (SELECT id FROM authorized_target)
			RETURNING id, name, zone_id, tenant_id
		),
		updated_credentials AS (
			UPDATE %s.tenant_credentials credential
			SET state = 'DELETING', updated_at = NOW()
			WHERE credential.bucket_id IN (SELECT id FROM updated_bucket)
			  AND credential.state = 'READY'
			RETURNING credential.id
		),
		inserted_outbox AS (
			INSERT INTO %s.storage_outbox_records (
				event_id, zone_id, job_topic, payload, owner_id, owner_type, status,
				job_version, resource_id, resource_name, payload_schema_version, trace_id, idle,
				actor_user_id, payload_key_id
			)
			SELECT $6, ub.zone_id, $7, $8, ub.tenant_id, $9, $10,
			       $11, ub.id::text, ub.name, $12, $13, $14,
			       $4, $15
			FROM updated_bucket ub
			CROSS JOIN (SELECT count(*) FROM updated_credentials) transitioned_credentials
		)
		SELECT id FROM updated_bucket;
	`, r.storage, r.hierarchy, r.hierarchy, r.storage, r.storage, r.storage, r.storage)

	var updatedID uuid.UUID
	err = r.db.QueryRow(ctx, query,
		id,
		workspaceID,
		tenantID,
		userID,
		zoneID,
		mo.EventID,
		mo.JobTopic,
		mo.Payload,
		mo.OwnerType,
		mo.Status,
		mo.JobVersion,
		mo.PayloadSchemaVersion,
		mo.TraceID,
		mo.Idle,
		mo.PayloadKeyID,
	).Scan(&updatedID)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storageTaxonomy.ErrNotFound
		}
		return fmt.Errorf("storage repo: delete tenant bucket failed: %w", err)
	}

	return nil
}
