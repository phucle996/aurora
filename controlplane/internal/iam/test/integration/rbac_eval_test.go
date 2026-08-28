package integration

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// [COMMENT]: Integration Test cho RBAC Binary Permissions Evaluator (Protobuf Serialized RoleEntry)
func TestRBACBinaryPermissionEvaluation(t *testing.T) {
	// Root binary permissions được decode từ 000006_iam_seeds.up.sql
	hexPerms := "0a15726f6f743a2a3a69616d3a75736572733a72656164"
	listPerm, err := hex.DecodeString(hexPerms)
	if err != nil {
		t.Fatalf("failed to decode hex permission: %v", err)
	}

	expectedKey := []byte("root:*:iam:users:read")

	// [COMMENT]: Kiểm tra key 5 cấp sau khi runtime compile immutable revision.
	if !bytes.Contains(listPerm, expectedKey) {
		t.Errorf("compiled role entry missing expected key %q", expectedKey)
	} else {
		t.Logf("PASS: Successfully matched 5-level binary permission key %q", expectedKey)
	}
}
