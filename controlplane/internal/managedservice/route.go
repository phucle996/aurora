package managedservice

import "github.com/gin-gonic/gin"

// RegisterRoutes reserves the module route boundary. Business routes are not
// registered until their corresponding workflow has a complete
// handler/service/repository vertical slice.
func RegisterRoutes(_ *gin.Engine, _ *Module) {}
