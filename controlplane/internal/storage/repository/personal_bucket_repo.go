package storageRepoImpl

import (
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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// [COMMENT]: PersonalBucketRepoImpl hiện thực interface PersonalBucketRepo kết nối với PostgreSQL.
type PersonalBucketRepoImpl struct {
	db        *pgxpool.Pool
	storage   string // Schema CSDL cho storage (e.g., "storage")
	hierarchy string // Schema CSDL cho hierarchy (e.g., "hierarchy")
	protector jobpayload.Protector
}

// [COMMENT]: NewPersonalBucketRepo khởi tạo repository với các thông số cấu hình và pgxpool.
func NewPersonalBucketRepo(
	db *pgxpool.Pool,
	cfg *config.Config,
	protector jobpayload.Protector,
) storageRepoInterface.PersonalBucketRepo {
	return &PersonalBucketRepoImpl{
		db:        db,
		storage:   cfg.SchemaSQL.Storage,
		hierarchy: cfg.SchemaSQL.Hierarchy,
		protector: protector,
	}
}

func (r *PersonalBucketRepoImpl) Create(ctx context.Context, bucket *storageEntity.PersonalBucket, workspaceID uuid.UUID, zoneID uuid.UUID, credential *storageEntity.PersonalCredential, outbox *storageEntity.StorageOutboxRecord) error {
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: outbox.ZoneID, SourceDomain: "STORAGE", JobTopic: outbox.JobTopic, ResourceID: outbox.ResourceID, JobVersion: outbox.JobVersion, PayloadSchemaVersion: outbox.PayloadSchemaVersion}, outbox.Payload)
	if err != nil {
		return err
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID
	mo := storageModel.OutboxEntityToModel(outbox)

	// [COMMENT]: CTE 3-way check ownership + admission: insert nguyên tử bucket + credential + outbox record chỉ khi workspace thuộc về user_id ($17) và owner được phép thương mại (ALLOW).
	query := fmt.Sprintf(`
		WITH admitted AS (
			SELECT EXISTS (
				SELECT 1 FROM %s.commercial_admission_projection
				WHERE owner_id = $17::text
				  AND owner_type = 'PERSONAL'
				  AND effective_at <= NOW()
				  AND (valid_until IS NULL OR valid_until > NOW())
				  AND decision = 'ALLOW'
			) AS ok
		),
		check_workspace AS (
			SELECT 1 FROM %s.personal_workspaces WHERE id = $3 AND owner_id = $17 AND zone_id = $4 AND (SELECT ok FROM admitted)
		),
		ins_bucket AS (
			INSERT INTO %s.personal_buckets (
				id, name, workspace_id, zone_id, status, capacity_quota_bytes, created_at, updated_at,
				encrypt_enabled, versioning_enabled, object_locking_enabled, replication_enabled,
				retention_days, legal_hold_enabled, tags
			)
			SELECT $1, $2, $3, $4, 'PROVISIONING', $5, $6, $7, $27, $28, $29, $30, $31, $32, $33
			FROM check_workspace
			RETURNING id
		),
		ins_credential AS (
			INSERT INTO %s.personal_credentials (
				id, bucket_id, access_key, policy, created_at, updated_at
			)
			SELECT $8, id, $9, $10, $11, $12
			FROM ins_bucket
		),
		ins_outbox AS (
			INSERT INTO %s.storage_outbox_records (
				event_id, zone_id, job_topic, payload, owner_id, owner_type, status, completed_at,
				job_version, resource_id, payload_schema_version, trace_id, idle,
				error_code, error_message, actor_user_id, payload_key_id,
				resource_name, rollback_quota_bytes
			)
			SELECT $13, $14, $15, $16, $17, $34, $18, $19, $20, $21, $22, $23, $24, $25, $26, $35, $36, $37, $38
			FROM ins_bucket
		)
		SELECT
			(SELECT ok FROM admitted) AS admitted,
			(SELECT id FROM ins_bucket) AS created_id;
	`, r.storage, r.hierarchy, r.storage, r.storage, r.storage)

	var admitted bool
	var createdID *uuid.UUID
	err = r.db.QueryRow(ctx, query,
		// [COMMENT]: $1-$7 — personal_buckets fields
		bucket.ID,
		bucket.Name,
		workspaceID,
		zoneID,
		bucket.CapacityQuotaBytes,
		bucket.CreatedAt,
		bucket.UpdatedAt,
		// [COMMENT]: $8-$12 — personal_credentials fields
		credential.ID,
		credential.AccessKey,
		credential.Policy,
		credential.CreatedAt,
		credential.UpdatedAt,
		// [COMMENT]: $13-$26 — storage_outbox_records fields
		mo.EventID,
		mo.ZoneID,
		mo.JobTopic,
		mo.Payload,
		mo.OwnerID,
		mo.Status,

		mo.CompletedAt,
		mo.JobVersion,
		mo.ResourceID,
		mo.PayloadSchemaVersion,
		mo.TraceID,
		mo.Idle,
		mo.ErrorCode,
		mo.ErrorMessage,
		// [COMMENT]: $27-$33 — advanced configurations
		bucket.EncryptEnabled,
		bucket.VersioningEnabled,
		bucket.ObjectLockingEnabled,
		bucket.ReplicationEnabled,
		bucket.RetentionDays,
		bucket.LegalHoldEnabled,
		func() []byte {
			b, _ := json.Marshal(bucket.Tags)
			return b
		}(),
		// [COMMENT]: $34 — owner_type ("PERSONAL")
		mo.OwnerType,
		mo.ActorUserID,
		mo.PayloadKeyID,
		mo.ResourceName,
		mo.RollbackQuotaBytes,
	).Scan(&admitted, &createdID)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return storageTaxonomy.ErrAlreadyExists
		}
		return fmt.Errorf("storage repo: create personal bucket failed: %w", err)
	}
	if !admitted {
		return storageTaxonomy.ErrCommercialAdmissionDenied
	}
	if createdID == nil {
		return storageTaxonomy.ErrNotFound
	}

	bucket.Status = storageEntity.BucketStatusProvisioning
	return nil
}

