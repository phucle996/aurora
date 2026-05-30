package mailReq

type CreateGatewayRequest struct {
	Name        string `json:"name" binding:"required"`
	RoutePolicy string `json:"route_policy" binding:"required"`
}

type UpdateGatewayRequest struct {
	Name        string `json:"name" binding:"required"`
	RoutePolicy string `json:"route_policy" binding:"required"`
	IsActive    bool   `json:"is_active"`
}
