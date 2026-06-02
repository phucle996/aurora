// ============================================================================
// 📂 FILE: policies/admincidr/compiler.go - Trình Biên Dịch & Xác Thực Admin CIDR
// ============================================================================
//
// 📌 VAI TRÒ (ROLE):
//   - Biên dịch cấu hình thô từ YAML sang định dạng trung gian có kiểm soát (Compiled).
//   - Thực thi triết lý Fail-Fast: Từ chối các cấu hình không hợp lệ ngay tại bước biên dịch.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Tệp [types.go](file:///home/phucle/Desktop/New/controlplane/internal/policyengine/policies/admincidr/types.go) trong cùng package.
//
// 🔒 RANH GIỚI BẢO MẬT/NGHIỆP VỤ (BOUNDARY):
//   - Ngăn chặn các chuỗi CIDR rỗng hoặc không hợp lệ.
//   - Bảo đảm trường `mode` phải có giá trị cụ thể để middleware biết cách hành xử chính xác.
//
// 🔄 CALLSITE FLOW:
//   - Được gọi bởi bộ điều phối chính `runtime.compilePolicies` khi tải hoặc nạp lại cấu hình.
//
// ============================================================================

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
