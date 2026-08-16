package service_test

import (
	"context"
	"errors"
	"testing"

	"cost-manager/api/internal/domain/entity"
	"cost-manager/api/internal/service"

	"github.com/google/uuid"
)

type mailZoneAdjustmentListRepoStub struct {
	query   entity.MailZoneAdjustmentListQuery
	items   []entity.MailZoneAdjustmentListItem
	hasMore bool
	err     error
}

func (s *mailZoneAdjustmentListRepoStub) ListMailZonePriceAdjustments(_ context.Context, query entity.MailZoneAdjustmentListQuery) ([]entity.MailZoneAdjustmentListItem, bool, error) {
	s.query = query
	return s.items, s.hasMore, s.err
}

func TestMailZoneAdjustmentListPreservesWorkflowProjection(t *testing.T) {
	zoneID := uuid.New()
	item := entity.MailZoneAdjustmentListItem{ID: uuid.New(), ZoneID: zoneID, VersionNumber: 7, IsLatest: true}
	repo := &mailZoneAdjustmentListRepoStub{items: []entity.MailZoneAdjustmentListItem{item}, hasMore: true}
	result, err := service.NewMailZoneAdjustmentListService(repo).ListMailZonePriceAdjustments(
		context.Background(),
		entity.MailZoneAdjustmentListQuery{ZoneID: zoneID, Limit: 25},
	)
	if err != nil {
		t.Fatal(err)
	}
	if repo.query.ZoneID != zoneID || repo.query.Limit != 25 {
		t.Fatalf("repository received the wrong trusted query: %#v", repo.query)
	}
	if result.ZoneID != zoneID || len(result.Items) != 1 || result.Items[0].ID != item.ID || !result.HasMore {
		t.Fatalf("unexpected flat list projection: %#v", result)
	}
	if result.ObservedAt.IsZero() || result.ObservedAt.Location().String() != "UTC" {
		t.Fatalf("observed_at must be populated in UTC: %v", result.ObservedAt)
	}
}

func TestMailZoneAdjustmentListReturnsRepositoryFailure(t *testing.T) {
	want := errors.New("list failed")
	repo := &mailZoneAdjustmentListRepoStub{err: want}
	result, err := service.NewMailZoneAdjustmentListService(repo).ListMailZonePriceAdjustments(
		context.Background(),
		entity.MailZoneAdjustmentListQuery{ZoneID: uuid.New(), Limit: 100},
	)
	if !errors.Is(err, want) || result != nil {
		t.Fatalf("expected repository error without a projection, got result=%#v err=%v", result, err)
	}
}
