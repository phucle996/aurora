package dto

// RegisterZoneEncryptionKeyRequest contains only transport JSON. The decoded
// X25519 bytes are mapped to a workflow entity by the handler.
type RegisterZoneEncryptionKeyRequest struct {
	PublicKey string `json:"public_key" binding:"required"`
}
