package mocks

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMockCacheMissingAndDelete(t *testing.T) {
	cache := NewMockCacheEngine()
	ctx := context.Background()

	if value, err := cache.Get(ctx, "missing"); err != nil || value != "" {
		t.Fatalf("missing key should return empty value without error, got %q, %v", value, err)
	}
	if err := cache.Set(ctx, "key", "value", time.Minute); err != nil {
		t.Fatalf("set cache value: %v", err)
	}
	if value, err := cache.Get(ctx, "key"); err != nil || value != "value" {
		t.Fatalf("stored key should return its value, got %q, %v", value, err)
	}
	if err := cache.Delete(ctx, "key"); err != nil {
		t.Fatalf("delete cache value: %v", err)
	}
	if value, err := cache.Get(ctx, "key"); err != nil || value != "" {
		t.Fatalf("deleted key should be absent, got %q, %v", value, err)
	}
}

func TestMockPublisherErrorAndSnapshot(t *testing.T) {
	publisher := NewMockAccountVerificationPublisher()
	publisher.PublishError = errors.New("broker unavailable")
	if err := publisher.PublishVerificationEvent(context.Background(), "user", "user@example.com", "token"); !errors.Is(err, publisher.PublishError) {
		t.Fatalf("expected configured publisher error, got %v", err)
	}

	publisher.PublishError = nil
	if err := publisher.PublishVerificationEvent(context.Background(), "user", "user@example.com", "token"); err != nil {
		t.Fatalf("publish event: %v", err)
	}
	events := publisher.GetPublishedEvents()
	if len(events) != 1 {
		t.Fatalf("expected one event, got %d", len(events))
	}
	events[0]["email"] = "mutated@example.com"
	if publisher.GetPublishedEvents()[0]["email"] != "user@example.com" {
		t.Fatal("published event snapshot must not expose the original map")
	}
}
