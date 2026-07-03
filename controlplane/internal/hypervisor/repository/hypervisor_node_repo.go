package hypervisorRepoImpl

import (
	"context"
	"fmt"

	"controlplane/internal/config"
	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorRepoInterface "controlplane/internal/hypervisor/domain/repo"
	hypervisorModel "controlplane/internal/hypervisor/model"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type NodeRepoPostgres struct {
	db     *pgxpool.Pool
	cfg    *config.Config
	schema string
}

// NewNodeRepoPostgres khởi tạo thực thể SQL Repository cho hypervisor nodes
func NewNodeRepoPostgres(cfg *config.Config, db *pgxpool.Pool) hypervisorRepoInterface.NodeRepository {
	return &NodeRepoPostgres{
		db:     db,
		cfg:    cfg,
		schema: cfg.SchemaSQL.Hypervisor,
	}
}

// ListNodesByZone thực thi truy vấn danh sách nodes vật lý thuộc zone từ database
func (r *NodeRepoPostgres) ListNodesByZone(ctx context.Context, zoneID uuid.UUID) ([]*hypervisorEntity.HypervisorNode, error) {

	// [COMMENT]: Thực thi câu lệnh SQL select tối ưu (không SELECT trường zone_id do đã lọc theo tham số)
	query := fmt.Sprintf(`
		SELECT id, node_code, name, status, 
		       cpu_cores_total, cpu_cores_used, 
		       ram_mb_total, ram_mb_used, 
		       storage_gb_total, storage_gb_used, 
		       last_active_at, created_at, updated_at
		FROM %s.nodes
		WHERE zone_id = $1
		ORDER BY node_code ASC
	`, r.schema)

	rows, err := r.db.Query(ctx, query, zoneID)
	if err != nil {
		return nil, fmt.Errorf("hypervisor repository: select nodes failed: %w", err)
	}
	defer rows.Close()

	var nodes []*hypervisorEntity.HypervisorNode
	for rows.Next() {
		var n hypervisorModel.HypervisorNode
		err := rows.Scan(
			&n.ID, &n.NodeCode, &n.Name, &n.Status,
			&n.CPUCoresTotal, &n.CPUCoresUsed,
			&n.RAMMBTotal, &n.RAMMBUsed,
			&n.StorageGBTotal, &n.StorageGBUsed,
			&n.LastActiveAt, &n.CreatedAt, &n.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("hypervisor repository: scan row data failed: %w", err)
		}

		// [COMMENT]: Gán lại zone_id từ tham số đầu vào do không lấy từ CSDL
		n.ZoneID = zoneID
		domainNode := hypervisorModel.NodeModelToEntity(n)
		nodes = append(nodes, &domainNode)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("hypervisor repository: rows iteration failed: %w", err)
	}

	return nodes, nil
}
