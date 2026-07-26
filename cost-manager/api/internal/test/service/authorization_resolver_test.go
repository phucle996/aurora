package service_test

import (
	"testing"

	billingService "cost-manager/api/internal/service"
)

func TestNewAuthorizationResolverRequiresBothRedisBoundaries(t *testing.T) {
	if _, err := billingService.NewAuthorizationResolver(nil, nil); err == nil {
		t.Fatal("expected resolver construction to reject missing Redis boundaries")
	}
}
