package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"controlplane/internal/managedservice/domain/entity"
	managedrepo "controlplane/internal/managedservice/domain/repo"
	"controlplane/internal/managedservice/taxonomy"
	managedserviceproto "controlplane/internal/managedservice/transport/proto"
	jobpayload "controlplane/internal/security"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
)

type tenantInstanceRepository struct {
	db              *pgxpool.Pool
	managedSchema   string
	hierarchySchema string
	protector       jobpayload.Protector
}

func NewTenantInstanceRepository(db *pgxpool.Pool, managedSchema, hierarchySchema string, protector jobpayload.Protector) managedrepo.TenantInstanceRepository {
	return &tenantInstanceRepository{db: db, managedSchema: managedSchema, hierarchySchema: hierarchySchema, protector: protector}
}

func (r *tenantInstanceRepository) CreateTenantInstance(ctx context.Context, in *entity.CreateTenantInstance) (*entity.CreateTenantInstanceResult, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text || ':' || $2::text, 0))`, in.WorkspaceID.String(), in.Code); err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	var workspaceExists bool
	workspaceQuery := fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM %s.tenant_workspaces WHERE id=$1 AND tenant_id=$2 AND zone_id=$3)`, r.hierarchySchema)
	if err := tx.QueryRow(ctx, workspaceQuery, in.WorkspaceID, in.TenantID, in.ZoneID).Scan(&workspaceExists); err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	if !workspaceExists {
		return nil, taxonomy.ErrNotFound
	}

	var existingID uuid.UUID
	var existingIntent []byte
	var existingName, existingState string
	var existingGeneration, existingSequence int64
	var existingPending *uuid.UUID
	instanceQuery := fmt.Sprintf(`SELECT id,name,state::text,generation,revision_sequence,pending_revision_id,create_intent_sha256 FROM %s.tenant_managed_service_instances WHERE workspace_id=$1 AND tenant_id=$2 AND zone_id=$3 AND code=$4 FOR UPDATE`, r.managedSchema)
	existingErr := tx.QueryRow(ctx, instanceQuery, in.WorkspaceID, in.TenantID, in.ZoneID, in.Code).Scan(&existingID, &existingName, &existingState, &existingGeneration, &existingSequence, &existingPending, &existingIntent)
	if existingErr == nil {
		if !bytes.Equal(existingIntent, in.CreateIntentSHA256) {
			return nil, taxonomy.ErrConflict
		}
		var operationID uuid.UUID
		var kind, state string
		var epoch int64
		opQuery := fmt.Sprintf(`SELECT id,kind::text,state::text,delivery_epoch FROM %s.tenant_managed_service_operations WHERE instance_id=$1 AND kind='create' ORDER BY created_at ASC,id ASC LIMIT 1`, r.managedSchema)
		if err := tx.QueryRow(ctx, opQuery, existingID).Scan(&operationID, &kind, &state, &epoch); err != nil {
			return nil, taxonomy.ErrUnavailable
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, taxonomy.ErrUnavailable
		}
		return &entity.CreateTenantInstanceResult{ID: existingID, Code: in.Code, Name: existingName, State: existingState, Generation: existingGeneration, RevisionSequence: existingSequence, PendingRevisionID: existingPending, OperationID: operationID, OperationKind: kind, OperationState: state, DeliveryEpoch: epoch, Deduplicated: true}, nil
	}
	if !errors.Is(existingErr, pgx.ErrNoRows) {
		return nil, taxonomy.ErrUnavailable
	}

	var templateYAML string
	var bundleHash, componentContractHash, inputSchemaHash []byte
	var componentContract []byte
	revisionQuery := fmt.Sprintf(`SELECT revision.template_yaml,revision.template_bundle_sha256,revision.component_contract,revision.component_contract_sha256,revision.input_schema_sha256 FROM %s.blueprint_revisions revision JOIN %s.service_blueprints blueprint ON blueprint.published_revision_id=revision.id AND blueprint.id=revision.blueprint_id WHERE revision.id=$1 AND revision.state='published'`, r.managedSchema, r.managedSchema)
	if err := tx.QueryRow(ctx, revisionQuery, in.BlueprintRevisionID).Scan(&templateYAML, &bundleHash, &componentContract, &componentContractHash, &inputSchemaHash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, taxonomy.ErrPreconditionFailed
		}
		return nil, taxonomy.ErrUnavailable
	}
	if !bytes.Equal(inputSchemaHash, in.InputSchemaSHA256) {
		return nil, taxonomy.ErrConflict
	}
	var componentRows []struct {
		ID              string   `json:"id"`
		ComponentID     string   `json:"component_id"`
		DocumentIndexes []uint32 `json:"document_indexes"`
		ApplyOrder      uint32   `json:"apply_order"`
		DeleteOrder     uint32   `json:"delete_order"`
		Readiness       struct {
			Type            string `json:"type"`
			DeadlineSeconds uint32 `json:"deadline_seconds"`
		} `json:"readiness"`
	}
	if err := json.Unmarshal(componentContract, &componentRows); err != nil {
		return nil, taxonomy.ErrPreconditionFailed
	}
	components := make([]*managedserviceproto.ManagedServiceComponentV1, 0, len(componentRows))
	for _, row := range componentRows {
		componentID := strings.TrimSpace(row.ID)
		if componentID == "" {
			componentID = strings.TrimSpace(row.ComponentID)
		}
		components = append(components, &managedserviceproto.ManagedServiceComponentV1{ComponentId: componentID, DocumentIndexes: row.DocumentIndexes, ApplyOrder: row.ApplyOrder, DeleteOrder: row.DeleteOrder, ReadinessRule: row.Readiness.Type, ReadinessDeadlineSeconds: row.Readiness.DeadlineSeconds})
	}
	desiredHash := in.DesiredSpecSHA256
	commandBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(&managedserviceproto.ManagedServiceCommandV1{CommandEventId: in.CommandEventID[:], OperationId: in.OperationID[:], InstanceId: in.InstanceID[:], OwnerType: managedserviceproto.ManagedServiceOwnerTypeV1_MANAGED_SERVICE_OWNER_TYPE_TENANT, OwnerId: in.TenantID[:], WorkspaceId: in.WorkspaceID[:], ZoneId: in.ZoneID[:], InstanceCode: in.Code, OperationKind: managedserviceproto.ManagedServiceOperationKindV1_MANAGED_SERVICE_OPERATION_KIND_CREATE, Generation: 1, InstanceRevisionId: in.InstanceRevisionID[:], BlueprintRevisionId: in.BlueprintRevisionID[:], TemplateYaml: templateYAML, Components: components, BundleHash: bundleHash, ComponentContractHash: componentContractHash, InputHash: in.InputSHA256, DesiredSpecHash: desiredHash, ParameterValues: in.Parameters, ParameterValuesSha256: in.InputSHA256, SchemaVersion: 1, IssuedAtUnixMs: in.IssuedAt.UnixMilli(), Traceparent: in.Traceparent, Tracestate: in.Tracestate})
	if err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: in.ZoneID, SourceDomain: "MANAGED_SERVICE", JobTopic: "managed_service.instance.execute", ResourceID: in.InstanceID.String(), JobVersion: 1, PayloadSchemaVersion: 1}, commandBytes)
	if err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	payloadHash := sha256.Sum256(protected.Payload)
	var result entity.CreateTenantInstanceResult
	query := fmt.Sprintf(`WITH inserted_instance AS (INSERT INTO %s.tenant_managed_service_instances(id,workspace_id,tenant_id,zone_id,code,name,state,generation,revision_sequence,create_intent_sha256,pending_revision_id) VALUES($1,$2,$3,$4,$5,$6,'provisioning',1,1,$7,$8) RETURNING id,code,name,state::text,generation,revision_sequence,pending_revision_id), inserted_revision AS (INSERT INTO %s.tenant_managed_service_instance_revisions(id,instance_id,revision,blueprint_revision_id,zone_id,template_bundle_sha256,component_contract_sha256,input_schema_sha256,protected_command_payload,protected_command_payload_sha256,payload_key_id,input_sha256,desired_spec_sha256,created_by) SELECT $8,(SELECT id FROM inserted_instance),1,$9,$4,$10,$11,$12,$13,$14,$15,$16,$17,$21 RETURNING id,instance_id), linked_instance AS (UPDATE %s.tenant_managed_service_instances instance SET pending_revision_id=(SELECT id FROM inserted_revision),updated_at=now() WHERE instance.id=(SELECT id FROM inserted_instance) RETURNING instance.id), inserted_operation AS (INSERT INTO %s.tenant_managed_service_operations(id,instance_id,target_revision_id,blueprint_revision_id,zone_id,kind,state,generation,attempt,delivery_epoch,current_command_event_id,status_version,template_bundle_sha256,component_contract_sha256,input_sha256,desired_spec_sha256,actor_user_id,retained_until) SELECT $18,(SELECT id FROM inserted_instance),(SELECT id FROM inserted_revision),$9,$4,'create','accepted',1,0,0,$19,1,$10,$11,$16,$17,$21,now()+interval '30 days' RETURNING id,kind::text,state::text,delivery_epoch), inserted_outbox AS (INSERT INTO %s.managed_service_outbox_records(event_id,zone_id,job_topic,payload,payload_key_id,owner_id,owner_type,actor_user_id,status,available_at,job_version,resource_id,payload_schema_version,trace_id,delivery_epoch) VALUES($19,$4,'managed_service.instance.execute',$13,$15,$3,'TENANT',$21,'PENDING',now(),1,$1::text,1,$20,0) RETURNING event_id) SELECT instance.id,instance.code,instance.name,instance.state,instance.generation,instance.revision_sequence,instance.pending_revision_id,operation.id,operation.kind,operation.state,operation.delivery_epoch FROM inserted_instance instance CROSS JOIN inserted_operation operation`, r.managedSchema, r.managedSchema, r.managedSchema, r.managedSchema, r.managedSchema)
	err = tx.QueryRow(ctx, query, in.InstanceID, in.WorkspaceID, in.TenantID, in.ZoneID, in.Code, in.Name, in.CreateIntentSHA256, in.InstanceRevisionID, in.BlueprintRevisionID, bundleHash, componentContractHash, inputSchemaHash, protected.Payload, payloadHash[:], protected.KeyID, in.InputSHA256, desiredHash, in.OperationID, in.CommandEventID, in.TraceID, in.ActorUserID).Scan(&result.ID, &result.Code, &result.Name, &result.State, &result.Generation, &result.RevisionSequence, &result.PendingRevisionID, &result.OperationID, &result.OperationKind, &result.OperationState, &result.DeliveryEpoch)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" {
			return nil, taxonomy.ErrConflict
		}
		return nil, taxonomy.ErrUnavailable
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	return &result, nil
}

