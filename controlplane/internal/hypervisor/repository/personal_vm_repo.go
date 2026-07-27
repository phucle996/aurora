package hypervisorRepoImpl

import (
	"context"
	"errors"
	"fmt"

	"controlplane/internal/config"
	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorRepoInterface "controlplane/internal/hypervisor/domain/repo"
	hypervisorTaxonomy "controlplane/internal/hypervisor/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PersonalVMRepoPostgres struct {
	db         *pgxpool.Pool
	hypervisor string
	hierarchy  string
}

func NewPersonalVMRepo(
	db *pgxpool.Pool,
	cfg *config.Config,
) hypervisorRepoInterface.PersonalVMRepository {
	return &PersonalVMRepoPostgres{
		db:         db,
		hypervisor: cfg.SchemaSQL.Hypervisor,
		hierarchy:  cfg.SchemaSQL.Hierarchy,
	}
}

func (r *PersonalVMRepoPostgres) GetAvailableImage(
	ctx context.Context,
	imageID uuid.UUID,
	zoneID uuid.UUID,
) (*hypervisorEntity.ImageArtifact, error) {
	query := fmt.Sprintf(`
		SELECT id, zone_id, name, code, distribution, release, revision,
		       architecture, format, size_bytes, sha256, object_key,
		       state, created_by, provider_template_vmid, error_code, error_message,
		       created_at, updated_at, available_at
		FROM %s.image_artifacts
		WHERE id = $1
		  AND zone_id = $2
		  AND state = 'AVAILABLE'
		  AND provider_template_vmid IS NOT NULL
	`, r.hypervisor)
	image := &hypervisorEntity.ImageArtifact{}
	err := r.db.QueryRow(ctx, query, imageID, zoneID).Scan(
		&image.ID,
		&image.ZoneID,
		&image.Name,
		&image.Code,
		&image.Distribution,
		&image.Release,
		&image.Revision,
		&image.Architecture,
		&image.Format,
		&image.SizeBytes,
		&image.SHA256,
		&image.ObjectKey,
		&image.State,
		&image.CreatedBy,
		&image.ProviderTemplateVMID,
		&image.ErrorCode,
		&image.ErrorMessage,
		&image.CreatedAt,
		&image.UpdatedAt,
		&image.AvailableAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, hypervisorTaxonomy.ErrImageNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("hypervisor repository: resolve VM image: %w", err)
	}
	return image, nil
}

