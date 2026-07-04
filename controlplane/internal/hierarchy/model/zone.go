package coreModel

import (
	coreEntity "controlplane/internal/hierarchy/domain/entity"
	"time"

	"github.com/google/uuid"
)

type Zone struct {
	ID          uuid.UUID  `db:"id"`
	Code        string     `db:"code"`
	Name        string     `db:"name"`
	Location    string     `db:"location"`
	Description string     `db:"description"`
	Status      string     `db:"status"`
	CreatedAt   *time.Time `db:"created_at"`
	UpdatedAt   *time.Time `db:"updated_at"`
}

type ZoneService struct {
	ID           uuid.UUID `db:"id"`
	ZoneID       uuid.UUID `db:"zone_id"`
	ServiceType  string    `db:"service_type"`
	DesiredState bool      `db:"desired_state"` // [COMMENT]: Map với cột desired_state trong DB
	ActualState  string    `db:"actual_state"`  // [COMMENT]: Map với cột actual_state trong DB
	CreatedAt    time.Time `db:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"`
}

func ZoneEntityToModel(e coreEntity.Zone) Zone {
	return Zone{
		ID:          e.ID,
		Code:        e.Code,
		Name:        e.Name,
		Location:    e.Location,
		Description: e.Description,
		Status:      string(e.Status),
		CreatedAt:   e.CreatedAt,
		UpdatedAt:   e.UpdatedAt,
	}
}

func ZoneModelToEntity(m Zone) coreEntity.Zone {
	return coreEntity.Zone{
		ID:          m.ID,
		Code:        m.Code,
		Name:        m.Name,
		Location:    m.Location,
		Description: m.Description,
		Status:      coreEntity.ZoneStatus(m.Status),
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

// ZoneServiceEntityToModel chuyển đổi thực thể domain thành database model để thực thi câu lệnh SQL.
func ZoneServiceEntityToModel(e coreEntity.ZoneService) ZoneService {
	return ZoneService{
		ID:           e.ID,
		ZoneID:       e.ZoneID,
		ServiceType:  string(e.ServiceType),
		DesiredState: e.DesiredState,
		ActualState:  e.ActualState,
		CreatedAt:    e.CreatedAt,
		UpdatedAt:    e.UpdatedAt,
	}
}

// ZoneServiceModelToEntity chuyển đổi database model thành thực thể domain phục vụ logic nghiệp vụ.
func ZoneServiceModelToEntity(m ZoneService) coreEntity.ZoneService {
	return coreEntity.ZoneService{
		ID:           m.ID,
		ZoneID:       m.ZoneID,
		ServiceType:  coreEntity.ZoneServiceType(m.ServiceType),
		DesiredState: m.DesiredState,
		ActualState:  m.ActualState,
		CreatedAt:    m.CreatedAt,
		UpdatedAt:    m.UpdatedAt,
	}
}
