package unit

import (
	"context"
	"testing"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamService "controlplane/internal/iam/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/observability"

	"github.com/google/uuid"
)

type personalDeviceRepositoryStub struct {
	items []iamEntity.PersonalDeviceListItem
	err   error
}

func (r *personalDeviceRepositoryStub) ListDevicesByUserID(context.Context, uuid.UUID, int32, int, int) ([]iamEntity.PersonalDeviceListItem, error) {
	return r.items, r.err
}

func TestPersonalDeviceServicePreservesHierarchyRejection(t *testing.T) {
	repo := &personalDeviceRepositoryStub{err: iamTaxonomy.ErrActionNotAllowed}
	metrics := &deviceWorkflowRecorderSpy{}
	service := iamService.NewPersonalDeviceService(repo, metrics)

	_, err := service.ListUserDevicesPlatform(context.Background(), uuid.New(), 40, 20, 0)
	if err != iamTaxonomy.ErrActionNotAllowed {
		t.Fatalf("expected raw hierarchy rejection, got %v", err)
	}
	if metrics.callCount != 1 || metrics.result != observability.ResultRejected || metrics.reason != observability.ReasonForbidden {
		t.Fatalf("unexpected personal workflow observation: calls=%d result=%s reason=%s", metrics.callCount, metrics.result, metrics.reason)
	}
}

func TestPersonalDeviceServiceReturnsRawRepositoryFailure(t *testing.T) {
	repositoryErr := context.DeadlineExceeded
	repo := &personalDeviceRepositoryStub{err: repositoryErr}
	metrics := &deviceWorkflowRecorderSpy{}
	service := iamService.NewPersonalDeviceService(repo, metrics)

	_, err := service.ListUserDevicesPlatform(context.Background(), uuid.New(), 40, 20, 0)
	if err != repositoryErr {
		t.Fatalf("expected raw repository error identity, got %v", err)
	}
	if metrics.callCount != 1 || metrics.result != observability.ResultFailure || metrics.reason != observability.ReasonTimeout {
		t.Fatalf("unexpected personal failure observation: calls=%d result=%s reason=%s", metrics.callCount, metrics.result, metrics.reason)
	}
}
