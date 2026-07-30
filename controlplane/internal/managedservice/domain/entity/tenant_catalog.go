package entity

import (
	"encoding/json"

	"github.com/google/uuid"
)

type ListTenantCatalog struct {
	UserID         uuid.UUID
	TenantID       uuid.UUID
	WorkspaceID    uuid.UUID
	ZoneID         uuid.UUID
	AfterVersionID uuid.UUID
	Limit          int
}

type TenantCatalogItem struct {
	CategoryID                uuid.UUID
	CategoryCode              string
	CategoryNameI18n          json.RawMessage
	CategoryDescriptionI18n   json.RawMessage
	CategoryIconKey           string
	DefinitionID              uuid.UUID
	DefinitionCode            string
	DefinitionNameI18n        json.RawMessage
	DefinitionDescriptionI18n json.RawMessage
	DefinitionIconKey         string
	VersionID                 uuid.UUID
	VersionCode               string
	VersionDisplay            string
	VersionNameI18n           json.RawMessage
	VersionDescriptionI18n    json.RawMessage
	VersionIconKey            string
	RevisionID                uuid.UUID
	RevisionNumber            int64
	ContractVersion           string
	ContractSHA256            []byte
}

type TenantCatalogPage struct {
	Items   []TenantCatalogItem
	HasMore bool
}
