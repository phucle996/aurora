package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"controlplane/pkg/logger"

	vaultapi "github.com/hashicorp/vault/api"
)

// [COMMENT]: signatureCacheEntry lưu giữ trạng thái hợp lệ và thời gian hết hạn của chữ ký JWT trong cache L1
type signatureCacheEntry struct {
	valid     bool
	expiresAt time.Time
}

// [COMMENT]: signatureCache là bộ nhớ đệm in-memory an toàn concurrency dùng để giảm tải REST call sang Vault Transit
type signatureCache struct {
	mu    sync.RWMutex
	store map[string]signatureCacheEntry
}

func (c *signatureCache) get(sigHash string) (bool, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.store[sigHash]
	if !ok {
		return false, false
	}
	if time.Now().After(entry.expiresAt) {
		return false, false
	}
	return entry.valid, true // Trả về kết quả xác thực và true báo hiệu cache hit
}

func (c *signatureCache) set(sigHash string, valid bool, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// [COMMENT]: Lazy cleanup - Khi số lượng bản ghi vượt quá 1000, thực hiện dọn dẹp các bản ghi hết hạn để tránh tràn bộ nhớ
	now := time.Now()
	if len(c.store) > 1000 {
		for k, entry := range c.store {
			if now.After(entry.expiresAt) {
				delete(c.store, k)
			}
		}
	}

	c.store[sigHash] = signatureCacheEntry{
		valid:     valid,
		expiresAt: now.Add(ttl),
	}
}

