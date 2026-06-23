package iamModel

import (
	"time"

	"github.com/google/uuid"

	"controlplane/internal/iam/domain/entity"
)

type OAuthClient struct {
	ID            uuid.UUID  `db:"id"`
	ClientID      string     `db:"client_id"`
	Name          string     `db:"name"`
	Description   *string    `db:"description"`
	ClientType    string     `db:"client_type"`
	RedirectURIs  []string   `db:"redirect_uris"`
	AllowedScopes []string   `db:"allowed_scopes"`
	GrantTypes    []string   `db:"grant_types"`
	ResponseTypes []string   `db:"response_types"`
	Status        string     `db:"status"`
	OwnerUserID   *uuid.UUID `db:"owner_user_id"`
	TenantID      *uuid.UUID `db:"tenant_id"`
	WorkspaceID   *uuid.UUID `db:"workspace_id"`
	CreatedAt     time.Time  `db:"created_at"`
	UpdatedAt     time.Time  `db:"updated_at"`
}

func OAuthClientEntityToModel(input iamEntity.OAuthClient) OAuthClient {
	return OAuthClient{ID: input.ID,
		ClientID:      input.ClientID,
		Name:          input.Name,
		Description:   input.Description,
		ClientType:    string(input.ClientType),
		RedirectURIs:  input.RedirectURIs,
		AllowedScopes: input.AllowedScopes,
		GrantTypes:    input.GrantTypes,
		ResponseTypes: input.ResponseTypes,
		Status:        string(input.Status),
		OwnerUserID:   input.OwnerUserID,
		TenantID:      input.TenantID,
		WorkspaceID:   input.WorkspaceID,
		CreatedAt:     input.CreatedAt,
		UpdatedAt:     input.UpdatedAt}
}
func OAuthClientModelToEntity(input OAuthClient) iamEntity.OAuthClient {
	return iamEntity.OAuthClient{ID: input.ID,
		ClientID:      input.ClientID,
		Name:          input.Name,
		Description:   input.Description,
		ClientType:    iamEntity.OAuthClientType(input.ClientType),
		RedirectURIs:  input.RedirectURIs,
		AllowedScopes: input.AllowedScopes,
		GrantTypes:    input.GrantTypes,
		ResponseTypes: input.ResponseTypes,
		Status:        iamEntity.OAuthClientStatus(input.Status),
		OwnerUserID:   input.OwnerUserID,
		TenantID:      input.TenantID,
		WorkspaceID:   input.WorkspaceID,
		CreatedAt:     input.CreatedAt,
		UpdatedAt:     input.UpdatedAt}
}

type OAuthClientSecret struct {
	ID           uuid.UUID  `db:"id"`
	ClientID     string     `db:"client_id"`
	SecretPrefix string     `db:"secret_prefix"`
	SecretHash   string     `db:"secret_hash"`
	SecretName   string     `db:"secret_name"`
	ExpiresAt    *time.Time `db:"expires_at"`
	RevokedAt    *time.Time `db:"revoked_at"`
	LastUsedAt   *time.Time `db:"last_used_at"`
	CreatedAt    time.Time  `db:"created_at"`
}

func OAuthClientSecretEntityToModel(input iamEntity.OAuthClientSecret) OAuthClientSecret {
	return OAuthClientSecret{ID: input.ID,
		ClientID:     input.ClientID,
		SecretPrefix: input.SecretPrefix,
		SecretHash:   input.SecretHash,
		SecretName:   input.SecretName,
		ExpiresAt:    input.ExpiresAt,
		RevokedAt:    input.RevokedAt,
		LastUsedAt:   input.LastUsedAt,
		CreatedAt:    input.CreatedAt}
}
func OAuthClientSecretModelToEntity(input OAuthClientSecret) iamEntity.OAuthClientSecret {
	return iamEntity.OAuthClientSecret{ID: input.ID,
		ClientID:     input.ClientID,
		SecretPrefix: input.SecretPrefix,
		SecretHash:   input.SecretHash,
		SecretName:   input.SecretName,
		ExpiresAt:    input.ExpiresAt,
		RevokedAt:    input.RevokedAt,
		LastUsedAt:   input.LastUsedAt,
		CreatedAt:    input.CreatedAt}
}

type OAuthAuthorizationCode struct {
	ID                  uuid.UUID  `db:"id"`
	CodeHash            string     `db:"code_hash"`
	ClientID            uuid.UUID  `db:"client_id"`
	UserID              uuid.UUID  `db:"user_id"`
	TenantID            *uuid.UUID `db:"tenant_id"`
	WorkspaceID         *uuid.UUID `db:"workspace_id"`
	RedirectURI         string     `db:"redirect_uri"`
	Scopes              []string   `db:"scopes"`
	CodeChallenge       *string    `db:"code_challenge"`
	CodeChallengeMethod *string    `db:"code_challenge_method"`
	ExpiresAt           time.Time  `db:"expires_at"`
	ConsumedAt          *time.Time `db:"consumed_at"`
	RevokedAt           *time.Time `db:"revoked_at"`
	CreatedAt           time.Time  `db:"created_at"`
}

