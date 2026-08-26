package hypervisorRepoImpl

import (
	"context"
	"errors"
	"fmt"

	"controlplane/internal/config"
	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorRepoInterface "controlplane/internal/hypervisor/domain/repo"
	hypervisorTaxonomy "controlplane/internal/hypervisor/taxonomy"
	hypervisorproto "controlplane/internal/hypervisor/transport/proto"
	jobpayload "controlplane/internal/security"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
)

// PersonalVMRepoPostgres triển khai PersonalVMRepository cho Hypervisor Controlplane trên PostgreSQL.
// Chịu trách nhiệm thực thi các truy vấn CTE nguyên tử (CTE-First) kết hợp thẩm định quyền thương mại
// (Commercial Admission), gói tài nguyên (Resource Plan), và ghi Outbox record trong cùng một RTT.
type PersonalVMRepoPostgres struct {
	db         *pgxpool.Pool
	hypervisor string
	hierarchy  string
	protector  jobpayload.Protector
}

// NewPersonalVMRepo khởi tạo một thể hiện PersonalVMRepoPostgres mới.
func NewPersonalVMRepo(
	db *pgxpool.Pool,
	cfg *config.Config,
	protector jobpayload.Protector,
) hypervisorRepoInterface.PersonalVMRepository {
	return &PersonalVMRepoPostgres{
		db:         db,
		hypervisor: cfg.SchemaSQL.Hypervisor,
		hierarchy:  cfg.SchemaSQL.Hierarchy,
		protector:  protector,
	}
}