var (
	verifyCache = &signatureCache{
		store: make(map[string]signatureCacheEntry),
	}
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

	// [COMMENT]: Khóa ký dự phòng mặc định dùng riêng cho Unit Tests/Integration Tests khi không chạy Vault
	testFallbackSecret = []byte("test_fallback_signing_secret_32_bytes_long")
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
	ZoneID   string `json:"zone_id,omitempty"`
	// AccessKey is the runtime session fragment ID carried by cookie access_key.
	// It is not the persistent physical device ID.
	AccessKey string `json:"access_key,omitempty"`
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

var (
	jwtEncoding = base64.RawURLEncoding
	// [COMMENT]: Biến toàn cục lưu trữ client kết nối tới Vault và tên khóa Transit
	vaultClient     *vaultapi.Client
	vaultTransitKey string
)

// [COMMENT]: InitVault cấu hình global Vault client và transit key name cho package security.
func InitVault(client *vaultapi.Client, keyName string) {
	vaultClient = client
	vaultTransitKey = keyName
}

// SignWithSecret creates a compact JWT using HMAC-SHA256 with an explicit raw
// secret value. This is the low-level helper used after runtime secret lookup.
func SignWithSecret(claims Claims, secret []byte) (string, error) {
	if vaultClient == nil && len(secret) == 0 {
		// [COMMENT]: Dùng khóa ký test_fallback trong môi trường test
		secret = testFallbackSecret
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

	// [COMMENT]: Nếu Vault Client được cấu hình, ta ủy thác việc ký HMAC-SHA256 lên Vault Transit Engine.
	if vaultClient != nil {
		// [COMMENT]: Encode dữ liệu cần ký dưới dạng Base64 Std
		inputB64 := base64.StdEncoding.EncodeToString([]byte(signingInput))

		// [COMMENT]: Gọi API transit/hmac của Vault để sinh chữ ký bảo mật
		data := map[string]interface{}{
			"input":     inputB64,
			"algorithm": "sha2-256",
		}
		secretRes, err := vaultClient.Logical().Write(fmt.Sprintf("transit/hmac/%s", vaultTransitKey), data)
		if err != nil {
			logger.SysErrorFields("security.jwt.SignWithSecret", "Vault HMAC signing failed", err, logger.Fields{
				"transit_key": vaultTransitKey,
			})
			return "", fmt.Errorf("security: vault sign failed: %w", err)
		}

		hmacVal, ok := secretRes.Data["hmac"].(string)
		if !ok || hmacVal == "" {
			return "", fmt.Errorf("security: invalid hmac response from vault")
		}

		// [COMMENT]: Vault trả về chuỗi định dạng "vault:v<version>:<base64-signature>".
		// Ta tách phiên bản khóa (version) và signature để lưu giữ thông tin phục vụ giải mã/xác thực.
		parts := strings.Split(hmacVal, ":")
		if len(parts) < 3 {
			return "", fmt.Errorf("security: malformed vault hmac signature")
		}

		version := parts[1] // Ví dụ "v1"
		sigB64 := parts[2]

		sigBytes, err := base64.StdEncoding.DecodeString(sigB64)
		if err != nil {
			return "", fmt.Errorf("security: decode vault signature failed: %w", err)
		}

		// [COMMENT]: Encode chữ ký bằng Base64 Raw URL để đúng đặc tả JWT
		sigRawURL := jwtEncoding.EncodeToString(sigBytes)

		// [COMMENT]: Đóng gói Token theo định dạng lai: header.payload.version_signature
		return signingInput + "." + version + "_" + sigRawURL, nil
	}

	// [COMMENT]: Fallback path ký bằng khóa đối xứng thô ở DB/Local (Dành cho môi trường Test hoặc Hybrid transition)
	mac := hmac.New(sha256.New, secret)
	if _, err := mac.Write([]byte(signingInput)); err != nil {
		return "", fmt.Errorf("security: sign jwt: %w", err)
	}

	signaturePart := jwtEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + signaturePart, nil
}

// Parse verifies a compact JWT signed with HMAC-SHA256.
func Parse(token string, secret []byte) (Claims, error) {
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

	// [COMMENT]: Xác định xem signature part có chứa version của Vault hay không (ví dụ "v1_XXXX")
	sigPart := parts[2]
	isVaultToken := false
	var vaultVersion string
	var rawSigB64URL string

	if strings.HasPrefix(sigPart, "v") {
		idx := strings.Index(sigPart, "_")
		if idx > 0 {
			isVaultToken = true
			vaultVersion = sigPart[:idx] // ví dụ "v1"
			rawSigB64URL = sigPart[idx+1:]
		}
	}

	if vaultClient != nil {
		// [COMMENT]: Môi trường chạy thật (HA/Production) yêu cầu bắt buộc dùng Vault, loại bỏ hoàn toàn Database/Local Fallback.
		if !isVaultToken {
			return Claims{}, fmt.Errorf("security: token lacks Vault signature prefix in production")
		}

		// [COMMENT]: Sinh hash SHA256 không đảo ngược được của phần chữ ký để làm key tra cứu in-memory cache L1 an toàn
		sigHashBytes := sha256.Sum256([]byte(sigPart))
		sigHash := base64.RawURLEncoding.EncodeToString(sigHashBytes[:])

		if valid, cached := verifyCache.get(sigHash); cached {
			if !valid {
				return Claims{}, ErrInvalidSignature
			}

			// Hợp lệ và còn hạn trong L1 cache, bỏ qua REST API call đến Vault
		} else {

			// [COMMENT]: Giải mã chữ ký Raw URL sang byte và encode lại sang Base64 Standard phục vụ API Vault
			sigBytes, err := jwtEncoding.DecodeString(rawSigB64URL)
			if err != nil {

				return Claims{}, ErrInvalidToken
			}
			sigB64 := base64.StdEncoding.EncodeToString(sigBytes)

			// [COMMENT]: Tái dựng chuỗi HMAC đúng chuẩn Vault: "vault:%s:%s"
			vaultHMAC := fmt.Sprintf("vault:%s:%s", vaultVersion, sigB64)

			inputB64 := base64.StdEncoding.EncodeToString([]byte(parts[0] + "." + parts[1]))



			// [COMMENT]: Gọi API transit/verify của Vault để xác thực chữ ký
			data := map[string]interface{}{
				"input":     inputB64,
				"hmac":      vaultHMAC,
				"algorithm": "sha2-256",
			}
			secretRes, err := vaultClient.Logical().Write(fmt.Sprintf("transit/verify/%s", vaultTransitKey), data)
			if err != nil {
				logger.SysErrorFields("security.jwt.Parse", "Vault HMAC verification failed", err, logger.Fields{
					"transit_key": vaultTransitKey,
				})
				return Claims{}, fmt.Errorf("security: vault verify failed: %w", err)
			}



			valid, ok := secretRes.Data["valid"].(bool)
			if !ok || !valid {
				// [COMMENT]: Cache trạng thái không hợp lệ trong 10 giây để giảm tải spamming

				verifyCache.set(sigHash, false, 10*time.Second)
				return Claims{}, ErrInvalidSignature
			}


			// [COMMENT]: Cache trạng thái hợp lệ trong 10 giây
			verifyCache.set(sigHash, true, 10*time.Second)
		}
	} else {
		// [COMMENT]: Chỉ cho phép fallback sang kiểm tra cục bộ đối xứng bằng secret trong môi trường Test
		if isVaultToken {
			return Claims{}, fmt.Errorf("security: vault token received but vault client not initialized")
		}
		if len(secret) == 0 {
			// [COMMENT]: Dùng khóa ký test_fallback trong môi trường test
			secret = testFallbackSecret
		}
		expectedSig, err := signInput(parts[0]+"."+parts[1], secret)
		if err != nil {
			return Claims{}, err
		}

		gotSig, err := jwtEncoding.DecodeString(sigPart)
		if err != nil {
			return Claims{}, ErrInvalidToken
		}
		if !hmac.Equal(gotSig, expectedSig) {
			return Claims{}, ErrInvalidSignature
		}
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
	claims.TenantID = strings.TrimSpace(claims.TenantID)
	claims.ZoneID = strings.TrimSpace(claims.ZoneID)
	claims.AccessKey = strings.TrimSpace(claims.AccessKey)
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

func signInput(signingInput string, secret []byte) ([]byte, error) {
	mac := hmac.New(sha256.New, secret)
	if _, err := mac.Write([]byte(signingInput)); err != nil {
		return nil, fmt.Errorf("security: sign jwt: %w", err)
	}
	return mac.Sum(nil), nil
}
