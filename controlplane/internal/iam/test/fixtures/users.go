package fixtures

import (
	"github.com/google/uuid"
)

// [COMMENT]: Mock user IDs chuẩn hóa cho môi trường kiểm thử IAM
var (
	TestRootUserID     = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	TestSysAdminID     = uuid.MustParse("00000000-0000-0000-0000-000000000002")
	TestPlatformUserID = uuid.MustParse("00000000-0000-0000-0000-000000000003")
	TestPendingUserID  = uuid.MustParse("00000000-0000-0000-0000-000000000099")
)

// TestUserFixture chứa cấu trúc dữ liệu user đại diện dùng trong unit/integration tests
type TestUserFixture struct {
	ID           uuid.UUID
	Username     string
	Email        string
	PasswordHash string
	Status       string
}

// [COMMENT]: Khởi tạo dữ liệu người dùng mẫu với Argon2id password hash sẵn sàng
func NewTestUserFixture() TestUserFixture {
	return TestUserFixture{
		ID:           TestPlatformUserID,
		Username:     "test_user",
		Email:        "test_user@aurora.cloud",
		PasswordHash: "argon2id$v=19$m=65536,t=1,p=2$vYGk/DrySMoSKrini+XPWw$43iqwaG1qll2360XKiADPE2ng7IbWhsIbMO69N/+KFY",
		Status:       "active",
	}
}
