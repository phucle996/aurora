package hierarchyHandler

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchySvcInterface "controlplane/internal/hierarchy/domain/service"
	hierarchyTaxonomy "controlplane/internal/hierarchy/taxonomy"
	hierarchyReq "controlplane/internal/hierarchy/transport/http/dto/req"
	"controlplane/pkg/apires"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type ZoneEncryptionKeyHandler struct {
	service hierarchySvcInterface.ZoneEncryptionKeyService
}

func NewZoneEncryptionKeyHandler(service hierarchySvcInterface.ZoneEncryptionKeyService) *ZoneEncryptionKeyHandler {
	return &ZoneEncryptionKeyHandler{service: service}
}

func (h *ZoneEncryptionKeyHandler) RegisterZoneEncryptionKey(c *gin.Context) {
	const op = "hierarchy.zone_encryption_key.register"

	// [COMMENT]: This route changes future Zone command confidentiality. It
	// fails closed unless ACR has consumed a proof bound to this critical request.
	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	proofID, proofErr := uuid.Parse(strings.TrimSpace(c.GetHeader("x-session-proof-challenge-id")))
	if actor != "sre" || c.GetHeader("x-session-proof-verified") != "true" || proofErr != nil || proofID == uuid.Nil {
		apires.RespondForbidden(c, "verified critical proof is required")
		return
	}

	zoneID, err := uuid.Parse(strings.TrimSpace(c.Param("zone_id")))
	if err != nil || zoneID == uuid.Nil {
		apires.RespondBadRequest(c, "invalid zone id")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var request hierarchyReq.RegisterZoneEncryptionKeyRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	encodedPublicKey := strings.TrimSpace(request.PublicKey)
	publicKeyBytes, err := base64.StdEncoding.Strict().DecodeString(encodedPublicKey)
	if err != nil || len(publicKeyBytes) != 32 || base64.StdEncoding.EncodeToString(publicKeyBytes) != encodedPublicKey {
		apires.RespondBadRequest(c, "public_key must be canonical base64 X25519 key material")
		return
	}
	publicKey, err := ecdh.X25519().NewPublicKey(publicKeyBytes)
	if err != nil {
		apires.RespondBadRequest(c, "invalid X25519 public key")
		return
	}
	validationPrivateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
		return
	}
	// [COMMENT]: ECDH rejects low-order X25519 points that would derive an
	// all-zero shared secret. Validation happens only at the HTTP trust boundary.
	if _, err := validationPrivateKey.ECDH(publicKey); err != nil {
		apires.RespondBadRequest(c, "invalid X25519 public key")
		return
	}

	ctx := pkgcontext.WithOperation(c.Request.Context(), op)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := h.service.RegisterZoneEncryptionKey(ctx, &hierarchyEntity.RegisterZoneEncryptionKey{
		ZoneID: zoneID, Actor: actor, ProofID: proofID, PublicKey: publicKeyBytes,
	})
	if err != nil {
		switch {
		case errors.Is(err, hierarchyTaxonomy.ErrNotFound):
			apires.RespondNotFound(c, "resource not found")
		case errors.Is(err, hierarchyTaxonomy.ErrConflict):
			apires.RespondConflict(c, "public key is already assigned to another zone")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondCreated(c, gin.H{
		"id": result.ID, "zone_id": result.ZoneID,
		"public_key":  base64.StdEncoding.EncodeToString(result.PublicKey),
		"fingerprint": hex.EncodeToString(result.Fingerprint),
		"algorithm":   result.Algorithm, "status": string(result.Status),
		"registered_by": result.RegisteredBy,
		"created_at":    result.CreatedAt, "updated_at": result.UpdatedAt,
	}, "zone encryption key registered")
}

func (h *ZoneEncryptionKeyHandler) ListZoneEncryptionKeys(c *gin.Context) {
	const op = "hierarchy.zone_encryption_key.list"

	// [COMMENT]: The public key is not secret, but the Zone key inventory is an
	// admin-plane capability and is not exposed through customer routes.
	if strings.TrimSpace(c.GetHeader("x-user-id")) != "sre" {
		apires.RespondForbidden(c, "forbidden")
		return
	}
	zoneID, err := uuid.Parse(strings.TrimSpace(c.Param("zone_id")))
	if err != nil || zoneID == uuid.Nil {
		apires.RespondBadRequest(c, "invalid zone id")
		return
	}
	limit := 50
	if rawLimit := strings.TrimSpace(c.Query("limit")); rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil || parsedLimit < 1 || parsedLimit > 100 {
			apires.RespondBadRequest(c, "invalid limit")
			return
		}
		limit = parsedLimit
	}
	listInput := &hierarchyEntity.ListZoneEncryptionKeys{ZoneID: zoneID, Limit: limit}
	if rawCursor := strings.TrimSpace(c.Query("cursor")); rawCursor != "" {
		decodedCursor, err := base64.RawURLEncoding.Strict().DecodeString(rawCursor)
		parts := strings.SplitN(string(decodedCursor), "|", 2)
		if err != nil || len(decodedCursor) > 128 || len(parts) != 2 {
			apires.RespondBadRequest(c, "invalid cursor")
			return
		}
		createdAtMicros, timeErr := strconv.ParseInt(parts[0], 10, 64)
		cursorID, idErr := uuid.Parse(parts[1])
		if timeErr != nil || createdAtMicros <= 0 || idErr != nil || cursorID == uuid.Nil {
			apires.RespondBadRequest(c, "invalid cursor")
			return
		}
		listInput.HasCursor = true
		listInput.CursorCreatedAt = time.UnixMicro(createdAtMicros).UTC()
		listInput.CursorID = cursorID
	}

	ctx := pkgcontext.WithOperation(c.Request.Context(), op)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	items, err := h.service.ListZoneEncryptionKeys(ctx, listInput)
	if err != nil {
		if errors.Is(err, hierarchyTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "resource not found")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	var nextCursor any
	if len(items) > limit {
		cursorItem := items[limit-1]
		nextCursor = base64.RawURLEncoding.EncodeToString([]byte(
			strconv.FormatInt(cursorItem.CreatedAt.UnixMicro(), 10) + "|" + cursorItem.ID.String(),
		))
		items = items[:limit]
	}
	rows := make([]gin.H, 0, len(items))
	for _, item := range items {
		rows = append(rows, gin.H{
			"id": item.ID, "zone_id": item.ZoneID,
			"public_key":  base64.StdEncoding.EncodeToString(item.PublicKey),
			"fingerprint": hex.EncodeToString(item.Fingerprint),
			"algorithm":   item.Algorithm, "status": string(item.Status),
			"registered_by": item.RegisteredBy, "activated_by": item.ActivatedBy,
			"decrypt_only_by": item.DecryptOnlyBy, "retired_by": item.RetiredBy,
			"created_at": item.CreatedAt, "updated_at": item.UpdatedAt,
			"activated_at": item.ActivatedAt, "decrypt_only_at": item.DecryptOnlyAt,
			"retired_at": item.RetiredAt,
		})
	}
	apires.RespondSuccess(c, gin.H{"items": rows, "count": len(rows), "next_cursor": nextCursor}, "zone encryption keys fetched")
}

func (h *ZoneEncryptionKeyHandler) ActivateZoneEncryptionKey(c *gin.Context) {
	const op = "hierarchy.zone_encryption_key.activate"

	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	proofID, proofErr := uuid.Parse(strings.TrimSpace(c.GetHeader("x-session-proof-challenge-id")))
	if actor != "sre" || c.GetHeader("x-session-proof-verified") != "true" || proofErr != nil || proofID == uuid.Nil {
		apires.RespondForbidden(c, "verified critical proof is required")
		return
	}
	zoneID, zoneErr := uuid.Parse(strings.TrimSpace(c.Param("zone_id")))
	keyID, keyErr := uuid.Parse(strings.TrimSpace(c.Param("key_id")))
	if zoneErr != nil || zoneID == uuid.Nil || keyErr != nil || keyID == uuid.Nil {
		apires.RespondBadRequest(c, "invalid zone or key id")
		return
	}

	ctx := pkgcontext.WithOperation(c.Request.Context(), op)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := h.service.ActivateZoneEncryptionKey(ctx, &hierarchyEntity.ActivateZoneEncryptionKey{
		ZoneID: zoneID, KeyID: keyID, Actor: actor, ProofID: proofID,
	})
	if err != nil {
		switch {
		case errors.Is(err, hierarchyTaxonomy.ErrNotFound):
			apires.RespondNotFound(c, "resource not found")
		case errors.Is(err, hierarchyTaxonomy.ErrInvalidTransition):
			apires.RespondConflict(c, "zone encryption key cannot be activated from its current state")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id": result.KeyID, "zone_id": result.ZoneID,
		"public_key":  base64.StdEncoding.EncodeToString(result.PublicKey),
		"fingerprint": hex.EncodeToString(result.Fingerprint),
		"algorithm":   result.Algorithm, "status": string(result.Status),
		"activated_by": result.ActivatedBy, "activated_at": result.ActivatedAt,
		"state_changed": result.StateChanged,
		"created_at":    result.CreatedAt, "updated_at": result.UpdatedAt,
	}, "zone encryption key activated")
}

func (h *ZoneEncryptionKeyHandler) RetireZoneEncryptionKey(c *gin.Context) {
	const op = "hierarchy.zone_encryption_key.retire"

	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	proofID, proofErr := uuid.Parse(strings.TrimSpace(c.GetHeader("x-session-proof-challenge-id")))
	if actor != "sre" || c.GetHeader("x-session-proof-verified") != "true" || proofErr != nil || proofID == uuid.Nil {
		apires.RespondForbidden(c, "verified critical proof is required")
		return
	}
	zoneID, zoneErr := uuid.Parse(strings.TrimSpace(c.Param("zone_id")))
	keyID, keyErr := uuid.Parse(strings.TrimSpace(c.Param("key_id")))
	if zoneErr != nil || zoneID == uuid.Nil || keyErr != nil || keyID == uuid.Nil {
		apires.RespondBadRequest(c, "invalid zone or key id")
		return
	}

	ctx := pkgcontext.WithOperation(c.Request.Context(), op)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := h.service.RetireZoneEncryptionKey(ctx, &hierarchyEntity.RetireZoneEncryptionKey{
		ZoneID: zoneID, KeyID: keyID, Actor: actor, ProofID: proofID,
	})
	if err != nil {
		switch {
		case errors.Is(err, hierarchyTaxonomy.ErrNotFound):
			apires.RespondNotFound(c, "resource not found")
		case errors.Is(err, hierarchyTaxonomy.ErrInvalidTransition):
			apires.RespondConflict(c, "active zone encryption key cannot be retired")
		case errors.Is(err, hierarchyTaxonomy.ErrConflict):
			apires.RespondConflict(c, "zone encryption key is still referenced by retained jobs")
		case errors.Is(err, hierarchyTaxonomy.ErrPreconditionFailed):
			apires.RespondConflict(c, "zone encryption key retirement drain window has not elapsed")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id": result.KeyID, "zone_id": result.ZoneID,
		"public_key":  base64.StdEncoding.EncodeToString(result.PublicKey),
		"fingerprint": hex.EncodeToString(result.Fingerprint),
		"algorithm":   result.Algorithm, "status": string(result.Status),
		"retired_by": result.RetiredBy, "retired_at": result.RetiredAt,
		"state_changed": result.StateChanged,
		"created_at":    result.CreatedAt, "updated_at": result.UpdatedAt,
	}, "zone encryption key retired")
}
