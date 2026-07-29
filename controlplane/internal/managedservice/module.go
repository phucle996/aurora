package managedservice

import (
	"errors"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Module is the application-layer shell for the Managed Service Platform.
// Workflow dependencies are intentionally absent until their vertical slices
// are shipped; keeping the shell small prevents speculative tables and routes.
type Module struct {
	cfg        *config.Config
	db         *pgxpool.Pool
	L1Registry *cacheengine.CacheRegistry
}

// NewModule wires the module boundary. Dependency failure is handled while
// building the app graph; service/repository methods must not repeat nil checks.
func NewModule(
	cfg *config.Config,
	db *pgxpool.Pool,
	cacheEngine *cacheengine.CacheRegistry,
) (*Module, error) {
	if cfg == nil {
		return nil, errors.New("managedservice module: config is required")
	}
	if db == nil {
		return nil, errors.New("managedservice module: database pool is required")
	}
	if cacheEngine == nil {
		return nil, errors.New("managedservice module: cache engine registry is required")
	}

	return &Module{
		cfg:        cfg,
		db:         db,
		L1Registry: cacheEngine,
	}, nil
}
