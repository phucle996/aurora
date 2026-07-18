package iamEntity

import "github.com/google/uuid"

// [COMMENT]: PersonalWalletProvisionEvent là một claim có lease; payload bất biến cho mọi retry.
type PersonalWalletProvisionEvent struct {
	ID       int64
	EventID  uuid.UUID
	Payload  []byte
	Attempts int
}
