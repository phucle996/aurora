package storageRepoImpl

import (
	"context"
	"errors"
	"fmt"

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

// TenantCredentialRepoImpl thực thi interface TenantCredentialRepo kết nối PostgreSQL.
type TenantCredentialRepoImpl struct {
	db        *pgxpool.Pool
	storage   string // schema storage
	hierarchy string // schema hierarchy
	protector jobpayload.Protector
}

// NewTenantCredentialRepo khởi tạo repository quản lý credentials cho bucket doanh nghiệp.
func NewTenantCredentialRepo(
	db *pgxpool.Pool,
	cfg *config.Config,
	protector jobpayload.Protector,
) storageRepoInterface.TenantCredentialRepo {
	return &TenantCredentialRepoImpl{
		db:        db,
		storage:   cfg.SchemaSQL.Storage,
		hierarchy: cfg.SchemaSQL.Hierarchy,
		protector: protector,
	}
}

func (r *TenantCredentialRepoImpl) Create(
	ctx context.Context,
	cred *storageEntity.TenantCredential,
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
		authorized_bucket AS (
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
			  AND (SELECT ok FROM admitted)
			FOR KEY SHARE OF b
		),
		ins_cred AS (
			INSERT INTO %s.tenant_credentials (
				id, bucket_id, access_key, policy, created_at, updated_at
			)
			SELECT $6, ab.id, $7, $8, NOW(), NOW()
			FROM authorized_bucket ab
			RETURNING id, bucket_id, access_key, policy, created_at, updated_at
		),
		inserted_outbox AS (
			INSERT INTO %s.storage_outbox_records (
				event_id, zone_id, job_topic, payload, owner_id, owner_type, status, completed_at,
				job_version, resource_id, payload_schema_version, trace_id, idle,
				error_code, error_message, actor_user_id, payload_key_id,
				resource_name
			)
			SELECT $9, ab.zone_id, $10, $11, ab.tenant_id, $12, $13, $14,
			       $15, ic.id::text, $16, $17, $18,
			       $19, $20, $4, $21,
			       ab.name
			FROM ins_cred ic
			JOIN authorized_bucket ab ON ic.bucket_id = ab.id
		)
		SELECT
			(SELECT ok FROM admitted) AS admitted,
			(SELECT id FROM ins_cred) AS created_id;
	`, r.storage, r.storage, r.hierarchy, r.hierarchy, r.storage, r.storage)

	var admitted bool
	var createdID *uuid.UUID
	err = r.db.QueryRow(ctx, query,
		cred.BucketID,
		workspaceID,
		tenantID,
		userID,
		zoneID,
		cred.ID,
		cred.AccessKey,
		cred.Policy,
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
	).Scan(&admitted, &createdID)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return storageTaxonomy.ErrAlreadyExists
		}
		return fmt.Errorf("storage repo: create tenant credential failed: %w", err)
	}
	if !admitted {
		return storageTaxonomy.ErrCommercialAdmissionDenied
	}
	if createdID == nil {
		return storageTaxonomy.ErrNotFound
	}

	return nil
}

func (r *TenantCredentialRepoImpl) GetByID(
	ctx context.Context,
	id uuid.UUID,
	bucketID uuid.UUID,
	workspaceID uuid.UUID,
	tenantID uuid.UUID,
	userID uuid.UUID,
	zoneID uuid.UUID,
) (*storageEntity.TenantCredential, error) {
	query := fmt.Sprintf(`
		SELECT c.id, c.bucket_id, c.access_key, c.policy, c.created_at, c.updated_at
		FROM %s.tenant_credentials c
		JOIN %s.tenant_buckets b ON c.bucket_id = b.id
		JOIN %s.tenant_workspaces w ON b.workspace_id = w.id
		JOIN %s.tenant_memberships m 
		  ON m.tenant_id = w.tenant_id 
		 AND m.user_id = $5 
		 AND m.status = 'active'
		WHERE c.id = $1 
		  AND c.bucket_id = $2 
		  AND b.workspace_id = $3 
		  AND b.tenant_id = $4 
		  AND w.zone_id = $6
	`, r.storage, r.storage, r.hierarchy, r.hierarchy)

	c := &storageEntity.TenantCredential{}
	err := r.db.QueryRow(ctx, query, id, bucketID, workspaceID, tenantID, userID, zoneID).Scan(
		&c.ID,
		&c.BucketID,
		&c.AccessKey,
		&c.Policy,
		&c.CreatedAt,
		&c.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, storageTaxonomy.ErrNotFound
		}
		return nil, fmt.Errorf("storage repo: get tenant credential by id failed: %w", err)
	}

	return c, nil
}

func (r *TenantCredentialRepoImpl) ListByBucket(
	ctx context.Context,
	bucketID uuid.UUID,
	workspaceID uuid.UUID,
	tenantID uuid.UUID,
	userID uuid.UUID,
	zoneID uuid.UUID,
) ([]*storageEntity.TenantCredential, error) {
	query := fmt.Sprintf(`
		SELECT c.id, c.bucket_id, c.access_key, c.policy, c.created_at, c.updated_at
		FROM %s.tenant_credentials c
		JOIN %s.tenant_buckets b ON c.bucket_id = b.id
		JOIN %s.tenant_workspaces w ON b.workspace_id = w.id
		JOIN %s.tenant_memberships m 
		  ON m.tenant_id = w.tenant_id 
		 AND m.user_id = $4 
		 AND m.status = 'active'
		WHERE c.bucket_id = $1 
		  AND b.workspace_id = $2 
		  AND b.tenant_id = $3 
		  AND w.zone_id = $5
		ORDER BY c.created_at DESC
	`, r.storage, r.storage, r.hierarchy, r.hierarchy)

	rows, err := r.db.Query(ctx, query, bucketID, workspaceID, tenantID, userID, zoneID)
	if err != nil {
		return nil, fmt.Errorf("storage repo: list tenant credentials failed: %w", err)
	}
	defer rows.Close()

	var result []*storageEntity.TenantCredential
	for rows.Next() {
		c := &storageEntity.TenantCredential{}
		err := rows.Scan(
			&c.ID,
			&c.BucketID,
			&c.AccessKey,
			&c.Policy,
			&c.CreatedAt,
			&c.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("storage repo: scan tenant credential row failed: %w", err)
		}
		result = append(result, c)
	}

	return result, nil
}

func (r *TenantCredentialRepoImpl) Delete(
	ctx context.Context,
	param *storageEntity.DeleteTenantCredential,
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

	query := fmt.Sprintf(`
		WITH authorized_credential AS (
			SELECT c.id, c.access_key, b.name AS bucket_name, b.zone_id, b.tenant_id
			FROM %s.tenant_credentials c
			JOIN %s.tenant_buckets b ON c.bucket_id = b.id
			JOIN %s.tenant_workspaces w ON b.workspace_id = w.id
			JOIN %s.tenant_memberships m 
			  ON m.tenant_id = w.tenant_id 
			 AND m.user_id = $5 
			 AND m.status = 'active'
			WHERE c.id = $1 
			  AND c.bucket_id = $2 
			  AND b.workspace_id = $3 
			  AND b.tenant_id = $4 
			  AND w.zone_id = $6
			FOR UPDATE OF c
		),
		deleted_credential AS (
			DELETE FROM %s.tenant_credentials
			WHERE id IN (SELECT id FROM authorized_credential)
			RETURNING id
		),
		inserted_outbox AS (
			INSERT INTO %s.storage_outbox_records (
				event_id, zone_id, job_topic, payload, owner_id, owner_type, status, completed_at,
				job_version, resource_id, payload_schema_version, trace_id, idle,
				error_code, error_message, actor_user_id, payload_key_id,
				resource_name
			)
			SELECT $7, ac.zone_id, $8, $9, ac.tenant_id, $10, $11, $12,
			       $13, ac.id::text, $14, $15, $16,
			       $17, $18, $5, $19,
			       ac.bucket_name
			FROM authorized_credential ac
		)
		SELECT id FROM deleted_credential;
	`, r.storage, r.storage, r.hierarchy, r.hierarchy, r.storage, r.storage)

	var deletedID uuid.UUID
	err = r.db.QueryRow(ctx, query,
		param.CredentialID,
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
		mo.CompletedAt,
		mo.JobVersion,
		mo.PayloadSchemaVersion,
		mo.TraceID,
		mo.Idle,
		mo.ErrorCode,
		mo.ErrorMessage,
		mo.PayloadKeyID,
	).Scan(&deletedID)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storageTaxonomy.ErrNotFound
		}
		return fmt.Errorf("storage repo: delete tenant credential failed: %w", err)
	}

	return nil
}

func (r *TenantCredentialRepoImpl) ListAccessKeys(
	ctx context.Context,
	bucketID uuid.UUID,
	workspaceID uuid.UUID,
	tenantID uuid.UUID,
	userID uuid.UUID,
	zoneID uuid.UUID,
) ([]string, error) {
	query := fmt.Sprintf(`
		SELECT c.access_key
		FROM %s.tenant_credentials c
		JOIN %s.tenant_buckets b ON c.bucket_id = b.id
		JOIN %s.tenant_workspaces w ON b.workspace_id = w.id
		JOIN %s.tenant_memberships m 
		  ON m.tenant_id = w.tenant_id 
		 AND m.user_id = $4 
		 AND m.status = 'active'
		WHERE c.bucket_id = $1 
		  AND b.workspace_id = $2 
		  AND b.tenant_id = $3 
		  AND w.zone_id = $5
	`, r.storage, r.storage, r.hierarchy, r.hierarchy)

	rows, err := r.db.Query(ctx, query, bucketID, workspaceID, tenantID, userID, zoneID)
	if err != nil {
		return nil, fmt.Errorf("storage repo: list access keys for tenant bucket failed: %w", err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("storage repo: scan tenant access key failed: %w", err)
		}
		keys = append(keys, key)
	}

	return keys, nil
}
