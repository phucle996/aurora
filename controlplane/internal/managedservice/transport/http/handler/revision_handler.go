package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"controlplane/internal/managedservice/domain/entity"
	managedservice "controlplane/internal/managedservice/domain/service"
	"controlplane/internal/managedservice/taxonomy"
	"controlplane/internal/managedservice/transport/http/dto"
	"controlplane/pkg/apires"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

type RevisionHandler struct {
	service managedservice.RevisionService
}

func NewRevisionHandler(service managedservice.RevisionService) *RevisionHandler {
	return &RevisionHandler{service: service}
}

func (h *RevisionHandler) CreateDraft(c *gin.Context) {
	// [COMMENT]: ACR owns signature, nonce and TOTP verification. Controlplane
	// only accepts the stripped-and-reinjected proof marker and opaque proof ID.
	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	proofID, proofErr := uuid.Parse(strings.TrimSpace(c.GetHeader("x-session-proof-challenge-id")))
	if actor != "sre" || c.GetHeader("x-session-proof-verified") != "true" || proofErr != nil || proofID == uuid.Nil {
		apires.RespondForbidden(c, "verified critical proof is required")
		return
	}

	blueprintID, blueprintErr := uuid.Parse(strings.TrimSpace(c.Param("blueprint_id")))
	if blueprintErr != nil || blueprintID == uuid.Nil {
		apires.RespondBadRequest(c, "invalid blueprint id")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2*1024*1024)
	var request dto.CreateDraftRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		apires.RespondBadRequest(c, "request must contain one JSON document")
		return
	}

	request.ContractVersion = strings.TrimSpace(request.ContractVersion)
	if request.ContractVersion != "platform-form/v1" || len(request.TemplateYAML) == 0 || len(request.TemplateYAML) > 1024*1024 {
		apires.RespondBadRequest(c, "invalid draft artifact")
		return
	}

	var componentContract []map[string]any
	var inputSchema map[string]any
	var uiSchema map[string]any
	var safeOutput map[string]any
	var zoneSelector map[string]any
	var capability map[string]any
	if json.Unmarshal(request.ComponentContract, &componentContract) != nil || componentContract == nil ||
		json.Unmarshal(request.InputSchema, &inputSchema) != nil || inputSchema == nil ||
		json.Unmarshal(request.UISchema, &uiSchema) != nil || uiSchema == nil ||
		json.Unmarshal(request.SafeObservedOutputSchema, &safeOutput) != nil || safeOutput == nil ||
		json.Unmarshal(request.ZoneSelector, &zoneSelector) != nil || zoneSelector == nil ||
		json.Unmarshal(request.CapabilityRequirement, &capability) != nil || capability == nil {
		apires.RespondBadRequest(c, "draft contracts must use the expected JSON shapes")
		return
	}
	componentJSON, componentErr := json.Marshal(componentContract)
	inputJSON, inputErr := json.Marshal(inputSchema)
	uiJSON, uiErr := json.Marshal(uiSchema)
	outputJSON, outputErr := json.Marshal(safeOutput)
	zoneJSON, zoneErr := json.Marshal(zoneSelector)
	capabilityJSON, capabilityErr := json.Marshal(capability)
	if componentErr != nil || inputErr != nil || uiErr != nil || outputErr != nil || zoneErr != nil || capabilityErr != nil ||
		len(componentJSON) > 256*1024 || len(inputJSON) > 256*1024 || len(uiJSON) > 256*1024 ||
		len(outputJSON) > 256*1024 || len(zoneJSON) > 64*1024 || len(capabilityJSON) > 64*1024 {
		apires.RespondBadRequest(c, "draft contract is too large")
		return
	}

	// [COMMENT]: Hash từng contract artifact riêng biệt rồi kết hợp thành contract hash tổng.
	// Đảm bảo tính idempotent – cùng một artifact luôn tạo ra cùng hash.
	templateHash := sha256.Sum256([]byte(request.TemplateYAML))
	componentHash := sha256.Sum256(componentJSON)
	inputHash := sha256.Sum256(inputJSON)
	uiHash := sha256.Sum256(uiJSON)
	outputHash := sha256.Sum256(outputJSON)
	zoneHash := sha256.Sum256(zoneJSON)
	capabilityHash := sha256.Sum256(capabilityJSON)
	contractHasher := sha256.New()
	contractHasher.Write([]byte("managed-service-contract-v1\x00"))
	contractHasher.Write([]byte(request.ContractVersion))
	contractHasher.Write([]byte("\x00component\x00"))
	contractHasher.Write(componentJSON)
	contractHasher.Write([]byte("\x00input\x00"))
	contractHasher.Write(inputJSON)
	contractHasher.Write([]byte("\x00ui\x00"))
	contractHasher.Write(uiJSON)
	contractHasher.Write([]byte("\x00output\x00"))
	contractHasher.Write(outputJSON)
	contractHasher.Write([]byte("\x00zone\x00"))
	contractHasher.Write(zoneJSON)
	contractHasher.Write([]byte("\x00capability\x00"))
	contractHasher.Write(capabilityJSON)
	contractHash := contractHasher.Sum(nil)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	result, err := h.service.CreateDraft(ctx, &entity.CreateDraft{
		Actor:                    actor,
		ProofID:                  proofID,
		BlueprintID:              blueprintID,
		TemplateYAML:             request.TemplateYAML,
		TemplateBundleSHA256:     templateHash[:],
		ContractVersion:          request.ContractVersion,
		ContractSHA256:           contractHash,
		ComponentContract:        componentJSON,
		ComponentContractSHA256:  componentHash[:],
		InputSchema:              inputJSON,
		InputSchemaSHA256:        inputHash[:],
		UISchema:                 uiJSON,
		UISchemaSHA256:           uiHash[:],
		SafeObservedOutputSchema: outputJSON,
		SafeOutputSHA256:         outputHash[:],
		ZoneSelector:             zoneJSON,
		ZoneSelectorSHA256:       zoneHash[:],
		CapabilityRequirement:    capabilityJSON,
		CapabilitySHA256:         capabilityHash[:],
	})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			apires.RespondNotFound(c, "blueprint not found")
		case errors.Is(err, taxonomy.ErrCatalogParentRetired):
			apires.RespondConflict(c, "blueprint is retired")
		default:
			logger.HandlerError(c, "managedservice.revision.create_draft", err)
			apires.RespondInternalError(c, "SRE_CATALOG_INTERNAL")
		}
		return
	}

	apires.RespondCreated(c, gin.H{
		"id": result.ID, "blueprint_id": result.BlueprintID, "revision": result.Revision,
		"state": result.State, "row_version": result.RowVersion,
		"template_bundle_sha256": hex.EncodeToString(result.TemplateBundleSHA256),
		"contract_sha256":        hex.EncodeToString(result.ContractSHA256),
	}, "draft created")
}