func (r *PersonalBucketRepoImpl) GetByID(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*storageEntity.PersonalBucket, error) {
	query := fmt.Sprintf(`
		SELECT b.id, b.name, b.zone_id, b.status, b.capacity_quota_bytes, b.used_bytes, b.versioning_enabled, b.lifecycle_rules, b.created_at, b.updated_at
		FROM %s.personal_buckets b
		JOIN %s.personal_workspaces w ON b.workspace_id = w.id
		WHERE b.id = $1 AND w.owner_id = $2
	`, r.storage, r.hierarchy)

	var b storageEntity.PersonalBucket
	var rawLifecycle []byte

	err := r.db.QueryRow(ctx, query, id, userID).Scan(
		&b.ID,
		&b.Name,
		&b.ZoneID,
		&b.Status,
		&b.CapacityQuotaBytes,
		&b.UsedBytes,
		&b.VersioningEnabled,
		&rawLifecycle,
		&b.CreatedAt,
		&b.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, storageTaxonomy.ErrNotFound
		}
		return nil, fmt.Errorf("storage repo: get personal bucket by id failed: %w", err)
	}

	if len(rawLifecycle) > 0 {
		_ = json.Unmarshal(rawLifecycle, &b.LifecycleRules)
	}
	if b.LifecycleRules == nil {
		b.LifecycleRules = []storageEntity.BucketLifecycleRule{}
	}

	return &b, nil
}

