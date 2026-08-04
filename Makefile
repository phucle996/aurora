# ============================================================================
# 🚀 Aurora Central & Zone Multi-Cluster Management Makefile
# ============================================================================
# Tập hợp các lệnh tự động hóa quy trình Bootstrapping, Secret Provisioning,
# và Orchestration cho toàn bộ môi trường Cloud-Native Local Dev.
# ============================================================================

.PHONY: help init-central init-zone up-central up-zone down-central down-zone logs-central logs-zone status clean

# [COMMENT]: Mặc định hiển thị danh sách các lệnh Make hỗ trợ
help:
	@echo "=============================================================="
	@echo "🛠️  AURORA SYSTEM MANAGEMENT COMMANDS"
	@echo "=============================================================="
	@echo "  make init-central  : Khởi chạy Vault dev, bootstrap secrets, up Central stack"
	@echo "  make init-zone     : Sinh Keyring X25519 cho Dataplane, up Zone stack"
	@echo "  make up-central    : Khởi động nhanh Central stack (không build lại)"
	@echo "  make up-zone       : Khởi động nhanh Zone stack (không build lại)"
	@echo "  make down-central  : Tắt toàn bộ Central stack"
	@echo "  make down-zone     : Tắt toàn bộ Zone stack"
	@echo "  make logs-central  : Theo dõi realtime log của Central"
	@echo "  make logs-zone     : Theo dõi realtime log của Zone"
	@echo "  make status        : Kiểm tra trạng thái sức khỏe các container"
	@echo "=============================================================="

# [COMMENT]: Khởi tạo hoàn chỉnh môi trường Central (Vault Dev Mode + Bootstrap Secrets + Microservices)
init-central:
	@echo "🚀 [1/3] Khởi động Vault container..."
	docker compose -f dev/central/compose.yml up -d vault
	@echo "⏳ [2/3] Chờ Vault REST API sẵn sàng..."
	@until curl -s http://localhost:8200/v1/sys/health >/dev/null; do sleep 1; done
	@echo "🔒 [3/3] Chạy Bootstrap Script nạp Static Tokens & Connections..."
	dev/central/vault/vault-bootstrap.sh -t root
	@echo "📦 [4/4] Khởi động toàn bộ Central Stack..."
	docker compose -f dev/central/compose.yml up -d

# [COMMENT]: Khởi tạo hoàn chỉnh môi trường Zone Edge (Sinh Keyring X25519 + Up Zone Services)
init-zone:
	@echo "🔑 [1/2] Kiểm tra/Sinh Keyring X25519 cho Zone Dataplane..."
	python3 scripts/gen-zone-keyring.py
	@echo "📦 [2/2] Khởi động toàn bộ Zone Stack..."
	docker compose -f dev/zone/compose.yml up -d

# [COMMENT]: Khởi động nhanh Central (không qua bước bootstrap)
up-central:
	docker compose -f dev/central/compose.yml up -d

# [COMMENT]: Khởi động nhanh Zone
up-zone:
	docker compose -f dev/zone/compose.yml up -d

# [COMMENT]: Dừng môi trường Central
down-central:
	docker compose -f dev/central/compose.yml down

# [COMMENT]: Dừng môi trường Zone
down-zone:
	docker compose -f dev/zone/compose.yml down

# [COMMENT]: Xem log Central
logs-central:
	docker compose -f dev/central/compose.yml logs -f

# [COMMENT]: Xem log Zone
logs-zone:
	docker compose -f dev/zone/compose.yml logs -f

# [COMMENT]: Check trạng thái các container
status:
	@echo "=== CENTRAL CONTAINERS ==="
	docker compose -f dev/central/compose.yml ps
	@echo ""
	@echo "=== ZONE CONTAINERS ==="
	docker compose -f dev/zone/compose.yml ps
