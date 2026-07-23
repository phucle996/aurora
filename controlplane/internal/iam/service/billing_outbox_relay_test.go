package iamSvcImpl

import (
	"testing"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamproto "controlplane/internal/iam/transport/rpc/proto"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

func TestBillingOutboxNotifyCoalescesBurst(t *testing.T) {
	relay := &BillingOutboxRelay{wake: make(chan struct{}, 1)}
	relay.Notify()
	relay.Notify()

	// [COMMENT]: Burst chỉ giữ một wake hint; durable rows không nằm trong channel nên không thể mất event.
	if got := len(relay.wake); got != 1 {
		t.Fatalf("expected one coalesced wake signal, got %d", got)
	}
}

func TestBillingOutboxFallbackDelayHasBoundedJitter(t *testing.T) {
	delay := billingOutboxFallbackDelay()
	if delay < billingOutboxFallbackInterval || delay >= billingOutboxFallbackInterval+billingOutboxFallbackJitter {
		t.Fatalf("fallback delay %s is outside [%s, %s)", delay, billingOutboxFallbackInterval, billingOutboxFallbackInterval+billingOutboxFallbackJitter)
	}
	if billingOutboxFallbackInterval < 10*time.Second {
		t.Fatal("fallback interval is too aggressive for idle database reconciliation")
	}
}

func TestValidBillingEventRejectsOwnerMismatch(t *testing.T) {
	eventID := uuid.New()
	ownerID := uuid.New()
	wire := &iamproto.PersonalWalletProvisionRequestedV1{
		EventId: eventID[:], SchemaVersion: 1, OwnerId: ownerID[:], OwnerType: "PERSONAL", Currency: "USD",
	}
	payload, err := proto.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}

	// [COMMENT]: Row metadata và protobuf payload phải đồng nhất trước khi event vượt trust boundary Shared Redis.
	event := iamEntity.BillingOutboxEvent{
		EventID: eventID, EventType: personalWalletProvisionEventType,
		OwnerID: uuid.New(), OwnerType: "PERSONAL", Payload: payload,
	}
	if validBillingEvent(event) {
		t.Fatal("expected owner mismatch to be rejected")
	}

	event.OwnerID = ownerID
	if !validBillingEvent(event) {
		t.Fatal("expected matching billing event to be accepted")
	}
}
