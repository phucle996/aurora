package ratelimit

import (
	"math"
	"strconv"
	"time"
)

const (
	HeaderRateLimitLimit     = "X-RateLimit-Limit"
	HeaderRateLimitRemaining = "X-RateLimit-Remaining"
	HeaderRateLimitReset     = "X-RateLimit-Reset"
	HeaderRetryAfter         = "Retry-After"
)

// TokensFloor converts remaining tokens to a non-negative integer.
func TokensFloor(tokens float64) int64 {
	if tokens <= 0 {
		return 0
	}
	return int64(math.Floor(tokens))
}

// RetryAfterSeconds converts a duration to seconds (ceil, non-negative).
func RetryAfterSeconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int(math.Ceil(float64(d) / float64(time.Second)))
}

// RateLimitHeaders builds common rate-limit headers.
// Reset is expressed as seconds until the bucket is full again.
func RateLimitHeaders(res Result) map[string]string {
	headers := map[string]string{
		HeaderRateLimitLimit:     strconv.FormatInt(res.Limit, 10),
		HeaderRateLimitRemaining: strconv.FormatInt(TokensFloor(res.Remaining), 10),
		HeaderRateLimitReset:     strconv.Itoa(RetryAfterSeconds(res.ResetAfter)),
	}
	if !res.Allowed {
		retry := RetryAfterSeconds(res.RetryAfter)
		if retry > 0 {
			headers[HeaderRetryAfter] = strconv.Itoa(retry)
		}
	}
	return headers
}

func toFloat64(v interface{}) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case int32:
		return float64(t), true
	case int16:
		return float64(t), true
	case int8:
		return float64(t), true
	case uint:
		return float64(t), true
	case uint64:
		return float64(t), true
	case uint32:
		return float64(t), true
	case uint16:
		return float64(t), true
	case uint8:
		return float64(t), true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	case []byte:
		f, err := strconv.ParseFloat(string(t), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func toInt64(v interface{}) (int64, bool) {
	switch t := v.(type) {
	case int64:
		return t, true
	case int:
		return int64(t), true
	case int32:
		return int64(t), true
	case int16:
		return int64(t), true
	case int8:
		return int64(t), true
	case uint:
		return int64(t), true
	case uint64:
		const maxInt64 = int64(^uint64(0) >> 1)
		if t > uint64(maxInt64) {
			return 0, false
		}
		return int64(t), true
	case uint32:
		return int64(t), true
	case uint16:
		return int64(t), true
	case uint8:
		return int64(t), true
	case float64:
		return int64(t), true
	case float32:
		return int64(t), true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return 0, false
		}
		return int64(f), true
	case []byte:
		f, err := strconv.ParseFloat(string(t), 64)
		if err != nil {
			return 0, false
		}
		return int64(f), true
	default:
		return 0, false
	}
}