func (r *PersonalBucketRepoImpl) GetByName(ctx context.Context, name string) (*storageEntity.PersonalBucket, error) {
	query := fmt.Sprintf(`
		SELECT id, name, zone_id, status, capacity_quota_bytes, used_bytes, versioning_enabled, lifecycle_rules, created_at, updated_at
		FROM %s.personal_buckets
		WHERE name = $1
	`, r.storage)

	var b storageEntity.PersonalBucket
	var rawLifecycle []byte

	err := r.db.QueryRow(ctx, query, name).Scan(
		&b.ID,
		&b.Name,
		&b.ZoneID,
		&b.Status,
		&b.CapacityQuotaBytes,
		&b.UsedBytes,
		&b.VersioningEnabled,
		&rawLifecycle,
		&b.CreatedAt,
		&b.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, storageTaxonomy.ErrNotFound
		}
		return nil, fmt.Errorf("storage repo: get personal bucket by name failed: %w", err)
	}

	if len(rawLifecycle) > 0 {
		_ = json.Unmarshal(rawLifecycle, &b.LifecycleRules)
	}
	if b.LifecycleRules == nil {
		b.LifecycleRules = []storageEntity.BucketLifecycleRule{}
	}

	return &b, nil
}

func (r *PersonalBucketRepoImpl) ListByWorkspace(ctx context.Context, workspaceID uuid.UUID, zoneID uuid.UUID, userID uuid.UUID) ([]*storageEntity.PersonalBucket, error) {
	query := fmt.Sprintf(`
		SELECT b.id, b.name, b.zone_id, b.status, b.capacity_quota_bytes, b.used_bytes, b.versioning_enabled, b.lifecycle_rules, b.created_at, b.updated_at
		FROM %s.personal_buckets b
		JOIN %s.personal_workspaces w ON b.workspace_id = w.id
		WHERE b.workspace_id = $1 AND b.zone_id = $2 AND w.owner_id = $3 AND w.zone_id = $2
		ORDER BY b.created_at DESC
	`, r.storage, r.hierarchy)

	rows, err := r.db.Query(ctx, query, workspaceID, zoneID, userID)
	if err != nil {
		return nil, fmt.Errorf("storage repo: list personal buckets by workspace failed: %w", err)
	}
	defer rows.Close()

	var buckets []*storageEntity.PersonalBucket
	for rows.Next() {
		var b storageEntity.PersonalBucket
		var rawLifecycle []byte

		err := rows.Scan(
			&b.ID,
			&b.Name,
			&b.ZoneID,
			&b.Status,
			&b.CapacityQuotaBytes,
			&b.UsedBytes,
			&b.VersioningEnabled,
			&rawLifecycle,
			&b.CreatedAt,
			&b.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("storage repo: scan personal bucket row failed: %w", err)
		}

		if len(rawLifecycle) > 0 {
			_ = json.Unmarshal(rawLifecycle, &b.LifecycleRules)
		}
		if b.LifecycleRules == nil {
			b.LifecycleRules = []storageEntity.BucketLifecycleRule{}
		}

		buckets = append(buckets, &b)
	}

	return buckets, nil
}

