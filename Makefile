# ============================================================================
# 🚀 Aurora Central & Zone Multi-Cluster Management Makefile
# ============================================================================
# Tập hợp các lệnh tự động hóa quy trình Bootstrapping, Secret Provisioning,
# và Orchestration cho toàn bộ môi trường Cloud-Native Local Dev.
# ============================================================================

.PHONY: help init-central init-zone up-central up-zone down-central down-zone clean clean-central clean-zone

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
	@echo "  make clean-central : Dọn dẹp triệt để Central (xóa cả Volumes)"
	@echo "  make clean-zone    : Dọn dẹp triệt để Zone (xóa Volumes & Keyring)"
	@echo "  make clean         : Dọn dẹp sạch sẽ toàn bộ hệ thống (Central + Zone)"
	@echo "=============================================================="

# [COMMENT]: Khởi tạo hoàn chỉnh môi trường Central (Vault Dev Mode + Bootstrap Secrets + Microservices)
init-central:
	@echo "🚀 [1/4] Khởi động Vault container..."
	docker compose -f dev/central/compose.yml up -d --no-build vault
	@echo "⏳ [2/4] Chờ Vault REST API sẵn sàng..."
	@until curl -s http://localhost:8200/v1/sys/health >/dev/null; do sleep 1; done
	@echo "🔒 [3/4] Chạy Bootstrap Script nạp Static Tokens & Connections..."
	dev/central/vault/vault-bootstrap.sh -t root
	@echo "📦 [4/4] Khởi động toàn bộ Central Stack..."
	docker compose -f dev/central/compose.yml pull
	docker compose -f dev/central/compose.yml up -d --no-build

# [COMMENT]: Khởi tạo hoàn chỉnh môi trường Zone Edge (Sinh Keyring X25519 + Up Zone Services)
init-zone:
	@echo "🔑 [1/2] Kiểm tra/Sinh Keyring X25519 cho Zone Dataplane..."
	python3 scripts/gen-zone-keyring.py
	@echo "📦 [2/2] Khởi động toàn bộ Zone Stack..."
	docker compose -f dev/zone/compose.yml pull
	docker compose -f dev/zone/compose.yml up -d --no-build

# [COMMENT]: Khởi động nhanh Central (không qua bước bootstrap)
up-central:
	docker compose -f dev/central/compose.yml pull
	docker compose -f dev/central/compose.yml up -d --no-build

# [COMMENT]: Khởi động nhanh Zone
up-zone:
	docker compose -f dev/zone/compose.yml pull
	docker compose -f dev/zone/compose.yml up -d --no-build

# [COMMENT]: Dừng môi trường Central
down-central:
	docker compose -f dev/central/compose.yml down

# [COMMENT]: Dừng môi trường Zone
down-zone:
	docker compose -f dev/zone/compose.yml down

# [COMMENT]: Dọn dẹp sạch sẽ môi trường Central (dừng container, xóa volumes và orphan containers)
clean-central:
	@echo "🧹 Dọn dẹp toàn bộ Central Stack và Volumes..."
	docker compose -f dev/central/compose.yml down -v --remove-orphans

# [COMMENT]: Dọn dẹp sạch sẽ môi trường Zone (dừng container, xóa volumes và tệp key secret đĩa local)
clean-zone:
	@echo "🧹 Dọn dẹp toàn bộ Zone Stack, Volumes và Keyring secret..."
	docker compose -f dev/zone/compose.yml down -v --remove-orphans
	rm -f dataplane/.secrets/job-payload-keys.json

# [COMMENT]: Dọn dẹp sạch sẽ toàn bộ hệ thống (cả Central và Zone)
clean: clean-central clean-zone
	@echo "✨ Đã dọn dẹp sạch sẽ toàn bộ hệ thống!"
