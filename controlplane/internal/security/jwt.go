package security

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// JWTAlgHS256 is the symmetric signing algorithm used by this package.
	JWTAlgHS256 = "HS256"
	// JWTType is the compact JWT type header value.
	JWTType = "JWT"
)

var (
	// ErrEmptySecret is returned when signing or parsing with an empty secret.
	ErrEmptySecret = errors.New("security: empty jwt secret")
	// ErrInvalidToken is returned when the token shape or payload is malformed.
	ErrInvalidToken = errors.New("security: invalid token")
	// ErrInvalidAlgorithm is returned when the token header is not HS256.
	ErrInvalidAlgorithm = errors.New("security: invalid jwt algorithm")
	// ErrInvalidSignature is returned when the HMAC signature does not match.
	ErrInvalidSignature = errors.New("security: invalid jwt signature")
	// ErrTokenExpired is returned when the token is past its exp time.
	ErrTokenExpired = errors.New("security: token expired")
	// ErrTokenNotYetValid is returned when the token is before its nbf time.
	ErrTokenNotYetValid = errors.New("security: token not yet valid")
	// ErrInvalidClaims is returned when claims are incomplete or inconsistent.
	ErrInvalidClaims = errors.New("security: invalid claims")
)

// Claims stores the application-specific JWT payload.
//
// Standard claim names are used where possible:
// - sub: subject / user ID
// - iat: issued at
// - nbf: not before
// - exp: expiration
// - lvl: user security level (0 = highest privilege)
type Claims struct {
	Subject  string `json:"sub"`
	Role     string `json:"role,omitempty"`
	Level    int    `json:"lvl"` // security level: 0=highest, higher=lower
	TenantID string `json:"tenant_id,omitempty"`
	// DeviceID is the runtime device fragment ID carried by cookie device_id.
	// It is not the persistent iam.devices.id value.
	DeviceID  string `json:"device_id,omitempty"`
	TokenID   string `json:"jti,omitempty"`
	Issuer    string `json:"iss,omitempty"`
	Audience  string `json:"aud,omitempty"`
	ClientID  string `json:"client_id,omitempty"`
	Scope     string `json:"scope,omitempty"`
	TokenUse  string `json:"token_use,omitempty"`
	IssuedAt  int64  `json:"iat"`
	NotBefore int64  `json:"nbf,omitempty"`
	ExpiresAt int64  `json:"exp"`
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

var jwtEncoding = base64.RawURLEncoding

// Sign creates a compact JWT using HMAC-SHA256 with the current primary secret
// from the requested secret family.
func Sign(ctx context.Context, provider SecretProvider, family string, claims Claims) (string, error) {
	if provider == nil {
		return "", ErrEmptySecret
	}

	secret, err := provider.GetPrimary(ctx, family)
	if err != nil {
		return "", err
	}

	return SignWithSecret(claims, secret.Value)
}

// SignWithSecret creates a compact JWT using HMAC-SHA256 with an explicit raw
// secret value. This is the low-level helper used after runtime secret lookup.
func SignWithSecret(claims Claims, secret string) (string, error) {
	if strings.TrimSpace(secret) == "" {
		return "", ErrEmptySecret
	}

	claims = normalizeClaims(claims)
	if err := validateClaimsForSigning(claims); err != nil {
		return "", err
	}

	headerJSON, err := json.Marshal(jwtHeader{
		Alg: JWTAlgHS256,
		Typ: JWTType,
	})
	if err != nil {
		return "", fmt.Errorf("security: encode jwt header: %w", err)
	}

	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("security: encode jwt claims: %w", err)
	}

	headerPart := jwtEncoding.EncodeToString(headerJSON)
	payloadPart := jwtEncoding.EncodeToString(payloadJSON)
	signingInput := headerPart + "." + payloadPart

	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := mac.Write([]byte(signingInput)); err != nil {
		return "", fmt.Errorf("security: sign jwt: %w", err)
	}

	signaturePart := jwtEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + signaturePart, nil
}

