package coreSvcImpl

import "time"

func ComputeRotationInterval(ttl time.Duration) time.Duration {
	if ttl < 24*time.Hour {
		return 24 * time.Hour
	}
	return ttl * 2
}
