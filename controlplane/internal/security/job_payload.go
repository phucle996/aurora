// Package security owns the transport security boundary between a typed
// Controlplane job and its durable outbox representation. Business modules
// provide already-validated routing metadata and receive one opaque byte slice.
package security

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

const (
	maxPlaintextBytes = 1_000_000
	hpkeInfo          = "aurora.platform.job-payload.v1"
)

var (
	ErrKeyUnavailable   = errors.New("job payload encryption key unavailable")
	ErrProtectionFailed = errors.New("job payload protection failed")
)

// Metadata is the stable route fence authenticated as HPKE additional data.
// Attempts, trace context and Kafka offsets are deliberately excluded because
// at-least-once retry must reuse the exact ciphertext committed in PostgreSQL.
type Metadata struct {
	ZoneID               uuid.UUID
	SourceDomain         string
	JobTopic             string
	ResourceID           string
	JobVersion           uint32
	PayloadSchemaVersion uint32
}

type Protected struct {
	Payload []byte
	KeyID   uuid.UUID
}

type Protector interface {
	Seal(context.Context, Metadata, []byte) (*Protected, error)
}

// ZonePayloadKeyResolver is implemented by Hierarchy, the owner of Zone public
// key lifecycle and readiness. Security deliberately has no PostgreSQL/schema
// dependency and trusts the typed result returned by this upstream boundary.
type ZonePayloadKeyResolver interface {
	ResolveZonePayloadKey(context.Context, *hierarchyEntity.ResolveZonePayloadKey) (*hierarchyEntity.ResolveZonePayloadKey, error)
}

type protector struct {
	resolver ZonePayloadKeyResolver
	random   io.Reader
}

// NewProtector returns the single platform codec shared by job-producing
// modules. App bootstrap validates the Hierarchy resolver once; workflow code
// never probes dependencies or reaches into the Hierarchy database directly.
func NewProtector(resolver ZonePayloadKeyResolver) (Protector, error) {
	if resolver == nil {
		return nil, errors.New("jobpayload: Zone payload key resolver is required")
	}
	return &protector{
		resolver: resolver,
		random:   rand.Reader,
	}, nil
}

func (p *protector) Seal(ctx context.Context, metadata Metadata, plaintext []byte) (*Protected, error) {
	resolved, err := p.resolver.ResolveZonePayloadKey(ctx, &hierarchyEntity.ResolveZonePayloadKey{ZoneID: metadata.ZoneID})
	if err != nil {
		// Preserve infrastructure cause for correlated logs/traces while the
		// handler still maps the generic security taxonomy.
		return nil, fmt.Errorf("%w: resolve active Zone key: %w", ErrProtectionFailed, err)
	}
	if resolved == nil || !resolved.Available {
		// Missing, stale or not-yet-loaded key material is one admission
		// outcome. The workflow remains uncommitted and can be retried after
		// the next fresh Zone readiness report.
		return nil, ErrKeyUnavailable
	}
	return seal(p.random, nil, resolved.KeyID, resolved.PublicKey, metadata, plaintext)
}

func seal(randomSource io.Reader, deterministicEphemeralKey []byte, keyID uuid.UUID, recipientPublicKey []byte, metadata Metadata, plaintext []byte) (*Protected, error) {
	if len(plaintext) == 0 || len(plaintext) > maxPlaintextBytes || len(recipientPublicKey) != 32 || len(plaintext) > math.MaxUint32 {
		return nil, ErrProtectionFailed
	}

	curve := ecdh.X25519()
	recipient, err := curve.NewPublicKey(recipientPublicKey)
	if err != nil {
		return nil, ErrProtectionFailed
	}
	var ephemeral *ecdh.PrivateKey
	if deterministicEphemeralKey == nil {
		ephemeral, err = curve.GenerateKey(randomSource)
	} else {
		// This branch exists only for the canonical Go/Rust wire vector. Production
		// always uses crypto/rand through GenerateKey above.
		ephemeral, err = curve.NewPrivateKey(deterministicEphemeralKey)
	}
	if err != nil {
		return nil, ErrProtectionFailed
	}
	sharedDH, err := ephemeral.ECDH(recipient)
	if err != nil {
		return nil, ErrProtectionFailed
	}
	encapsulatedKey := ephemeral.PublicKey().Bytes()
	sharedSecret, err := deriveKEMSharedSecret(sharedDH, encapsulatedKey, recipientPublicKey)
	if err != nil {
		return nil, ErrProtectionFailed
	}
	key, nonce, err := deriveKeySchedule(sharedSecret, []byte(hpkeInfo))
	if err != nil {
		return nil, ErrProtectionFailed
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrProtectionFailed
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrProtectionFailed
	}
	aad, err := additionalData(keyID, metadata)
	if err != nil {
		return nil, ErrProtectionFailed
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, aad)

	wire, err := proto.MarshalOptions{Deterministic: true}.Marshal(&ProtectedPayloadV1{
		SchemaVersion:   1,
		RecipientZoneId: metadata.ZoneID[:],
		KeyId:           keyID[:],
		Encoding:        PayloadEncodingV1_PAYLOAD_ENCODING_HPKE_X25519_HKDF_SHA256_AES_256_GCM,
		EncapsulatedKey: encapsulatedKey,
		Ciphertext:      ciphertext,
		PlaintextSize:   uint32(len(plaintext)),
	})
	if err != nil {
		return nil, ErrProtectionFailed
	}
	return &Protected{Payload: wire, KeyID: keyID}, nil
}

