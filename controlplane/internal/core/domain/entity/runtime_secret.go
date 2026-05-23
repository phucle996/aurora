package coreEntity

import "time"

type RuntimeSecret struct {
	VersionID   string
	FamilyCode  string
	Secret      string
	Fingerprint string
	IsPrimary   bool
	ActivatedAt *time.Time
	NotBefore   time.Time
	NotAfter    *time.Time
}

type RuntimeSecretFamily struct {
	Family     SecretFamily
	Primary    RuntimeSecret
	Candidates []RuntimeSecret
	LoadedAt   time.Time
}
