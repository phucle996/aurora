package security

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
)

var (
	ErrInvalidPublicKey  = errors.New("security: invalid ed25519 public key")
	ErrInvalidPrivateKey = errors.New("security: invalid ed25519 private key")
	ErrInvalidSignature  = errors.New("security: invalid ed25519 signature format")
)

// VerifyEd25519Signature verifies an Ed25519 signature against a message and a base64 encoded public key.
func VerifyEd25519Signature(publicKeyBase64 string, message []byte, signatureBase64 string) (bool, error) {
	pubBytes, err := base64.StdEncoding.DecodeString(publicKeyBase64)
	if err != nil || len(pubBytes) != ed25519.PublicKeySize {
		return false, fmt.Errorf("%w: failed to decode public key", ErrInvalidPublicKey)
	}

	sigBytes, err := base64.StdEncoding.DecodeString(signatureBase64)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return false, fmt.Errorf("%w: failed to decode signature", ErrInvalidSignature)
	}

	pubKey := ed25519.PublicKey(pubBytes)
	return ed25519.Verify(pubKey, message, sigBytes), nil
}

// VerifyEd25519PrivateKey checks if a given private key corresponds to the stored public key.
func VerifyEd25519PrivateKey(publicKeyBase64 string, privateKeyBase64 string) (bool, error) {
	pubBytes, err := base64.StdEncoding.DecodeString(publicKeyBase64)
	if err != nil || len(pubBytes) != ed25519.PublicKeySize {
		return false, fmt.Errorf("%w: failed to decode public key", ErrInvalidPublicKey)
	}

	privBytes, err := base64.StdEncoding.DecodeString(privateKeyBase64)
	if err != nil {
		return false, fmt.Errorf("%w: failed to decode private key", ErrInvalidPrivateKey)
	}

	var derivedPubKey ed25519.PublicKey
	if len(privBytes) == ed25519.PrivateKeySize {
		derivedPubKey = ed25519.PrivateKey(privBytes).Public().(ed25519.PublicKey)
	} else if len(privBytes) == ed25519.SeedSize {
		derivedPubKey = ed25519.NewKeyFromSeed(privBytes).Public().(ed25519.PublicKey)
	} else {
		return false, fmt.Errorf("%w: private key length must be 32 or 64 bytes", ErrInvalidPrivateKey)
	}

	return ed25519.PublicKey(pubBytes).Equal(derivedPubKey), nil
}
