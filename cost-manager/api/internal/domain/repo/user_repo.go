package billingRepoInterface

import (
	"context"
	"cost-manager/api/internal/domain/entity"
)

// UserRepository định nghĩa các thao tác cơ sở dữ liệu cho User
type UserRepository interface {
	GetByEmployeeCode(ctx context.Context, employeeCode string) (*entity.User, error)
}
