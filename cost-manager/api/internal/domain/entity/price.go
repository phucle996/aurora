package entity

import (
	"time"

	"github.com/google/uuid"
)

// Price là domain entity, không chứa JSON tag
type Price struct {
	ID            uuid.UUID
	ServiceType   string
	ZoneCode      string
	UnitPrice     float64
	Currency      string
	Tier          string
	EffectiveFrom time.Time
	EffectiveTo   *time.Time
	CreatedAt     time.Time
}

// Zone là domain entity thuần
type Zone struct {
	ID     string
	Code   string
	Name   string
	Status string
}
