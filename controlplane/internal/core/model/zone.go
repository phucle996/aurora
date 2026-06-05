package coreModel

import (
	coreEntity "controlplane/internal/core/domain/entity"
	"time"
)

type Zone struct {
	ID        string
	Code      string
	Name      string
	Location  string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type ZoneService struct {
	ID          string
	ZoneID      string
	ServiceType string
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func ZoneEntityToModel(e coreEntity.Zone) Zone {
	return Zone{
		ID:        e.ID,
		Code:      e.Code,
		Name:      e.Name,
		Location:  e.Location,
		Status:    string(e.Status),
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
	}
}

func ZoneModelToEntity(m Zone) coreEntity.Zone {
	return coreEntity.Zone{
		ID:        m.ID,
		Code:      m.Code,
		Name:      m.Name,
		Location:  m.Location,
		Status:    coreEntity.ZoneStatus(m.Status),
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

func ZoneServiceEntityToModel(e coreEntity.ZoneService) ZoneService {
	return ZoneService{
		ID:          e.ID,
		ZoneID:      e.ZoneID,
		ServiceType: string(e.ServiceType),
		Enabled:     e.Enabled,
		CreatedAt:   e.CreatedAt,
		UpdatedAt:   e.UpdatedAt,
	}
}

func ZoneServiceModelToEntity(m ZoneService) coreEntity.ZoneService {
	return coreEntity.ZoneService{
		ID:          m.ID,
		ZoneID:      m.ZoneID,
		ServiceType: coreEntity.ZoneServiceType(m.ServiceType),
		Enabled:     m.Enabled,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}
