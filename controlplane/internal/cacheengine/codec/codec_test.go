package codec

import (
	"reflect"
	"testing"

	iamproto "controlplane/internal/iam/transport/rpc/proto"
)

type simpleStruct struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func TestCodecJSONFallback(t *testing.T) {
	input := simpleStruct{Name: "Antigravity", Age: 1}

	// Marshal
	bytes, err := MarshalData(input)
	if err != nil {
		t.Fatalf("failed to marshal simpleStruct: %v", err)
	}

	// Unmarshal
	var output simpleStruct
	err = UnmarshalData(bytes, &output)
	if err != nil {
		t.Fatalf("failed to unmarshal simpleStruct: %v", err)
	}

	if output.Name != input.Name || output.Age != input.Age {
		t.Errorf("unmarshaled data mismatch: got %+v, want %+v", output, input)
	}
}

func TestCodecProtobuf(t *testing.T) {
	input := &iamproto.RoleEntry{
		Permissions: []string{"read", "write"},
	}

	// Marshal
	bytes, err := MarshalData(input)
	if err != nil {
		t.Fatalf("failed to marshal Protobuf: %v", err)
	}

	// Unmarshal to pointer-to-pointer (**iamproto.RoleEntry)
	var output *iamproto.RoleEntry
	err = UnmarshalData(bytes, &output)
	if err != nil {
		t.Fatalf("failed to unmarshal Protobuf: %v", err)
	}

	if output == nil {
		t.Fatal("expected unmarshaled output to be non-nil")
	}

	if !reflect.DeepEqual(output.Permissions, input.Permissions) {
		t.Errorf("unmarshaled permissions mismatch: got %v, want %v", output.Permissions, input.Permissions)
	}
}

// Benchmark cho Marshal JSON (Fallback)
func BenchmarkMarshalJSON(b *testing.B) {
	input := simpleStruct{Name: "Antigravity-JSON-Benchmark", Age: 42}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := MarshalData(input)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark cho Marshal Protobuf (Optimized)
func BenchmarkMarshalProtobuf(b *testing.B) {
	input := &iamproto.RoleEntry{
		Permissions: []string{"read", "write", "update", "delete", "admin"},
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := MarshalData(input)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark cho Unmarshal JSON (Fallback)
func BenchmarkUnmarshalJSON(b *testing.B) {
	input := simpleStruct{Name: "Antigravity-JSON-Benchmark", Age: 42}
	bytes, _ := MarshalData(input)
	var output simpleStruct

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		err := UnmarshalData(bytes, &output)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark cho Unmarshal Protobuf (Optimized)
func BenchmarkUnmarshalProtobuf(b *testing.B) {
	input := &iamproto.RoleEntry{
		Permissions: []string{"read", "write", "update", "delete", "admin"},
	}
	bytes, _ := MarshalData(input)
	var output *iamproto.RoleEntry

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		err := UnmarshalData(bytes, &output)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark cho Unmarshal Protobuf (Preallocated)
func BenchmarkUnmarshalProtobufPreallocated(b *testing.B) {
	input := &iamproto.RoleEntry{
		Permissions: []string{"read", "write", "update", "delete", "admin"},
	}
	bytes, _ := MarshalData(input)
	output := &iamproto.RoleEntry{}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		output.Permissions = nil
		err := UnmarshalData(bytes, output)
		if err != nil {
			b.Fatal(err)
		}
	}
}
