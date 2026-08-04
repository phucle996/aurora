package unit

import (
	"testing"
	"time"

	"github.com/google/uuid"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamModel "controlplane/internal/iam/model"
)

func TestIAMModelEntityConversionsPreserveDurableFields(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	id := uuid.New()
	userID := uuid.New()
	workspaceID := uuid.New()
	roleID := uuid.New()
	phone := "+84900000000"
	secretHint := "ABCD"
	clientDeviceID := id.String()

	user := iamEntity.User{
		ID: id, Username: "ada", Email: "ada@example.com", Phone: &phone,
		PasswordHash: "hash", Status: iamEntity.UserStatusActive, CreatedAt: now, UpdatedAt: now,
	}
	if got := iamModel.UserModelToEntity(iamModel.UserEntityToModel(user)); got != user {
		t.Fatalf("user conversion lost fields: %#v", got)
	}

	history := iamEntity.PasswordHistory{ID: id, UserID: userID, PasswordHash: "old-hash", CreatedAt: now}
	if got := iamModel.PasswordHistoryModelToEntity(iamModel.PasswordHistoryEntityToModel(history)); got != history {
		t.Fatalf("password history conversion lost fields: %#v", got)
	}

	profile := iamEntity.UserProfile{
		UserID: userID, Username: "ada", AccountEmail: "ada@example.com", Phone: &phone,
		Fullname: "Ada Lovelace", Address: stringPtr("London"), AvatarURL: stringPtr("https://avatar"),
		Bio: stringPtr("mathematician"), Locale: "en-GB", Timezone: "Europe/London",
		CreatedAt: now, UpdatedAt: now,
	}
	if got := iamModel.UserProfileModelToEntity(iamModel.UserProfileEntityToModel(profile)); got.UserID != profile.UserID ||
		got.AccountEmail != profile.AccountEmail || *got.Address != *profile.Address {
		t.Fatalf("profile conversion lost fields: %#v", got)
	}

	setting := iamEntity.MFASetting{
		ID: id, UserID: userID, SecretCiphertext: "ciphertext", SecretKeyID: "key-v1",
		CreatedAt: now, UpdatedAt: now,
	}
	if got := iamModel.MFASettingModelToEntity(iamModel.MFASettingEntityToModel(setting)); got != setting {
		t.Fatalf("mfa setting conversion lost fields: %#v", got)
	}

	recovery := iamEntity.MFARecoveryCode{ID: id, MFASettingID: id, CodeHash: "hash", CodeHint: &secretHint, CreatedAt: now}
	if got := iamModel.MFARecoveryCodeModelToEntity(iamModel.MFARecoveryCodeEntityToModel(recovery)); got != recovery {
		t.Fatalf("mfa recovery conversion lost fields: %#v", got)
	}

	device := iamEntity.Device{
		ID: "device-row", UserID: userID, DeviceName: "laptop", DeviceType: stringPtr("desktop"),
		OSName: stringPtr("linux"), BrowserName: stringPtr("browser"), PublicKey: "pub",
		PublicKeyFingerprint: "fingerprint", ClientDeviceID: &clientDeviceID, RevokedAt: &now,
		LastSeenIP: &phone, LastSeenUserAgent: stringPtr("ua"), LastSeenAt: &now,
		CreatedAt: now, UpdatedAt: now,
	}
	deviceModel := iamModel.DeviceEntityToModel(device)
	if got := iamModel.DeviceModelToEntity(deviceModel); got.ID != device.ID ||
		got.ClientDeviceID == nil || *got.ClientDeviceID != *device.ClientDeviceID {
		t.Fatalf("device conversion lost identity fields: %#v", got)
	}

	adminDevice := iamEntity.AdminDevice{
		ID: id, DeviceName: "admin-laptop", DeviceType: stringPtr("desktop"), OSName: stringPtr("linux"),
		BrowserName: stringPtr("browser"), PublicKey: "pub", PublicKeyFingerprint: "fingerprint",
		ClientDeviceID: &clientDeviceID, QuarantinedAt: &now, RevokedAt: &now,
		LastSeenIP: &phone, LastSeenUserAgent: stringPtr("ua"), LastSeenAt: &now,
		CreatedAt: now, UpdatedAt: now,
	}
	adminModel := iamModel.AdminDeviceEntityToModel(adminDevice)
	adminEntity := iamModel.AdminDeviceModelToEntity(adminModel)
	if adminEntity.ID != adminDevice.ID || adminEntity.ClientDeviceID == nil || *adminEntity.ClientDeviceID != id.String() {
		t.Fatalf("admin device conversion lost identity fields: %#v", adminEntity)
	}

	userRole := iamEntity.UserRole{
		ID: id, UserID: userID, Username: "ada", WorkspaceID: workspaceID, RoleID: roleID,
		RoleName: "owner", RoleLevel: 1, RoleVersion: 2, ListPerm: []byte{1, 2}, CreatedAt: now, UpdatedAt: now,
	}
	if got := iamModel.UserRoleModelToEntity(iamModel.UserRoleEntityToModel(userRole)); got.RoleID != roleID ||
		got.WorkspaceID != workspaceID || got.RoleVersion != 2 {
		t.Fatalf("user role conversion lost fields: %#v", got)
	}

	role := iamModel.Role{ID: id, Code: "owner", Name: "Owner", Description: "owner role", RoleLevel: 1, Scope: "platform", CreatedBy: userID, CreatedAt: now, UpdatedAt: now}
	if got := iamModel.RoleModelToEntity(role); got.ID != role.ID || got.CreatedBy != userID {
		t.Fatalf("role conversion lost fields: %#v", got)
	}

	permission := iamModel.Permission{ID: id, Module: "iam", Object: "users", Behavior: "read", Description: "read users", CreatedAt: now, UpdatedAt: now}
	if got := iamModel.PermissionModelToEntity(permission); got.ID != permission.ID || got.Behavior != permission.Behavior {
		t.Fatalf("permission conversion lost fields: %#v", got)
	}
}

func stringPtr(value string) *string {
	return &value
}
