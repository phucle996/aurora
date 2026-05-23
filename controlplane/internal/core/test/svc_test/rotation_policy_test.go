package svc_test

import (
	"testing"
	"time"

	coreSvcImpl "controlplane/internal/core/service"
)

func TestComputeRotationInterval(t *testing.T) {
	cases := []struct {
		name string
		ttl  time.Duration
		want time.Duration
	}{
		{name: "ttl less than 24h", ttl: 6 * time.Hour, want: 24 * time.Hour},
		{name: "ttl equal 24h", ttl: 24 * time.Hour, want: 48 * time.Hour},
		{name: "ttl greater than 24h", ttl: 72 * time.Hour, want: 144 * time.Hour},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := coreSvcImpl.ComputeRotationInterval(tc.ttl)
			if got != tc.want {
				t.Fatalf("ComputeRotationInterval() = %s, want %s", got, tc.want)
			}
		})
	}
}
