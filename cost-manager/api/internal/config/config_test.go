package config

import (
	"testing"
	"time"
)

func TestLoadConfigWalletAdmissionRelayPolicy(t *testing.T) {
	t.Setenv("WALLET_ADMISSION_REPLICA_ACKS", "0")
	t.Setenv("WALLET_ADMISSION_DURABLE_WAIT", "1500ms")

	cfg := LoadConfig()
	if cfg.WalletAdmissionRelay.ReplicaAcks != 0 {
		t.Fatalf("ReplicaAcks = %d, want 0", cfg.WalletAdmissionRelay.ReplicaAcks)
	}
	if cfg.WalletAdmissionRelay.DurableWait != 1500*time.Millisecond {
		t.Fatalf("DurableWait = %s, want 1500ms", cfg.WalletAdmissionRelay.DurableWait)
	}
}
