package entity

import (
	"time"

	"github.com/google/uuid"
)

type ZoneEncryptionKeyStatus string

const (
	ZoneEncryptionKeyStatusStaged      ZoneEncryptionKeyStatus = "staged"
	ZoneEncryptionKeyStatusActive      ZoneEncryptionKeyStatus = "active"
	ZoneEncryptionKeyStatusDecryptOnly ZoneEncryptionKeyStatus = "decrypt_only"
	ZoneEncryptionKeyStatusRetired     ZoneEncryptionKeyStatus = "retired"

	// ZoneEncryptionKeyAlgorithm is fixed for V1 so producers and Dataplane do
	// not negotiate a weaker suite through business data.
	ZoneEncryptionKeyAlgorithm = "HPKE_X25519_HKDF_SHA256_AES_256_GCM"
)

// RegisterZoneEncryptionKey is the single business entity of the register
// workflow. ID, fingerprint, algorithm and initial status are system-owned and
// are populated by the service before persistence.
type RegisterZoneEncryptionKey struct {
	ID           uuid.UUID
	ZoneID       uuid.UUID
	Actor        string
	ProofID      uuid.UUID
	PublicKey    []byte
	Fingerprint  []byte
	Algorithm    string
	Status       ZoneEncryptionKeyStatus
	RegisteredBy string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ListZoneEncryptionKeys is deliberately independent from mutation entities.
// PublicKey is public capability material; private key bytes never enter this
// module or this entity pipeline.
type ListZoneEncryptionKeys struct {
	ZoneID          uuid.UUID
	Limit           int
	HasCursor       bool
	CursorCreatedAt time.Time
	CursorID        uuid.UUID
	ID              uuid.UUID
	PublicKey       []byte
	Fingerprint     []byte
	Algorithm       string
	Status          ZoneEncryptionKeyStatus
	RegisteredBy    string
	ActivatedBy     string
	DecryptOnlyBy   string
	RetiredBy       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ActivatedAt     *time.Time
	DecryptOnlyAt   *time.Time
	RetiredAt       *time.Time
}

// ActivateZoneEncryptionKey owns the STAGED -> ACTIVE workflow. The repository
// serializes on the Zone row so concurrent SRE requests cannot leave two ACTIVE
// keys for one Zone.
type ActivateZoneEncryptionKey struct {
	ZoneID       uuid.UUID
	KeyID        uuid.UUID
	Actor        string
	ProofID      uuid.UUID
	PublicKey    []byte
	Fingerprint  []byte
	Algorithm    string
	Status       ZoneEncryptionKeyStatus
	ActivatedBy  string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ActivatedAt  *time.Time
	StateChanged bool
}

// RetireZoneEncryptionKey owns the STAGED|DECRYPT_ONLY -> RETIRED workflow.
// ACTIVE is intentionally excluded so an operator cannot destroy the only
// encryption capability selected for new commands.
type RetireZoneEncryptionKey struct {
	ZoneID       uuid.UUID
	KeyID        uuid.UUID
	Actor        string
	ProofID      uuid.UUID
	PublicKey    []byte
	Fingerprint  []byte
	Algorithm    string
	Status       ZoneEncryptionKeyStatus
	RetiredBy    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	RetiredAt    *time.Time
	StateChanged bool
}
