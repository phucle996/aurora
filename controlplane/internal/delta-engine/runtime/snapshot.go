package runtime

import "controlplane/internal/delta-engine/types"

// RuntimeSnapshot chứa bản sao bất biến của toàn bộ trạng thái trong RAM tại một thời điểm.
type RuntimeSnapshot struct {
	Zones        map[string]types.ZoneState
	RatePolicies map[string]types.RatePolicyState
	Version      uint64
}

// NewRuntimeSnapshot tạo một snapshot trống ban đầu.
func NewRuntimeSnapshot() *RuntimeSnapshot {
	return &RuntimeSnapshot{
		Zones:        make(map[string]types.ZoneState),
		RatePolicies: make(map[string]types.RatePolicyState),
	}
}

// Clone thực hiện Copy-On-Write bằng cách sao chép nông các bản đồ lưu trữ (shallow copy maps).
// Điều này giúp tránh việc khóa toàn bộ hot-path đọc lúc ghi.
func (s *RuntimeSnapshot) Clone() *RuntimeSnapshot {
	next := &RuntimeSnapshot{
		Zones:        make(map[string]types.ZoneState, len(s.Zones)),
		RatePolicies: make(map[string]types.RatePolicyState, len(s.RatePolicies)),
		Version:      s.Version,
	}
	for k, v := range s.Zones {
		next.Zones[k] = v
	}
	for k, v := range s.RatePolicies {
		next.RatePolicies[k] = v
	}
	return next
}
