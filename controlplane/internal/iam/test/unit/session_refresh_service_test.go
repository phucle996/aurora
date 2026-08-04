package unit

import (
	"context"
	"errors"
	"testing"
	"time"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamServiceContract "controlplane/internal/iam/domain/service"
	iamService "controlplane/internal/iam/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/observability"
	"controlplane/internal/security"

	"github.com/google/uuid"
)

type refreshTokenRepositoryStub struct {
	issued     *iamEntity.IssueDeviceRefreshToken
	issueErr   error
	recovered  *iamEntity.RecoverUserSession
	recoverErr error
	deleted    string
	deleteErr  error
}

func (stub *refreshTokenRepositoryStub) IssueDeviceRefreshToken(_ context.Context, in *iamEntity.IssueDeviceRefreshToken) error {
	stub.issued = in
	return stub.issueErr
}

func (stub *refreshTokenRepositoryStub) RecoverUserSession(_ context.Context, in *iamEntity.RecoverUserSession) (*iamEntity.RecoverUserSession, error) {
	if stub.recovered == nil {
		return nil, stub.recoverErr
	}
	out := *stub.recovered
	out.TokenHash = in.TokenHash
	out.RequestedTenantID = in.RequestedTenantID
	out.Now = in.Now
	return &out, stub.recoverErr
}

func (stub *refreshTokenRepositoryStub) DeleteByHash(_ context.Context, tokenHash string) (int64, error) {
	stub.deleted = tokenHash
	if stub.deleteErr != nil {
		return 0, stub.deleteErr
	}
	return 1, nil
}

func newSessionRefreshService(stub *refreshTokenRepositoryStub) iamServiceContract.SessionRefreshService {
	return iamService.NewSessionRefreshService(
		&config.Config{Security: config.SecurityCfg{RefreshTokenTTL: 24 * time.Hour}},
		stub,
		observability.NewNoopWorkflowRecorder(),
	)
}

func TestSessionRefreshIssueBindsOnlyUserAndDevice(t *testing.T) {
	stub := &refreshTokenRepositoryStub{}
	service := newSessionRefreshService(stub)
	userID, deviceID := uuid.New(), uuid.New()

	raw, expiresAt, err := service.IssueDeviceRefreshToken(context.Background(), userID, deviceID)
	if err != nil {
		t.Fatalf("issue refresh credential: %v", err)
	}
	if stub.issued == nil {
		t.Fatal("repository did not receive issuance entity")
	}
	if stub.issued.UserID != userID || stub.issued.DeviceID != deviceID {
		t.Fatalf("credential binding mismatch: %#v", stub.issued)
	}
	if stub.issued.TokenHash != security.HashTokenSHA256(raw) || stub.issued.TokenHash == raw {
		t.Fatal("repository must receive only the SHA-256 token digest")
	}
	if !expiresAt.Equal(stub.issued.ExpiresAt) || expiresAt.Sub(stub.issued.IssuedAt) != 24*time.Hour {
		t.Fatalf("unexpected absolute expiry: issued=%v expires=%v", stub.issued.IssuedAt, expiresAt)
	}
}

func TestSessionRefreshIssueRejectsInactiveDevice(t *testing.T) {
	stub := &refreshTokenRepositoryStub{issueErr: iamTaxonomy.ErrNotFound}
	service := newSessionRefreshService(stub)

	_, _, err := service.IssueDeviceRefreshToken(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, iamTaxonomy.ErrInvalidCredential) {
		t.Fatalf("expected invalid credential taxonomy, got %v", err)
	}
}

func TestSessionRecoveryMapsCredentialAndContextOutcomes(t *testing.T) {
	userID, deviceID, tenantID := uuid.New(), uuid.New(), uuid.New()
	tests := []struct {
		name         string
		stub         *refreshTokenRepositoryStub
		requested    *uuid.UUID
		wantValid    bool
		wantAccess   bool
		wantFallback bool
		wantErr      error
	}{
		{
			name: "tenant authorized",
			stub: &refreshTokenRepositoryStub{recovered: &iamEntity.RecoverUserSession{
				CredentialValid: true, ContextAuthorized: true, UserID: userID,
				DeviceID: deviceID, RoleLevel: 8, ResolvedTenantID: &tenantID,
			}},
			requested: &tenantID, wantValid: true, wantAccess: true,
		},
		{
			name: "credential rejected",
			stub: &refreshTokenRepositoryStub{
				recovered: &iamEntity.RecoverUserSession{}, recoverErr: iamTaxonomy.ErrInvalidCredential,
			},
		},
		{
			name: "stale tenant context",
			stub: &refreshTokenRepositoryStub{
				recovered: &iamEntity.RecoverUserSession{
					CredentialValid: true, PersonalFallbackAuthorized: true,
				},
				recoverErr: iamTaxonomy.ErrActionNotAllowed,
			},
			requested: &tenantID, wantValid: true, wantFallback: true,
		},
		{
			name:    "database unavailable",
			stub:    &refreshTokenRepositoryStub{recoverErr: errors.New("database unavailable")},
			wantErr: iamTaxonomy.ErrAuthenticationUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newSessionRefreshService(test.stub)
			result, err := service.RecoverUserSession(context.Background(), "opaque-refresh-token", test.requested)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("expected %v, got %v", test.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected recovery error: %v", err)
			}
			if result == nil || result.CredentialValid != test.wantValid ||
				result.ContextAuthorized != test.wantAccess || result.PersonalFallbackAuthorized != test.wantFallback {
				t.Fatalf("unexpected recovery outcome: %#v", result)
			}
			if test.stub.recovered != nil && result.TokenHash != security.HashTokenSHA256("opaque-refresh-token") {
				t.Fatal("service did not hash the raw credential before repository lookup")
			}
		})
	}
}

func TestSessionRefreshRevokeIsIdempotent(t *testing.T) {
	stub := &refreshTokenRepositoryStub{deleteErr: iamTaxonomy.ErrNotFound}
	service := newSessionRefreshService(stub)

	if err := service.RevokeOpaqueRefreshToken(context.Background(), "opaque-refresh-token"); err != nil {
		t.Fatalf("already absent credential must be successful: %v", err)
	}
	if stub.deleted != security.HashTokenSHA256("opaque-refresh-token") {
		t.Fatal("revoke repository did not receive the token digest")
	}
}
