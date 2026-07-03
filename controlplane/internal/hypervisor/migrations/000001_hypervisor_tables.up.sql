-- ======================================================================================================
-- 📂 MIGRATION: 000001_hypervisor_tables.up.sql
--            Hypervisor Module — Table Definitions & Constraints
-- ======================================================================================================

-- [COMMENT]: Bảng lưu trữ thông tin logic và trạng thái tải của Proxmox Hypervisor Nodes
CREATE TABLE IF NOT EXISTS nodes (
    id UUID PRIMARY KEY,
    zone_id UUID NOT NULL, -- Ánh xạ logic tới hierarchy.zones(id) để phục vụ xếp lịch ảo hóa
    node_code VARCHAR(100) NOT NULL, -- Định danh vật lý nhận diện từ Proxmox API (ví dụ: pve-node-01)
    name VARCHAR(255) NOT NULL,      -- Tên hiển thị do SRE đặt hoặc tự cấu hình
    status VARCHAR(32) NOT NULL DEFAULT 'disconnected', -- disconnected, connecting, connected, degraded, maintenance
    
    -- Các thông số dung lượng tài nguyên (Capacity metrics) được cập nhật động từ Heartbeat
    cpu_cores_total INT NOT NULL DEFAULT 0,
    cpu_cores_used INT NOT NULL DEFAULT 0,
    ram_mb_total BIGINT NOT NULL DEFAULT 0,
    ram_mb_used BIGINT NOT NULL DEFAULT 0,
    storage_gb_total BIGINT NOT NULL DEFAULT 0,
    storage_gb_used BIGINT NOT NULL DEFAULT 0,
    
    -- Metadata giám sát và audit
    last_active_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    
    -- Ràng buộc unique để tránh tạo trùng lặp node vật lý trong cùng một phân vùng (Zone)
    CONSTRAINT ux_hypervisor_nodes_zone_code UNIQUE (zone_id, node_code)
);

-- Tạo các chỉ mục tối ưu hóa hiệu năng truy vấn cho luồng SRE đọc trạng thái
CREATE INDEX IF NOT EXISTS idx_hypervisor_nodes_zone ON nodes(zone_id);
CREATE INDEX IF NOT EXISTS idx_hypervisor_nodes_status ON nodes(status);

COMMENT ON TABLE nodes IS 'Bảng lưu trữ thông tin logic và trạng thái tải của Proxmox Hypervisor Nodes';
COMMENT ON COLUMN nodes.id IS 'ID định danh duy nhất của Node, được tự động sinh UUIDv7 ở tầng dịch vụ';
COMMENT ON COLUMN nodes.zone_id IS 'Mã phân vùng Zone chứa node ảo hóa này';
COMMENT ON COLUMN nodes.node_code IS 'Định danh máy chủ vật lý do Proxmox Cluster trả về';
COMMENT ON COLUMN nodes.status IS 'Trạng thái hoạt động vật lý của node (connected, disconnected, v.v.)';
