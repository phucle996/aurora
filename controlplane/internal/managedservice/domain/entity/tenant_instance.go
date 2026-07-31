package entity

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type ListTenantInstances struct {
	ActorUserID uuid.UUID
	TenantID    uuid.UUID
	WorkspaceID uuid.UUID
	ZoneID      uuid.UUID
	AfterID     uuid.UUID
	Limit       int
}

type TenantInstanceListItem struct {
	ID                   uuid.UUID
	Code                 string
	Name                 string
	DesiredState         string
	Generation           int64
	ActiveRevisionID     *uuid.UUID
	PendingRevisionID    *uuid.UUID
	ObservedState        string
	ObservedStateVersion int64
	ObservedAt           *time.Time
	MetadataVersion      int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
	LatestOperationID    *uuid.UUID
	LatestOperationKind  *string
	LatestOperationState *string
	LatestOperationGen   *int64
	LatestOperationTry   *int16
	LatestOperationAt    *time.Time
}

type TenantInstancePage struct {
	Items   []TenantInstanceListItem
	HasMore bool
}

type GetTenantInstance struct {
	ActorUserID uuid.UUID
	TenantID    uuid.UUID
	WorkspaceID uuid.UUID
	ZoneID      uuid.UUID
	Code        string
}

type TenantInstanceDetail struct {
	ID                    uuid.UUID
	Code                  string
	Name                  string
	DesiredState          string
	Generation            int64
	RevisionSequence      int64
	ActiveRevisionID      *uuid.UUID
	PendingRevisionID     *uuid.UUID
	ObservedState         string
	ObservedStateVersion  int64
	ObservedOutput        json.RawMessage
	ObservedAt            *time.Time
	MetadataVersion       int64
	CreatedAt             time.Time
	UpdatedAt             time.Time
	LatestOperationID     *uuid.UUID
	LatestOperationKind   *string
	LatestOperationState  *string
	LatestOperationGen    *int64
	LatestOperationTry    *int16
	LatestOperationAt     *time.Time
	LatestOperationDoneAt *time.Time
}

type ListTenantInstanceOperations struct {
	ActorUserID      uuid.UUID
	TenantID         uuid.UUID
	WorkspaceID      uuid.UUID
	ZoneID           uuid.UUID
	InstanceCode     string
	AfterOperationID uuid.UUID
	Limit            int
}

type TenantInstanceOperationListItem struct {
	ID                  uuid.UUID
	Kind                string
	State               string
	Generation          int64
	Attempt             int16
	TargetRevisionID    uuid.UUID
	BlueprintRevisionID uuid.UUID
	RetryOfOperationID  *uuid.UUID
	StatusVersion       int64
	LastErrorCode       *string
	LastSanitizedError  *string
	CompletedAt         *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type TenantInstanceOperationPage struct {
	Items   []TenantInstanceOperationListItem
	HasMore bool
}

type GetTenantInstanceOperation struct {
	ActorUserID  uuid.UUID
	TenantID     uuid.UUID
	WorkspaceID  uuid.UUID
	ZoneID       uuid.UUID
	InstanceCode string
	OperationID  uuid.UUID
}

type TenantInstanceOperationDetail struct {
	ID                  uuid.UUID
	InstanceID          uuid.UUID
	Kind                string
	State               string
	Generation          int64
	Attempt             int16
	TargetRevisionID    uuid.UUID
	BlueprintRevisionID uuid.UUID
	RetryOfOperationID  *uuid.UUID
	StatusVersion       int64
	LastErrorCode       *string
	LastSanitizedError  *string
	CompletedAt         *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type RenameTenantInstance struct {
	ActorUserID             uuid.UUID
	TenantID                uuid.UUID
	WorkspaceID             uuid.UUID
	ZoneID                  uuid.UUID
	Code                    string
	Name                    string
	ExpectedMetadataVersion int64
}

type RenameTenantInstanceResult struct {
	ID              uuid.UUID
	Code            string
	Name            string
	MetadataVersion int64
	UpdatedAt       time.Time
}
