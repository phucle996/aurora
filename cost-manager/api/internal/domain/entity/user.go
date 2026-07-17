package entity

import (
	"time"

	"github.com/google/uuid"
)

// User đại diện cho nhân sự kiểm toán nội bộ trong hệ thống (bảng billing.users)
type User struct {
	ID             uuid.UUID
	EmployeeCode   string
	PublicKey      string
	Fullname       string
	Email          string
	RoleID         string
	Level          int
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
