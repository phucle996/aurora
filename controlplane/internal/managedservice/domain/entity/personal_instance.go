package entity

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type ListPersonalInstances struct {
	UserID      uuid.UUID
	WorkspaceID uuid.UUID
	ZoneID      uuid.UUID
	AfterID     uuid.UUID
	Limit       int
}

type PersonalInstanceListItem struct {
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

type PersonalInstancePage struct {
	Items   []PersonalInstanceListItem
	HasMore bool
}

type GetPersonalInstance struct {
	UserID      uuid.UUID
	WorkspaceID uuid.UUID
	ZoneID      uuid.UUID
	Code        string
}

type PersonalInstanceDetail struct {
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
	NetworkContract       PersonalNetworkContract
}

type PersonalNetworkContract struct {
	Namespace  string
	Components []PersonalNetworkComponent
}

type PersonalNetworkComponent struct {
	ComponentCode string
	ServiceName   string
	PodSelector   map[string]string
	Ports         []PersonalNetworkPort
}

type PersonalNetworkPort struct {
	Name     string
	Port     int32
	Protocol string
}

type ListPersonalInstanceOperations struct {
	UserID           uuid.UUID
	WorkspaceID      uuid.UUID
	ZoneID           uuid.UUID
	InstanceCode     string
	AfterOperationID uuid.UUID
	Limit            int
}

type PersonalInstanceOperationListItem struct {
	ID                  uuid.UUID
	Kind                string
	State               string
	Generation          int64
	Attempt             int16
	DeliveryEpoch       int64
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

type PersonalInstanceOperationPage struct {
	Items   []PersonalInstanceOperationListItem
	HasMore bool
}

type GetPersonalInstanceOperation struct {
	UserID       uuid.UUID
	WorkspaceID  uuid.UUID
	ZoneID       uuid.UUID
	InstanceCode string
	OperationID  uuid.UUID
}

type PersonalInstanceOperationDetail struct {
	ID                  uuid.UUID
	InstanceID          uuid.UUID
	Kind                string
	State               string
	Generation          int64
	Attempt             int16
	DeliveryEpoch       int64
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

type RenamePersonalInstance struct {
	UserID                  uuid.UUID
	WorkspaceID             uuid.UUID
	ZoneID                  uuid.UUID
	Code                    string
	Name                    string
	ExpectedMetadataVersion int64
}

type RenamePersonalInstanceResult struct {
	ID              uuid.UUID
	Code            string
	Name            string
	MetadataVersion int64
	UpdatedAt       time.Time
}

type CreatePersonalInstance struct {
	UserID              uuid.UUID
	WorkspaceID         uuid.UUID
	ZoneID              uuid.UUID
	Code                string
	Name                string
	BlueprintRevisionID uuid.UUID
	InputSchemaSHA256   []byte
	Parameters          []byte
	InputSHA256         []byte
	DesiredSpecSHA256   []byte
	CreateIntentSHA256  []byte
	TraceID             []byte
	Traceparent         string
	Tracestate          string
	InstanceID          uuid.UUID
	InstanceRevisionID  uuid.UUID
	OperationID         uuid.UUID
	CommandEventID      uuid.UUID
	IssuedAt            time.Time
}

type CreatePersonalInstanceResult struct {
	ID                uuid.UUID
	Code              string
	Name              string
	DesiredState      string
	Generation        int64
	RevisionSequence  int64
	PendingRevisionID *uuid.UUID
	OperationID       uuid.UUID
	OperationKind     string
	OperationState    string
	DeliveryEpoch     int64
	Deduplicated      bool
}

type ResizePersonalInstance struct {
	UserID             uuid.UUID
	WorkspaceID        uuid.UUID
	ZoneID             uuid.UUID
	Code               string
	ExpectedGeneration int64
	Parameters         []byte
	InputSHA256        []byte
	DesiredSpecSHA256  []byte
	TraceID            []byte
	Traceparent        string
	Tracestate         string
	InstanceRevisionID uuid.UUID
	OperationID        uuid.UUID
	CommandEventID     uuid.UUID
	IssuedAt           time.Time
}

type ResizePersonalInstanceResult struct {
	ID                uuid.UUID
	Code              string
	Generation        int64
	PendingRevisionID *uuid.UUID
	OperationID       uuid.UUID
	OperationKind     string
	OperationState    string
	DeliveryEpoch     int64
}

type DeletePersonalInstance struct {
	UserID             uuid.UUID
	WorkspaceID        uuid.UUID
	ZoneID             uuid.UUID
	Code               string
	ExpectedGeneration int64
	OperationID        uuid.UUID
	CommandEventID     uuid.UUID
	IssuedAt           time.Time
	TraceID            []byte
	Traceparent        string
	Tracestate         string
}

type DeletePersonalInstanceResult struct {
	ID              uuid.UUID
	Code            string
	Generation      int64
	OperationID     uuid.UUID
	OperationKind   string
	OperationState  string
	DeliveryEpoch   int64
	AlreadyDeleting bool
}

type RetryPersonalInstance struct {
	UserID      uuid.UUID
	WorkspaceID uuid.UUID
	ZoneID      uuid.UUID
	Code        string
	OperationID uuid.UUID
	IssuedAt    time.Time
}

type RetryPersonalInstanceResult struct {
	ID            uuid.UUID
	InstanceID    uuid.UUID
	Kind          string
	State         string
	Generation    int64
	Attempt       int16
	DeliveryEpoch int64
}
