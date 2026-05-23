package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

type OAuthClientType string

type OAuthClientStatus string

const (
	OAuthClientTypePublic       OAuthClientType = "public"
	OAuthClientTypeConfidential OAuthClientType = "confidential"
)

const (
	OAuthClientStatusActive   OAuthClientStatus = "active"
	OAuthClientStatusDisabled OAuthClientStatus = "disabled"
	OAuthClientStatusRevoked  OAuthClientStatus = "revoked"
)

type OAuthClient struct {
	ID            uuid.UUID
	ClientID      string
	Name          string
	Description   *string
	ClientType    OAuthClientType
	RedirectURIs  []string
	AllowedScopes []string
	GrantTypes    []string
	ResponseTypes []string
	Status        OAuthClientStatus
	OwnerUserID   *uuid.UUID
	TenantID      *uuid.UUID
	WorkspaceID   *uuid.UUID
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type OAuthClientSecret struct {
	ID           uuid.UUID
	ClientID     string
	SecretPrefix string
	SecretHash   string
	SecretName   string
	ExpiresAt    *time.Time
	RevokedAt    *time.Time
	LastUsedAt   *time.Time
	CreatedAt    time.Time
}

type OAuthAuthorizationCode struct {
	ID                  uuid.UUID
	CodeHash            string
	ClientID            uuid.UUID
	UserID              uuid.UUID
	TenantID            *uuid.UUID
	WorkspaceID         *uuid.UUID
	RedirectURI         string
	Scopes              []string
	CodeChallenge       *string
	CodeChallengeMethod *string
	ExpiresAt           time.Time
	ConsumedAt          *time.Time
	RevokedAt           *time.Time
	CreatedAt           time.Time
}

type OAuthGrant struct {
	ID          uuid.UUID
	ClientID    string
	UserID      uuid.UUID
	TenantID    *uuid.UUID
	WorkspaceID *uuid.UUID
	Scopes      []string
	GrantedAt   time.Time
	RevokedAt   *time.Time
	ExpiresAt   *time.Time
	CreatedAt   time.Time
}

type OAuthToken struct {
	ID               uuid.UUID
	ClientID         string
	UserID           *string
	GrantID          *uuid.UUID
	AccessTokenHash  string
	RefreshTokenHash *string
	TokenFamilyID    string
	Scopes           []string
	IssuedAt         time.Time
	ExpiresAt        time.Time
	RotatedAt        *time.Time
	RevokedAt        *time.Time
	CreatedAt        time.Time
}
