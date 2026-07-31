package repository

import (
	"context"
	"fmt"

	"controlplane/internal/managedservice/domain/entity"
	managedrepo "controlplane/internal/managedservice/domain/repo"
	"controlplane/internal/managedservice/taxonomy"
	jobpayload "controlplane/internal/security"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type personalInstanceRepository struct {
	db              *pgxpool.Pool
	managedSchema   string
	hierarchySchema string
	protector       jobpayload.Protector
}

func NewPersonalInstanceRepository(db *pgxpool.Pool, managedSchema, hierarchySchema string, protector jobpayload.Protector) managedrepo.PersonalInstanceRepository {
	return &personalInstanceRepository{db: db, managedSchema: managedSchema, hierarchySchema: hierarchySchema, protector: protector}
}

func (r *personalInstanceRepository) ListPersonalInstances(ctx context.Context, in *entity.ListPersonalInstances) (*entity.PersonalInstancePage, error) {
	// [COMMENT]: This projection enumerates safe columns explicitly. Parameter
	// envelopes and input/desired hashes stay opaque to every customer read path.
	query := fmt.Sprintf(`WITH scope AS MATERIALIZED (
		SELECT workspace.id
		FROM %s.personal_workspaces workspace
		WHERE workspace.id=$1 AND workspace.owner_id=$2 AND workspace.zone_id=$3
	)
	SELECT instance.id,instance.code,instance.name,instance.state::text,instance.generation,
		instance.active_revision_id,instance.pending_revision_id,instance.observed_state::text,
		instance.observed_state_version,instance.observed_at,instance.metadata_version,
		instance.created_at,instance.updated_at,
		latest.id,latest.kind,latest.state,latest.generation,latest.attempt,latest.created_at
	FROM scope
	JOIN %s.personal_managed_service_instances instance
		ON instance.workspace_id=scope.id AND instance.user_id=$2 AND instance.zone_id=$3
	LEFT JOIN LATERAL (
		SELECT operation.id,operation.kind::text,operation.state::text,operation.generation,
			operation.attempt,operation.created_at
		FROM %s.personal_managed_service_operations operation
		WHERE operation.instance_id=instance.id AND operation.zone_id=instance.zone_id
		ORDER BY operation.id DESC
		LIMIT 1
	) latest ON true
	WHERE ($4::uuid=$6::uuid OR instance.id>$4)
	ORDER BY instance.id
	LIMIT $5`, r.hierarchySchema, r.managedSchema, r.managedSchema)

	rows, err := r.db.Query(ctx, query, in.WorkspaceID, in.UserID, in.ZoneID, in.AfterID, in.Limit+1, uuid.Nil)
	if err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	defer rows.Close()

	items := make([]entity.PersonalInstanceListItem, 0, in.Limit+1)
	for rows.Next() {
		var item entity.PersonalInstanceListItem
		if err := rows.Scan(
			&item.ID, &item.Code, &item.Name, &item.DesiredState, &item.Generation,
			&item.ActiveRevisionID, &item.PendingRevisionID, &item.ObservedState,
			&item.ObservedStateVersion, &item.ObservedAt, &item.MetadataVersion,
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

	page := &entity.PersonalInstancePage{Items: items}
	if len(items) > in.Limit {
		page.HasMore = true
		page.Items = items[:in.Limit]
	}
	return page, nil
}

func (r *personalInstanceRepository) GetPersonalInstance(ctx context.Context, in *entity.GetPersonalInstance) (*entity.PersonalInstanceDetail, error) {
	query := fmt.Sprintf(`WITH scope AS MATERIALIZED (
		SELECT workspace.id
		FROM %s.personal_workspaces workspace
		WHERE workspace.id=$1 AND workspace.owner_id=$2 AND workspace.zone_id=$3
	)
	SELECT instance.id,instance.code,instance.name,instance.state::text,instance.generation,
		instance.revision_sequence,instance.active_revision_id,instance.pending_revision_id,
		instance.observed_state::text,instance.observed_state_version,instance.observed_output,
		instance.observed_at,instance.metadata_version,instance.created_at,instance.updated_at,
		latest.id,latest.kind,latest.state,latest.generation,latest.attempt,latest.created_at,latest.completed_at
	FROM scope
	JOIN %s.personal_managed_service_instances instance
		ON instance.workspace_id=scope.id AND instance.user_id=$2 AND instance.zone_id=$3 AND instance.code=$4
	LEFT JOIN LATERAL (
		SELECT operation.id,operation.kind::text,operation.state::text,operation.generation,
			operation.attempt,operation.created_at,operation.completed_at
		FROM %s.personal_managed_service_operations operation
		WHERE operation.instance_id=instance.id AND operation.zone_id=instance.zone_id
		ORDER BY operation.id DESC
		LIMIT 1
	) latest ON true`, r.hierarchySchema, r.managedSchema, r.managedSchema)

	var result entity.PersonalInstanceDetail
	err := r.db.QueryRow(ctx, query, in.WorkspaceID, in.UserID, in.ZoneID, in.Code).Scan(
		&result.ID, &result.Code, &result.Name, &result.DesiredState, &result.Generation,
		&result.RevisionSequence, &result.ActiveRevisionID, &result.PendingRevisionID,
		&result.ObservedState, &result.ObservedStateVersion, &result.ObservedOutput,
		&result.ObservedAt, &result.MetadataVersion, &result.CreatedAt, &result.UpdatedAt,
		&result.LatestOperationID, &result.LatestOperationKind, &result.LatestOperationState,
		&result.LatestOperationGen, &result.LatestOperationTry, &result.LatestOperationAt,
		&result.LatestOperationDoneAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, taxonomy.ErrNotFound
		}
		return nil, taxonomy.ErrUnavailable
	}
	return &result, nil
}

func (r *personalInstanceRepository) ListPersonalInstanceOperations(ctx context.Context, in *entity.ListPersonalInstanceOperations) (*entity.PersonalInstanceOperationPage, error) {
	query := fmt.Sprintf(`WITH scope AS MATERIALIZED (
		SELECT workspace.id
		FROM %s.personal_workspaces workspace
		WHERE workspace.id=$1 AND workspace.owner_id=$2 AND workspace.zone_id=$3
	), target AS MATERIALIZED (
		SELECT instance.id,instance.zone_id
		FROM scope
		JOIN %s.personal_managed_service_instances instance
			ON instance.workspace_id=scope.id AND instance.user_id=$2 AND instance.zone_id=$3 AND instance.code=$4
	)
	SELECT operation.id,operation.kind::text,operation.state::text,operation.generation,operation.attempt,
		operation.target_revision_id,operation.blueprint_revision_id,operation.retry_of_operation_id,
		operation.status_version,operation.last_error_code,operation.last_sanitized_error,
		operation.completed_at,operation.created_at,operation.updated_at
	FROM target
	JOIN %s.personal_managed_service_operations operation
		ON operation.instance_id=target.id AND operation.zone_id=target.zone_id
	WHERE ($5::uuid=$7::uuid OR operation.id<$5)
	ORDER BY operation.id DESC
	LIMIT $6`, r.hierarchySchema, r.managedSchema, r.managedSchema)

	rows, err := r.db.Query(ctx, query, in.WorkspaceID, in.UserID, in.ZoneID, in.InstanceCode, in.AfterOperationID, in.Limit+1, uuid.Nil)
	if err != nil {
		return nil, taxonomy.ErrUnavailable
	}
	defer rows.Close()

	items := make([]entity.PersonalInstanceOperationListItem, 0, in.Limit+1)
	for rows.Next() {
		var item entity.PersonalInstanceOperationListItem
		if err := rows.Scan(
			&item.ID, &item.Kind, &item.State, &item.Generation, &item.Attempt,
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

	page := &entity.PersonalInstanceOperationPage{Items: items}
	if len(items) > in.Limit {
		page.HasMore = true
		page.Items = items[:in.Limit]
	}
	return page, nil
}

func (r *personalInstanceRepository) GetPersonalInstanceOperation(ctx context.Context, in *entity.GetPersonalInstanceOperation) (*entity.PersonalInstanceOperationDetail, error) {
	query := fmt.Sprintf(`WITH scope AS MATERIALIZED (
		SELECT workspace.id
		FROM %s.personal_workspaces workspace
		WHERE workspace.id=$1 AND workspace.owner_id=$2 AND workspace.zone_id=$3
	), target AS MATERIALIZED (
		SELECT instance.id,instance.zone_id
		FROM scope
		JOIN %s.personal_managed_service_instances instance
			ON instance.workspace_id=scope.id AND instance.user_id=$2 AND instance.zone_id=$3 AND instance.code=$4
	)
	SELECT operation.id,operation.instance_id,operation.kind::text,operation.state::text,
		operation.generation,operation.attempt,operation.target_revision_id,operation.blueprint_revision_id,
		operation.retry_of_operation_id,operation.status_version,operation.last_error_code,
		operation.last_sanitized_error,operation.completed_at,operation.created_at,operation.updated_at
	FROM target
	JOIN %s.personal_managed_service_operations operation
		ON operation.instance_id=target.id AND operation.zone_id=target.zone_id AND operation.id=$5`, r.hierarchySchema, r.managedSchema, r.managedSchema)

	var result entity.PersonalInstanceOperationDetail
	err := r.db.QueryRow(ctx, query, in.WorkspaceID, in.UserID, in.ZoneID, in.InstanceCode, in.OperationID).Scan(
		&result.ID, &result.InstanceID, &result.Kind, &result.State, &result.Generation,
		&result.Attempt, &result.TargetRevisionID, &result.BlueprintRevisionID,
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

func (r *personalInstanceRepository) RenamePersonalInstance(ctx context.Context, in *entity.RenamePersonalInstance) (*entity.RenamePersonalInstanceResult, error) {
	// [COMMENT]: Scope lock, optimistic-version decision and rename are one SQL
	// statement. A concurrent rename can only win one metadata version; this flow
	// intentionally never touches generation, revision heads or the outbox.
	query := fmt.Sprintf(`WITH scope AS MATERIALIZED (
		SELECT workspace.id
		FROM %s.personal_workspaces workspace
		WHERE workspace.id=$1 AND workspace.owner_id=$2 AND workspace.zone_id=$3
		FOR KEY SHARE
	), target AS MATERIALIZED (
		SELECT instance.id,instance.code,instance.name,instance.metadata_version,instance.updated_at
		FROM scope
		JOIN %s.personal_managed_service_instances instance
			ON instance.workspace_id=scope.id AND instance.user_id=$2 AND instance.zone_id=$3 AND instance.code=$4
		FOR UPDATE OF instance
	), updated AS (
		UPDATE %s.personal_managed_service_instances instance
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
	var result entity.RenamePersonalInstanceResult
	err := r.db.QueryRow(ctx, query,
		in.WorkspaceID, in.UserID, in.ZoneID, in.Code, in.Name, in.ExpectedMetadataVersion, uuid.Nil,
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
