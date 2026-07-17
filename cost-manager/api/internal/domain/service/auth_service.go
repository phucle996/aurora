package billingSvcInterface

import (
	"context"
	"cost-manager/api/internal/domain/entity"
)

// AuthService định nghĩa các nghiệp vụ xử lý xác thực kiểm toán
type AuthService interface {
	VerifyCredentials(ctx context.Context, employeeCode, secretKey string) (*entity.User, error)
}