func (r *PersonalBucketRepoImpl) UpdateQuota(ctx context.Context, id uuid.UUID, userID uuid.UUID, quotaBytes int64, outbox *storageEntity.StorageOutboxRecord) error {
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: outbox.ZoneID, SourceDomain: "STORAGE", JobTopic: outbox.JobTopic, ResourceID: outbox.ResourceID, JobVersion: outbox.JobVersion, PayloadSchemaVersion: outbox.PayloadSchemaVersion}, outbox.Payload)
	if err != nil {
		return err
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("storage repo: begin tx failed: %w", err)
	}
	defer tx.Rollback(ctx)

	selectQuery := fmt.Sprintf(`
		WITH admitted AS (
			SELECT (
				$3::bigint <= (
					SELECT b.capacity_quota_bytes
					FROM %s.personal_buckets b
					JOIN %s.personal_workspaces w ON b.workspace_id = w.id
					WHERE b.id = $1 AND w.owner_id = $2
				)
				OR EXISTS (
					SELECT 1 FROM %s.commercial_admission_projection
					WHERE owner_id = $2::text
					  AND owner_type = 'PERSONAL'
					  AND effective_at <= NOW()
					  AND (valid_until IS NULL OR valid_until > NOW())
					  AND decision = 'ALLOW'
				)
			) AS ok
		)
		SELECT b.name, b.capacity_quota_bytes, b.used_bytes, (SELECT ok FROM admitted)
		FROM %s.personal_buckets b
		JOIN %s.personal_workspaces w ON b.workspace_id = w.id
		WHERE b.id = $1 AND w.owner_id = $2
		  AND b.status NOT IN ('DELETING')
		FOR UPDATE
	`, r.storage, r.hierarchy, r.storage, r.storage, r.hierarchy)

	var bucketName string
	var currentQuota, usedBytes int64
	var admitted bool
	err = tx.QueryRow(ctx, selectQuery, id, userID, quotaBytes).Scan(&bucketName, &currentQuota, &usedBytes, &admitted)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storageTaxonomy.ErrNotFound
		}
		return fmt.Errorf("storage repo: select bucket for update failed: %w", err)
	}
	if !admitted {
		return storageTaxonomy.ErrCommercialAdmissionDenied
	}

	if quotaBytes-usedBytes < 1073741824 {
		return storageTaxonomy.ErrResizeLimitTooLow
	}

	updateQuery := fmt.Sprintf(`
		UPDATE %s.personal_buckets
		SET capacity_quota_bytes = $1, status = 'UPDATING', updated_at = $2
		WHERE id = $3
	`, r.storage)

	_, err = tx.Exec(ctx, updateQuery, quotaBytes, time.Now(), id)
	if err != nil {
		return fmt.Errorf("storage repo: update personal bucket capacity failed: %w", err)
	}

	outbox.ResourceName = bucketName
	outbox.RollbackQuotaBytes = &currentQuota
	mo := storageModel.OutboxEntityToModel(outbox)
	insertOutboxQuery := fmt.Sprintf(`
		INSERT INTO %s.storage_outbox_records (
			event_id, zone_id, job_topic, payload, owner_id, owner_type, status, completed_at,
			job_version, resource_id, payload_schema_version, trace_id, idle,
			error_code, error_message, actor_user_id, payload_key_id,
			resource_name, rollback_quota_bytes
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
	`, r.storage)

	_, err = tx.Exec(ctx, insertOutboxQuery,
		mo.EventID,
		mo.ZoneID,
		mo.JobTopic,
		mo.Payload,
		mo.OwnerID,
		mo.OwnerType,
		mo.Status,

		mo.CompletedAt,
		mo.JobVersion,
		mo.ResourceID,
		mo.PayloadSchemaVersion,
		mo.TraceID,
		mo.Idle,
		mo.ErrorCode,
		mo.ErrorMessage,
		mo.ActorUserID,
		mo.PayloadKeyID,
		mo.ResourceName,
		mo.RollbackQuotaBytes,
	)
	if err != nil {
		return fmt.Errorf("storage repo: insert resize outbox failed: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("storage repo: commit resize tx failed: %w", err)
	}

	return nil
}

