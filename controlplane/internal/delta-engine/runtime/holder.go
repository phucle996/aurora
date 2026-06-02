package runtime

import (
	"sync"
	"sync/atomic"
)

// SnapshotHolder sử dụng atomic.Pointer để quản lý trạng thái nguyên tử,
// hỗ trợ các hot-path đọc dữ liệu mà không cần tranh chấp Lock (Lock-Free reads).
type SnapshotHolder struct {
	ptr atomic.Pointer[RuntimeSnapshot]
	mu  sync.Mutex // Dùng lock để tuần tự hóa quá trình ghi (serialize writes), loại bỏ race condition.
}

// NewSnapshotHolder khởi tạo holder với một snapshot trống.
func NewSnapshotHolder() *SnapshotHolder {
	h := &SnapshotHolder{}
	h.ptr.Store(NewRuntimeSnapshot())
	return h
}

// Get trả về snapshot hiện tại (lock-free). Phục vụ hot-path đọc của Controlplane và Dataplane.
func (h *SnapshotHolder) Get() *RuntimeSnapshot {
	return h.ptr.Load()
}

// Update áp dụng một hàm thay đổi trạng thái (mutation function) thông qua cơ chế Copy-on-Write.
func (h *SnapshotHolder) Update(mutateFn func(*RuntimeSnapshot) *RuntimeSnapshot) {
	h.mu.Lock()
	defer h.mu.Unlock()

	current := h.ptr.Load()
	next := mutateFn(current)
	h.ptr.Store(next)
}