func (r *PersonalVMRepoPostgres) CreateOrGet(
	ctx context.Context,
	vm *hypervisorEntity.PersonalVM,
	outbox *hypervisorEntity.HypervisorOutboxRecord,
) (*hypervisorEntity.PersonalVMCreateResult, error) {
	query := fmt.Sprintf(`
		WITH authorized_scope AS (
			SELECT 1
			FROM %s.personal_workspaces workspace
			JOIN %s.zones zone
			  ON zone.id = workspace.zone_id
			JOIN %s.image_artifacts image
			  ON image.id = $7
			 AND image.zone_id = zone.id
			 AND image.revision = $8
			 AND image.sha256 = $9
			 AND image.state = 'AVAILABLE'
			 AND image.provider_template_vmid IS NOT NULL
			WHERE workspace.id = $2
			  AND workspace.owner_id = $4
			  AND workspace.zone_id = $3
			  AND zone.status = 'active'
			  AND EXISTS (
				SELECT 1
				FROM %s.zone_services service
				WHERE service.zone_id = zone.id
				  AND service.service_type = 'hypervisor'
				  AND service.desired_state = TRUE
			  )
		),
		inserted_vm AS (
			INSERT INTO %s.personal_vms (
				id, workspace_id, zone_id, owner_user_id, name, image,
				image_id, image_revision, image_sha256,
				cpu_cores, memory_mb, disk_gb, ssh_public_key, spec_hash,
				status, operation_id, provider_name, created_at, updated_at
			)
			SELECT $1, $2, $3, $4, $5, $6,
			       $7, $8, $9,
			       $10, $11, $12, $13, $14,
			       $15, $16, $17, $18, $19
			FROM authorized_scope
			ON CONFLICT (workspace_id, name) DO NOTHING
			RETURNING *
		),
		inserted_outbox AS (
			INSERT INTO %s.hypervisor_outbox_records (
				event_id, zone_id, job_topic, payload, actor_user_id,
				status, job_version, resource_id, payload_schema_version,
				trace_id, idle
			)
			SELECT $20, $21, $22, $23, $24,
			       $25, $26, $27, $28, $29, $30
			FROM inserted_vm
			RETURNING event_id
		)
		SELECT id, workspace_id, zone_id, owner_user_id, name, image,
		       image_id, image_revision, image_sha256,
		       cpu_cores, memory_mb, disk_gb, ssh_public_key, spec_hash,
		       status, operation_id, provider_name, provider_vmid,
		       host(ipv4_address),
		       created_at, updated_at, provisioned_at, TRUE AS created
		FROM inserted_vm
		JOIN inserted_outbox ON TRUE
	`, r.hierarchy, r.hierarchy, r.hypervisor, r.hierarchy, r.hypervisor, r.hypervisor)

	row := r.db.QueryRow(
		ctx,
		query,
		vm.ID,
		vm.WorkspaceID,
		vm.ZoneID,
		vm.OwnerUserID,
		vm.Name,
		vm.Image,
		vm.ImageID,
		vm.ImageRevision,
		vm.ImageSHA256,
		vm.CPUCores,
		vm.MemoryMB,
		vm.DiskGB,
		vm.SSHPublicKey,
		vm.SpecHash,
		vm.Status,
		vm.OperationID,
		vm.ProviderName,
		vm.CreatedAt,
		vm.UpdatedAt,
		outbox.EventID,
		outbox.ZoneID,
		outbox.JobTopic,
		outbox.Payload,
		outbox.ActorUserID,
		outbox.Status,
		outbox.JobVersion,
		outbox.ResourceID,
		outbox.PayloadSchemaVersion,
		outbox.TraceID,
		outbox.IdleSeconds,
	)

	var current hypervisorEntity.PersonalVM
	var created bool
	if err := row.Scan(
		&current.ID,
		&current.WorkspaceID,
		&current.ZoneID,
		&current.OwnerUserID,
		&current.Name,
		&current.Image,
		&current.ImageID,
		&current.ImageRevision,
		&current.ImageSHA256,
		&current.CPUCores,
		&current.MemoryMB,
		&current.DiskGB,
		&current.SSHPublicKey,
		&current.SpecHash,
		&current.Status,
		&current.OperationID,
		&current.ProviderName,
		&current.ProviderVMID,
		&current.IPv4Address,
		&current.CreatedAt,
		&current.UpdatedAt,
		&current.ProvisionedAt,
		&created,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// INSERT .. ON CONFLICT can lose to a row that was invisible to the
			// statement snapshot. This second statement observes the winner
			// after the conflict wait without creating another outbox record.
			existingQuery := fmt.Sprintf(`
				SELECT current_vm.id, current_vm.workspace_id, current_vm.zone_id,
				       current_vm.owner_user_id, current_vm.name, current_vm.image,
				       current_vm.image_id, current_vm.image_revision, current_vm.image_sha256,
				       current_vm.cpu_cores, current_vm.memory_mb, current_vm.disk_gb,
				       current_vm.ssh_public_key, current_vm.spec_hash,
				       current_vm.status, current_vm.operation_id, current_vm.provider_name,
				       current_vm.provider_vmid,
				       host(current_vm.ipv4_address), current_vm.created_at,
				       current_vm.updated_at, current_vm.provisioned_at
				FROM %s.personal_vms current_vm
				JOIN %s.personal_workspaces workspace
				  ON workspace.id = current_vm.workspace_id
				 AND workspace.owner_id = $3
				 AND workspace.zone_id = $4
				JOIN %s.zones zone
				  ON zone.id = workspace.zone_id
				 AND zone.status = 'active'
				WHERE current_vm.workspace_id = $1
				  AND current_vm.name = $2
				  AND EXISTS (
					SELECT 1
					FROM %s.zone_services service
					WHERE service.zone_id = zone.id
					  AND service.service_type = 'hypervisor'
					  AND service.desired_state = TRUE
				  )
			`, r.hypervisor, r.hierarchy, r.hierarchy, r.hierarchy)
			if existingErr := r.db.QueryRow(
				ctx,
				existingQuery,
				vm.WorkspaceID,
				vm.Name,
				vm.OwnerUserID,
				vm.ZoneID,
			).Scan(
				&current.ID,
				&current.WorkspaceID,
				&current.ZoneID,
				&current.OwnerUserID,
				&current.Name,
				&current.Image,
				&current.ImageID,
				&current.ImageRevision,
				&current.ImageSHA256,
				&current.CPUCores,
				&current.MemoryMB,
				&current.DiskGB,
				&current.SSHPublicKey,
				&current.SpecHash,
				&current.Status,
				&current.OperationID,
				&current.ProviderName,
				&current.ProviderVMID,
				&current.IPv4Address,
				&current.CreatedAt,
				&current.UpdatedAt,
				&current.ProvisionedAt,
			); existingErr != nil {
				if errors.Is(existingErr, pgx.ErrNoRows) {
					return nil, hypervisorTaxonomy.ErrScopeUnavailable
				}
				return nil, fmt.Errorf(
					"hypervisor repository: read concurrent personal VM winner: %w",
					existingErr,
				)
			}
			return &hypervisorEntity.PersonalVMCreateResult{
				VM:      &current,
				Created: false,
			}, nil
		}
		return nil, fmt.Errorf("hypervisor repository: create personal VM: %w", err)
	}

	return &hypervisorEntity.PersonalVMCreateResult{
		VM:      &current,
		Created: created,
	}, nil
}