func (r *PersonalBucketRepoImpl) UpdateVersioning(ctx context.Context, id uuid.UUID, userID uuid.UUID, versioningEnabled bool, outbox *storageEntity.StorageOutboxRecord) (*storageEntity.PersonalBucket, error) {
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: outbox.ZoneID, SourceDomain: "STORAGE", JobTopic: outbox.JobTopic, ResourceID: outbox.ResourceID, JobVersion: outbox.JobVersion, PayloadSchemaVersion: outbox.PayloadSchemaVersion}, outbox.Payload)
	if err != nil {
		return nil, err
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("storage repo: begin tx failed: %w", err)
	}
	defer tx.Rollback(ctx)

	selectQuery := fmt.Sprintf(`
		WITH admitted AS (
			SELECT EXISTS (
				SELECT 1 FROM %s.commercial_admission_projection
				WHERE owner_id = $2::text
				  AND owner_type = 'PERSONAL'
				  AND effective_at <= NOW()
				  AND (valid_until IS NULL OR valid_until > NOW())
				  AND decision = 'ALLOW'
			) AS ok
		)
		SELECT b.id, b.name, b.zone_id, b.capacity_quota_bytes, b.used_bytes, b.lifecycle_rules, b.created_at, b.updated_at, (SELECT ok FROM admitted)
		FROM %s.personal_buckets b
		JOIN %s.personal_workspaces w ON b.workspace_id = w.id
		WHERE b.id = $1 AND w.owner_id = $2
		  AND b.status NOT IN ('DELETING')
		FOR UPDATE
	`, r.storage, r.storage, r.hierarchy)

	var b storageEntity.PersonalBucket
	var rawLifecycle []byte
	var admitted bool
	err = tx.QueryRow(ctx, selectQuery, id, userID).Scan(
		&b.ID,
		&b.Name,
		&b.ZoneID,
		&b.CapacityQuotaBytes,
		&b.UsedBytes,
		&rawLifecycle,
		&b.CreatedAt,
		&b.UpdatedAt,
		&admitted,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, storageTaxonomy.ErrNotFound
		}
		return nil, fmt.Errorf("storage repo: select bucket for versioning update failed: %w", err)
	}
	if !admitted {
		return nil, storageTaxonomy.ErrCommercialAdmissionDenied
	}

	if len(rawLifecycle) > 0 {
		_ = json.Unmarshal(rawLifecycle, &b.LifecycleRules)
	}
	if b.LifecycleRules == nil {
		b.LifecycleRules = []storageEntity.BucketLifecycleRule{}
	}

	now := time.Now()
	updateQuery := fmt.Sprintf(`
		UPDATE %s.personal_buckets
		SET versioning_enabled = $1, status = 'UPDATING', updated_at = $2
		WHERE id = $3
	`, r.storage)

	_, err = tx.Exec(ctx, updateQuery, versioningEnabled, now, id)
	if err != nil {
		return nil, fmt.Errorf("storage repo: update versioning failed: %w", err)
	}

	b.VersioningEnabled = versioningEnabled
	b.Status = storageEntity.BucketStatusUpdating
	b.UpdatedAt = now

	outbox.ResourceName = b.Name
	mo := storageModel.OutboxEntityToModel(outbox)
	insertOutboxQuery := fmt.Sprintf(`
		INSERT INTO %s.storage_outbox_records (
			event_id, zone_id, job_topic, payload, owner_id, owner_type, status, completed_at,
			job_version, resource_id, payload_schema_version, trace_id, idle,
			error_code, error_message, actor_user_id, payload_key_id,
			resource_name
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
	`, r.storage)

	_, err = tx.Exec(ctx, insertOutboxQuery,
		mo.EventID,
		mo.ZoneID,
		mo.JobTopic,
		mo.Payload,
		mo.OwnerID,
		mo.OwnerType,
		mo.Status,
		mo.CompletedAt,
		mo.JobVersion,
		mo.ResourceID,
		mo.PayloadSchemaVersion,
		mo.TraceID,
		mo.Idle,
		mo.ErrorCode,
		mo.ErrorMessage,
		mo.ActorUserID,
		mo.PayloadKeyID,
		mo.ResourceName,
	)
	if err != nil {
		return nil, fmt.Errorf("storage repo: insert versioning outbox failed: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("storage repo: commit versioning tx failed: %w", err)
	}

	return &b, nil
}

