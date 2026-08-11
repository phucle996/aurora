# Aurora Central & Zone local orchestration
#
# Runtime order is explicit and intentionally does not build images locally:
#   Central infrastructure -> Vault bootstrap -> Central applications
#   Zone infrastructure -> Zone applications

SHELL := /bin/bash

CENTRAL_COMPOSE := docker compose --env-file dev/central/.env -f dev/central/compose.yml
ZONE_COMPOSE := docker compose --env-file dev/zone/.env -f dev/zone/compose.yml

CENTRAL_INFRA_SERVICES := \
	psql changefeed-init billing-psql pbouncer psql-exporter \
	redis-exporter acr-redis cp-redis nats kafka-1 kafka-init kafka-ui \
	otel-collector victoriatraces victoriametrics victorialogs grafana \
	centrifugo scylla vault

CENTRAL_APP_SERVICES := \
	controlplane1 controlplane2 controlplane3 envoy cost-manager \
	cloud-console admin-ui-nginx cost-console-nginx job-orchestrator acr \
	notification-service

ZONE_INFRA_SERVICES := \
	nats-zone-z1 zone-victoriametrics zone-victorialogs zone-victoriatraces \
	zone-otel-collector stalwart minio

ZONE_APP_SERVICES := \
	dataplane-vn-n1 dataplane-vn-n2 dataplane-vn-n3 zone-runtime-stream \
	zone-control zone-public-edge-authorizer zone-public-edge-gateway

CENTRAL_APP_ENV_FILES := \
	controlplane/.env acr/.env cost-manager/api/.env cloud-console/.env \
	cost-console/.env job-orchestrator/.env notification-service/.env

ZONE_APP_ENV_FILES := \
	dataplane/.env zone-runtime-stream/.env zone-control/.env \
	zone-public-edge-gateway/authorizer/.env

.PHONY: help check-central-env check-central-app-env check-zone-env \
	check-zone-app-env central-infra central-bootstrap central-app \
	zone-infra zone-keyring zone-app init-central init-zone up-central up-zone \
	down-central down-zone clean clean-central clean-zone

help:
	@echo "Aurora local orchestration (GHCR images only; no local Docker build)"
	@echo "  make central-infra   Start Central infrastructure only"
	@echo "  make central-bootstrap  Seed Vault after Central infrastructure"
	@echo "  make central-app     Pull and start Central applications"
	@echo "  make init-central    Run Central infra -> Vault -> app"
	@echo "  make zone-infra      Start Zone infrastructure (after Central transport)"
	@echo "  make zone-app        Generate keyring, pull and start Zone applications"
	@echo "  make init-zone       Run Zone infra -> app"
	@echo "  make up-central      Resume the complete Central flow"
	@echo "  make up-zone         Resume the complete Zone flow"
	@echo "  make down-zone       Stop Zone before Central"
	@echo "  make down-central    Stop Central"
	@echo "  make clean           Remove both stacks and their volumes"

check-central-env:
	@test -f dev/central/.env || { echo "Missing dev/central/.env; copy .env.example and set GHCR digests."; exit 1; }
	@if grep -q 'sha256:replace_me' dev/central/.env; then \
		echo "dev/central/.env still contains replace_me image digests."; exit 1; \
	fi

check-central-app-env:
	@for file in $(CENTRAL_APP_ENV_FILES); do \
		if [[ ! -f "$$file" ]]; then \
			echo "Missing $$file; copy that workload's .env.example and fill its dev bootstrap values."; \
			exit 1; \
		fi; \
	done

check-zone-env:
	@test -f dev/zone/.env || { echo "Missing dev/zone/.env; copy .env.example and set GHCR digests."; exit 1; }
	@if grep -q 'sha256:replace_me' dev/zone/.env; then \
		echo "dev/zone/.env still contains replace_me image digests."; exit 1; \
	fi

check-zone-app-env:
	@for file in $(ZONE_APP_ENV_FILES); do \
		if [[ ! -f "$$file" ]]; then \
			echo "Missing $$file; copy that workload's .env.example and fill its dev bootstrap values."; \
			exit 1; \
		fi; \
	done

# Phase 1: start only Central infrastructure. Selecting services keeps missing
# first-party env files from blocking the infrastructure phase.
central-infra: check-central-env
	@echo "[central 1/3] Starting infrastructure (no build)..."
	$(CENTRAL_COMPOSE) up -d --no-build $(CENTRAL_INFRA_SERVICES)

# Phase 2: wait for Vault and write all capability-scoped records/tokens.
central-bootstrap: central-infra
	@echo "[central 2/3] Waiting for Vault readiness..."
	@until curl --fail --silent http://localhost:8200/v1/sys/health >/dev/null; do sleep 1; done
	@echo "[central 2/3] Seeding Vault connections and workload policies..."
	dev/central/vault/vault-bootstrap.sh -t "$${VAULT_ROOT_TOKEN:-root}"

# Phase 3: only now pull and start first-party Central applications.
central-app: check-central-app-env central-bootstrap
	@echo "[central 3/3] Pulling deployable Central images..."
	$(CENTRAL_COMPOSE) config -q
	$(CENTRAL_COMPOSE) pull $(CENTRAL_APP_SERVICES)
	@echo "[central 3/3] Starting Central applications (no build)..."
	$(CENTRAL_COMPOSE) up -d --no-build $(CENTRAL_APP_SERVICES)

init-central: central-app
up-central: central-app

# Central owns aurora-dev-transport. Zone infrastructure can start after that
# network exists; Zone application env/keyring checks happen in zone-app.
zone-infra: central-infra check-zone-env
	@echo "[zone 1/2] Starting Zone infrastructure (no build)..."
	$(ZONE_COMPOSE) up -d --no-build $(ZONE_INFRA_SERVICES)

zone-keyring:
	@echo "[zone 2/2] Generating the local Zone payload keyring..."
	python3 scripts/gen-zone-keyring.py
	@test -f dataplane/.secrets/job-payload-keys.json || { echo "Zone keyring generation did not produce the expected file."; exit 1; }

zone-app: check-zone-app-env zone-infra zone-keyring
	@echo "[zone 2/2] Pulling deployable Zone images..."
	$(ZONE_COMPOSE) config -q
	$(ZONE_COMPOSE) pull $(ZONE_APP_SERVICES)
	@echo "[zone 2/2] Starting Zone applications (no build)..."
	$(ZONE_COMPOSE) up -d --no-build $(ZONE_APP_SERVICES)

init-zone: zone-app
up-zone: zone-app

down-zone:
	$(ZONE_COMPOSE) down

down-central:
	$(CENTRAL_COMPOSE) down

clean-central:
	$(CENTRAL_COMPOSE) down -v --remove-orphans

clean-zone:
	$(ZONE_COMPOSE) down -v --remove-orphans
	rm -f dataplane/.secrets/job-payload-keys.json

clean: clean-central clean-zone
	@echo "Aurora Central and Zone volumes removed."