func (h *RevisionHandler) GetDraft(c *gin.Context) {
	// [COMMENT]: Chỉ SRE mới có quyền xem chi tiết draft.
	if strings.TrimSpace(c.GetHeader("x-user-id")) != "sre" {
		apires.RespondForbidden(c, "forbidden")
		return
	}

	draftID, err := uuid.Parse(strings.TrimSpace(c.Param("draft_id")))
	if err != nil || draftID == uuid.Nil {
		apires.RespondBadRequest(c, "invalid draft id")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	result, err := h.service.GetDraft(ctx, &entity.GetDraft{DraftID: draftID})
	if err != nil {
		if errors.Is(err, taxonomy.ErrCatalogNotFound) {
			apires.RespondNotFound(c, "draft not found")
			return
		}
		logger.HandlerError(c, "managedservice.revision.get_draft", err)
		apires.RespondInternalError(c, "SRE_CATALOG_INTERNAL")
		return
	}

	// [COMMENT]: Raw YAML is only returned by this dedicated SRE editor route;
	// it is never part of customer discovery or the general admin list model.
	apires.RespondSuccess(c, gin.H{
		"id":                          result.ID,
		"blueprint_id":                result.BlueprintID,
		"revision":                    result.Revision,
		"state":                       result.State,
		"template_yaml":               result.TemplateYAML,
		"template_bundle_sha256":      hex.EncodeToString(result.TemplateBundleSHA256),
		"contract_version":            result.ContractVersion,
		"contract_sha256":             hex.EncodeToString(result.ContractSHA256),
		"component_contract":          result.ComponentContract,
		"input_schema":                result.InputSchema,
		"ui_schema":                   result.UISchema,
		"safe_observed_output_schema": result.SafeObservedOutputSchema,
		"zone_selector":               result.ZoneSelector,
		"capability_requirement":      result.CapabilityRequirement,
		"row_version":                 result.RowVersion,
		"validated_row_version":       result.ValidatedRowVersion,
		"validated_at":                result.ValidatedAt,
	}, "draft fetched")
}

func (h *RevisionHandler) ListRevisions(c *gin.Context) {
	// [COMMENT]: Chỉ SRE mới có quyền list revisions của blueprint.
	if strings.TrimSpace(c.GetHeader("x-user-id")) != "sre" {
		apires.RespondForbidden(c, "forbidden")
		return
	}

	blueprintID, err := uuid.Parse(strings.TrimSpace(c.Param("blueprint_id")))
	if err != nil || blueprintID == uuid.Nil {
		apires.RespondBadRequest(c, "invalid blueprint id")
		return
	}

	// [COMMENT]: Default limit 100, tối đa 100 – phù hợp với revision list dày.
	limit := 100
	if rawLimit := strings.TrimSpace(c.Query("limit")); rawLimit != "" {
		parsed, parseErr := strconv.Atoi(rawLimit)
		if parseErr != nil || parsed < 1 || parsed > 100 {
			apires.RespondBadRequest(c, "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	result, err := h.service.ListRevisions(ctx, &entity.ListRevisions{BlueprintID: blueprintID, Limit: limit})
	if err != nil {
		logger.HandlerError(c, "managedservice.revision.list", err)
		apires.RespondInternalError(c, "SRE_CATALOG_INTERNAL")
		return
	}

	items := make([]gin.H, 0, len(result))
	for _, item := range result {
		items = append(items, gin.H{"id": item.ID, "blueprint_id": item.BlueprintID, "revision": item.Revision, "state": item.State, "row_version": item.RowVersion, "template_bundle_sha256": hex.EncodeToString(item.TemplateBundleSHA256), "contract_version": item.ContractVersion, "contract_sha256": hex.EncodeToString(item.ContractSHA256), "validated_row_version": item.ValidatedRowVersion, "validated_at": item.ValidatedAt, "created_at": item.CreatedAt, "published_at": item.PublishedAt, "retired_at": item.RetiredAt})
	}

	apires.RespondSuccess(c, gin.H{"items": items}, "revisions fetched")
}

func (h *RevisionHandler) PatchDraft(c *gin.Context) {
	// [COMMENT]: Patch draft là thao tác critical – bắt buộc có verified proof header.
	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	proofID, proofErr := uuid.Parse(strings.TrimSpace(c.GetHeader("x-session-proof-challenge-id")))
	if actor != "sre" || c.GetHeader("x-session-proof-verified") != "true" || proofErr != nil || proofID == uuid.Nil {
		apires.RespondForbidden(c, "verified critical proof is required")
		return
	}

	draftID, draftErr := uuid.Parse(strings.TrimSpace(c.Param("draft_id")))
	if draftErr != nil || draftID == uuid.Nil {
		apires.RespondBadRequest(c, "invalid draft id")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2*1024*1024)
	var request dto.PatchDraftRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		apires.RespondBadRequest(c, "request must contain one JSON document")
		return
	}

	request.ContractVersion = strings.TrimSpace(request.ContractVersion)
	if request.ExpectedVersion < 1 || request.ContractVersion != "platform-form/v1" || len(request.TemplateYAML) == 0 || len(request.TemplateYAML) > 1024*1024 {
		apires.RespondBadRequest(c, "invalid draft artifact")
		return
	}

	var componentContract []map[string]any
	var inputSchema map[string]any
	var uiSchema map[string]any
	var safeOutput map[string]any
	var zoneSelector map[string]any
	var capability map[string]any
	if json.Unmarshal(request.ComponentContract, &componentContract) != nil || componentContract == nil ||
		json.Unmarshal(request.InputSchema, &inputSchema) != nil || inputSchema == nil ||
		json.Unmarshal(request.UISchema, &uiSchema) != nil || uiSchema == nil ||
		json.Unmarshal(request.SafeObservedOutputSchema, &safeOutput) != nil || safeOutput == nil ||
		json.Unmarshal(request.ZoneSelector, &zoneSelector) != nil || zoneSelector == nil ||
		json.Unmarshal(request.CapabilityRequirement, &capability) != nil || capability == nil {
		apires.RespondBadRequest(c, "draft contracts must use the expected JSON shapes")
		return
	}
	componentJSON, componentErr := json.Marshal(componentContract)
	inputJSON, inputErr := json.Marshal(inputSchema)
	uiJSON, uiErr := json.Marshal(uiSchema)
	outputJSON, outputErr := json.Marshal(safeOutput)
	zoneJSON, zoneErr := json.Marshal(zoneSelector)
	capabilityJSON, capabilityErr := json.Marshal(capability)
	if componentErr != nil || inputErr != nil || uiErr != nil || outputErr != nil || zoneErr != nil || capabilityErr != nil ||
		len(componentJSON) > 256*1024 || len(inputJSON) > 256*1024 || len(uiJSON) > 256*1024 ||
		len(outputJSON) > 256*1024 || len(zoneJSON) > 64*1024 || len(capabilityJSON) > 64*1024 {
		apires.RespondBadRequest(c, "draft contract is too large")
		return
	}

	// [COMMENT]: Tính lại toàn bộ hashes để đảm bảo tính toàn vẹn khi patch.
	templateHash := sha256.Sum256([]byte(request.TemplateYAML))
	componentHash := sha256.Sum256(componentJSON)
	inputHash := sha256.Sum256(inputJSON)
	uiHash := sha256.Sum256(uiJSON)
	outputHash := sha256.Sum256(outputJSON)
	zoneHash := sha256.Sum256(zoneJSON)
	capabilityHash := sha256.Sum256(capabilityJSON)
	contractHasher := sha256.New()
	contractHasher.Write([]byte("managed-service-contract-v1\x00"))
	contractHasher.Write([]byte(request.ContractVersion))
	contractHasher.Write([]byte("\x00component\x00"))
	contractHasher.Write(componentJSON)
	contractHasher.Write([]byte("\x00input\x00"))
	contractHasher.Write(inputJSON)
	contractHasher.Write([]byte("\x00ui\x00"))
	contractHasher.Write(uiJSON)
	contractHasher.Write([]byte("\x00output\x00"))
	contractHasher.Write(outputJSON)
	contractHasher.Write([]byte("\x00zone\x00"))
	contractHasher.Write(zoneJSON)
	contractHasher.Write([]byte("\x00capability\x00"))
	contractHasher.Write(capabilityJSON)
	contractHash := contractHasher.Sum(nil)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	result, err := h.service.PatchDraft(ctx, &entity.PatchDraft{
		DraftID:                  draftID,
		Actor:                    actor,
		ProofID:                  proofID,
		ExpectedVersion:          request.ExpectedVersion,
		TemplateYAML:             request.TemplateYAML,
		TemplateBundleSHA256:     templateHash[:],
		ContractVersion:          request.ContractVersion,
		ContractSHA256:           contractHash,
		ComponentContract:        componentJSON,
		ComponentContractSHA256:  componentHash[:],
		InputSchema:              inputJSON,
		InputSchemaSHA256:        inputHash[:],
		UISchema:                 uiJSON,
		UISchemaSHA256:           uiHash[:],
		SafeObservedOutputSchema: outputJSON,
		SafeOutputSHA256:         outputHash[:],
		ZoneSelector:             zoneJSON,
		ZoneSelectorSHA256:       zoneHash[:],
		CapabilityRequirement:    capabilityJSON,
		CapabilitySHA256:         capabilityHash[:],
	})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			apires.RespondNotFound(c, "draft not found")
		case errors.Is(err, taxonomy.ErrCatalogConcurrentChange):
			apires.RespondConflict(c, "refresh draft and retry")
		case errors.Is(err, taxonomy.ErrCatalogRecordImmutable):
			apires.RespondConflict(c, "published revision cannot change")
		default:
			logger.HandlerError(c, "managedservice.revision.patch_draft", err)
			apires.RespondInternalError(c, "SRE_CATALOG_INTERNAL")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id": result.ID, "state": result.State, "row_version": result.RowVersion,
		"template_bundle_sha256": hex.EncodeToString(result.TemplateBundleSHA256),
		"contract_sha256":        hex.EncodeToString(result.ContractSHA256),
	}, "draft updated")
}

func (h *RevisionHandler) ValidateDraft(c *gin.Context) {
	// [COMMENT]: Validate draft là thao tác critical – bắt buộc có verified proof header.
	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	proofID, proofErr := uuid.Parse(strings.TrimSpace(c.GetHeader("x-session-proof-challenge-id")))
	if actor != "sre" || c.GetHeader("x-session-proof-verified") != "true" || proofErr != nil || proofID == uuid.Nil {
		apires.RespondForbidden(c, "verified critical proof is required")
		return
	}

	draftID, draftErr := uuid.Parse(strings.TrimSpace(c.Param("draft_id")))
	if draftErr != nil || draftID == uuid.Nil {
		apires.RespondBadRequest(c, "invalid draft id")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2*1024*1024)
	var request dto.ValidateDraftRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		apires.RespondBadRequest(c, "request must contain one JSON document")
		return
	}

	request.ContractVersion = strings.TrimSpace(request.ContractVersion)
	if request.ExpectedVersion < 1 || request.ContractVersion != "platform-form/v1" || len(request.TemplateYAML) == 0 || len(request.TemplateYAML) > 1024*1024 {
		// [COMMENT]: Dùng 422 thay vì 400 để phân biệt lỗi validation logic với lỗi parse.
		apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "draft artifact is invalid")
		return
	}

	var componentContract []map[string]any
	var inputSchema map[string]any
	var uiSchema map[string]any
	var safeOutput map[string]any
	var zoneSelector map[string]any
	var capability map[string]any
	if json.Unmarshal(request.ComponentContract, &componentContract) != nil || componentContract == nil ||
		json.Unmarshal(request.InputSchema, &inputSchema) != nil || inputSchema == nil ||
		json.Unmarshal(request.UISchema, &uiSchema) != nil || uiSchema == nil ||
		json.Unmarshal(request.SafeObservedOutputSchema, &safeOutput) != nil || safeOutput == nil ||
		json.Unmarshal(request.ZoneSelector, &zoneSelector) != nil || zoneSelector == nil ||
		json.Unmarshal(request.CapabilityRequirement, &capability) != nil || capability == nil {
		apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "draft contracts use invalid JSON shapes")
		return
	}

	componentJSON, componentErr := json.Marshal(componentContract)
	inputJSON, inputErr := json.Marshal(inputSchema)
	uiJSON, uiErr := json.Marshal(uiSchema)
	outputJSON, outputErr := json.Marshal(safeOutput)
	zoneJSON, zoneErr := json.Marshal(zoneSelector)
	capabilityJSON, capabilityErr := json.Marshal(capability)
	if componentErr != nil || inputErr != nil || uiErr != nil || outputErr != nil || zoneErr != nil || capabilityErr != nil ||
		len(componentJSON) > 256*1024 || len(inputJSON) > 256*1024 || len(uiJSON) > 256*1024 ||
		len(outputJSON) > 256*1024 || len(zoneJSON) > 64*1024 || len(capabilityJSON) > 64*1024 {
		apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "draft contract exceeds validation limits")
		return
	}

	// [COMMENT]: Published form contracts are trusted by every customer read
	// path. Validation therefore closes the finite field/widget vocabulary here
	// instead of making repositories or Console guess an arbitrary JSON shape.
	fields, fieldsOK := inputSchema["fields"].([]any)
	if !fieldsOK || len(fields) > 64 || len(inputSchema) != 1 {
		apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "input schema fields are invalid")
		return
	}
	fieldKeys := make(map[string]struct{}, len(fields))
	fieldTypes := make(map[string]string, len(fields))
	fieldCardinality := make(map[string]string, len(fields))
	fieldKeyPattern := regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	allowedTypes := map[string]struct{}{"STRING": {}, "BOOLEAN": {}, "INT64": {}, "DECIMAL": {}, "ENUM": {}, "DNS_LABEL": {}, "CIDR": {}, "PORT": {}, "DURATION": {}, "BYTE_SIZE": {}}
	allowedCardinality := map[string]struct{}{"ONE": {}, "LIST": {}, "SET": {}}
	for _, rawField := range fields {
		field, fieldOK := rawField.(map[string]any)
		if !fieldOK {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "input schema field must be an object")
			return
		}
		for key := range field {
			if key != "key" && key != "value_type" && key != "cardinality" && key != "required" && key != "mutable" && key != "enum_values" && key != "min" && key != "max" && key != "min_length" && key != "max_length" && key != "min_items" && key != "max_items" {
				apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "input schema field contains an unsupported property")
				return
			}
		}
		key, keyOK := field["key"].(string)
		valueType, typeOK := field["value_type"].(string)
		cardinality, cardinalityOK := field["cardinality"].(string)
		_, requiredOK := field["required"].(bool)
		_, mutableOK := field["mutable"].(bool)
		key = strings.TrimSpace(key)
		if !keyOK || !fieldKeyPattern.MatchString(key) || !typeOK || !cardinalityOK || !requiredOK || !mutableOK {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "input schema field contract is invalid")
			return
		}
		if _, exists := allowedTypes[valueType]; !exists {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "input schema value type is unsupported")
			return
		}
		if _, exists := allowedCardinality[cardinality]; !exists {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "input schema cardinality is unsupported")
			return
		}
		if _, exists := fieldKeys[key]; exists {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "input schema field keys must be unique")
			return
		}
		fieldKeys[key] = struct{}{}
		fieldTypes[key] = valueType
		fieldCardinality[key] = cardinality
		if enumValues, exists := field["enum_values"]; exists {
			values, valuesOK := enumValues.([]any)
			if !valuesOK || len(values) == 0 || len(values) > 128 || valueType != "ENUM" {
				apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "input schema enum constraint is invalid")
				return
			}
			seenValues := make(map[string]struct{}, len(values))
			for _, rawValue := range values {
				value, valueOK := rawValue.(string)
				if !valueOK || len(value) == 0 || len(value) > 4096 {
					apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "input schema enum value is invalid")
					return
				}
				if _, exists := seenValues[value]; exists {
					apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "input schema enum values must be unique")
					return
				}
				seenValues[value] = struct{}{}
			}
		} else if valueType == "ENUM" {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "enum fields require enum_values")
			return
		}

		minNumber, hasMin := field["min"].(float64)
		_, minExists := field["min"]
		maxNumber, hasMax := field["max"].(float64)
		_, maxExists := field["max"]
		numericConstraintAllowed := valueType == "INT64" || valueType == "DECIMAL" || valueType == "PORT"
		if (minExists && (!hasMin || !numericConstraintAllowed || math.IsInf(minNumber, 0) || math.IsNaN(minNumber))) ||
			(maxExists && (!hasMax || !numericConstraintAllowed || math.IsInf(maxNumber, 0) || math.IsNaN(maxNumber))) ||
			(minExists && maxExists && minNumber > maxNumber) {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "input schema numeric range is invalid")
			return
		}
		if valueType == "INT64" || valueType == "PORT" {
			if (minExists && (minNumber != math.Trunc(minNumber) || math.Abs(minNumber) > 9007199254740991)) ||
				(maxExists && (maxNumber != math.Trunc(maxNumber) || math.Abs(maxNumber) > 9007199254740991)) {
				apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "input schema integer range is invalid")
				return
			}
		}
		if valueType == "PORT" && ((minExists && (minNumber < 1 || minNumber > 65535)) || (maxExists && (maxNumber < 1 || maxNumber > 65535))) {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "input schema port range is invalid")
			return
		}

		minLength, hasMinLength := field["min_length"].(float64)
		_, minLengthExists := field["min_length"]
		maxLength, hasMaxLength := field["max_length"].(float64)
		_, maxLengthExists := field["max_length"]
		lengthConstraintAllowed := valueType == "STRING" || valueType == "ENUM" || valueType == "DNS_LABEL" || valueType == "CIDR" || valueType == "DURATION" || valueType == "BYTE_SIZE"
		if (minLengthExists && (!hasMinLength || !lengthConstraintAllowed || minLength < 0 || minLength != math.Trunc(minLength) || minLength > 4096)) ||
			(maxLengthExists && (!hasMaxLength || !lengthConstraintAllowed || maxLength < 0 || maxLength != math.Trunc(maxLength) || maxLength > 4096)) ||
			(minLengthExists && maxLengthExists && minLength > maxLength) {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "input schema string length is invalid")
			return
		}

		minItems, hasMinItems := field["min_items"].(float64)
		_, minItemsExists := field["min_items"]
		maxItems, hasMaxItems := field["max_items"].(float64)
		_, maxItemsExists := field["max_items"]
		if (minItemsExists && (!hasMinItems || cardinality == "ONE" || minItems < 0 || minItems != math.Trunc(minItems) || minItems > 64)) ||
			(maxItemsExists && (!hasMaxItems || cardinality == "ONE" || maxItems < 0 || maxItems != math.Trunc(maxItems) || maxItems > 64)) ||
			(minItemsExists && maxItemsExists && minItems > maxItems) {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "input schema collection size is invalid")
			return
		}
	}

	groups, groupsOK := uiSchema["groups"].([]any)
	uiFields, uiFieldsOK := uiSchema["fields"].([]any)
	if !groupsOK || !uiFieldsOK || len(groups) > 32 || len(uiFields) != len(fields) || len(uiSchema) != 2 {
		apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "ui schema shape is invalid")
		return
	}
	groupKeys := make(map[string]struct{}, len(groups))
	for _, rawGroup := range groups {
		group, groupOK := rawGroup.(map[string]any)
		if !groupOK {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "ui schema group is invalid")
			return
		}
		for property := range group {
			if property != "key" && property != "label_i18n" && property != "order" {
				apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "ui schema group contains an unsupported property")
				return
			}
		}
		key, keyOK := group["key"].(string)
		label, labelOK := group["label_i18n"].(map[string]any)
		order, orderOK := group["order"].(float64)
		english, englishOK := label["en"].(string)
		key = strings.TrimSpace(key)
		if !groupOK || !keyOK || !fieldKeyPattern.MatchString(key) || !labelOK || !englishOK || strings.TrimSpace(english) == "" || len(english) > 160 || !orderOK || order < 0 || order != math.Trunc(order) {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "ui schema group is invalid")
			return
		}
		if len(label) == 0 || len(label) > 16 {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "ui schema group localization is invalid")
			return
		}
		for locale, localizedLabel := range label {
			text, textOK := localizedLabel.(string)
			if len(locale) == 0 || len(locale) > 16 || !textOK || strings.TrimSpace(text) == "" || len(text) > 160 {
				apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "ui schema group localization is invalid")
				return
			}
		}
		if _, exists := groupKeys[key]; exists {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "ui schema group keys must be unique")
			return
		}
		groupKeys[key] = struct{}{}
	}
	uiFieldKeys := make(map[string]struct{}, len(uiFields))
	for _, rawUIField := range uiFields {
		uiField, uiFieldOK := rawUIField.(map[string]any)
		if !uiFieldOK {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "ui schema field is invalid")
			return
		}
		for property := range uiField {
			if property != "key" && property != "group" && property != "widget" && property != "label_i18n" && property != "help_i18n" && property != "placeholder_i18n" && property != "order" {
				apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "ui schema field contains an unsupported property")
				return
			}
		}
		key, keyOK := uiField["key"].(string)
		widget, widgetOK := uiField["widget"].(string)
		group, groupOK := uiField["group"].(string)
		label, labelOK := uiField["label_i18n"].(map[string]any)
		order, orderOK := uiField["order"].(float64)
		english, englishOK := label["en"].(string)
		key = strings.TrimSpace(key)
		group = strings.TrimSpace(group)
		if !uiFieldOK || !keyOK || !widgetOK || !groupOK || !labelOK || !englishOK || strings.TrimSpace(english) == "" || len(english) > 160 || !orderOK || order < 0 || order != math.Trunc(order) {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "ui schema field is invalid")
			return
		}
		if len(label) == 0 || len(label) > 16 {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "ui schema field localization is invalid")
			return
		}
		for locale, localizedLabel := range label {
			text, textOK := localizedLabel.(string)
			if len(locale) == 0 || len(locale) > 16 || !textOK || strings.TrimSpace(text) == "" || len(text) > 160 {
				apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "ui schema field localization is invalid")
				return
			}
		}
		for _, optionalLocalization := range []string{"help_i18n", "placeholder_i18n"} {
			rawLocalization, exists := uiField[optionalLocalization]
			if !exists {
				continue
			}
			localization, localizationOK := rawLocalization.(map[string]any)
			if !localizationOK || len(localization) == 0 || len(localization) > 16 {
				apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "ui schema optional localization is invalid")
				return
			}
			for locale, localizedText := range localization {
				text, textOK := localizedText.(string)
				if len(locale) == 0 || len(locale) > 16 || !textOK || len(text) > 4096 {
					apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "ui schema optional localization is invalid")
					return
				}
			}
		}
		if _, exists := fieldKeys[key]; !exists {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "ui schema references an unknown input field")
			return
		}
		if _, exists := groupKeys[group]; !exists {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "ui schema references an unknown group")
			return
		}
		if _, exists := uiFieldKeys[key]; exists {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "ui schema field keys must be unique")
			return
		}
		uiFieldKeys[key] = struct{}{}
		compatible := false
		if fieldCardinality[key] != "ONE" {
			compatible = widget == "TOKEN_LIST" || (fieldTypes[key] == "ENUM" && widget == "MULTI_SELECT")
		} else {
			switch fieldTypes[key] {
			case "BOOLEAN":
				compatible = widget == "SWITCH"
			case "INT64", "DECIMAL", "PORT":
				compatible = widget == "NUMBER"
			case "ENUM":
				compatible = widget == "SELECT" || widget == "RADIO"
			case "STRING":
				compatible = widget == "TEXT" || widget == "TEXTAREA"
			default:
				compatible = widget == "TEXT"
			}
		}
		if !compatible {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "ui widget is incompatible with the input field")
			return
		}
	}

	mode, modeOK := zoneSelector["mode"].(string)
	if !modeOK || (mode != "all" && mode != "allow_list") {
		apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "zone selector mode is invalid")
		return
	}
	if mode == "all" {
		if len(zoneSelector) != 1 {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "all-zone selector cannot contain an allow-list")
			return
		}
	} else {
		zoneIDs, zoneIDsOK := zoneSelector["zone_ids"].([]any)
		if !zoneIDsOK || len(zoneIDs) == 0 || len(zoneIDs) > 128 || len(zoneSelector) != 2 {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "zone allow-list is invalid")
			return
		}
		seenZoneIDs := make(map[uuid.UUID]struct{}, len(zoneIDs))
		for _, rawZoneID := range zoneIDs {
			zoneIDText, zoneIDOK := rawZoneID.(string)
			zoneID, zoneParseErr := uuid.Parse(strings.TrimSpace(zoneIDText))
			if !zoneIDOK || zoneParseErr != nil || zoneID == uuid.Nil {
				apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "zone allow-list contains an invalid id")
				return
			}
			if _, exists := seenZoneIDs[zoneID]; exists {
				apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "zone allow-list contains a duplicate id")
				return
			}
			seenZoneIDs[zoneID] = struct{}{}
		}
	}

	requiredCapabilities, capabilitiesOK := capability["all_of"].([]any)
	if !capabilitiesOK || len(requiredCapabilities) > 16 || len(capability) != 1 {
		apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "capability requirement is invalid")
		return
	}
	allowedCapabilities := map[string]struct{}{"mail": {}, "hypervisor": {}, "kubernetes": {}, "ai": {}, "storage": {}, "database": {}}
	seenCapabilities := make(map[string]struct{}, len(requiredCapabilities))
	for _, rawCapability := range requiredCapabilities {
		capabilityName, capabilityOK := rawCapability.(string)
		if !capabilityOK {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "capability requirement contains an invalid value")
			return
		}
		if _, exists := allowedCapabilities[capabilityName]; !exists {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "capability requirement is unsupported")
			return
		}
		if _, exists := seenCapabilities[capabilityName]; exists {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "capability requirement contains a duplicate")
			return
		}
		seenCapabilities[capabilityName] = struct{}{}
	}

	// [COMMENT]: Validation walks YAML nodes, never interpolated text. CP checks
	// publish safety but deliberately does not enumerate !aurora/param keys or
	// bind them to input_schema; that relationship remains SRE-owned contract.
	yamlDecoder := yaml.NewDecoder(strings.NewReader(request.TemplateYAML))
	documentComponents := make(map[string]struct{})
	documentCount := 0
	componentPattern := regexp.MustCompile(`^[a-z][a-z0-9-]{0,26}$`)
	for {
		var document yaml.Node
		err := yamlDecoder.Decode(&document)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "template must contain Kubernetes YAML objects")
			return
		}
		documentCount++
		if documentCount > 128 {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "template contains too many YAML documents")
			return
		}
		root := document.Content[0]
		var apiVersionNode *yaml.Node
		var kindNode *yaml.Node
		var metadataNode *yaml.Node
		var dataNode *yaml.Node
		var stringDataNode *yaml.Node
		for index := 0; index < len(root.Content); index += 2 {
			key := root.Content[index]
			value := root.Content[index+1]
			switch key.Value {
			case "apiVersion":
				apiVersionNode = value
			case "kind":
				kindNode = value
			case "metadata":
				metadataNode = value
			case "data":
				dataNode = value
			case "stringData":
				stringDataNode = value
			}
		}
		if apiVersionNode == nil || kindNode == nil || metadataNode == nil ||
			apiVersionNode.Kind != yaml.ScalarNode || kindNode.Kind != yaml.ScalarNode || metadataNode.Kind != yaml.MappingNode ||
			strings.TrimSpace(apiVersionNode.Value) == "" || strings.TrimSpace(kindNode.Value) == "" ||
			strings.HasPrefix(apiVersionNode.Tag, "!aurora/") || strings.HasPrefix(kindNode.Tag, "!aurora/") {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "apiVersion, kind and metadata must be static")
			return
		}
		// [COMMENT]: Cấm hardcode giá trị trong Secret để tránh lộ credential trong catalog.
		if kindNode.Value == "Secret" && ((dataNode != nil && dataNode.Kind == yaml.MappingNode && len(dataNode.Content) > 0) ||
			(stringDataNode != nil && stringDataNode.Kind == yaml.MappingNode && len(stringDataNode.Content) > 0)) {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "literal Kubernetes Secret values are forbidden")
			return
		}

		var componentName string
		for index := 0; index < len(metadataNode.Content); index += 2 {
			key := metadataNode.Content[index]
			value := metadataNode.Content[index+1]
			if key.Value == "namespace" || key.Value == "generateName" {
				apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "template cannot own namespace or generated names")
				return
			}
			if key.Value == "name" {
				if value.Kind != yaml.ScalarNode || value.Tag != "!aurora/component" || !componentPattern.MatchString(strings.TrimSpace(value.Value)) {
					apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "metadata.name must use a valid !aurora/component tag")
					return
				}
				componentName = strings.TrimSpace(value.Value)
				continue
			}
			stack := []*yaml.Node{value}
			for len(stack) > 0 {
				last := len(stack) - 1
				node := stack[last]
				stack = stack[:last]
				if strings.HasPrefix(node.Tag, "!aurora/") {
					apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "metadata cannot contain template parameters")
					return
				}
				stack = append(stack, node.Content...)
			}
			if (key.Value == "annotations" || key.Value == "labels") && value.Kind == yaml.MappingNode {
				for metadataIndex := 0; metadataIndex < len(value.Content); metadataIndex += 2 {
					if strings.HasPrefix(strings.TrimSpace(value.Content[metadataIndex].Value), "platform.aurora.io/") {
						apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "template cannot set protected platform metadata")
						return
					}
				}
			}
		}
		if componentName == "" {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "every YAML document must declare a component name")
			return
		}
		allNodes := []*yaml.Node{root}
		for len(allNodes) > 0 {
			last := len(allNodes) - 1
			node := allNodes[last]
			allNodes = allNodes[:last]
			if node.Kind == yaml.ScalarNode && (strings.Contains(node.Value, "{{") || strings.Contains(node.Value, "}}")) {
				apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "text interpolation is not supported")
				return
			}
			if strings.HasPrefix(node.Tag, "!") && !strings.HasPrefix(node.Tag, "!!") {
				switch node.Tag {
				case "!aurora/param":
					parameterKey := strings.TrimSpace(node.Value)
					if node.Kind != yaml.ScalarNode || parameterKey == "" || len(parameterKey) > 128 {
						apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "invalid !aurora/param tag")
						return
					}
				case "!aurora/component":
					if node.Kind != yaml.ScalarNode || !componentPattern.MatchString(strings.TrimSpace(node.Value)) {
						apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "invalid !aurora/component tag")
						return
					}
				default:
					apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "unsupported YAML tag")
					return
				}
			}
			allNodes = append(allNodes, node.Content...)
		}
		documentComponents[componentName] = struct{}{}
	}
	if documentCount == 0 {
		apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "template must contain at least one YAML document")
		return
	}

	// [COMMENT]: Đảm bảo contract component map 1-1 với YAML document components.
	contractComponents := make(map[string]struct{}, len(componentContract))
	applyOrders := make(map[int64]struct{}, len(componentContract))
	deleteOrders := make(map[int64]struct{}, len(componentContract))
	if len(componentContract) == 0 || len(componentContract) > 128 {
		apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "component contract is empty or too large")
		return
	}
	for _, component := range componentContract {
		id, idOK := component["id"].(string)
		applyOrder, applyOK := component["apply_order"].(float64)
		deleteOrder, deleteOK := component["delete_order"].(float64)
		readiness, readinessOK := component["readiness"].(map[string]any)
		id = strings.TrimSpace(id)
		if !idOK || !componentPattern.MatchString(id) || !applyOK || !deleteOK ||
			applyOrder < 1 || deleteOrder < 1 || applyOrder != math.Trunc(applyOrder) || deleteOrder != math.Trunc(deleteOrder) ||
			!readinessOK || len(readiness) == 0 {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "component id, order and readiness are required")
			return
		}
		if _, exists := contractComponents[id]; exists {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "component ids must be unique")
			return
		}
		if _, exists := applyOrders[int64(applyOrder)]; exists {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "apply order must be unique")
			return
		}
		if _, exists := deleteOrders[int64(deleteOrder)]; exists {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "delete order must be unique")
			return
		}
		contractComponents[id] = struct{}{}
		applyOrders[int64(applyOrder)] = struct{}{}
		deleteOrders[int64(deleteOrder)] = struct{}{}
	}
	// [COMMENT]: Mọi component trong YAML phải có entry trong contract và ngược lại.
	for component := range documentComponents {
		if _, exists := contractComponents[component]; !exists {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "YAML component is missing from component contract")
			return
		}
	}
	for component := range contractComponents {
		if _, exists := documentComponents[component]; !exists {
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_FAILED", "component contract has no YAML document")
			return
		}
	}

	templateHash := sha256.Sum256([]byte(request.TemplateYAML))
	contractHasher := sha256.New()
	contractHasher.Write([]byte("managed-service-contract-v1\x00"))
	contractHasher.Write([]byte(request.ContractVersion))
	contractHasher.Write([]byte("\x00component\x00"))
	contractHasher.Write(componentJSON)
	contractHasher.Write([]byte("\x00input\x00"))
	contractHasher.Write(inputJSON)
	contractHasher.Write([]byte("\x00ui\x00"))
	contractHasher.Write(uiJSON)
	contractHasher.Write([]byte("\x00output\x00"))
	contractHasher.Write(outputJSON)
	contractHasher.Write([]byte("\x00zone\x00"))
	contractHasher.Write(zoneJSON)
	contractHasher.Write([]byte("\x00capability\x00"))
	contractHasher.Write(capabilityJSON)
	contractHash := contractHasher.Sum(nil)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	result, err := h.service.ValidateDraft(ctx, &entity.ValidateDraft{
		DraftID: draftID, Actor: actor, ProofID: proofID,
		ExpectedVersion: request.ExpectedVersion, TemplateBundleSHA256: templateHash[:],
		ContractSHA256: contractHash, ValidationContract: request.ContractVersion,
	})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			apires.RespondNotFound(c, "draft not found")
		case errors.Is(err, taxonomy.ErrCatalogRevisionStale):
			apires.RespondConflict(c, "draft changed; reload before validating")
		case errors.Is(err, taxonomy.ErrCatalogRecordImmutable):
			apires.RespondConflict(c, "published revision cannot be validated")
		default:
			logger.HandlerError(c, "managedservice.revision.validate_draft", err)
			apires.RespondInternalError(c, "SRE_CATALOG_INTERNAL")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id": result.ID, "row_version": result.RowVersion,
		"validated_row_version": result.ValidatedRowVersion, "validated_at": result.ValidatedAt,
		"template_bundle_sha256": hex.EncodeToString(result.TemplateBundleSHA256),
		"contract_sha256":        hex.EncodeToString(result.ContractSHA256),
	}, "draft validated")
}