func (r *PersonalBucketRepoImpl) UpdateLifecycle(ctx context.Context, id uuid.UUID, userID uuid.UUID, rules []storageEntity.BucketLifecycleRule, outbox *storageEntity.StorageOutboxRecord) (*storageEntity.PersonalBucket, error) {
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: outbox.ZoneID, SourceDomain: "STORAGE", JobTopic: outbox.JobTopic, ResourceID: outbox.ResourceID, JobVersion: outbox.JobVersion, PayloadSchemaVersion: outbox.PayloadSchemaVersion}, outbox.Payload)
	if err != nil {
		return nil, err
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("storage repo: begin tx failed: %w", err)
	}
	defer tx.Rollback(ctx)

	selectQuery := fmt.Sprintf(`
		WITH admitted AS (
			SELECT EXISTS (
				SELECT 1 FROM %s.commercial_admission_projection
				WHERE owner_id = $2::text
				  AND owner_type = 'PERSONAL'
				  AND effective_at <= NOW()
				  AND (valid_until IS NULL OR valid_until > NOW())
				  AND decision = 'ALLOW'
			) AS ok
		)
		SELECT b.id, b.name, b.zone_id, b.capacity_quota_bytes, b.used_bytes, b.versioning_enabled, b.created_at, b.updated_at, (SELECT ok FROM admitted)
		FROM %s.personal_buckets b
		JOIN %s.personal_workspaces w ON b.workspace_id = w.id
		WHERE b.id = $1 AND w.owner_id = $2
		  AND b.status NOT IN ('DELETING')
		FOR UPDATE
	`, r.storage, r.storage, r.hierarchy)

	var b storageEntity.PersonalBucket
	var admitted bool
	err = tx.QueryRow(ctx, selectQuery, id, userID).Scan(
		&b.ID,
		&b.Name,
		&b.ZoneID,
		&b.CapacityQuotaBytes,
		&b.UsedBytes,
		&b.VersioningEnabled,
		&b.CreatedAt,
		&b.UpdatedAt,
		&admitted,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, storageTaxonomy.ErrNotFound
		}
		return nil, fmt.Errorf("storage repo: select bucket for lifecycle update failed: %w", err)
	}
	if !admitted {
		return nil, storageTaxonomy.ErrCommercialAdmissionDenied
	}

	for _, rule := range rules {
		if rule.NoncurrentVersionExpirationDays > 0 && !b.VersioningEnabled {
			return nil, storageTaxonomy.ErrVersioningRequired
		}
	}

	rulesJSON, err := json.Marshal(rules)
	if err != nil {
		return nil, fmt.Errorf("storage repo: marshal lifecycle rules failed: %w", err)
	}

	now := time.Now()
	updateQuery := fmt.Sprintf(`
		UPDATE %s.personal_buckets
		SET lifecycle_rules = $1, status = 'UPDATING', updated_at = $2
		WHERE id = $3
	`, r.storage)

	_, err = tx.Exec(ctx, updateQuery, rulesJSON, now, id)
	if err != nil {
		return nil, fmt.Errorf("storage repo: update lifecycle failed: %w", err)
	}

	b.LifecycleRules = rules
	b.Status = storageEntity.BucketStatusUpdating
	b.UpdatedAt = now

	outbox.ResourceName = b.Name
	mo := storageModel.OutboxEntityToModel(outbox)
	insertOutboxQuery := fmt.Sprintf(`
		INSERT INTO %s.storage_outbox_records (
			event_id, zone_id, job_topic, payload, owner_id, owner_type, status, completed_at,
			job_version, resource_id, payload_schema_version, trace_id, idle,
			error_code, error_message, actor_user_id, payload_key_id,
			resource_name
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
	`, r.storage)

	_, err = tx.Exec(ctx, insertOutboxQuery,
		mo.EventID,
		mo.ZoneID,
		mo.JobTopic,
		mo.Payload,
		mo.OwnerID,
		mo.OwnerType,
		mo.Status,
		mo.CompletedAt,
		mo.JobVersion,
		mo.ResourceID,
		mo.PayloadSchemaVersion,
		mo.TraceID,
		mo.Idle,
		mo.ErrorCode,
		mo.ErrorMessage,
		mo.ActorUserID,
		mo.PayloadKeyID,
		mo.ResourceName,
	)
	if err != nil {
		return nil, fmt.Errorf("storage repo: insert lifecycle outbox failed: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("storage repo: commit lifecycle tx failed: %w", err)
	}

	return &b, nil
}