func (r *tenantInstanceRepository) ResizeTenantInstance(ctx context.Context, in *entity.ResizeTenantInstance) (*entity.ResizeTenantInstanceResult, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	var instanceID, revisionID, blueprintRevisionID uuid.UUID
	var generation int64
	var state string
	q := fmt.Sprintf(`SELECT instance.id,instance.generation,instance.state::text,instance.active_revision_id FROM %s.tenant_managed_service_instances instance JOIN %s.tenant_workspaces workspace ON workspace.id=instance.workspace_id WHERE instance.workspace_id=$1 AND instance.tenant_id=$2 AND instance.zone_id=$3 AND instance.code=$4 AND workspace.tenant_id=$2 AND workspace.zone_id=$3 FOR UPDATE OF instance`, r.managedSchema, r.hierarchySchema)
	if err := tx.QueryRow(ctx, q, in.WorkspaceID, in.TenantID, in.ZoneID, in.Code).Scan(&instanceID, &generation, &state, &revisionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, taxonomy.ErrNotFound
		}
		return nil, taxonomy.ErrUnavailable
	}
	if state != "active" || generation != in.ExpectedGeneration {
		return nil, taxonomy.ErrConflict
	}
	var active bool
	if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM %s.tenant_managed_service_operations WHERE instance_id=$1 AND state IN ('accepted','dispatching','running','retrying'))`, r.managedSchema), instanceID).Scan(&active); err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	if active || revisionID == uuid.Nil {
		return nil, taxonomy.ErrConflict
	}
	var bundleHash, componentHash, schemaHash []byte
	var templateYAML string
	var componentContract []byte
	if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT revision.blueprint_revision_id,revision.template_bundle_sha256,revision.component_contract_sha256,revision.input_schema_sha256,blueprint_revision.template_yaml,blueprint_revision.component_contract FROM %s.tenant_managed_service_instance_revisions revision JOIN %s.blueprint_revisions blueprint_revision ON blueprint_revision.id=revision.blueprint_revision_id WHERE revision.id=$1`, r.managedSchema, r.managedSchema), revisionID).Scan(&blueprintRevisionID, &bundleHash, &componentHash, &schemaHash, &templateYAML, &componentContract); err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	var componentRows []struct {
		ID              string   `json:"id"`
		ComponentID     string   `json:"component_id"`
		DocumentIndexes []uint32 `json:"document_indexes"`
		ApplyOrder      uint32   `json:"apply_order"`
		DeleteOrder     uint32   `json:"delete_order"`
		Readiness       struct {
			Type            string `json:"type"`
			DeadlineSeconds uint32 `json:"deadline_seconds"`
		} `json:"readiness"`
	}
	if err := json.Unmarshal(componentContract, &componentRows); err != nil {
		return nil, taxonomy.ErrPreconditionFailed
	}
	components := make([]*managedserviceproto.ManagedServiceComponentV1, 0, len(componentRows))
	for _, row := range componentRows {
		componentID := strings.TrimSpace(row.ID)
		if componentID == "" {
			componentID = strings.TrimSpace(row.ComponentID)
		}
		components = append(components, &managedserviceproto.ManagedServiceComponentV1{ComponentId: componentID, DocumentIndexes: row.DocumentIndexes, ApplyOrder: row.ApplyOrder, DeleteOrder: row.DeleteOrder, ReadinessRule: row.Readiness.Type, ReadinessDeadlineSeconds: row.Readiness.DeadlineSeconds})
	}
	nextGeneration := generation + 1
	commandBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(&managedserviceproto.ManagedServiceCommandV1{CommandEventId: in.CommandEventID[:], OperationId: in.OperationID[:], InstanceId: instanceID[:], OwnerType: managedserviceproto.ManagedServiceOwnerTypeV1_MANAGED_SERVICE_OWNER_TYPE_TENANT, OwnerId: in.TenantID[:], WorkspaceId: in.WorkspaceID[:], ZoneId: in.ZoneID[:], InstanceCode: in.Code, OperationKind: managedserviceproto.ManagedServiceOperationKindV1_MANAGED_SERVICE_OPERATION_KIND_RESIZE, Generation: uint64(nextGeneration), InstanceRevisionId: in.InstanceRevisionID[:], BlueprintRevisionId: blueprintRevisionID[:], TemplateYaml: templateYAML, Components: components, BundleHash: bundleHash, ComponentContractHash: componentHash, InputHash: in.InputSHA256, DesiredSpecHash: in.DesiredSpecSHA256, ParameterValues: in.Parameters, ParameterValuesSha256: in.InputSHA256, SchemaVersion: 1, IssuedAtUnixMs: in.IssuedAt.UnixMilli(), Traceparent: in.Traceparent, Tracestate: in.Tracestate})
	if err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: in.ZoneID, SourceDomain: "MANAGED_SERVICE", JobTopic: "managed_service.instance.execute", ResourceID: instanceID.String(), JobVersion: 1, PayloadSchemaVersion: 1}, commandBytes)
	if err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	payloadHash := sha256.Sum256(protected.Payload)
	var result entity.ResizeTenantInstanceResult
	query := fmt.Sprintf(`WITH inserted_revision AS (INSERT INTO %s.tenant_managed_service_instance_revisions(id,instance_id,revision,blueprint_revision_id,zone_id,template_bundle_sha256,component_contract_sha256,input_schema_sha256,protected_command_payload,protected_command_payload_sha256,payload_key_id,input_sha256,desired_spec_sha256,created_by) SELECT $1,$2,instance.revision_sequence+1,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13 FROM %s.tenant_managed_service_instances instance WHERE instance.id=$2 RETURNING id),updated_instance AS (UPDATE %s.tenant_managed_service_instances SET state='updating',generation=$14,revision_sequence=revision_sequence+1,pending_revision_id=(SELECT id FROM inserted_revision),updated_at=now() WHERE id=$2 AND state='active' RETURNING id,code,generation,pending_revision_id),inserted_operation AS (INSERT INTO %s.tenant_managed_service_operations(id,instance_id,target_revision_id,blueprint_revision_id,zone_id,kind,state,generation,attempt,delivery_epoch,current_command_event_id,status_version,template_bundle_sha256,component_contract_sha256,input_sha256,desired_spec_sha256,actor_user_id,retained_until) SELECT $15,$2,(SELECT id FROM inserted_revision),$3,$4,'resize','accepted',$14,0,0,$16,1,$5,$6,$11,$12,$18,now()+interval '30 days' RETURNING id,kind::text,state::text,delivery_epoch),inserted_outbox AS (INSERT INTO %s.managed_service_outbox_records(event_id,zone_id,job_topic,payload,payload_key_id,owner_id,owner_type,actor_user_id,status,available_at,job_version,resource_id,payload_schema_version,trace_id,delivery_epoch) VALUES($16,$4,'managed_service.instance.execute',$8,$10,$17,'TENANT',$18,'PENDING',now(),1,$2::text,1,$19,0) RETURNING event_id) SELECT updated_instance.id,updated_instance.code,updated_instance.generation,updated_instance.pending_revision_id,inserted_operation.id,inserted_operation.kind,inserted_operation.state,inserted_operation.delivery_epoch FROM updated_instance CROSS JOIN inserted_operation`, r.managedSchema, r.managedSchema, r.managedSchema, r.managedSchema, r.managedSchema)
	if err := tx.QueryRow(ctx, query, in.InstanceRevisionID, instanceID, blueprintRevisionID, in.ZoneID, bundleHash, componentHash, schemaHash, protected.Payload, payloadHash[:], protected.KeyID, in.InputSHA256, in.DesiredSpecSHA256, in.ActorUserID, nextGeneration, in.OperationID, in.CommandEventID, in.TenantID, in.ActorUserID, in.TraceID).Scan(&result.ID, &result.Code, &result.Generation, &result.PendingRevisionID, &result.OperationID, &result.OperationKind, &result.OperationState, &result.DeliveryEpoch); err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	return &result, nil
}

