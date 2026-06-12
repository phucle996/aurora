package middleware_metrics

// RecordAuthAttempt ghi nhận một lượt xác thực qua middleware.
func RecordAuthAttempt(middleware, status, reason string) {
	if authRequestsCounter != nil {
		authRequestsCounter.WithLabelValues(middleware, status, reason).Inc()
	}
}

// RecordCacheOperation ghi nhận một thao tác đọc/ghi cache ở tầng middleware.
func RecordCacheOperation(middleware, cacheName, level, outcome string) {
	if cacheOperationsCounter != nil {
		cacheOperationsCounter.WithLabelValues(middleware, cacheName, level, outcome).Inc()
	}
}