func (h *RevisionHandler) PublishDraft(c *gin.Context) {
	// [COMMENT]: Publish là thao tác critical và không thể hoàn tác – bắt buộc có verified proof header.
	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	proofID, proofErr := uuid.Parse(strings.TrimSpace(c.GetHeader("x-session-proof-challenge-id")))
	if actor != "sre" || c.GetHeader("x-session-proof-verified") != "true" || proofErr != nil || proofID == uuid.Nil {
		apires.RespondForbidden(c, "verified critical proof is required")
		return
	}

	draftID, draftErr := uuid.Parse(strings.TrimSpace(c.Param("draft_id")))
	if draftErr != nil || draftID == uuid.Nil {
		apires.RespondBadRequest(c, "invalid draft id")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var request dto.PublishDraftRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	// [COMMENT]: Client phải gửi kèm expected hash để đảm bảo publish đúng artifact đã validate.
	bundleHash, bundleErr := hex.DecodeString(strings.TrimSpace(request.ExpectedBundleSHA256))
	contractHash, contractErr := hex.DecodeString(strings.TrimSpace(request.ExpectedContractSHA256))
	if request.ExpectedVersion < 1 || bundleErr != nil || contractErr != nil || len(bundleHash) != sha256.Size || len(contractHash) != sha256.Size {
		apires.RespondBadRequest(c, "invalid publish precondition")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	result, err := h.service.PublishDraft(ctx, &entity.PublishDraft{
		DraftID: draftID, Actor: actor, ProofID: proofID,
		ExpectedVersion: request.ExpectedVersion, ExpectedBundleSHA256: bundleHash,
		ExpectedContractHash: contractHash,
	})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			apires.RespondNotFound(c, "draft not found")
		case errors.Is(err, taxonomy.ErrCatalogRevisionStale):
			apires.RespondConflict(c, "draft changed; reload before publishing")
		case errors.Is(err, taxonomy.ErrCatalogValidationFailed):
			// [COMMENT]: 422 để phân biệt lỗi pre-condition validation với conflict state.
			apires.RespondUnprocessableEntity(c, "SRE_CATALOG_VALIDATION_REQUIRED", "current draft must be validated before publish")
		case errors.Is(err, taxonomy.ErrCatalogRecordImmutable):
			apires.RespondConflict(c, "revision cannot be published")
		default:
			logger.HandlerError(c, "managedservice.revision.publish_draft", err)
			apires.RespondInternalError(c, "SRE_CATALOG_INTERNAL")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id": result.ID, "blueprint_id": result.BlueprintID, "revision": result.Revision,
		"state": result.State, "row_version": result.RowVersion,
		"template_bundle_sha256": hex.EncodeToString(result.TemplateBundleSHA256),
		"contract_sha256":        hex.EncodeToString(result.ContractSHA256),
	}, "revision published")
}