func (r *PersonalVMRepoPostgres) List(
	ctx context.Context,
	workspaceID uuid.UUID,
	zoneID uuid.UUID,
	ownerUserID uuid.UUID,
	limit int32,
) ([]*hypervisorEntity.PersonalVM, error) {
	query := fmt.Sprintf(`
		SELECT vm.id, vm.workspace_id, vm.zone_id, vm.owner_user_id, vm.name, vm.image,
		       vm.image_id, vm.image_revision, vm.image_sha256,
		       vm.cpu_cores, vm.memory_mb, vm.disk_gb, vm.ssh_public_key, vm.spec_hash,
		       vm.status, vm.operation_id, vm.provider_name,
		       vm.provider_vmid, host(vm.ipv4_address),
		       vm.created_at, vm.updated_at, vm.provisioned_at
		FROM %s.personal_vms vm
		JOIN %s.personal_workspaces workspace
		  ON workspace.id = vm.workspace_id
		 AND workspace.owner_id = $3
		WHERE vm.workspace_id = $1
		  AND vm.zone_id = $2
		ORDER BY vm.created_at DESC, vm.id DESC
		LIMIT $4
	`, r.hypervisor, r.hierarchy)

	rows, err := r.db.Query(ctx, query, workspaceID, zoneID, ownerUserID, limit)
	if err != nil {
		return nil, fmt.Errorf("hypervisor repository: list personal VMs: %w", err)
	}
	defer rows.Close()

	vms := make([]*hypervisorEntity.PersonalVM, 0)
	for rows.Next() {
		vm := &hypervisorEntity.PersonalVM{}
		if err := rows.Scan(
			&vm.ID,
			&vm.WorkspaceID,
			&vm.ZoneID,
			&vm.OwnerUserID,
			&vm.Name,
			&vm.Image,
			&vm.ImageID,
			&vm.ImageRevision,
			&vm.ImageSHA256,
			&vm.CPUCores,
			&vm.MemoryMB,
			&vm.DiskGB,
			&vm.SSHPublicKey,
			&vm.SpecHash,
			&vm.Status,
			&vm.OperationID,
			&vm.ProviderName,
			&vm.ProviderVMID,
			&vm.IPv4Address,
			&vm.CreatedAt,
			&vm.UpdatedAt,
			&vm.ProvisionedAt,
		); err != nil {
			return nil, fmt.Errorf("hypervisor repository: scan personal VM: %w", err)
		}
		vms = append(vms, vm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("hypervisor repository: iterate personal VMs: %w", err)
	}
	return vms, nil
}

func (r *PersonalVMRepoPostgres) Get(
	ctx context.Context,
	vmID uuid.UUID,
	workspaceID uuid.UUID,
	ownerUserID uuid.UUID,
) (*hypervisorEntity.PersonalVM, error) {
	query := fmt.Sprintf(`
		SELECT vm.id, vm.workspace_id, vm.zone_id, vm.owner_user_id, vm.name, vm.image,
		       vm.image_id, vm.image_revision, vm.image_sha256,
		       vm.cpu_cores, vm.memory_mb, vm.disk_gb, vm.ssh_public_key, vm.spec_hash,
		       vm.status, vm.operation_id, vm.provider_name,
		       vm.provider_vmid, host(vm.ipv4_address),
		       vm.created_at, vm.updated_at, vm.provisioned_at
		FROM %s.personal_vms vm
		JOIN %s.personal_workspaces workspace
		  ON workspace.id = vm.workspace_id
		 AND workspace.owner_id = $3
		WHERE vm.id = $1
		  AND vm.workspace_id = $2
	`, r.hypervisor, r.hierarchy)

	vm := &hypervisorEntity.PersonalVM{}
	if err := r.db.QueryRow(ctx, query, vmID, workspaceID, ownerUserID).Scan(
		&vm.ID,
		&vm.WorkspaceID,
		&vm.ZoneID,
		&vm.OwnerUserID,
		&vm.Name,
		&vm.Image,
		&vm.ImageID,
		&vm.ImageRevision,
		&vm.ImageSHA256,
		&vm.CPUCores,
		&vm.MemoryMB,
		&vm.DiskGB,
		&vm.SSHPublicKey,
		&vm.SpecHash,
		&vm.Status,
		&vm.OperationID,
		&vm.ProviderName,
		&vm.ProviderVMID,
		&vm.IPv4Address,
		&vm.CreatedAt,
		&vm.UpdatedAt,
		&vm.ProvisionedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, hypervisorTaxonomy.ErrNotFound
		}
		return nil, fmt.Errorf("hypervisor repository: get personal VM: %w", err)
	}
	return vm, nil
}
