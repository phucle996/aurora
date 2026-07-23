package service

import (
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

func encodeRoleEntry(permissions ...string) []byte {
	var binary []byte
	for _, permission := range permissions {
		binary = protowire.AppendTag(binary, 1, protowire.BytesType)
		binary = protowire.AppendString(binary, permission)
	}
	return binary
}

func TestDecodeBillingPermissions(t *testing.T) {
	permissions, err := decodeBillingPermissions(encodeRoleEntry(
		"billing:tier:read",
		"billing:tier:read",
		"billing:tier:publish",
	))
	if err != nil {
		t.Fatalf("decode Billing permissions: %v", err)
	}
	if len(permissions) != 2 {
		t.Fatalf("expected duplicate permission to be collapsed, got %d entries", len(permissions))
	}
}

func TestDecodeBillingPermissionsRejectsCrossDomainPayload(t *testing.T) {
	if _, err := decodeBillingPermissions(encodeRoleEntry("iam:users:manage")); err == nil {
		t.Fatal("expected cross-domain permission payload to be rejected")
	}
}