func (h *RevisionHandler) RetireRevision(c *gin.Context) {
	// [COMMENT]: Retire revision là thao tác critical – bắt buộc có verified proof header.
	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	proofID, proofErr := uuid.Parse(strings.TrimSpace(c.GetHeader("x-session-proof-challenge-id")))
	if actor != "sre" || c.GetHeader("x-session-proof-verified") != "true" || proofErr != nil || proofID == uuid.Nil {
		apires.RespondForbidden(c, "verified critical proof is required")
		return
	}

	revisionID, revisionErr := uuid.Parse(strings.TrimSpace(c.Param("revision_id")))
	if revisionErr != nil || revisionID == uuid.Nil {
		apires.RespondBadRequest(c, "invalid revision id")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var request dto.RetireRevisionRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.ExpectedVersion < 1 {
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	result, err := h.service.RetireRevision(ctx, &entity.RetireRevision{
		RevisionID: revisionID, Actor: actor, ProofID: proofID,
		ExpectedVersion: request.ExpectedVersion,
	})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			apires.RespondNotFound(c, "revision not found")
		case errors.Is(err, taxonomy.ErrCatalogConcurrentChange):
			apires.RespondConflict(c, "refresh revision and retry")
		default:
			// [COMMENT]: Mọi lỗi transition khác đều map sang conflict.
			apires.RespondConflict(c, "revision cannot be retired")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{"id": result.ID, "state": result.State, "row_version": result.RowVersion}, "revision retired")
}

func (h *RevisionHandler) DeleteDraft(c *gin.Context) {
	// [COMMENT]: Delete draft là thao tác critical – bắt buộc có verified proof header.
	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	proofID, proofErr := uuid.Parse(strings.TrimSpace(c.GetHeader("x-session-proof-challenge-id")))
	if actor != "sre" || c.GetHeader("x-session-proof-verified") != "true" || proofErr != nil || proofID == uuid.Nil {
		apires.RespondForbidden(c, "verified critical proof is required")
		return
	}

	draftID, draftErr := uuid.Parse(strings.TrimSpace(c.Param("draft_id")))
	if draftErr != nil || draftID == uuid.Nil {
		apires.RespondBadRequest(c, "invalid draft id")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var request dto.DeleteDraftRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.ExpectedVersion < 1 {
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	err := h.service.DeleteDraft(ctx, &entity.DeleteDraft{
		DraftID: draftID, Actor: actor, ProofID: proofID,
		ExpectedVersion: request.ExpectedVersion,
	})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			apires.RespondNotFound(c, "draft not found")
		case errors.Is(err, taxonomy.ErrCatalogConcurrentChange):
			apires.RespondConflict(c, "refresh draft and retry")
		case errors.Is(err, taxonomy.ErrCatalogRecordPinned):
			apires.RespondConflict(c, "revision is pinned by an instance")
		default:
			// [COMMENT]: Mọi lỗi immutable/transition khác đều là conflict.
			apires.RespondConflict(c, "only an unpinned draft can be deleted")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{"id": draftID}, "draft deleted")
}
