package mailEntity

import "time"

type Gateway struct {
	ID          string
	TenantID    string
	Name        string
	RoutePolicy string
	IsActive    bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