func (r *PersonalBucketRepoImpl) Delete(ctx context.Context, id uuid.UUID, userID uuid.UUID, outbox *storageEntity.StorageOutboxRecord) error {
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: outbox.ZoneID, SourceDomain: "STORAGE", JobTopic: outbox.JobTopic, ResourceID: outbox.ResourceID, JobVersion: outbox.JobVersion, PayloadSchemaVersion: outbox.PayloadSchemaVersion}, outbox.Payload)
	if err != nil {
		return err
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID
	mo := storageModel.OutboxEntityToModel(outbox)

	// Mark status = 'DELETING' and record pending outbox event.
	query := fmt.Sprintf(`
		WITH locked_bucket AS (
			SELECT b.id, b.name
			FROM %s.personal_buckets b
			JOIN %s.personal_workspaces w ON b.workspace_id = w.id
			WHERE b.id = $1 AND w.owner_id = $2
			  AND b.status NOT IN ('DELETING')
			FOR UPDATE
		),
		updated_bucket AS (
			UPDATE %s.personal_buckets
			SET status = 'DELETING', updated_at = NOW()
			WHERE id IN (SELECT id FROM locked_bucket)
			RETURNING id, name
		)
		INSERT INTO %s.storage_outbox_records (
			event_id, zone_id, job_topic, payload, owner_id, owner_type, status, completed_at,
			job_version, resource_id, payload_schema_version, trace_id, idle,
			error_code, error_message, actor_user_id, payload_key_id,
			resource_name, rollback_quota_bytes
		)
		SELECT $3, $4, $5, $6, $7, $17, $8, $9, $10, $11, $12, $13, $14, $15, $16, $18, $19, updated_bucket.name, NULL
		FROM updated_bucket;
	`, r.storage, r.hierarchy, r.storage, r.storage)

	res, err := r.db.Exec(ctx, query,
		id,
		userID,
		mo.EventID,
		mo.ZoneID,
		mo.JobTopic,
		mo.Payload,
		mo.OwnerID,
		mo.Status,
		mo.CompletedAt,
		mo.JobVersion,
		mo.ResourceID,
		mo.PayloadSchemaVersion,
		mo.TraceID,
		mo.Idle,
		mo.ErrorCode,
		mo.ErrorMessage,
		mo.OwnerType,
		mo.ActorUserID,
		mo.PayloadKeyID,
	)

	if err != nil {
		return fmt.Errorf("storage repo: atomic delete bucket outbox failed: %w", err)
	}

	if res.RowsAffected() == 0 {
		return storageTaxonomy.ErrNotFound
	}

	return nil
}

func (r *PersonalBucketRepoImpl) ListNamesByWorkspace(ctx context.Context, workspaceID uuid.UUID, zoneID uuid.UUID, userID uuid.UUID) ([]string, error) {
	query := fmt.Sprintf(`
		SELECT b.name
		FROM %s.personal_buckets b
		JOIN %s.personal_workspaces w ON b.workspace_id = w.id
		WHERE b.workspace_id = $1 AND b.zone_id = $2 AND w.owner_id = $3 AND w.zone_id = $2
		ORDER BY b.created_at DESC
	`, r.storage, r.hierarchy)

	rows, err := r.db.Query(ctx, query, workspaceID, zoneID, userID)
	if err != nil {
		return nil, fmt.Errorf("storage repo: list personal bucket names failed: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("storage repo: scan personal bucket name row failed: %w", err)
		}
		names = append(names, name)
	}

	return names, nil
}