// GetAvailableImage tra cứu thông tin Image Artifact đang ở trạng thái AVAILABLE và đã có Proxmox template ID trong Zone.
func (r *PersonalVMRepoPostgres) GetAvailableImage(
	ctx context.Context,
	imageID uuid.UUID,
	zoneID uuid.UUID,
) (*hypervisorEntity.ImageArtifact, error) {
	query := fmt.Sprintf(`
		SELECT
			id,
			zone_id,
			name,
			code,
			distribution,
			release,
			revision,
			architecture,
			format,
			size_bytes,
			sha256,
			object_key,
			state,
			created_by,
			provider_template_vmid,
			error_code,
			error_message,
			created_at,
			updated_at,
			available_at
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

// CreateOrGet thực hiện tạo máy ảo cá nhân (Personal VM) hoặc lấy bản ghi hiện có nếu đã tồn tại (Idempotency).
// Toàn bộ quá trình kiểm tra thẩm định thương mại, gói tài nguyên, quyền workspace/zone,
// chèn bản ghi VM và ghi Outbox record được đóng gói trong một câu lệnh CTE nguyên tử duy nhất.
func (r *PersonalVMRepoPostgres) CreateOrGet(
	ctx context.Context,
	vm *hypervisorEntity.PersonalVM,
	outbox *hypervisorEntity.HypervisorOutboxRecord,
) (*hypervisorEntity.PersonalVMCreateResult, error) {
	// [COMMENT]: 1. Mã hóa niêm phong (Sealing) payload Outbox với Protector
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{
		ZoneID:               outbox.ZoneID,
		SourceDomain:         "HYPERVISOR",
		JobTopic:             outbox.JobTopic,
		ResourceID:           outbox.ResourceID,
		JobVersion:           uint32(outbox.JobVersion),
		PayloadSchemaVersion: uint32(outbox.PayloadSchemaVersion),
	}, outbox.Payload)
	if err != nil {
		return nil, err
	}
	outbox.Payload = protected.Payload
	outbox.PayloadKeyID = protected.KeyID

	// [COMMENT]: 2. Giao dịch CTE tạo VM nguyên tử:
	// - commercial_admission: Thẩm định quyền thương mại còn hiệu lực của User.
	// - resource_plan: Xác thực cấu hình gói tài nguyên (vCPU, RAM, Boot Disk, Hash, Active window).
	// - authorized_scope: Kiểm tra quyền sở hữu Workspace, trạng thái Zone active và Image AVAILABLE.
	// - inserted_vm: Chèn bản ghi VM mới nếu thỏa mãn toàn bộ điều kiện (ON CONFLICT DO NOTHING).
	// - inserted_outbox: Chỉ ghi Outbox record khi inserted_vm thực sự tạo dòng mới thành công.
	query := fmt.Sprintf(`
		WITH commercial_admission AS (
			SELECT 1
			FROM %s.commercial_admission_projection admission
			WHERE admission.owner_id = $4
			  AND admission.owner_type = 'PERSONAL'
			  AND admission.decision = 'ALLOW'
			  AND admission.effective_at <= NOW()
			  AND (admission.valid_until IS NULL OR admission.valid_until > NOW())
		),
		resource_plan AS (
			SELECT
				revision_id,
				plan_id,
				revision_number,
				cpu_cores,
				memory_mib,
				boot_disk_gib,
				content_sha256
			FROM %s.hypervisor_resource_plan_revisions
			WHERE plan_id                 = $10
			  AND revision_id             = $11
			  AND revision_number         = $12
			  AND content_sha256          = $13
			  AND cpu_cores               = $14
			  AND memory_mib              = $15
			  AND boot_disk_gib           = $16
			  AND state                   = 'ACTIVE'
			  AND allow_create            = TRUE
			  AND effective_from         <= NOW()
			  AND (effective_to IS NULL OR NOW() < effective_to)
		),
		authorized_scope AS (
			SELECT 1
			FROM commercial_admission
			CROSS JOIN resource_plan
			CROSS JOIN %s.personal_workspaces workspace
			JOIN %s.zones zone
			  ON zone.id = workspace.zone_id
			JOIN %s.image_artifacts image
			  ON image.id                   = $7
			 AND image.zone_id              = zone.id
			 AND image.revision             = $8
			 AND image.sha256               = $9
			 AND image.state                = 'AVAILABLE'
			 AND image.provider_template_vmid IS NOT NULL
			WHERE workspace.id       = $2
			  AND workspace.owner_id = $4
			  AND workspace.zone_id  = $3
			  AND zone.status        = 'active'
			  AND EXISTS (
			      SELECT 1
			      FROM %s.zone_services service
			      WHERE service.zone_id      = zone.id
			        AND service.service_type = 'hypervisor'
			        AND service.desired_state = TRUE
			  )
		),
		inserted_vm AS (
			INSERT INTO %s.personal_vms (
				id,
				workspace_id,
				zone_id,
				owner_user_id,
				name,
				image,
				image_id,
				image_revision,
				image_sha256,
				resource_plan_id,
				resource_plan_revision_id,
				resource_plan_revision_number,
				resource_plan_content_sha256,
				cpu_cores,
				memory_mb,
				boot_disk_gb,
				disk_gb,
				additional_disk_sizes_gb,
				ssh_public_key,
				spec_hash,
				status,
				operation_id,
				provider_name,
				created_at,
				updated_at
			)
			SELECT
				$1,  -- id
				$2,  -- workspace_id
				$3,  -- zone_id
				$4,  -- owner_user_id
				$5,  -- name
				$6,  -- image (string code)
				$7,  -- image_id
				$8,  -- image_revision
				$9,  -- image_sha256
				$10, -- resource_plan_id
				$11, -- resource_plan_revision_id
				$12, -- resource_plan_revision_number
				$13, -- resource_plan_content_sha256
				$14, -- cpu_cores
				$15, -- memory_mb
				$16, -- boot_disk_gb
				$17, -- disk_gb
				$18, -- additional_disk_sizes_gb
				$19, -- ssh_public_key
				$20, -- spec_hash
				$21, -- status
				$22, -- operation_id
				$23, -- provider_name
				$24, -- created_at
				$25  -- updated_at
			FROM authorized_scope
			ON CONFLICT (workspace_id, name) DO NOTHING
			RETURNING *
		),
		inserted_outbox AS (
			INSERT INTO %s.hypervisor_outbox_records (
				event_id,
				zone_id,
				job_topic,
				payload,
				actor_user_id,
				owner_id,
				owner_type,
				status,
				job_version,
				resource_id,
				payload_schema_version,
				trace_id,
				idle,
				payload_key_id,
				resource_name
			)
			SELECT
				$26, -- event_id
				$27, -- zone_id
				$28, -- job_topic
				$29, -- payload (sealed)
				$30, -- actor_user_id
				$31, -- owner_id
				$32, -- owner_type
				$33, -- status
				$34, -- job_version
				$35, -- resource_id
				$36, -- payload_schema_version
				$37, -- trace_id
				$38, -- idle
				$39, -- payload_key_id
				$40  -- resource_name
			FROM inserted_vm
			RETURNING event_id
		)
		SELECT
			id,
			workspace_id,
			zone_id,
			owner_user_id,
			name,
			image,
			image_id,
			image_revision,
			image_sha256,
			resource_plan_id,
			resource_plan_revision_id,
			resource_plan_revision_number,
			resource_plan_content_sha256,
			cpu_cores,
			memory_mb,
			boot_disk_gb,
			disk_gb,
			additional_disk_sizes_gb,
			ssh_public_key,
			spec_hash,
			status,
			operation_id,
			provider_name,
			provider_vmid,
			host(ipv4_address),
			created_at,
			updated_at,
			provisioned_at,
			TRUE AS created
		FROM inserted_vm
		JOIN inserted_outbox ON TRUE
	`, r.hypervisor, r.hypervisor, r.hierarchy, r.hierarchy, r.hypervisor, r.hierarchy, r.hypervisor, r.hypervisor)

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
		vm.ResourcePlanID,
		vm.ResourcePlanRevisionID,
		vm.ResourcePlanRevisionNumber,
		vm.ResourcePlanContentSHA256,
		vm.CPUCores,
		vm.MemoryMB,
		vm.BootDiskGB,
		vm.DiskGB,
		vm.AdditionalDiskSizesGB,
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
		outbox.OwnerID,
		outbox.OwnerType,
		outbox.Status,
		outbox.JobVersion,
		outbox.ResourceID,
		outbox.PayloadSchemaVersion,
		outbox.TraceID,
		outbox.IdleSeconds,
		outbox.PayloadKeyID,
		outbox.ResourceName,
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
		&current.ResourcePlanID,
		&current.ResourcePlanRevisionID,
		&current.ResourcePlanRevisionNumber,
		&current.ResourcePlanContentSHA256,
		&current.CPUCores,
		&current.MemoryMB,
		&current.BootDiskGB,
		&current.DiskGB,
		&current.AdditionalDiskSizesGB,
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
		// [COMMENT]: Xử lý trường hợp không tạo được bản ghi mới do xung đột tên hoặc vi phạm điều kiện
		if errors.Is(err, pgx.ErrNoRows) {
			// [COMMENT]: 3. Đọc bản ghi VM đã tồn tại từ trước (Concurrent Winner)
			// Tránh việc tạo trùng Outbox record nhưng vẫn thẩm định quyền thương mại tại cùng một ranh giới bền vững.
			existingQuery := fmt.Sprintf(`
				WITH commercial_admission AS (
					SELECT 1
					FROM %s.commercial_admission_projection admission
					WHERE admission.owner_id = $3
					  AND admission.owner_type = 'PERSONAL'
					  AND admission.decision = 'ALLOW'
					  AND admission.effective_at <= NOW()
					  AND (admission.valid_until IS NULL OR admission.valid_until > NOW())
				)
				SELECT
					current_vm.id,
					current_vm.workspace_id,
					current_vm.zone_id,
					current_vm.owner_user_id,
					current_vm.name,
					current_vm.image,
					current_vm.image_id,
					current_vm.image_revision,
					current_vm.image_sha256,
					current_vm.resource_plan_id,
					current_vm.resource_plan_revision_id,
					current_vm.resource_plan_revision_number,
					current_vm.resource_plan_content_sha256,
					current_vm.cpu_cores,
					current_vm.memory_mb,
					current_vm.boot_disk_gb,
					current_vm.disk_gb,
					current_vm.additional_disk_sizes_gb,
					current_vm.ssh_public_key,
					current_vm.spec_hash,
					current_vm.status,
					current_vm.operation_id,
					current_vm.provider_name,
					current_vm.provider_vmid,
					host(current_vm.ipv4_address),
					current_vm.created_at,
					current_vm.updated_at,
					current_vm.provisioned_at
				FROM commercial_admission
				CROSS JOIN %s.personal_vms current_vm
				JOIN %s.personal_workspaces workspace
				  ON workspace.id       = current_vm.workspace_id
				 AND workspace.owner_id = $3
				 AND workspace.zone_id  = $4
				JOIN %s.zones zone
				  ON zone.id     = workspace.zone_id
				 AND zone.status = 'active'
				WHERE current_vm.workspace_id = $1
				  AND current_vm.name         = $2
				  AND EXISTS (
				      SELECT 1
				      FROM %s.zone_services service
				      WHERE service.zone_id      = zone.id
				        AND service.service_type = 'hypervisor'
				        AND service.desired_state = TRUE
				  )
			`, r.hypervisor, r.hypervisor, r.hierarchy, r.hierarchy, r.hierarchy)

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
				&current.ResourcePlanID,
				&current.ResourcePlanRevisionID,
				&current.ResourcePlanRevisionNumber,
				&current.ResourcePlanContentSHA256,
				&current.CPUCores,
				&current.MemoryMB,
				&current.BootDiskGB,
				&current.DiskGB,
				&current.AdditionalDiskSizesGB,
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
				// [COMMENT]: 4. Phân loại nguyên nhân thất bại (Failure Classification)
				if errors.Is(existingErr, pgx.ErrNoRows) {
					// Kiểm tra quyền thương mại
					admissionQuery := fmt.Sprintf(`
						WITH commercial_admission AS (
							SELECT 1
							FROM %s.commercial_admission_projection admission
							WHERE admission.owner_id = $1
							  AND admission.owner_type = 'PERSONAL'
							  AND admission.decision = 'ALLOW'
							  AND admission.effective_at <= NOW()
							  AND (admission.valid_until IS NULL OR admission.valid_until > NOW())
						)
						SELECT EXISTS (SELECT 1 FROM commercial_admission)
					`, r.hypervisor)

					var commercialAdmissionAllowed bool
					if admissionErr := r.db.QueryRow(ctx, admissionQuery, vm.OwnerUserID).Scan(&commercialAdmissionAllowed); admissionErr != nil {
						return nil, fmt.Errorf("hypervisor repository: classify commercial admission: %w", admissionErr)
					}
					if !commercialAdmissionAllowed {
						return nil, hypervisorTaxonomy.ErrCommercialAdmissionDenied
					}

					// Kiểm tra tính khả dụng của gói tài nguyên
					resourcePlanQuery := fmt.Sprintf(`
						SELECT EXISTS (
							SELECT 1
							FROM %s.hypervisor_resource_plan_revisions
							WHERE plan_id         = $1
							  AND revision_id     = $2
							  AND revision_number = $3
							  AND content_sha256  = $4
							  AND cpu_cores       = $5
							  AND memory_mib      = $6
							  AND boot_disk_gib   = $7
							  AND effective_from <= NOW()
							  AND (effective_to IS NULL OR NOW() < effective_to)
						)`, r.hypervisor)

					var resourcePlanAllowed bool
					if resourcePlanErr := r.db.QueryRow(
						ctx,
						resourcePlanQuery,
						vm.ResourcePlanID,
						vm.ResourcePlanRevisionID,
						vm.ResourcePlanRevisionNumber,
						vm.ResourcePlanContentSHA256,
						vm.CPUCores,
						vm.MemoryMB,
						vm.BootDiskGB,
					).Scan(&resourcePlanAllowed); resourcePlanErr != nil {
						return nil, fmt.Errorf("hypervisor repository: classify resource plan: %w", resourcePlanErr)
					}
					if !resourcePlanAllowed {
						return nil, hypervisorTaxonomy.ErrResourcePlanUnavailable
					}

					return nil, hypervisorTaxonomy.ErrScopeUnavailable
				}
				return nil, fmt.Errorf("hypervisor repository: read concurrent personal VM winner: %w", existingErr)
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

// List truy vấn danh sách máy ảo cá nhân trong một Workspace và Zone thuộc quyền sở hữu của User.
func (r *PersonalVMRepoPostgres) List(
	ctx context.Context,
	workspaceID uuid.UUID,
	zoneID uuid.UUID,
	ownerUserID uuid.UUID,
	limit int32,
) ([]*hypervisorEntity.PersonalVM, error) {
	query := fmt.Sprintf(`
		SELECT
			vm.id,
			vm.workspace_id,
			vm.zone_id,
			vm.owner_user_id,
			vm.name,
			vm.image,
			vm.image_id,
			vm.image_revision,
			vm.image_sha256,
			vm.resource_plan_id,
			vm.resource_plan_revision_id,
			vm.resource_plan_revision_number,
			vm.resource_plan_content_sha256,
			vm.cpu_cores,
			vm.memory_mb,
			vm.boot_disk_gb,
			vm.disk_gb,
			vm.additional_disk_sizes_gb,
			vm.ssh_public_key,
			vm.spec_hash,
			vm.status,
			vm.operation_id,
			vm.provider_name,
			vm.provider_vmid,
			host(vm.ipv4_address),
			vm.created_at,
			vm.updated_at,
			vm.provisioned_at
		FROM %s.personal_vms vm
		JOIN %s.personal_workspaces workspace
		  ON workspace.id       = vm.workspace_id
		 AND workspace.owner_id = $3
		 AND workspace.zone_id  = $2
		WHERE vm.workspace_id  = $1
		  AND vm.zone_id       = $2
		  AND vm.owner_user_id = $3
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
			&vm.ResourcePlanID,
			&vm.ResourcePlanRevisionID,
			&vm.ResourcePlanRevisionNumber,
			&vm.ResourcePlanContentSHA256,
			&vm.CPUCores,
			&vm.MemoryMB,
			&vm.BootDiskGB,
			&vm.DiskGB,
			&vm.AdditionalDiskSizesGB,
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

// Get truy vấn thông tin chi tiết của một máy ảo cá nhân theo VMID, WorkspaceID, ZoneID và OwnerUserID.
func (r *PersonalVMRepoPostgres) Get(
	ctx context.Context,
	vmID uuid.UUID,
	workspaceID uuid.UUID,
	zoneID uuid.UUID,
	ownerUserID uuid.UUID,
) (*hypervisorEntity.PersonalVM, error) {
	query := fmt.Sprintf(`
		SELECT
			vm.id,
			vm.workspace_id,
			vm.zone_id,
			vm.owner_user_id,
			vm.name,
			vm.image,
			vm.image_id,
			vm.image_revision,
			vm.image_sha256,
			vm.resource_plan_id,
			vm.resource_plan_revision_id,
			vm.resource_plan_revision_number,
			vm.resource_plan_content_sha256,
			vm.cpu_cores,
			vm.memory_mb,
			vm.boot_disk_gb,
			vm.disk_gb,
			vm.additional_disk_sizes_gb,
			vm.ssh_public_key,
			vm.spec_hash,
			vm.status,
			vm.operation_id,
			vm.provider_name,
			vm.provider_vmid,
			host(vm.ipv4_address),
			vm.created_at,
			vm.updated_at,
			vm.provisioned_at
		FROM %s.personal_vms vm
		JOIN %s.personal_workspaces workspace
		  ON workspace.id       = vm.workspace_id
		 AND workspace.owner_id = $4
		 AND workspace.zone_id  = $3
		WHERE vm.id            = $1
		  AND vm.workspace_id  = $2
		  AND vm.zone_id       = $3
		  AND vm.owner_user_id = $4
	`, r.hypervisor, r.hierarchy)

	vm := &hypervisorEntity.PersonalVM{}
	if err := r.db.QueryRow(ctx, query, vmID, workspaceID, zoneID, ownerUserID).Scan(
		&vm.ID,
		&vm.WorkspaceID,
		&vm.ZoneID,
		&vm.OwnerUserID,
		&vm.Name,
		&vm.Image,
		&vm.ImageID,
		&vm.ImageRevision,
		&vm.ImageSHA256,
		&vm.ResourcePlanID,
		&vm.ResourcePlanRevisionID,
		&vm.ResourcePlanRevisionNumber,
		&vm.ResourcePlanContentSHA256,
		&vm.CPUCores,
		&vm.MemoryMB,
		&vm.BootDiskGB,
		&vm.DiskGB,
		&vm.AdditionalDiskSizesGB,
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

// BeginDelete bắt đầu quy trình xóa máy ảo cá nhân:
// 1. Khóa và đọc trạng thái VM (FOR UPDATE).
// 2. Kiểm tra tính hợp lệ của trạng thái xóa (READY và có provider_vmid).
// 3. Niêm phong payload xóa và cập nhật state = DELETING kèm ghi Outbox record nguyên tử qua CTE.
func (r *PersonalVMRepoPostgres) BeginDelete(
	ctx context.Context,
	vmID uuid.UUID,
	workspaceID uuid.UUID,
	zoneID uuid.UUID,
	ownerUserID uuid.UUID,
	traceID []byte,
) (*hypervisorEntity.PersonalVMDeleteResult, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("hypervisor repository: begin delete transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// [COMMENT]: 1. Khóa dòng và đọc trạng thái VM trong cùng 1 câu lệnh (FOR UPDATE)
	lockQuery := fmt.Sprintf(`
		SELECT
			vm.name,
			vm.provider_name,
			COALESCE(vm.provider_vmid, 0),
			vm.status,
			vm.operation_id
		FROM %s.personal_vms vm
		JOIN %s.personal_workspaces workspace
		  ON workspace.id       = vm.workspace_id
		 AND workspace.owner_id = $3
		 AND workspace.zone_id  = $4
		WHERE vm.id            = $1
		  AND vm.workspace_id  = $2
		  AND vm.zone_id       = $4
		  AND vm.owner_user_id = $3
		FOR UPDATE OF vm
	`, r.hypervisor, r.hierarchy)

	var (
		name         string
		providerName string
		providerVMID int64
		status       hypervisorEntity.VMStatus
		operationID  uuid.UUID
	)
	err = tx.QueryRow(ctx, lockQuery, vmID, workspaceID, ownerUserID, zoneID).Scan(
		&name,
		&providerName,
		&providerVMID,
		&status,
		&operationID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, hypervisorTaxonomy.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("hypervisor repository: lock VM for delete: %w", err)
	}

	// [COMMENT]: 2. Kiểm tra tính lũy quyền (Idempotency): Nếu VM đã ở trạng thái DELETING, trả về kết quả đang chạy
	if status == hypervisorEntity.VMStatusDeleting {
		return &hypervisorEntity.PersonalVMDeleteResult{
			VMID:        vmID,
			OperationID: operationID,
			Status:      status,
		}, nil
	}

	// [COMMENT]: 3. Kiểm tra tiền điều kiện: Chỉ cho phép xóa khi VM ở trạng thái READY và đã được cấp phát VMID hạ tầng
	if status != hypervisorEntity.VMStatusReady || providerVMID <= 0 {
		return nil, hypervisorTaxonomy.ErrVMStateConflict
	}

	// [COMMENT]: 4. Sinh OperationID mới và đóng gói protobuf payload
	newOperationID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	payloadBytes, err := proto.Marshal(&hypervisorproto.VmDeleteV1{
		SchemaVersion: 1,
		VmId:          vmID[:],
		ProviderName:  providerName,
		ProviderVmid:  uint64(providerVMID),
	})
	if err != nil {
		return nil, err
	}

	// [COMMENT]: 5. Niêm phong (Seal) payload với Protector
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{
		ZoneID:               zoneID,
		SourceDomain:         "HYPERVISOR",
		JobTopic:             "hypervisor.vm.delete",
		ResourceID:           vmID.String(),
		JobVersion:           1,
		PayloadSchemaVersion: 1,
	}, payloadBytes)
	if err != nil {
		return nil, err
	}

	// [COMMENT]: 6. CTE nguyên tử: Ghi Outbox record và cập nhật status sang DELETING
	mutationQuery := fmt.Sprintf(`
		WITH inserted_outbox AS (
			INSERT INTO %s.hypervisor_outbox_records (
				event_id,
				zone_id,
				job_topic,
				payload,
				payload_key_id,
				actor_user_id,
				owner_id,
				owner_type,
				status,
				job_version,
				resource_id,
				resource_name,
				payload_schema_version,
				trace_id,
				idle
			)
			VALUES (
				$1,                     -- event_id / operation_id
				$2,                     -- zone_id
				'hypervisor.vm.delete', -- job_topic
				$3,                     -- payload (sealed)
				$4,                     -- payload_key_id
				$5,                     -- actor_user_id (owner_user_id)
				$5,                     -- owner_id
				'PERSONAL',             -- owner_type
				'PENDING',              -- status
				1,                      -- job_version
				$6,                     -- resource_id (vm_id string)
				$7,                     -- resource_name
				1,                      -- payload_schema_version
				$8,                     -- trace_id
				600                     -- idle seconds
			)
			RETURNING event_id
		)
		UPDATE %s.personal_vms
		SET status       = 'DELETING',
		    operation_id = $1,
		    updated_at   = NOW()
		WHERE id = $9
		RETURNING
			id,
			operation_id,
			status
	`, r.hypervisor, r.hypervisor)

	result := &hypervisorEntity.PersonalVMDeleteResult{}
	err = tx.QueryRow(
		ctx,
		mutationQuery,
		newOperationID,
		zoneID,
		protected.Payload,
		protected.KeyID,
		ownerUserID,
		vmID.String(),
		name,
		traceID,
		vmID,
	).Scan(
		&result.VMID,
		&result.OperationID,
		&result.Status,
	)
	if err != nil {
		return nil, fmt.Errorf("hypervisor repository: apply delete mutation: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("hypervisor repository: commit delete transaction: %w", err)
	}

	return result, nil
}
