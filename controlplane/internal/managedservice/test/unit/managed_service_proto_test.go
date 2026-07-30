package unit

import (
	"testing"

	managedserviceproto "controlplane/internal/managedservice/transport/proto"

	"google.golang.org/protobuf/proto"
)

func TestManagedServiceCommandEnvelopeFieldContract(t *testing.T) {
	command := &managedserviceproto.ManagedServiceCommandV1{
		CommandEventId:          make([]byte, 16),
		OperationId:             make([]byte, 16),
		InstanceId:              make([]byte, 16),
		OwnerType:               managedserviceproto.ManagedServiceOwnerTypeV1_MANAGED_SERVICE_OWNER_TYPE_PERSONAL,
		OwnerId:                 make([]byte, 16),
		WorkspaceId:             make([]byte, 16),
		ZoneId:                  make([]byte, 16),
		InstanceCode:            "orders-kafka",
		OperationKind:           managedserviceproto.ManagedServiceOperationKindV1_MANAGED_SERVICE_OPERATION_KIND_CREATE,
		Generation:              1,
		InstanceRevisionId:      make([]byte, 16),
		BlueprintRevisionId:     make([]byte, 16),
		TemplateYaml:            "apiVersion: v1\nkind: ConfigMap\n",
		BundleHash:              make([]byte, 32),
		ComponentContractHash:   make([]byte, 32),
		InputHash:               make([]byte, 32),
		DesiredSpecHash:         make([]byte, 32),
		ParameterEnvelope:       []byte("fixture-envelope-v1"),
		ParameterEnvelopeSha256: make([]byte, 32),
		SchemaVersion:           1,
	}
	encoded, err := proto.Marshal(command)
	if err != nil {
		t.Fatalf("marshal command: %v", err)
	}
	var decoded managedserviceproto.ManagedServiceCommandV1
	if err := proto.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal command: %v", err)
	}
	if got := string(decoded.GetParameterEnvelope()); got != "fixture-envelope-v1" {
		t.Fatalf("parameter envelope mismatch: %q", got)
	}

	fields := managedserviceproto.File_managed_service_proto.Messages().ByName("ManagedServiceCommandV1").Fields()
	if got := fields.ByName("parameter_envelope").Number(); got != 20 {
		t.Fatalf("parameter_envelope field number = %d, want 20", got)
	}
	if got := fields.ByName("parameter_envelope_sha256").Number(); got != 21 {
		t.Fatalf("parameter_envelope_sha256 field number = %d, want 21", got)
	}
}

func TestManagedServiceResultReservesRemovedFieldFourteen(t *testing.T) {
	ranges := managedserviceproto.File_managed_service_proto.Messages().ByName("ManagedServiceResultV1").ReservedRanges()
	if !ranges.Has(14) {
		t.Fatal("ManagedServiceResultV1 must reserve removed field 14")
	}
}