func additionalData(keyID uuid.UUID, metadata Metadata) ([]byte, error) {
	fields := []string{metadata.SourceDomain, metadata.JobTopic, metadata.ResourceID}
	for _, field := range fields {
		if len(field) == 0 || len(field) > math.MaxUint16 {
			return nil, ErrProtectionFailed
		}
	}

	aad := make([]byte, 0, 96+len(metadata.SourceDomain)+len(metadata.JobTopic)+len(metadata.ResourceID))
	aad = append(aad, []byte("AURORA-JOB-PAYLOAD-AAD-V1\x00")...)
	aad = append(aad, keyID[:]...)
	aad = append(aad, metadata.ZoneID[:]...)
	for _, field := range fields {
		var size [2]byte
		binary.BigEndian.PutUint16(size[:], uint16(len(field)))
		aad = append(aad, size[:]...)
		aad = append(aad, field...)
	}
	var versions [8]byte
	binary.BigEndian.PutUint32(versions[0:4], metadata.JobVersion)
	binary.BigEndian.PutUint32(versions[4:8], metadata.PayloadSchemaVersion)
	aad = append(aad, versions[:]...)
	return aad, nil
}

func deriveKEMSharedSecret(dh, encapsulatedKey, recipientPublicKey []byte) ([]byte, error) {
	kemSuiteID := []byte{'K', 'E', 'M', 0x00, 0x20}
	eaePRK := labeledExtract(nil, kemSuiteID, "eae_prk", dh)
	kemContext := make([]byte, 0, len(encapsulatedKey)+len(recipientPublicKey))
	kemContext = append(kemContext, encapsulatedKey...)
	kemContext = append(kemContext, recipientPublicKey...)
	return labeledExpand(eaePRK, kemSuiteID, "shared_secret", kemContext, 32)
}

func deriveKeySchedule(sharedSecret, info []byte) ([]byte, []byte, error) {
	suiteID := []byte{'H', 'P', 'K', 'E', 0x00, 0x20, 0x00, 0x01, 0x00, 0x02}
	pskIDHash := labeledExtract(nil, suiteID, "psk_id_hash", nil)
	infoHash := labeledExtract(nil, suiteID, "info_hash", info)
	context := make([]byte, 0, 1+len(pskIDHash)+len(infoHash))
	context = append(context, 0x00) // HPKE base mode.
	context = append(context, pskIDHash...)
	context = append(context, infoHash...)
	secret := labeledExtract(sharedSecret, suiteID, "secret", nil)
	key, err := labeledExpand(secret, suiteID, "key", context, 32)
	if err != nil {
		return nil, nil, err
	}
	nonce, err := labeledExpand(secret, suiteID, "base_nonce", context, 12)
	if err != nil {
		return nil, nil, err
	}
	return key, nonce, nil
}

func labeledExtract(salt, suiteID []byte, label string, ikm []byte) []byte {
	input := make([]byte, 0, len("HPKE-v1")+len(suiteID)+len(label)+len(ikm))
	input = append(input, "HPKE-v1"...)
	input = append(input, suiteID...)
	input = append(input, label...)
	input = append(input, ikm...)
	mac := hmac.New(sha256.New, salt)
	_, _ = mac.Write(input)
	return mac.Sum(nil)
}

func labeledExpand(prk, suiteID []byte, label string, info []byte, length int) ([]byte, error) {
	if length <= 0 || length > 255*sha256.Size {
		return nil, ErrProtectionFailed
	}
	labeledInfo := make([]byte, 0, 2+len("HPKE-v1")+len(suiteID)+len(label)+len(info))
	var encodedLength [2]byte
	binary.BigEndian.PutUint16(encodedLength[:], uint16(length))
	labeledInfo = append(labeledInfo, encodedLength[:]...)
	labeledInfo = append(labeledInfo, "HPKE-v1"...)
	labeledInfo = append(labeledInfo, suiteID...)
	labeledInfo = append(labeledInfo, label...)
	labeledInfo = append(labeledInfo, info...)

	result := make([]byte, 0, length)
	var previous []byte
	for counter := byte(1); len(result) < length; counter++ {
		mac := hmac.New(sha256.New, prk)
		_, _ = mac.Write(previous)
		_, _ = mac.Write(labeledInfo)
		_, _ = mac.Write([]byte{counter})
		previous = mac.Sum(nil)
		result = append(result, previous...)
	}
	return result[:length], nil
}