func OAuthAuthorizationCodeEntityToModel(input iamEntity.OAuthAuthorizationCode) OAuthAuthorizationCode {
	return OAuthAuthorizationCode{ID: input.ID,
		CodeHash:            input.CodeHash,
		ClientID:            input.ClientID,
		UserID:              input.UserID,
		TenantID:            input.TenantID,
		WorkspaceID:         input.WorkspaceID,
		RedirectURI:         input.RedirectURI,
		Scopes:              input.Scopes,
		CodeChallenge:       input.CodeChallenge,
		CodeChallengeMethod: input.CodeChallengeMethod,
		ExpiresAt:           input.ExpiresAt,
		ConsumedAt:          input.ConsumedAt,
		RevokedAt:           input.RevokedAt,
		CreatedAt:           input.CreatedAt}
}
func OAuthAuthorizationCodeModelToEntity(input OAuthAuthorizationCode) iamEntity.OAuthAuthorizationCode {
	return iamEntity.OAuthAuthorizationCode{ID: input.ID,
		CodeHash:            input.CodeHash,
		ClientID:            input.ClientID,
		UserID:              input.UserID,
		TenantID:            input.TenantID,
		WorkspaceID:         input.WorkspaceID,
		RedirectURI:         input.RedirectURI,
		Scopes:              input.Scopes,
		CodeChallenge:       input.CodeChallenge,
		CodeChallengeMethod: input.CodeChallengeMethod,
		ExpiresAt:           input.ExpiresAt,
		ConsumedAt:          input.ConsumedAt,
		RevokedAt:           input.RevokedAt,
		CreatedAt:           input.CreatedAt}
}

type OAuthGrant struct {
	ID          uuid.UUID  `db:"id"`
	ClientID    string     `db:"client_id"`
	UserID      uuid.UUID  `db:"user_id"`
	TenantID    *uuid.UUID `db:"tenant_id"`
	WorkspaceID *uuid.UUID `db:"workspace_id"`
	Scopes      []string   `db:"scopes"`
	GrantedAt   time.Time  `db:"granted_at"`
	RevokedAt   *time.Time `db:"revoked_at"`
	ExpiresAt   *time.Time `db:"expires_at"`
	CreatedAt   time.Time  `db:"created_at"`
}

func OAuthGrantEntityToModel(input iamEntity.OAuthGrant) OAuthGrant {
	return OAuthGrant{ID: input.ID,
		ClientID:    input.ClientID,
		UserID:      input.UserID,
		TenantID:    input.TenantID,
		WorkspaceID: input.WorkspaceID,
		Scopes:      input.Scopes,
		GrantedAt:   input.GrantedAt,
		RevokedAt:   input.RevokedAt,
		ExpiresAt:   input.ExpiresAt,
		CreatedAt:   input.CreatedAt}
}
func OAuthGrantModelToEntity(input OAuthGrant) iamEntity.OAuthGrant {
	return iamEntity.OAuthGrant{ID: input.ID,
		ClientID:    input.ClientID,
		UserID:      input.UserID,
		TenantID:    input.TenantID,
		WorkspaceID: input.WorkspaceID,
		Scopes:      input.Scopes,
		GrantedAt:   input.GrantedAt,
		RevokedAt:   input.RevokedAt,
		ExpiresAt:   input.ExpiresAt,
		CreatedAt:   input.CreatedAt}
}

type OAuthToken struct {
	ID               uuid.UUID  `db:"id"`
	ClientID         string     `db:"client_id"`
	UserID           *string    `db:"user_id"`
	GrantID          *uuid.UUID `db:"grant_id"`
	AccessTokenHash  string     `db:"access_token_hash"`
	RefreshTokenHash *string    `db:"refresh_token_hash"`
	Scopes           []string   `db:"scopes"`
	IssuedAt         time.Time  `db:"issued_at"`
	ExpiresAt        time.Time  `db:"expires_at"`
	RotatedAt        *time.Time `db:"rotated_at"`
	RevokedAt        *time.Time `db:"revoked_at"`
	CreatedAt        time.Time  `db:"created_at"`
}

func OAuthTokenEntityToModel(input iamEntity.OAuthToken) OAuthToken {
	return OAuthToken{ID: input.ID,
		ClientID:         input.ClientID,
		UserID:           input.UserID,
		GrantID:          input.GrantID,
		AccessTokenHash:  input.AccessTokenHash,
		RefreshTokenHash: input.RefreshTokenHash,
		Scopes:           input.Scopes,
		IssuedAt:         input.IssuedAt,
		ExpiresAt:        input.ExpiresAt,
		RotatedAt:        input.RotatedAt,
		RevokedAt:        input.RevokedAt,
		CreatedAt:        input.CreatedAt}
}
func OAuthTokenModelToEntity(input OAuthToken) iamEntity.OAuthToken {
	return iamEntity.OAuthToken{ID: input.ID,
		ClientID:         input.ClientID,
		UserID:           input.UserID,
		GrantID:          input.GrantID,
		AccessTokenHash:  input.AccessTokenHash,
		RefreshTokenHash: input.RefreshTokenHash,
		Scopes:           input.Scopes,
		IssuedAt:         input.IssuedAt,
		ExpiresAt:        input.ExpiresAt,
		RotatedAt:        input.RotatedAt,
		RevokedAt:        input.RevokedAt,
		CreatedAt:        input.CreatedAt}
}
