package handler

import "testing"

func TestValidTraceparent(t *testing.T) {
	t.Parallel()

	if !validTraceparent("") {
		t.Fatal("empty propagation context must remain rolling-compatible")
	}
	if !validTraceparent("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01") {
		t.Fatal("valid W3C traceparent was rejected")
	}
	for _, candidate := range []string{
		"00-00000000000000000000000000000000-00f067aa0ba902b7-01",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01",
		"ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"not-a-traceparent",
	} {
		if validTraceparent(candidate) {
			t.Fatalf("malformed traceparent accepted: %s", candidate)
		}
	}
}