func (r *tenantInstanceRepository) DeleteTenantInstance(ctx context.Context, in *entity.DeleteTenantInstance) (*entity.DeleteTenantInstanceResult, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	var instanceID, revisionID, blueprintRevisionID uuid.UUID
	var generation int64
	var state string
	q := fmt.Sprintf(`SELECT instance.id,instance.generation,instance.state::text,COALESCE(instance.pending_revision_id,instance.active_revision_id) FROM %s.tenant_managed_service_instances instance JOIN %s.tenant_workspaces workspace ON workspace.id=instance.workspace_id WHERE instance.workspace_id=$1 AND instance.tenant_id=$2 AND instance.zone_id=$3 AND instance.code=$4 AND workspace.tenant_id=$2 AND workspace.zone_id=$3 FOR UPDATE OF instance`, r.managedSchema, r.hierarchySchema)
	if err := tx.QueryRow(ctx, q, in.WorkspaceID, in.TenantID, in.ZoneID, in.Code).Scan(&instanceID, &generation, &state, &revisionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, taxonomy.ErrNotFound
		}
		return nil, taxonomy.ErrUnavailable
	}
	if state == "deleting" {
		// A transport retry may still carry the pre-delete generation because the
		// first response was lost after commit. Both observed generations converge
		// on the one durable DELETE operation; no second command is created.
		if in.ExpectedGeneration != generation && in.ExpectedGeneration != generation-1 {
			return nil, taxonomy.ErrConflict
		}
		var opID uuid.UUID
		var kind, opState string
		var epoch int64
		if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT id,kind::text,state::text,delivery_epoch FROM %s.tenant_managed_service_operations WHERE instance_id=$1 AND kind='delete' ORDER BY created_at DESC,id DESC LIMIT 1`, r.managedSchema), instanceID).Scan(&opID, &kind, &opState, &epoch); err != nil {
			return nil, taxonomy.ErrUnavailable
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, taxonomy.ErrUnavailable
		}
		return &entity.DeleteTenantInstanceResult{ID: instanceID, Code: in.Code, Generation: generation, OperationID: opID, OperationKind: kind, OperationState: opState, DeliveryEpoch: epoch, AlreadyDeleting: true}, nil
	}
	if generation != in.ExpectedGeneration {
		return nil, taxonomy.ErrConflict
	}
	var active bool
	if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM %s.tenant_managed_service_operations WHERE instance_id=$1 AND state IN ('accepted','dispatching','running','retrying'))`, r.managedSchema), instanceID).Scan(&active); err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	if active || revisionID == uuid.Nil {
		return nil, taxonomy.ErrConflict
	}
	var bundleHash, componentHash, inputHash, desiredHash []byte
	var templateYAML string
	var componentContract []byte
	if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT revision.blueprint_revision_id,revision.template_bundle_sha256,revision.component_contract_sha256,revision.input_sha256,revision.desired_spec_sha256,blueprint_revision.template_yaml,blueprint_revision.component_contract FROM %s.tenant_managed_service_instance_revisions revision JOIN %s.blueprint_revisions blueprint_revision ON blueprint_revision.id=revision.blueprint_revision_id WHERE revision.id=$1`, r.managedSchema, r.managedSchema), revisionID).Scan(&blueprintRevisionID, &bundleHash, &componentHash, &inputHash, &desiredHash, &templateYAML, &componentContract); err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	var componentRows []struct {
		ID              string   `json:"id"`
		ComponentID     string   `json:"component_id"`
		DocumentIndexes []uint32 `json:"document_indexes"`
		ApplyOrder      uint32   `json:"apply_order"`
		DeleteOrder     uint32   `json:"delete_order"`
		Readiness       struct {
			Type            string `json:"type"`
			DeadlineSeconds uint32 `json:"deadline_seconds"`
		} `json:"readiness"`
	}
	if err := json.Unmarshal(componentContract, &componentRows); err != nil {
		return nil, taxonomy.ErrPreconditionFailed
	}
	components := make([]*managedserviceproto.ManagedServiceComponentV1, 0, len(componentRows))
	for _, row := range componentRows {
		componentID := strings.TrimSpace(row.ID)
		if componentID == "" {
			componentID = strings.TrimSpace(row.ComponentID)
		}
		components = append(components, &managedserviceproto.ManagedServiceComponentV1{ComponentId: componentID, DocumentIndexes: row.DocumentIndexes, ApplyOrder: row.ApplyOrder, DeleteOrder: row.DeleteOrder, ReadinessRule: row.Readiness.Type, ReadinessDeadlineSeconds: row.Readiness.DeadlineSeconds})
	}
	nextGeneration := generation + 1
	commandBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(&managedserviceproto.ManagedServiceCommandV1{CommandEventId: in.CommandEventID[:], OperationId: in.OperationID[:], InstanceId: instanceID[:], OwnerType: managedserviceproto.ManagedServiceOwnerTypeV1_MANAGED_SERVICE_OWNER_TYPE_TENANT, OwnerId: in.TenantID[:], WorkspaceId: in.WorkspaceID[:], ZoneId: in.ZoneID[:], InstanceCode: in.Code, OperationKind: managedserviceproto.ManagedServiceOperationKindV1_MANAGED_SERVICE_OPERATION_KIND_DELETE, Generation: uint64(nextGeneration), InstanceRevisionId: revisionID[:], BlueprintRevisionId: blueprintRevisionID[:], TemplateYaml: templateYAML, Components: components, BundleHash: bundleHash, ComponentContractHash: componentHash, InputHash: inputHash, DesiredSpecHash: desiredHash, ParameterValuesSha256: inputHash, SchemaVersion: 1, IssuedAtUnixMs: in.IssuedAt.UnixMilli(), Traceparent: in.Traceparent, Tracestate: in.Tracestate})
	if err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: in.ZoneID, SourceDomain: "MANAGED_SERVICE", JobTopic: "managed_service.instance.execute", ResourceID: instanceID.String(), JobVersion: 1, PayloadSchemaVersion: 1}, commandBytes)
	if err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	var result entity.DeleteTenantInstanceResult
	query := fmt.Sprintf(`WITH updated_instance AS (UPDATE %s.tenant_managed_service_instances SET state='deleting',generation=$2,updated_at=now() WHERE id=$1 RETURNING id,code,generation),inserted_operation AS (INSERT INTO %s.tenant_managed_service_operations(id,instance_id,target_revision_id,blueprint_revision_id,zone_id,kind,state,generation,attempt,delivery_epoch,current_command_event_id,status_version,template_bundle_sha256,component_contract_sha256,input_sha256,desired_spec_sha256,actor_user_id,retained_until) VALUES($3,$1,$4,$5,$6,'delete','accepted',$2,0,0,$7,1,$8,$9,$10,$11,$12,now()+interval '30 days') RETURNING id,kind::text,state::text,delivery_epoch),inserted_outbox AS (INSERT INTO %s.managed_service_outbox_records(event_id,zone_id,job_topic,payload,payload_key_id,owner_id,owner_type,actor_user_id,status,available_at,job_version,resource_id,payload_schema_version,trace_id,delivery_epoch) VALUES($7,$6,'managed_service.instance.execute',$13,$14,$16,'TENANT',$12,'PENDING',now(),1,$1::text,1,$15,0) RETURNING event_id) SELECT updated_instance.id,updated_instance.code,updated_instance.generation,inserted_operation.id,inserted_operation.kind,inserted_operation.state,inserted_operation.delivery_epoch FROM updated_instance CROSS JOIN inserted_operation`, r.managedSchema, r.managedSchema, r.managedSchema)
	if err := tx.QueryRow(ctx, query, instanceID, nextGeneration, in.OperationID, revisionID, blueprintRevisionID, in.ZoneID, in.CommandEventID, bundleHash, componentHash, inputHash, desiredHash, in.ActorUserID, protected.Payload, protected.KeyID, in.TraceID, in.TenantID).Scan(&result.ID, &result.Code, &result.Generation, &result.OperationID, &result.OperationKind, &result.OperationState, &result.DeliveryEpoch); err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	return &result, nil
}

func (r *tenantInstanceRepository) RetryTenantInstance(ctx context.Context, in *entity.RetryTenantInstance) (*entity.RetryTenantInstanceResult, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	var instanceID uuid.UUID
	var kind, state string
	var generation, instanceGeneration int64
	var lifecycle string
	var latest bool
	var attempt int16
	var epoch int64
	var eventID uuid.UUID
	q := fmt.Sprintf(`SELECT instance.id,operation.kind::text,operation.state::text,operation.generation,operation.attempt,operation.delivery_epoch,operation.current_command_event_id,instance.generation,instance.state::text,operation.id=(SELECT latest.id FROM %s.tenant_managed_service_operations latest WHERE latest.instance_id=instance.id ORDER BY latest.created_at DESC,latest.id DESC LIMIT 1) FROM %s.tenant_managed_service_operations operation JOIN %s.tenant_managed_service_instances instance ON instance.id=operation.instance_id JOIN %s.tenant_workspaces workspace ON workspace.id=instance.workspace_id WHERE operation.id=$1 AND instance.code=$2 AND instance.workspace_id=$3 AND instance.tenant_id=$4 AND instance.zone_id=$5 AND workspace.tenant_id=$4 AND workspace.zone_id=$5 FOR UPDATE OF instance,operation`, r.managedSchema, r.managedSchema, r.managedSchema, r.hierarchySchema)
	if err := tx.QueryRow(ctx, q, in.OperationID, in.Code, in.WorkspaceID, in.TenantID, in.ZoneID).Scan(&instanceID, &kind, &state, &generation, &attempt, &epoch, &eventID, &instanceGeneration, &lifecycle, &latest); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, taxonomy.ErrNotFound
		}
		return nil, taxonomy.ErrUnavailable
	}
	compatibleLifecycle := kind == "create" && lifecycle == "provisioning" || kind == "resize" && lifecycle == "active" || kind == "delete" && lifecycle == "deleting"
	if state != "terminal_failed" || generation != instanceGeneration || !latest || !compatibleLifecycle {
		return nil, taxonomy.ErrConflict
	}
	newEpoch := epoch + 1
	var result entity.RetryTenantInstanceResult
	update := fmt.Sprintf(`WITH operation_update AS (UPDATE %s.tenant_managed_service_operations SET state='accepted',attempt=0,delivery_epoch=$2,completed_at=NULL,last_error_code=NULL,last_sanitized_error=NULL,status_version=status_version+1,updated_at=now() WHERE id=$1 AND state='terminal_failed' RETURNING id,instance_id,kind::text,state::text,generation,attempt,delivery_epoch),outbox_update AS (UPDATE %s.managed_service_outbox_records SET status='PENDING',delivery_epoch=$2,completed_at=NULL,error_code=NULL,error_message=NULL,updated_at=now() WHERE event_id=$3 AND delivery_epoch=$4 RETURNING event_id) SELECT operation_update.id,operation_update.instance_id,operation_update.kind,operation_update.state,operation_update.generation,operation_update.attempt,operation_update.delivery_epoch FROM operation_update JOIN outbox_update ON true`, r.managedSchema, r.managedSchema)
	if err := tx.QueryRow(ctx, update, in.OperationID, newEpoch, eventID, epoch).Scan(&result.ID, &result.InstanceID, &result.Kind, &result.State, &result.Generation, &result.Attempt, &result.DeliveryEpoch); err != nil {
		return nil, taxonomy.ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	return &result, nil
}

func (r *tenantInstanceRepository) ListTenantInstances(ctx context.Context, in *entity.ListTenantInstances) (*entity.TenantInstancePage, error) {
	// [COMMENT]: This projection enumerates safe columns explicitly. Parameter
	// envelopes and input/desired hashes stay opaque to every customer read path.
	query := fmt.Sprintf(`WITH scope AS MATERIALIZED (
		SELECT workspace.id
		FROM %s.tenant_workspaces workspace
		WHERE workspace.id=$1 AND workspace.tenant_id=$2 AND workspace.zone_id=$3
	)
	SELECT instance.id,instance.code,instance.name,instance.state::text,instance.generation,
		instance.active_revision_id,instance.pending_revision_id,instance.metadata_version,
		instance.created_at,instance.updated_at,
		latest.id,latest.kind,latest.state,latest.generation,latest.attempt,latest.created_at
	FROM scope
	JOIN %s.tenant_managed_service_instances instance
		ON instance.workspace_id=scope.id AND instance.tenant_id=$2 AND instance.zone_id=$3
	LEFT JOIN LATERAL (
		SELECT operation.id,operation.kind::text,operation.state::text,operation.generation,
			operation.attempt,operation.created_at
		FROM %s.tenant_managed_service_operations operation
		WHERE operation.instance_id=instance.id AND operation.zone_id=instance.zone_id
		ORDER BY operation.id DESC
		LIMIT 1
	) latest ON true
	WHERE ($4::uuid=$6::uuid OR instance.id>$4)
	ORDER BY instance.id
	LIMIT $5`, r.hierarchySchema, r.managedSchema, r.managedSchema)

	rows, err := r.db.Query(ctx, query, in.WorkspaceID, in.TenantID, in.ZoneID, in.AfterID, in.Limit+1, uuid.Nil)
	if err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	defer rows.Close()

	items := make([]entity.TenantInstanceListItem, 0, in.Limit+1)
	for rows.Next() {
		var item entity.TenantInstanceListItem
		if err := rows.Scan(
			&item.ID, &item.Code, &item.Name, &item.State, &item.Generation,
			&item.ActiveRevisionID, &item.PendingRevisionID, &item.MetadataVersion,
			&item.CreatedAt, &item.UpdatedAt,
			&item.LatestOperationID, &item.LatestOperationKind, &item.LatestOperationState,
			&item.LatestOperationGen, &item.LatestOperationTry, &item.LatestOperationAt,
		); err != nil {
			return nil, taxonomy.ErrUnavailable
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, taxonomy.ErrUnavailable
	}

	page := &entity.TenantInstancePage{Items: items}
	if len(items) > in.Limit {
		page.HasMore = true
		page.Items = items[:in.Limit]
	}
	return page, nil
}

func (r *tenantInstanceRepository) GetTenantInstance(ctx context.Context, in *entity.GetTenantInstance) (*entity.TenantInstanceDetail, error) {
	query := fmt.Sprintf(`WITH scope AS MATERIALIZED (
		SELECT workspace.id
		FROM %s.tenant_workspaces workspace
		WHERE workspace.id=$1 AND workspace.tenant_id=$2 AND workspace.zone_id=$3
	)
	SELECT instance.id,instance.code,instance.name,instance.state::text,instance.generation,
		instance.revision_sequence,instance.active_revision_id,instance.pending_revision_id,
		instance.metadata_version,instance.created_at,instance.updated_at,
		latest.id,latest.kind,latest.state,latest.generation,latest.attempt,latest.created_at,latest.completed_at,
		COALESCE(blueprint_revision.component_contract,'[]'::jsonb),
		COALESCE(blueprint_revision.contract_version,''),COALESCE(blueprint_revision.input_schema,'{}'::jsonb),
		COALESCE(blueprint_revision.input_schema_sha256,decode('','hex')),COALESCE(blueprint_revision.ui_schema,'{}'::jsonb),
		COALESCE(blueprint_revision.ui_schema_sha256,decode('','hex'))
	FROM scope
	JOIN %s.tenant_managed_service_instances instance
		ON instance.workspace_id=scope.id AND instance.tenant_id=$2 AND instance.zone_id=$3 AND instance.code=$4
	LEFT JOIN %s.tenant_managed_service_instance_revisions instance_revision
		ON instance_revision.id=COALESCE(instance.pending_revision_id,instance.active_revision_id)
	LEFT JOIN %s.blueprint_revisions blueprint_revision
		ON blueprint_revision.id=instance_revision.blueprint_revision_id
	LEFT JOIN LATERAL (
		SELECT operation.id,operation.kind::text,operation.state::text,operation.generation,
			operation.attempt,operation.created_at,operation.completed_at
		FROM %s.tenant_managed_service_operations operation
		WHERE operation.instance_id=instance.id AND operation.zone_id=instance.zone_id
		ORDER BY operation.id DESC
		LIMIT 1
	) latest ON true`, r.hierarchySchema, r.managedSchema, r.managedSchema, r.managedSchema, r.managedSchema)

	var result entity.TenantInstanceDetail
	var componentContract []byte
	err := r.db.QueryRow(ctx, query, in.WorkspaceID, in.TenantID, in.ZoneID, in.Code).Scan(
		&result.ID, &result.Code, &result.Name, &result.State, &result.Generation,
		&result.RevisionSequence, &result.ActiveRevisionID, &result.PendingRevisionID,
		&result.MetadataVersion, &result.CreatedAt, &result.UpdatedAt,
		&result.LatestOperationID, &result.LatestOperationKind, &result.LatestOperationState,
		&result.LatestOperationGen, &result.LatestOperationTry, &result.LatestOperationAt,
		&result.LatestOperationDoneAt, &componentContract, &result.ResizeContractVersion,
		&result.ResizeInputSchema, &result.ResizeInputSchemaHash, &result.ResizeUISchema, &result.ResizeUISchemaHash,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, taxonomy.ErrNotFound
		}
		return nil, taxonomy.ErrUnavailable
	}
	namespaceBytes := append(append([]byte{}, in.TenantID[:]...), in.WorkspaceID[:]...)
	result.NetworkContract.Namespace = "aur-ms-t-" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(namespaceBytes))
	var componentRows []struct {
		ID          string `json:"id"`
		ComponentID string `json:"component_id"`
		Ports       []struct {
			Name     string `json:"name"`
			Port     int32  `json:"port"`
			Protocol string `json:"protocol"`
		} `json:"ports"`
	}
	if json.Unmarshal(componentContract, &componentRows) == nil {
		result.NetworkContract.Components = make([]entity.TenantNetworkComponent, 0, len(componentRows))
		for _, row := range componentRows {
			componentID := strings.TrimSpace(row.ID)
			if componentID == "" {
				componentID = strings.TrimSpace(row.ComponentID)
			}
			if componentID == "" {
				continue
			}
			serviceName := result.Code + "-" + componentID
			if componentID == "primary" {
				serviceName = result.Code
			}
			ports := make([]entity.TenantNetworkPort, 0, len(row.Ports))
			for _, port := range row.Ports {
				ports = append(ports, entity.TenantNetworkPort{Name: port.Name, Port: port.Port, Protocol: port.Protocol})
			}
			result.NetworkContract.Components = append(result.NetworkContract.Components, entity.TenantNetworkComponent{ComponentCode: componentID, ServiceName: serviceName, PodSelector: map[string]string{"aurora.io/instance": result.Code, "aurora.io/component": componentID}, Ports: ports})
		}
	}
	return &result, nil
}

func (r *tenantInstanceRepository) ListTenantInstanceOperations(ctx context.Context, in *entity.ListTenantInstanceOperations) (*entity.TenantInstanceOperationPage, error) {
	query := fmt.Sprintf(`WITH scope AS MATERIALIZED (
		SELECT workspace.id
		FROM %s.tenant_workspaces workspace
		WHERE workspace.id=$1 AND workspace.tenant_id=$2 AND workspace.zone_id=$3
	), target AS MATERIALIZED (
		SELECT instance.id,instance.zone_id
		FROM scope
		JOIN %s.tenant_managed_service_instances instance
			ON instance.workspace_id=scope.id AND instance.tenant_id=$2 AND instance.zone_id=$3 AND instance.code=$4
	)
	SELECT operation.id,operation.kind::text,operation.state::text,operation.generation,operation.attempt,operation.delivery_epoch,
		operation.target_revision_id,operation.blueprint_revision_id,operation.retry_of_operation_id,
		operation.status_version,operation.last_error_code,operation.last_sanitized_error,
		operation.completed_at,operation.created_at,operation.updated_at
	FROM target
	JOIN %s.tenant_managed_service_operations operation
		ON operation.instance_id=target.id AND operation.zone_id=target.zone_id
	WHERE ($5::uuid=$7::uuid OR operation.id<$5)
	ORDER BY operation.id DESC
	LIMIT $6`, r.hierarchySchema, r.managedSchema, r.managedSchema)

	rows, err := r.db.Query(ctx, query, in.WorkspaceID, in.TenantID, in.ZoneID, in.InstanceCode, in.AfterOperationID, in.Limit+1, uuid.Nil)
	if err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	defer rows.Close()

	items := make([]entity.TenantInstanceOperationListItem, 0, in.Limit+1)
	for rows.Next() {
		var item entity.TenantInstanceOperationListItem
		if err := rows.Scan(
			&item.ID, &item.Kind, &item.State, &item.Generation, &item.Attempt, &item.DeliveryEpoch,
			&item.TargetRevisionID, &item.BlueprintRevisionID, &item.RetryOfOperationID,
			&item.StatusVersion, &item.LastErrorCode, &item.LastSanitizedError,
			&item.CompletedAt, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, taxonomy.ErrUnavailable
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, taxonomy.ErrUnavailable
	}

	page := &entity.TenantInstanceOperationPage{Items: items}
	if len(items) > in.Limit {
		page.HasMore = true
		page.Items = items[:in.Limit]
	}
	return page, nil
}

func (r *tenantInstanceRepository) GetTenantInstanceOperation(ctx context.Context, in *entity.GetTenantInstanceOperation) (*entity.TenantInstanceOperationDetail, error) {
	query := fmt.Sprintf(`WITH scope AS MATERIALIZED (
		SELECT workspace.id
		FROM %s.tenant_workspaces workspace
		WHERE workspace.id=$1 AND workspace.tenant_id=$2 AND workspace.zone_id=$3
	), target AS MATERIALIZED (
		SELECT instance.id,instance.zone_id
		FROM scope
		JOIN %s.tenant_managed_service_instances instance
			ON instance.workspace_id=scope.id AND instance.tenant_id=$2 AND instance.zone_id=$3 AND instance.code=$4
	)
	SELECT operation.id,operation.instance_id,operation.kind::text,operation.state::text,
		operation.generation,operation.attempt,operation.delivery_epoch,operation.target_revision_id,operation.blueprint_revision_id,
		operation.retry_of_operation_id,operation.status_version,operation.last_error_code,
		operation.last_sanitized_error,operation.completed_at,operation.created_at,operation.updated_at
	FROM target
	JOIN %s.tenant_managed_service_operations operation
		ON operation.instance_id=target.id AND operation.zone_id=target.zone_id AND operation.id=$5`, r.hierarchySchema, r.managedSchema, r.managedSchema)

	var result entity.TenantInstanceOperationDetail
	err := r.db.QueryRow(ctx, query, in.WorkspaceID, in.TenantID, in.ZoneID, in.InstanceCode, in.OperationID).Scan(
		&result.ID, &result.InstanceID, &result.Kind, &result.State, &result.Generation,
		&result.Attempt, &result.DeliveryEpoch, &result.TargetRevisionID, &result.BlueprintRevisionID,
		&result.RetryOfOperationID, &result.StatusVersion, &result.LastErrorCode,
		&result.LastSanitizedError, &result.CompletedAt, &result.CreatedAt, &result.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, taxonomy.ErrNotFound
		}
		return nil, taxonomy.ErrUnavailable
	}
	return &result, nil
}

func (r *tenantInstanceRepository) RenameTenantInstance(ctx context.Context, in *entity.RenameTenantInstance) (*entity.RenameTenantInstanceResult, error) {
	// [COMMENT]: Tenant ownership is a physical predicate in this CTE. The actor
	// is authorization context only and never replaces tenant_id as ownership.
	query := fmt.Sprintf(`WITH scope AS MATERIALIZED (
		SELECT workspace.id
		FROM %s.tenant_workspaces workspace
		WHERE workspace.id=$1 AND workspace.tenant_id=$2 AND workspace.zone_id=$3
		FOR KEY SHARE
	), target AS MATERIALIZED (
		SELECT instance.id,instance.code,instance.name,instance.metadata_version,instance.updated_at
		FROM scope
		JOIN %s.tenant_managed_service_instances instance
			ON instance.workspace_id=scope.id AND instance.tenant_id=$2 AND instance.zone_id=$3 AND instance.code=$4
		FOR UPDATE OF instance
	), updated AS (
		UPDATE %s.tenant_managed_service_instances instance
		SET name=$5,metadata_version=instance.metadata_version+1,updated_at=now()
		FROM target
		WHERE instance.id=target.id AND target.metadata_version=$6
		RETURNING instance.id,instance.code,instance.name,instance.metadata_version,instance.updated_at
	)
	SELECT CASE
		WHEN EXISTS (SELECT 1 FROM updated) THEN 'updated'
		WHEN EXISTS (SELECT 1 FROM target) THEN 'conflict'
		ELSE 'not_found'
	END,
	COALESCE((SELECT id FROM updated),$7::uuid),
	COALESCE((SELECT code FROM updated),''),
	COALESCE((SELECT name FROM updated),''),
	COALESCE((SELECT metadata_version FROM updated),0),
	COALESCE((SELECT updated_at FROM updated),now())`, r.hierarchySchema, r.managedSchema, r.managedSchema)

	var decision string
	var result entity.RenameTenantInstanceResult
	err := r.db.QueryRow(ctx, query,
		in.WorkspaceID, in.TenantID, in.ZoneID, in.Code, in.Name, in.ExpectedMetadataVersion, uuid.Nil,
	).Scan(&decision, &result.ID, &result.Code, &result.Name, &result.MetadataVersion, &result.UpdatedAt)
	if err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	switch decision {
	case "updated":
		return &result, nil
	case "conflict":
		return nil, taxonomy.ErrConflict
	default:
		return nil, taxonomy.ErrNotFound
	}
}
