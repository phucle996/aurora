package migrations

import (
	"encoding/hex"
	"reflect"
	"regexp"
	"strings"
	"testing"

	iamproto "controlplane/internal/iam/transport/rpc/proto"

	"google.golang.org/protobuf/proto"
)

func TestBootstrapRoleEntriesMatchSeededPermissions(t *testing.T) {
	sql, err := Files.ReadFile("000006_iam_seeds.up.sql")
	if err != nil {
		t.Fatalf("read IAM bootstrap seed: %v", err)
	}

	// [COMMENT]: Chỉ bốn bootstrap RoleEntry có hex literal không rỗng; workspace seed dùng binary rỗng riêng.
	matches := regexp.MustCompile(`decode\('([0-9a-f]+)', 'hex'\)`).FindAllStringSubmatch(string(sql), -1)
	if len(matches) != 4 {
		t.Fatalf("expected 4 precompiled bootstrap RoleEntry values, got %d", len(matches))
	}

	allPermissions := []string{
		"iam:users:read",
		"iam:users:manage",
		"iam:role:read",
		"iam:role:write",
		"iam:role:assign",
		"iam:role:delete",
		"iam:permissions:read",
		"storage:bucket:read",
		"storage:bucket:write",
		"storage:bucket:delete",
		"storage:credential:read",
		"storage:credential:write",
		"storage:credential:delete",
		"hierarchy:workspace:create",
		"hierarchy:workspace:read",
		"hierarchy:workspace:update",
		"hierarchy:workspace:delete",
		"iam:device:read",
		"iam:mfa:view",
		"email:consumer:create",
		"email:consumer:read",
		"email:consumer:update",
		"email:consumer:delete",
		"email:template:create",
		"email:template:read",
		"email:template:publish",
		"email:template:delete",
	}
	readOnlyPermissions := []string{
		"iam:users:read",
		"iam:role:read",
		"iam:permissions:read",
		"storage:bucket:read",
		"storage:credential:read",
		"hierarchy:workspace:read",
		"iam:device:read",
		"iam:mfa:view",
		"email:consumer:read",
		"email:template:read",
	}

	tests := []struct {
		username string
		want     []string
	}{
		{username: "root", want: allPermissions},
		{username: "sys_admin", want: allPermissions},
		{username: "support_operator", want: readOnlyPermissions},
		{username: "audit_viewer", want: readOnlyPermissions},
	}

	for i, tt := range tests {
		t.Run(tt.username, func(t *testing.T) {
			binaryEntry, err := hex.DecodeString(matches[i][1])
			if err != nil {
				t.Fatalf("decode seeded RoleEntry hex: %v", err)
			}

			var entry iamproto.RoleEntry
			if err := proto.Unmarshal(binaryEntry, &entry); err != nil {
				t.Fatalf("unmarshal seeded RoleEntry: %v", err)
			}

			// [COMMENT]: So sánh toàn bộ snapshot để rebuild Email không làm rơi quyền cũ hoặc cấp mutation cho support/audit.
			got := make([]string, 0, len(entry.Permissions))
			prefix := tt.username + ":*:"
			for _, permission := range entry.Permissions {
				if !strings.HasPrefix(permission, prefix) {
					t.Fatalf("permission %q does not use bootstrap identity prefix %q", permission, prefix)
				}
				got = append(got, strings.TrimPrefix(permission, prefix))
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("permissions mismatch\nwant: %#v\n got: %#v", tt.want, got)
			}
		})
	}
}

func TestIAMSeedRollbackCoversPermissionCatalog(t *testing.T) {
	upSQL, err := Files.ReadFile("000006_iam_seeds.up.sql")
	if err != nil {
		t.Fatalf("read IAM bootstrap up migration: %v", err)
	}
	downSQL, err := Files.ReadFile("000006_iam_seeds.down.sql")
	if err != nil {
		t.Fatalf("read IAM bootstrap down migration: %v", err)
	}

	// [COMMENT]: Chỉ đọc đúng statement permission để các VALUES của role/user không bị nhận nhầm là catalog.
	_, upPermissionSQL, found := strings.Cut(string(upSQL), "INSERT INTO permissions")
	if !found {
		t.Fatal("up migration does not contain permission seed statement")
	}
	upPermissionSQL, _, found = strings.Cut(upPermissionSQL, "-- ----------------------------------------------------------------------------\n-- 3)")
	if !found {
		t.Fatal("up migration permission statement has no role-section boundary")
	}
	_, downPermissionSQL, found := strings.Cut(string(downSQL), "DELETE FROM permissions")
	if !found {
		t.Fatal("down migration does not contain permission rollback statement")
	}
	downPermissionSQL, _, found = strings.Cut(downPermissionSQL, "DELETE FROM user_profiles")
	if !found {
		t.Fatal("down migration permission statement has no profile-section boundary")
	}

	// [COMMENT]: Rollback phải theo đúng triple identity; permission table không có cột code dạng legacy.
	upPattern := regexp.MustCompile(`\(gen_random_uuid\(\), '([a-z]+)', '([a-z]+)', '([a-z]+)',`)
	downPattern := regexp.MustCompile(`\('([a-z]+)', '([a-z]+)', '([a-z]+)'\)`)
	upMatches := upPattern.FindAllStringSubmatch(upPermissionSQL, -1)
	downMatches := downPattern.FindAllStringSubmatch(downPermissionSQL, -1)
	if len(upMatches) == 0 || len(upMatches) != len(downMatches) {
		t.Fatalf("permission catalog mismatch: up=%d down=%d", len(upMatches), len(downMatches))
	}

	downCatalog := make(map[string]struct{}, len(downMatches))
	for _, match := range downMatches {
		downCatalog[strings.Join(match[1:], ":")] = struct{}{}
	}
	for _, match := range upMatches {
		permission := strings.Join(match[1:], ":")
		if _, ok := downCatalog[permission]; !ok {
			t.Errorf("rollback does not remove seeded permission %q", permission)
		}
	}
}
