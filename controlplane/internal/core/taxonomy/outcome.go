package coreTaxonomy

const (

	// Reusable Cache & Invalidation outcomes for SRE telemetry
	OutcomeL1InvalidateSuccess = "l1_invalidate_success"
	OutcomeL1InvalidateFailed  = "l1_invalidate_failed"
	OutcomeL1FanoutSuccess     = "l1_fanout_success"
	OutcomeL1FanoutFailed      = "l1_fanout_failed"

	// [COMMENT]: Kết quả truy vấn dữ liệu từ downstream repository
	OutcomeRepoSuccess = "repo_success"
	OutcomeRepoFailed  = "repo_failed"
)
