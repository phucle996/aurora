package service

import (
	"context"
	"fmt"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingSvcInterface "cost-manager/api/internal/domain/service"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"cost-manager/api/pkg/security"
)

type authService struct {
	userRepo billingRepoInterface.UserRepository
}

// NewAuthService creates a new AuthService instance
func NewAuthService(userRepo billingRepoInterface.UserRepository) billingSvcInterface.AuthService {
	return &authService{
		userRepo: userRepo,
	}
}

// VerifyCredentials verifies auditor credentials by querying the DB and checking the Ed25519 key/signature
func (s *authService) VerifyCredentials(ctx context.Context, employeeCode, secretKey string) (*entity.User, error) {
	const op = "service.auth.VerifyCredentials"

	// Get user details from repository by employee code
	user, err := s.userRepo.GetByEmployeeCode(ctx, employeeCode)
	if err != nil {
		return nil, err
	}

	if user.Status != "ACTIVE" {
		return nil, billingTaxonomy.ErrNotFound
	}

	// Verify secret key against stored Ed25519 Public Key
	valid, verifyErr := security.VerifyEd25519PrivateKey(user.PublicKey, secretKey)
	if !valid || verifyErr != nil {
		// Try verifying as signature over employee code
		sigValid, sigErr := security.VerifyEd25519Signature(user.PublicKey, []byte(employeeCode), secretKey)
		if !sigValid || sigErr != nil {
			return nil, fmt.Errorf("%s: invalid ed25519 secret key or signature", op)
		}
	}

	return user, nil
}
