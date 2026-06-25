package admincidr

import (
	errorx "controlplane/internal/policyengine/errorx"
	"fmt"
	"strings"
)

// Compile phân tích cú pháp, loại bỏ phần tử rỗng và xác thực tính hợp lệ của cấu hình Admin CIDR.
// Nếu cấu hình không hợp lệ, hàm sẽ trả về lỗi được bọc bởi `errorx.ErrPolicyInvalid`.
//
// # Tham số:
//   - `src`: Cấu trúc cấu hình thô `AdminCIDRPolicy` được đọc từ YAML.
//
// # Trả về:
//   - `CompiledPolicy`: Cấu hình đã xác thực thành công.
//   - `error`: Lỗi biên dịch nếu phát hiện dữ liệu không hợp lệ.
func Compile(src AdminCIDRPolicy) (CompiledPolicy, error) {
	allowlist := make([]string, 0, len(src.Allowlist))
	for _, item := range src.Allowlist {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		allowlist = append(allowlist, trimmed)
	}

	if len(allowlist) == 0 {
		return CompiledPolicy{}, fmt.Errorf("%w: admin_cidr: allowlist cannot be empty", errorx.ErrPolicyInvalid)
	}

	mode := strings.TrimSpace(src.Mode)
	if mode == "" {
		return CompiledPolicy{}, fmt.Errorf("%w: admin_cidr: mode is required", errorx.ErrPolicyInvalid)
	}

	return CompiledPolicy{
		Enabled:   src.Enabled,
		Mode:      mode,
		Allowlist: allowlist,
	}, nil
}