// Parse verifies a compact JWT signed with HMAC-SHA256.
func Parse(token, secret string) (Claims, error) {
	if strings.TrimSpace(secret) == "" {
		return Claims{}, ErrEmptySecret
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return Claims{}, ErrInvalidToken
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, ErrInvalidToken
	}

	headerBytes, err := jwtEncoding.DecodeString(parts[0])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}

	var hdr jwtHeader
	if err := json.Unmarshal(headerBytes, &hdr); err != nil {
		return Claims{}, ErrInvalidToken
	}
	if !strings.EqualFold(hdr.Alg, JWTAlgHS256) {
		return Claims{}, ErrInvalidAlgorithm
	}
	if hdr.Typ != "" && !strings.EqualFold(hdr.Typ, JWTType) {
		return Claims{}, ErrInvalidToken
	}

	payloadBytes, err := jwtEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}

	expectedSig, err := signInput(parts[0]+"."+parts[1], secret)
	if err != nil {
		return Claims{}, err
	}

	gotSig, err := jwtEncoding.DecodeString(parts[2])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	if !hmac.Equal(gotSig, expectedSig) {
		return Claims{}, ErrInvalidSignature
	}

	var claims Claims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return Claims{}, ErrInvalidToken
	}

	claims = normalizeClaims(claims)
	if err := validateClaimsForParse(claims, time.Now().UTC()); err != nil {
		return Claims{}, err
	}

	return claims, nil
}

// ExtractBearerToken returns the token string from a Bearer authorization header.
func ExtractBearerToken(header string) (string, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", false
	}

	if len(header) < len("Bearer ") || !strings.EqualFold(header[:6], "Bearer") || header[6] != ' ' {
		return "", false
	}

	token := strings.TrimSpace(header[7:])
	if token == "" {
		return "", false
	}

	return token, true
}

func normalizeClaims(claims Claims) Claims {
	claims.Subject = strings.TrimSpace(claims.Subject)
	claims.Role = strings.TrimSpace(claims.Role)
	claims.DeviceID = strings.TrimSpace(claims.DeviceID)
	claims.TokenID = strings.TrimSpace(claims.TokenID)
	claims.Issuer = strings.TrimSpace(claims.Issuer)
	claims.Audience = strings.TrimSpace(claims.Audience)
	claims.ClientID = strings.TrimSpace(claims.ClientID)
	claims.Scope = strings.TrimSpace(claims.Scope)
	claims.TokenUse = strings.TrimSpace(claims.TokenUse)
	return claims
}

func validateClaimsForSigning(claims Claims) error {
	if claims.Subject == "" && claims.ClientID == "" {
		return ErrInvalidClaims
	}
	if claims.ExpiresAt <= 0 {
		return ErrInvalidClaims
	}
	if claims.IssuedAt <= 0 {
		return ErrInvalidClaims
	}
	if claims.NotBefore > 0 && claims.NotBefore > claims.ExpiresAt {
		return ErrInvalidClaims
	}
	if claims.IssuedAt > claims.ExpiresAt {
		return ErrInvalidClaims
	}
	return nil
}

func validateClaimsForParse(claims Claims, now time.Time) error {
	if claims.Subject == "" && claims.ClientID == "" {
		return ErrInvalidClaims
	}
	if claims.ExpiresAt <= 0 {
		return ErrInvalidClaims
	}

	nowUnix := now.Unix()
	if claims.NotBefore > 0 && nowUnix < claims.NotBefore {
		return ErrTokenNotYetValid
	}
	if nowUnix > claims.ExpiresAt {
		return ErrTokenExpired
	}
	return nil
}

func signInput(signingInput, secret string) ([]byte, error) {
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := mac.Write([]byte(signingInput)); err != nil {
		return nil, fmt.Errorf("security: sign jwt: %w", err)
	}
	return mac.Sum(nil), nil
}
