package res

// [COMMENT]: ConsumerSummaryResponse chứa số liệu tổng hợp về trạng thái của các Kafka Consumer trong workspace.
type ConsumerSummaryResponse struct {
	Total                int64 `json:"total"`
	Enabled              int64 `json:"enabled"`
	Paused               int64 `json:"paused"`
	NeedsCredentials     int64 `json:"needs_credentials"`
	Running              int64 `json:"running"`
	Degraded             int64 `json:"degraded"`
	Error                int64 `json:"error"`
	WithoutFreshRuntime  int64 `json:"without_fresh_runtime"`
	ActiveLogicalSlots   int64 `json:"active_logical_slots"`
}

// [COMMENT]: TemplateSummaryResponse chứa số liệu tổng hợp về danh mục email template bất biến trong workspace.
type TemplateSummaryResponse struct {
	Total                  int64 `json:"total"`
	InUse                  int64 `json:"in_use"`
	Unused                 int64 `json:"unused"`
	TotalImmutableVersions int64 `json:"total_immutable_versions"`
	RecentlyUpdated        int64 `json:"recently_updated"`
}
